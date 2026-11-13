package dbmigrate

import (
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
)

// CallSite is one database call found by go/ast (for the S3 port).
type CallSite struct {
	File       string   `json:"file"`
	Line       int      `json:"line"`
	Func       string   `json:"enclosing_func"`
	Method     string   `json:"method"`
	Static     bool     `json:"static"`
	SQL        string   `json:"sql,omitempty"`  // static text (code, not data)
	Expr       string   `json:"expr,omitempty"` // dynamic query expression source
	Categories []string `json:"mysql_categories,omitempty"`
	Native     string   `json:"native,omitempty"`
	Audit      []string `json:"portable_audit,omitempty"` // MySQL-only constructs found in a portableSQL text // nativeSQL (dialect variants) | portableSQL (audited identical)
}

type Inventory struct {
	Files      int            `json:"files_with_calls"`
	Sites      int            `json:"call_sites"`
	Static     int            `json:"static"`
	Dynamic    int            `json:"dynamic"`
	ByCategory map[string]int `json:"by_category"`
	Native     int            `json:"native_sites"` // syntax-ported (incl. provisional), never blocked
	// NativeByKind counts wrapped sites by marker; BlockedOnPG are wrapped by
	// blockedOnPGSQL (MySQL-only, no PG port: NOT native); Provisional lists
	// provisionalSQL/pgProvisionalSQL sites (syntax-ported, open cutover gate).
	NativeByKind    map[string]int `json:"native_by_kind"`
	BlockedOnPG     int            `json:"blocked_on_pg_sites"`
	BlockedList     []string       `json:"blocked_on_pg_list"`
	ProvisionalList []string       `json:"provisional_gate_list"`
	RuntimeVerified int            `json:"runtime_verified_sites"`
	// HelperInternal counts Exec/QueryRow calls inside the sqldb dialect helper
	// package itself (they execute the caller-supplied, already-counted query),
	// excluded from Sites so no call is counted twice.
	HelperInternal int `json:"helper_internal_calls_excluded"`
	// PreparedStmtCalls counts Exec/Query on a *sql.Stmt obtained from a
	// Prepare/PrepareContext in the same function: the SQL is counted once at
	// the Prepare site, the statement call carries only arguments.
	PreparedStmtCalls int `json:"prepared_stmt_calls_excluded"`
	mixedCase         []string
	AuditFail         int        `json:"portable_audit_failures"`
	CallSites         []CallSite `json:"call_sites_list"`
}

var dbMethods = map[string]bool{"Query": true, "QueryContext": true, "QueryRow": true, "QueryRowContext": true,
	"Exec": true, "ExecContext": true, "Prepare": true, "PrepareContext": true, "InsertID": true, "InsertSelectID": true}

// mysqlCategories are construct classes needing a PG decision (lexical scan
// of static SQL; the S3 port reviews each site, nothing is rewritten here).
var mysqlCategories = map[string]*regexp.Regexp{
	"upsert_on_duplicate": regexp.MustCompile(`(?i)ON\s+DUPLICATE\s+KEY`), "insert_ignore": regexp.MustCompile(`(?i)INSERT\s+IGNORE`),
	"replace_into": regexp.MustCompile(`(?i)REPLACE\s+INTO`), "date_format": regexp.MustCompile(`(?i)DATE_FORMAT\s*\(`),
	"date_arith": regexp.MustCompile(`(?i)DATE_(ADD|SUB)\s*\(|INTERVAL\s+[0-9?]+\s+[A-Z]+`), "now_curdate": regexp.MustCompile(`(?i)\b(NOW|CURDATE|CURTIME|UTC_TIMESTAMP)\s*\(`),
	"timestampdiff": regexp.MustCompile(`(?i)\b(TIMESTAMPDIFF|DATEDIFF|WEEKDAY|DAYOFWEEK|YEARWEEK)\s*\(`), "if_ifnull": regexp.MustCompile(`(?i)\b(IF|IFNULL)\s*\(`),
	"group_concat": regexp.MustCompile(`(?i)GROUP_CONCAT`), "json_func": regexp.MustCompile(`(?i)\bJSON_[A-Z]+\s*\(|->>?\s*'\$`),
	"find_in_set_field": regexp.MustCompile(`(?i)FIND_IN_SET|FIELD\s*\(`), "backtick": regexp.MustCompile("`"),
	"locks": regexp.MustCompile(`(?i)GET_LOCK|RELEASE_LOCK`), "for_update": regexp.MustCompile(`(?i)FOR\s+UPDATE|LOCK\s+IN\s+SHARE`),
	"cast_mysql": regexp.MustCompile(`(?i)AS\s+(UNSIGNED|SIGNED|CHAR)\b`), "collate": regexp.MustCompile(`(?i)\bCOLLATE\b`),
	"information_schema": regexp.MustCompile(`(?i)information_schema`), "group_by": regexp.MustCompile(`(?i)GROUP\s+BY`),
	"limit_offset_comma": regexp.MustCompile(`(?i)LIMIT\s+\S+\s*,`), "regexp": regexp.MustCompile(`(?i)\bREGEXP\b`),
	"concat": regexp.MustCompile(`(?i)\bCONCAT\s*\(`), "like": regexp.MustCompile(`(?i)\bLIKE\b`),
}

