// Command pgnative-sqlcheck is a read-only DEV tool (coordination id:
// pgnative_sqlcheck_v1, pgnative-C). It walks internal/ and cmd/ with go/ast,
// collects the PG-side SQL literals (2nd argument of nativeSQL/sqlFragment,
// the argument of portableSQL/pgOnlySQL/selectedSQL), and:
//
//   - lint (always, no DB): app.norm misuse, ON CONFLICT targets vs the
//     native inventory/schema, norm function vs the ux__ index of the column;
//
//   - prepare (-prepare, DSN from PG_DSN, never printed): PREPARE + DEALLOCATE
//     of every complete static DML/SELECT inside one READ ONLY transaction that
//     is always rolled back. Nothing else is ever sent.
//
//     pgnative-sqlcheck [-root .] [-inventory docs/pgnative/native-ddl-inventory.tsv] [-schema schema.json] [-prepare] [-out report.tsv]
package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type site struct {
	Pos, Helper, SQL, Skip string
}

type finding struct{ Pos, Kind, Detail string }

var pgArg = map[string]int{"nativeSQL": 1, "sqlFragment": 1, "portableSQL": 0, "pgOnlySQL": 0, "selectedSQL": 0}

func main() {
	root := flag.String("root", ".", "repo root")
	inv := flag.String("inventory", "docs/pgnative/native-ddl-inventory.tsv", "native unique index inventory")
	schemaFile := flag.String("schema", "", "optional schema.json (PK/raw unique targets for ON CONFLICT)")
	prepare := flag.Bool("prepare", false, "PREPARE every complete statement against PG_DSN (read-only tx, rolled back)")
	out := flag.String("out", "", "TSV report file (default stdout)")
	flag.Parse()

	sites := collect(*root)
	ux := loadInventory(*inv)
	raw := loadSchemaKeys(*schemaFile)
	var finds []finding
	seen := map[string]bool{}
	for _, s := range sites {
		for _, f := range lint(s, ux, raw) {
			if k := f.Kind + "|" + f.Detail + "|" + strings.SplitN(f.Pos, ":", 2)[0]; !seen[k] {
				seen[k] = true
				finds = append(finds, f)
			}
		}
	}
	res := map[string]string{}
	if *prepare {
		res = prepareAll(sites)
	}
	w := os.Stdout
	if *out != "" {
		f, err := os.Create(*out)
		die(err)
		defer f.Close()
		w = f
	}
	bw := bufio.NewWriter(w)
	defer bw.Flush()
	fmt.Fprintln(bw, "pos\thelper\tkind\tdetail")
	var skipped, ok, failed int
	for _, s := range sites {
		switch {
		case s.Skip != "":
			skipped++
			fmt.Fprintf(bw, "%s\t%s\tSKIPPED\t%s\n", s.Pos, s.Helper, s.Skip)
		case *prepare:
			if e := res[s.Pos+"|"+s.SQL]; e == "" {
				ok++
				fmt.Fprintf(bw, "%s\t%s\tok\t\n", s.Pos, s.Helper)
			} else {
				failed++
				fmt.Fprintf(bw, "%s\t%s\terror\t%s\n", s.Pos, s.Helper, oneLine(e))
			}
		}
	}
	for _, f := range finds {
		fmt.Fprintf(bw, "%s\tlint\t%s\t%s\n", f.Pos, f.Kind, oneLine(f.Detail))
	}
	fmt.Fprintf(os.Stderr, "{\"sites\":%d,\"skipped\":%d,\"prepared_ok\":%d,\"prepare_errors\":%d,\"lint_hits\":%d}\n", len(sites), skipped, ok, failed, len(finds))
}

func die(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "pgnative-sqlcheck:", err)
		os.Exit(2)
	}
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// --- collection -----------------------------------------------------------

