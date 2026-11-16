// Command pgnative-sqlcheck is a read-only DEV tool (coordination id:
// pgnative_sqlcheck_v1, pgnative-C). It walks internal/ and cmd/ with go/ast,
// collects the PG-side SQL literals (2nd argument of nativeSQL/sqlFragment,
// the argument of portableSQL/pgOnlySQL/selectedSQL), and:
//
//   - lint (always, no DB): app.norm misuse, ON CONFLICT targets vs the
//     native inventory/schema, norm function vs the ux__ index of the column;
//
//   - prepare (-prepare, DSN from PG_DSN, never printed; or -emit-psql /
//     -psql-result through docker exec psql): EXPLAIN (GENERIC_PLAN) (plans,
//     so ON CONFLICT 42P10 is caught; PREPARE alone is not) of every complete static DML/SELECT inside one READ ONLY transaction that
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
	planFile := flag.String("plan", "", "optional migration plan.sql: every PK/UNIQUE constraint and unique index becomes a valid ON CONFLICT target (any other target is flagged)")
	emitPsql := flag.String("emit-psql", "", "write a psql script that PREPAREs every complete site (read-only tx, rolled back) and exit; run it with psql -X -f and feed the output to -psql-result")
	psqlResult := flag.String("psql-result", "", "comma-separated psql outputs of the -emit-psql script (one per search_path run): report per-site OK/ERROR+SQLSTATE")
	flag.Parse()

	sites := collect(*root)
	ux := loadInventory(*inv)
	raw := loadSchemaKeys(*schemaFile)
	if *planFile != "" {
		raw = loadPlanKeys(*planFile, raw)
	}
	if *emitPsql != "" {
		die(os.WriteFile(*emitPsql, []byte(psqlScript(sites)), 0o644))
		return
	}
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
	if *psqlResult != "" {
		// Several outputs (one per search_path run, e.g. public and
		// bot_context): a site fails only if it fails in every run.
		for i, p := range strings.Split(*psqlResult, ",") {
			b, err := os.ReadFile(p)
			die(err)
			r := parsePsqlResult(sites, string(b))
			if i == 0 {
				res = r
				continue
			}
			for k := range res {
				if r[k] == "" {
					delete(res, k)
				}
			}
		}
		*prepare = true
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
	fmt.Fprintln(bw, "pos\thelper\tkind\tdetail\tsql")
	var skipped, ok, failed int
	for _, s := range sites {
		switch {
		case s.Skip != "":
			skipped++
			fmt.Fprintf(bw, "%s\t%s\tSKIPPED\t%s\t%s\n", s.Pos, s.Helper, s.Skip, oneLine(s.SQL))
		case *prepare:
			if e := res[s.Pos+"|"+s.SQL]; e == "" {
				ok++
				fmt.Fprintf(bw, "%s\t%s\tok\t\t%s\n", s.Pos, s.Helper, oneLine(s.SQL))
			} else {
				failed++
				fmt.Fprintf(bw, "%s\t%s\terror\t%s\t%s\n", s.Pos, s.Helper, oneLine(e), oneLine(s.SQL))
			}
		}
	}
	for _, f := range finds {
		fmt.Fprintf(bw, "%s\tlint\t%s\t%s\t\n", f.Pos, f.Kind, oneLine(f.Detail))
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
			taken := map[token.Pos]bool{}
			ast.Inspect(f, func(n ast.Node) bool {
				// Dialect branches (pgnative_sqlcheck_v2): every complete string
				// expression inside the PG arm of `if <...Postgres...> {}` or a
				// `case ...Postgres:` is a PG site, e.g.
				// `if st.Postgres { q = "... ON CONFLICT ..." }`.
				if ifs, ok := n.(*ast.IfStmt); ok {
					thenPG, elsePG := pgBranch(ifs.Cond)
					if thenPG {
						out = append(out, branchSites(fset, ifs.Body, taken)...)
						if ifs.Else != nil {
							markLits(ifs.Else, taken) // MySQL/SQLite arm: never PG
						}
					}
					if elsePG && ifs.Else != nil {
						out = append(out, branchSites(fset, ifs.Else, taken)...)
						markLits(ifs.Body, taken)
					}
					return true
				}
				if cc, ok := n.(*ast.CaseClause); ok {
					for _, e := range cc.List {
						if mentionsPG(e) {
							for _, st := range cc.Body {
								out = append(out, branchSites(fset, st, taken)...)
							}
							break
						}
					}
					return true
				}
				// Any bare literal mentioning app.norm / ON CONFLICT is linted; a
				// complete PG-only statement (ON CONFLICT / RETURNING / EXCLUDED.)
				// is prepared too, wherever it lives.
				if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					if taken[lit.Pos()] {
						return true
					}
					if v, err := strconv.Unquote(lit.Value); err == nil && (strings.Contains(v, "app.norm") || pgOnlyRE.MatchString(v)) {
						s := site{Pos: fset.Position(lit.Pos()).String(), Helper: "literal", SQL: v, Skip: "literal piece (lint only)"}
						if pgOnlyRE.MatchString(v) && skipReason(v, "literal") == "" {
							s.Helper, s.Skip = "pgliteral", ""
						}
						out = append(out, s)
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
				if id.Name == "nativeSQL" || id.Name == "sqlFragment" {
					markLits(call.Args[0], taken) // the MySQL arm is never PG
				}
				if c, isCall := arg.(*ast.CallExpr); isCall {
					if inner, ok := c.Fun.(*ast.Ident); ok && (inner.Name == "nativeSQL" || inner.Name == "sqlFragment") {
						return true // portableSQL(nativeSQL(...)): the inner call is collected itself
					}
				}
				if lit, isLit := arg.(*ast.BasicLit); isLit && taken[lit.Pos()] {
					return true // literal of a non-PG dialect arm
				}
				if _, isIdent := arg.(*ast.Ident); isIdent {
					return true // selectedSQL(q): q is resolved by the pgbranch/pgliteral sites
				}
				s := site{Pos: fset.Position(arg.Pos()).String(), Helper: id.Name}
				if v, ok := constString(arg); ok {
					markLits(arg, taken)
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

// pgOnlyRE: syntax that only exists on the PG side of a dialect switch.
var pgOnlyRE = regexp.MustCompile(`(?i)\bON\s+CONFLICT\b|\bRETURNING\b|\bEXCLUDED\.`)

var pgNameRE = regexp.MustCompile(`(?i)postgres|^pg$|^ispg$|^usepg$`)

// mentionsPG: the expression names Postgres (sqldb.Postgres, st.Postgres,
// isPostgres(), pg, DriverPostgres ...).
func mentionsPG(e ast.Expr) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && pgNameRE.MatchString(id.Name) {
			found = true
		}
		return !found
	})
	return found
}

// pgBranch: which arm of `if cond` runs on PG. `x == Postgres`, `x.Postgres`,
// `a && x.Postgres` -> then; `x != Postgres`, `!x.Postgres` -> else. `||`
// and anything else is ignored (conservative: no false PG sites).
func pgBranch(cond ast.Expr) (thenPG, elsePG bool) {
	switch c := cond.(type) {
	case *ast.ParenExpr:
		return pgBranch(c.X)
	case *ast.UnaryExpr:
		if c.Op == token.NOT {
			if _, isBin := c.X.(*ast.BinaryExpr); !isBin && mentionsPG(c.X) {
				return false, true
			}
		}
	case *ast.BinaryExpr:
		switch c.Op {
		case token.EQL:
			return mentionsPG(c.X) || mentionsPG(c.Y), false
		case token.NEQ:
			return false, mentionsPG(c.X) || mentionsPG(c.Y)
		case token.LAND:
			a, _ := pgBranch(c.X)
			b, _ := pgBranch(c.Y)
			return a || b, false
		}
	case *ast.Ident, *ast.SelectorExpr, *ast.CallExpr:
		return mentionsPG(c), false
	}
	return false, false
}

// branchSites collects every constant DML string expression under n.
func branchSites(fset *token.FileSet, n ast.Node, taken map[token.Pos]bool) []site {
	var out []site
	ast.Inspect(n, func(m ast.Node) bool {
		e, ok := m.(ast.Expr)
		if !ok {
			return true
		}
		switch x := e.(type) {
		case *ast.BasicLit:
		case *ast.BinaryExpr:
			if x.Op != token.ADD {
				return true
			}
		default:
			return true
		}
		v, ok := constString(e)
		if !ok {
			if _, isBin := e.(*ast.BinaryExpr); isBin {
				markLits(e, taken) // "... FROM "+seq: dynamic, never a site
				return false
			}
			return true
		}
		if taken[e.Pos()] || !dmlRE.MatchString(v) {
			return false
		}
		markLits(e, taken)
		out = append(out, site{Pos: fset.Position(e.Pos()).String(), Helper: "pgbranch", SQL: v, Skip: skipReason(v, "pgbranch")})
		return false
	})
	return out
}

func markLits(e ast.Node, taken map[token.Pos]bool) {
	ast.Inspect(e, func(m ast.Node) bool {
		if l, ok := m.(*ast.BasicLit); ok {
			taken[l.Pos()] = true
		}
		return true
	})
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
	for _, s := range sites {
		if s.Skip != "" {
			continue
		}

		if _, err := tx.ExecContext(ctx, "SAVEPOINT pgnc"); err != nil {
			die(err)
		}
		rows, err := tx.QueryContext(ctx, explainSQL(s.SQL))
		if err == nil {
			for rows.Next() {
			}
			err = rows.Err()
			rows.Close()
		}
		if err != nil {
			res[s.Pos+"|"+s.SQL] = err.Error()
			_, _ = tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT pgnc")
			continue
		}
		_, _ = tx.ExecContext(ctx, "RELEASE SAVEPOINT pgnc")
	}
	return res
}

// --- psql mode (no DSN needed: docker exec ... psql -X -f) ----------------

// explainSQL: EXPLAIN (GENERIC_PLAN) (PG >= 16) parses, analyses AND plans
// the statement with $n parameters, without executing it. PREPARE alone is
// NOT enough: ON CONFLICT arbiter inference (42P10) runs in the planner, so a
// conflict target that matches no unique index PREPAREs fine and only fails
// at EXECUTE (the bot_inbound_queue cutover rollback).
func explainSQL(q string) string {
	return "EXPLAIN (GENERIC_PLAN, COSTS OFF) " + strings.TrimRight(strings.TrimSpace(rebind(q)), ";")
}

// psqlScript: one READ ONLY transaction, always rolled back; per site a
// marker line, SAVEPOINT, EXPLAIN (GENERIC_PLAN), ROLLBACK TO. VERBOSITY
// verbose prints "ERROR:  <SQLSTATE>: msg".
func psqlScript(sites []site) string {
	var b strings.Builder
	b.WriteString("\\set ON_ERROR_STOP off\n\\set VERBOSITY verbose\n\\set SHOW_CONTEXT never\nSET client_min_messages = warning;\nBEGIN READ ONLY;\n")
	for i, s := range sites {
		if s.Skip != "" {
			continue
		}
		fmt.Fprintf(&b, "\\warn @@site %d\nSAVEPOINT pgnc;\n%s;\nROLLBACK TO SAVEPOINT pgnc;\n", i, explainSQL(s.SQL))
	}
	b.WriteString("\\warn @@end\nROLLBACK;\n")
	return b.String()
}

var psqlErrRE = regexp.MustCompile(`(?m)^(?:psql:[^\n]*?)?ERROR:\s+([0-9A-Z]{5}): ([^\n]*)`)

// parsePsqlResult maps the first ERROR after each "@@site i" marker to site i
// (DEALLOCATE/ROLLBACK noise after a failed PREPARE is ignored). A site whose
// marker is missing is reported as not run.
func parsePsqlResult(sites []site, out string) map[string]string {
	res := map[string]string{}
	chunks := strings.Split(out, "@@site ")
	seen := map[int]bool{}
	for _, c := range chunks[1:] {
		nl := strings.IndexByte(c, '\n')
		if nl < 0 {
			nl = len(c)
		}
		i, err := strconv.Atoi(strings.TrimSpace(c[:nl]))
		if err != nil || i < 0 || i >= len(sites) {
			continue
		}
		seen[i] = true
		if m := psqlErrRE.FindStringSubmatch(c[nl:]); m != nil {
			res[sites[i].Pos+"|"+sites[i].SQL] = m[1] + ": " + m[2]
		}
	}
	for i, s := range sites {
		if s.Skip == "" && !seen[i] {
			res[s.Pos+"|"+s.SQL] = "NOTRUN: no psql output for this site"
		}
	}
	return res
}

var (
	planTableRE  = regexp.MustCompile(`^CREATE TABLE "([^"]+)"`)
	planConstrRE = regexp.MustCompile(`CONSTRAINT "[^"]+" (?:PRIMARY KEY|UNIQUE) \((.*)\)`)
	planUxRE     = regexp.MustCompile(`^CREATE UNIQUE INDEX "[^"]+" ON "([^"]+)" \((.*)\);$`)
)

// loadPlanKeys adds the PG unique keys of plan.sql (constraints and unique
// expression indexes, items normalised like ON CONFLICT targets).
func loadPlanKeys(p string, into map[string][]map[string]bool) map[string][]map[string]bool {
	if into == nil {
		into = map[string][]map[string]bool{}
	}
	f, err := os.Open(p)
	die(err)
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	table := ""
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if m := planTableRE.FindStringSubmatch(line); m != nil {
			table = m[1]
			continue
		}
		if table != "" {
			if m := planConstrRE.FindStringSubmatch(line); m != nil {
				into[table] = append(into[table], setOf(splitTop(m[1])))
			}
			if strings.HasPrefix(line, ");") {
				table = ""
			}
		}
		if m := planUxRE.FindStringSubmatch(line); m != nil {
			into[m[1]] = append(into[m[1]], setOf(splitTop(m[2])))
		}
	}
	die(sc.Err())
	return into
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
		// A table with no known key (e.g. bot_context tables created by
		// botstore.Init, absent from plan.sql) is left to the EXPLAIN gate.
		known := len(ux[table]) > 0 || len(raw[table]) > 0
		if !matched && (hasNorm || raw != nil) && (known || hasNorm) {
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