var portableBanned = regexp.MustCompile("(?i)`|\\bIFNULL\\s*\\(|\\bIF\\s*\\(|\\bJSON_[A-Z]+\\s*\\(|DATE_FORMAT|DATE_(ADD|SUB)\\s*\\(|\\bNOW\\s*\\(|CURDATE|CURTIME|UTC_TIMESTAMP|INSERT\\s+IGNORE|ON\\s+DUPLICATE|REPLACE\\s+INTO|GROUP_CONCAT|LIMIT\\s+\\S+\\s*,|FIND_IN_SET|\\bSTR_TO_DATE|\\bTIMESTAMPDIFF|\\bUNIX_TIMESTAMP|->>?|\\bREGEXP\\b|\\bCAST\\s*\\([^)]*\\bAS\\s+(UNSIGNED|SIGNED|CHAR)\\b")

// PortableAudit lists the MySQL-only constructs of a portableSQL text and any
// unquoted mixed-case schema identifier (PG folds it to lowercase).
func PortableAudit(sql string, mixedCase []string) []string {
	var out []string
	for _, m := range portableBanned.FindAllString(sql, -1) {
		out = append(out, "mysql_only:"+m)
	}
	// Identifier check ignores single-quoted SQL string literals ('POSTRES' is
	// a value, not the "POSTRES" table) and comments, via a quote-aware scan
	// (identAuditText). Any ambiguity makes it fall back to the raw text, so
	// the check can only over-report, never hide an identifier.
	ident := identAuditText(sql)
	for _, id := range mixedCase {
		re := regexp.MustCompile("(^|[^\"A-Za-z0-9_])" + regexp.QuoteMeta(id) + "($|[^\"A-Za-z0-9_])")
		if re.MatchString(ident) {
			out = append(out, "unquoted_mixed_case:"+id)
		}
	}
	return out
}