func collect(root string) []site {
	var out []site
	fset := token.NewFileSet()
	for _, dir := range []string{"internal", "cmd"} {
		_ = filepath.Walk(filepath.Join(root, dir), func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") || strings.Contains(p, "internal/dbmigrate") || strings.Contains(p, "cmd/pgnative-sqlcheck") {
				return nil
			}
			f, err := parser.ParseFile(fset, p, nil, 0)
			if err != nil {
				return nil
			}
			ast.Inspect(f, func(n ast.Node) bool {
				// Any bare literal mentioning app.norm / ON CONFLICT is linted too
				// (pieces of dynamically assembled SQL); never prepared.
				if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					if v, err := strconv.Unquote(lit.Value); err == nil && (strings.Contains(v, "app.norm") || strings.Contains(strings.ToUpper(v), "ON CONFLICT")) {
						out = append(out, site{Pos: fset.Position(lit.Pos()).String(), Helper: "literal", SQL: v, Skip: "literal piece (lint only)"})
					}
					return true
				}
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				id, ok := call.Fun.(*ast.Ident)
				if !ok {
					return true
				}
				idx, ok := pgArg[id.Name]
				if !ok || len(call.Args) <= idx {
					return true
				}
				arg := call.Args[idx]
				if c, isCall := arg.(*ast.CallExpr); isCall {
					if inner, ok := c.Fun.(*ast.Ident); ok && pgArg[inner.Name] >= 0 && (inner.Name == "nativeSQL" || inner.Name == "sqlFragment") {
						return true // portableSQL(nativeSQL(...)): the inner call is collected itself
					}
				}
				s := site{Pos: fset.Position(arg.Pos()).String(), Helper: id.Name}
				if v, ok := constString(arg); ok {
					s.SQL = v
					s.Skip = skipReason(v, id.Name)
				} else {
					s.Skip = "dynamic"
				}
				out = append(out, s)
				return true
			})
			return nil
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Pos < out[j].Pos })
	return out
}

// constString folds string literals and + concatenations of literals.
func constString(e ast.Expr) (string, bool) {
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind != token.STRING {
			return "", false
		}
		v, err := strconv.Unquote(x.Value)
		return v, err == nil
	case *ast.ParenExpr:
		return constString(x.X)
	case *ast.BinaryExpr:
		if x.Op != token.ADD {
			return "", false
		}
		a, ok1 := constString(x.X)
		b, ok2 := constString(x.Y)
		return a + b, ok1 && ok2
	}
	return "", false
}

var dmlRE = regexp.MustCompile(`(?is)^\s*(SELECT|INSERT|UPDATE|DELETE|WITH)\b`)

func skipReason(q, helper string) string {
	switch {
	case strings.Contains(q, "%s") || strings.Contains(q, "%d") || strings.Contains(q, "%v") || strings.Contains(q, "%q"):
		return "format fragments"
	case helper == "sqlFragment" || !dmlRE.MatchString(q):
		return "fragment or DDL/utility (lint only)"
	case strings.Contains(strings.TrimRight(strings.TrimSpace(q), ";"), ";"):
		return "multiple statements"
	case regexp.MustCompile(`(?i)\bpg_temp\.|\bTEMP(ORARY)?\s+TABLE\b`).MatchString(q):
		return "needs temp objects"
	}
	return ""
}