// identAuditText is a conservative lexical pass (not a SQL parser): outside
// quotes it drops "-- " comments (only when followed by whitespace/end: the
// form both MySQL and PG treat as a comment) and /* */ comments; it replaces
// single-quoted literal bodies with ” (” escape supported); "..." and
// backtick-quoted identifiers are copied verbatim. It returns the RAW text
// (fail closed) on a backslash inside a literal (MySQL \' escape vs PG
// standard strings), an unterminated literal/identifier/comment, or a "#"
// (MySQL-only comment) outside quotes. Not modelled: PG dollar quoting
// ($$...$$, which then simply stays in the checked text) and E” strings.
func identAuditText(sql string) string {
	var b strings.Builder
	for i := 0; i < len(sql); {
		c := sql[i]
		switch {
		case c == '\'':
			j, closed := i+1, false
			for j < len(sql) {
				if sql[j] == '\\' {
					return sql
				}
				if sql[j] == '\'' {
					if j+1 < len(sql) && sql[j+1] == '\'' {
						j += 2
						continue
					}
					closed = true
					break
				}
				j++
			}
			if !closed {
				return sql
			}
			b.WriteString("''")
			i = j + 1
		case c == '"' || c == '`':
			j := strings.IndexByte(sql[i+1:], c)
			if j < 0 {
				return sql
			}
			b.WriteString(sql[i : i+j+2])
			i += j + 2
		case c == '#':
			return sql
		case c == '-' && i+1 < len(sql) && sql[i+1] == '-' && (i+2 == len(sql) || sql[i+2] == ' ' || sql[i+2] == '\t' || sql[i+2] == '\n' || sql[i+2] == '\r'):
			if j := strings.IndexByte(sql[i:], '\n'); j < 0 {
				i = len(sql)
			} else {
				i += j
			}
			b.WriteByte(' ')
		case c == '/' && i+1 < len(sql) && sql[i+1] == '*':
			j := strings.Index(sql[i+2:], "*/")
			if j < 0 {
				return sql
			}
			i += j + 4
			b.WriteByte(' ')
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

// SQLInventory walks every non-test .go file under root; mixedCase (schema
// identifiers with upper-case letters) feeds the portable audit.
func SQLInventory(root string, mixedCase ...string) (*Inventory, error) {
	inv := &Inventory{ByCategory: map[string]int{}, mixedCase: mixedCase}
	files := map[string]bool{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() && (info.Name() == "vendor" || info.Name() == "node_modules" || strings.HasPrefix(info.Name(), ".")) && path != root {
			return filepath.SkipDir
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return perr
		}
		src, _ := os.ReadFile(path)
		consts := map[string]string{}
		ast.Inspect(f, func(n ast.Node) bool {
			if vs, ok := n.(*ast.ValueSpec); ok {
				for i, name := range vs.Names {
					if i < len(vs.Values) {
						if s, ok := staticString(vs.Values[i], consts); ok {
							consts[name.Name] = s
						}
					}
				}
			}
			return true
		})
		rel, _ := filepath.Rel(root, path)
		for _, d := range f.Decls {
			fn, _ := d.(*ast.FuncDecl)
			fname := ""
			if fn != nil {
				fname = fn.Name.Name
			}
			// Pre-pass: identifiers assigned from x.Prepare/PrepareContext(...).
			stmts := map[string]bool{}
			ast.Inspect(d, func(n ast.Node) bool {
				as, ok := n.(*ast.AssignStmt)
				if !ok || len(as.Rhs) != 1 || len(as.Lhs) == 0 {
					return true
				}
				if c, ok := as.Rhs[0].(*ast.CallExpr); ok {
					if s2, ok := c.Fun.(*ast.SelectorExpr); ok && (s2.Sel.Name == "Prepare" || s2.Sel.Name == "PrepareContext") {
						if id, ok := as.Lhs[0].(*ast.Ident); ok {
							stmts[id.Name] = true
						}
					}
				}
				return true
			})
			ast.Inspect(d, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || !dbMethods[sel.Sel.Name] || len(call.Args) == 0 {
					return true
				}
				if recv, ok := sel.X.(*ast.Ident); ok && stmts[recv.Name] && !strings.HasPrefix(sel.Sel.Name, "Prepare") {
					inv.PreparedStmtCalls++
					return true
				}
				qi := 0
				if strings.HasSuffix(sel.Sel.Name, "Context") {
					qi = 1
				}
				if sel.Sel.Name == "InsertID" || sel.Sel.Name == "InsertSelectID" {
					// sqldb.InsertID/InsertSelectID(ctx, dialect, ex, q, ...); any other
					// receiver (e.g. botstore.DB.InsertID(ctx, q, ...)) has q at index 1.
					qi = 1
					if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "sqldb" {
						qi = 3
					}
				}
				if qi >= len(call.Args) {
					return true
				}
				if strings.HasPrefix(filepath.ToSlash(rel), "internal/sqldb/") {
					inv.HelperInternal++
					return true
				}
				site := CallSite{File: rel, Line: fset.Position(call.Pos()).Line, Func: fname, Method: sel.Sel.Name}
				if inner, ok := call.Args[qi].(*ast.CallExpr); ok {
					if id, ok := inner.Fun.(*ast.Ident); ok && (id.Name == "nativeSQL" || id.Name == "portableSQL" || id.Name == "selectedSQL" || id.Name == "pgOnlySQL" || id.Name == "mysqlOnlySQL" || id.Name == "blockedOnPGSQL" || id.Name == "provisionalSQL" || id.Name == "pgProvisionalSQL") && len(inner.Args) > 0 {
						site.Native = id.Name
						if s, ok := staticString(inner.Args[0], consts); ok {
							site.SQL = s
							if id.Name == "portableSQL" {
								site.Audit = PortableAudit(s, inv.mixedCase)
							}
						}
						if inv.NativeByKind == nil {
							inv.NativeByKind = map[string]int{}
						}
						inv.NativeByKind[id.Name]++
						loc := fmt.Sprintf("%s:%d", rel, site.Line)
						switch id.Name {
						case "blockedOnPGSQL":
							inv.BlockedOnPG++
							inv.BlockedList = append(inv.BlockedList, loc)
						case "provisionalSQL", "pgProvisionalSQL":
							inv.ProvisionalList = append(inv.ProvisionalList, loc)
							inv.Native++
						default:
							inv.Native++
						}
						files[rel] = true
						inv.CallSites = append(inv.CallSites, site)
						return true
					}
				}
				if s, ok := staticString(call.Args[qi], consts); ok {
					site.Static, site.SQL = true, s
					for cat, re := range mysqlCategories {
						if re.MatchString(s) {
							site.Categories = append(site.Categories, cat)
							inv.ByCategory[cat]++
						}
					}
					sort.Strings(site.Categories)
					inv.Static++
				} else {
					a := call.Args[qi]
					site.Expr = string(src[fset.Position(a.Pos()).Offset:fset.Position(a.End()).Offset])
					inv.Dynamic++
				}
				files[rel] = true
				inv.CallSites = append(inv.CallSites, site)
				return true
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	inv.Files, inv.Sites = len(files), len(inv.CallSites)
	for _, c := range inv.CallSites {
		if len(c.Audit) > 0 {
			inv.AuditFail++
		}
	}
	return inv, nil
}

// staticString resolves string literals, file constants and + of both.
func staticString(e ast.Expr, consts map[string]string) (string, bool) {
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind == token.STRING {
			s, err := strconv.Unquote(x.Value)
			return s, err == nil
		}
	case *ast.Ident:
		s, ok := consts[x.Name]
		return s, ok
	case *ast.ParenExpr:
		return staticString(x.X, consts)
	case *ast.BinaryExpr:
		if x.Op == token.ADD {
			a, ok1 := staticString(x.X, consts)
			b, ok2 := staticString(x.Y, consts)
			return a + b, ok1 && ok2
		}
	}
	return "", false
}