// rebind converts ? placeholders (outside quotes) to $1..$n.
func rebind(q string) string {
	var b strings.Builder
	n := 0
	inS, inD := false, false
	for i := 0; i < len(q); i++ {
		c := q[i]
		switch {
		case c == 39 && !inD: // single quote
			inS = !inS
		case c == '"' && !inS:
			inD = !inD
		case c == '?' && !inS && !inD:
			n++
			b.WriteString("$" + strconv.Itoa(n))
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// --- prepare --------------------------------------------------------------

func prepareAll(sites []site) map[string]string {
	dsn := os.Getenv("PG_DSN")
	if dsn == "" {
		die(fmt.Errorf("-prepare needs PG_DSN"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	db, err := sql.Open("pgx", dsn)
	die(err)
	defer db.Close()
	conn, err := db.Conn(ctx)
	die(err)
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	die(err)
	defer tx.Rollback() // always: nothing is ever committed
	res := map[string]string{}
	for i, s := range sites {
		if s.Skip != "" {
			continue
		}
		name := fmt.Sprintf("pgnc_%d", i)
		if _, err := tx.ExecContext(ctx, "SAVEPOINT pgnc"); err != nil {
			die(err)
		}
		_, err := tx.ExecContext(ctx, "PREPARE "+name+" AS "+rebind(s.SQL))
		if err != nil {
			res[s.Pos+"|"+s.SQL] = err.Error()
			_, _ = tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT pgnc")
			continue
		}
		_, _ = tx.ExecContext(ctx, "DEALLOCATE "+name)
		_, _ = tx.ExecContext(ctx, "RELEASE SAVEPOINT pgnc")
	}
	return res
}

// --- lint -----------------------------------------------------------------

type uxIndex struct {
	Table string
	Items map[string]bool   // normalised key items
	Funcs map[string]string // text column -> app.norm / app.norm_ascii
}

var normItemRE = regexp.MustCompile(`"|\s+|::[a-z_ ]+`)

func normItem(s string) string { return strings.ToLower(normItemRE.ReplaceAllString(s, "")) }

func splitTop(s string) []string {
	var out []string
	d, st := 0, 0
	for i, c := range s {
		switch c {
		case '(':
			d++
		case ')':
			d--
		case ',':
			if d == 0 {
				out = append(out, s[st:i])
				st = i + 1
			}
		}
	}
	return append(out, s[st:])
}

func setOf(list []string) map[string]bool {
	m := map[string]bool{}
	for _, x := range list {
		if x = normItem(x); x != "" {
			m[x] = true
		}
	}
	return m
}

func sameSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func loadInventory(p string) map[string][]uxIndex {
	f, err := os.Open(p)
	die(err)
	defer f.Close()
	out := map[string][]uxIndex{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	first := true
	for sc.Scan() {
		if first {
			first = false
			continue
		}
		c := strings.Split(sc.Text(), "	")
		if len(c) < 8 {
			continue
		}
		tgt := strings.TrimSuffix(strings.TrimPrefix(c[7], "ON CONFLICT ("), ")")
		ix := uxIndex{Table: c[0], Items: setOf(splitTop(tgt)), Funcs: map[string]string{}}
		tc, fn := strings.Split(c[3], ","), strings.Split(c[4], ",")
		for i := range tc {
			if i < len(fn) {
				ix.Funcs[tc[i]] = fn[i]
			}
		}
		out[c[0]] = append(out[c[0]], ix)
	}
	return out
}

// loadSchemaKeys: table -> raw PK/unique column sets (optional).
func loadSchemaKeys(p string) map[string][]map[string]bool {
	out := map[string][]map[string]bool{}
	if p == "" {
		return nil
	}
	b, err := os.ReadFile(p)
	die(err)
	var s struct {
		Tables []struct {
			Name    string `json:"name"`
			Indexes []struct {
				Name   string `json:"name"`
				Unique bool   `json:"unique"`
				Cols   []struct {
					Name string `json:"name"`
				} `json:"cols"`
			} `json:"indexes"`
		} `json:"tables"`
	}
	die(json.Unmarshal(b, &s))
	for _, t := range s.Tables {
		for _, ix := range t.Indexes {
			if !ix.Unique && ix.Name != "PRIMARY" {
				continue
			}
			var cols []string
			for _, c := range ix.Cols {
				cols = append(cols, c.Name)
			}
			out[t.Name] = append(out[t.Name], setOf(cols))
		}
	}
	return out
}

var (
	normCallRE = regexp.MustCompile(`(app\.norm(?:_ascii)?)\(((?:[^()]|\([^()]*\))*)\)\s*(=|<>|!=)\s*`)
	normInRE   = regexp.MustCompile(`(app\.norm(?:_ascii)?)\((?:[^()]|\([^()]*\))*\)\s+(?:NOT\s+)?IN\s*\(`)
	rhsFuncRE  = regexp.MustCompile(`^(app\.norm(?:_ascii)?)\(`)
	rhsLitRE   = regexp.MustCompile(`^'((?:[^']|'')*)'`)
	conflictRE = regexp.MustCompile(`(?is)INSERT\s+INTO\s+"?(\w+)"?.*?ON\s+CONFLICT\s*\(`)
	tableRefRE = regexp.MustCompile(`(?i)\b(?:FROM|JOIN|UPDATE|INTO)\s+"?(\w+)"?`)
	colStripRE = regexp.MustCompile(`^(?:"?\w+"?\.)?"?(\w+)"?$`)
)

func lint(s site, ux map[string][]uxIndex, raw map[string][]map[string]bool) []finding {
	q := s.SQL
	if q == "" {
		return nil
	}
	var out []finding
	add := func(kind, detail string) { out = append(out, finding{s.Pos, kind, detail}) }
	tables := map[string]bool{}
	for _, m := range tableRefRE.FindAllStringSubmatch(q, -1) {
		tables[m[1]] = true
	}
	// (a) + (c)
	for _, m := range normCallRE.FindAllStringSubmatchIndex(q, -1) {
		fn, arg := q[m[2]:m[3]], q[m[4]:m[5]]
		rhs := strings.TrimSpace(q[m[1]:])
		switch {
		case rhsFuncRE.MatchString(rhs):
			if g := rhsFuncRE.FindStringSubmatch(rhs)[1]; g != fn {
				add("a_norm_mismatch", fn+"(...) vs "+g+"(...)")
			}
		case rhsLitRE.MatchString(rhs):
			if lit := rhsLitRE.FindStringSubmatch(rhs)[1]; lit != strings.ToLower(lit) || lit != strings.TrimRight(lit, " ") {
				add("a_norm_vs_literal", fn+"("+arg+") = '"+lit+"'")
			}
		default:
			add("a_norm_vs_raw", fn+"("+arg+") = "+firstToken(rhs))
		}
		if cm := colStripRE.FindStringSubmatch(strings.TrimSpace(arg)); cm != nil {
			for t := range tables {
				for _, ix := range ux[t] {
					if want, ok := ix.Funcs[cm[1]]; ok && want != fn {
						add("c_index_func", t+"."+cm[1]+" uses "+fn+", ux__ index uses "+want)
					}
				}
			}
		}
	}
	// (a') IN lists: every element must carry the same norm function.
	for _, m := range normInRE.FindAllStringSubmatchIndex(q, -1) {
		fn := q[m[2]:m[3]]
		open := m[1] - 1
		d, end := 0, -1
		for i := open; i < len(q); i++ {
			if q[i] == '(' {
				d++
			} else if q[i] == ')' {
				if d--; d == 0 {
					end = i
					break
				}
			}
		}
		if end < 0 {
			continue
		}
		for _, el := range splitTop(q[open+1 : end]) {
			if el = strings.TrimSpace(el); el != "" && !strings.HasPrefix(el, fn+"(") {
				add("a_norm_in_raw", fn+"(...) IN (... "+el+" ...)")
				break
			}
		}
	}
	// (b)
	for _, m := range conflictRE.FindAllStringSubmatchIndex(q, -1) {
		table := q[m[2]:m[3]]
		open := m[1] - 1
		d, end := 0, -1
		for i := open; i < len(q); i++ {
			if q[i] == '(' {
				d++
			} else if q[i] == ')' {
				if d--; d == 0 {
					end = i
					break
				}
			}
		}
		if end < 0 {
			continue
		}
		got := setOf(splitTop(q[open+1 : end]))
		matched := false
		for _, ix := range ux[table] {
			matched = matched || sameSet(got, ix.Items)
		}
		for _, k := range raw[table] {
			matched = matched || sameSet(got, k)
		}
		hasNorm := strings.Contains(q[open:end], "app.norm")
		if !matched && (hasNorm || raw != nil) {
			add("b_on_conflict", table+" ("+strings.TrimSpace(q[open+1:end])+")")
		}
	}
	return out
}

func firstToken(s string) string {
	if f := strings.Fields(s); len(f) > 0 {
		return f[0]
	}
	return ""
}
