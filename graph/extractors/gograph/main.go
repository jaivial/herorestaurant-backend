// Command gograph extracts a code graph from the Go backend using real type
// information (go/packages), not regex. Output is newline-delimited JSON
// (triples.jsonl) consumed by graph/loader/load.py.
//
// Deterministic by design: every node and edge here is a fact the compiler
// agrees with. No LLM is involved, so there is nothing to hallucinate. The
// fuzzy layer (docs, intent, decisions) is handled separately by DSPy.
//
// Extracted:
//   Package, File, Func, Type          nodes
//   IN, IMPORTS, DECLARES              structure edges
//   CALLS                              call graph (static callee resolution)
//   Endpoint + HANDLED_BY              chi routes, with nested prefix stacks
//   Table + READS/WRITES               SQL parsed out of query string literals
//   EnvVar + USES_ENV                  os.Getenv / os.LookupEnv
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
)

// ---------------------------------------------------------------- output model

type node struct {
	Kind  string         `json:"kind"` // "node"
	Label string         `json:"label"`
	Key   string         `json:"key"`
	Props map[string]any `json:"props"`
}

type edge struct {
	Kind      string         `json:"kind"` // "edge"
	Type      string         `json:"type"`
	FromLabel string         `json:"fromLabel"`
	FromKey   string         `json:"fromKey"`
	ToLabel   string         `json:"toLabel"`
	ToKey     string         `json:"toKey"`
	Props     map[string]any `json:"props"`
}

// emitter de-duplicates by identity so re-running is stable and the loader does
// not waste work MERGEing the same fact twice.
type emitter struct {
	enc   *json.Encoder
	seenN map[string]bool
	seenE map[string]bool
	nodes int
	edges int
}

func newEmitter(w *os.File) *emitter {
	return &emitter{enc: json.NewEncoder(w), seenN: map[string]bool{}, seenE: map[string]bool{}}
}

func (e *emitter) node(label, key string, props map[string]any) {
	if key == "" {
		return
	}
	id := label + "\x00" + key
	if e.seenN[id] {
		return
	}
	e.seenN[id] = true
	e.nodes++
	if props == nil {
		props = map[string]any{}
	}
	_ = e.enc.Encode(node{Kind: "node", Label: label, Key: key, Props: props})
}

func (e *emitter) edge(typ, fl, fk, tl, tk string, props map[string]any) {
	if fk == "" || tk == "" {
		return
	}
	id := strings.Join([]string{typ, fl, fk, tl, tk}, "\x00")
	if e.seenE[id] {
		return
	}
	e.seenE[id] = true
	e.edges++
	if props == nil {
		props = map[string]any{}
	}
	_ = e.enc.Encode(edge{Kind: "edge", Type: typ, FromLabel: fl, FromKey: fk, ToLabel: tl, ToKey: tk, Props: props})
}

// ---------------------------------------------------------------------- main

func main() {
	var (
		root    = flag.String("root", ".", "module root to analyse")
		repo    = flag.String("repo", "backend", "repo name recorded on nodes")
		out     = flag.String("out", "triples.jsonl", "output jsonl path")
		incTest = flag.Bool("tests", false, "include _test.go files")
	)
	flag.Parse()

	absRoot, err := filepath.Abs(*root)
	if err != nil {
		fatal(err)
	}
	f, err := os.Create(*out)
	if err != nil {
		fatal(err)
	}
	defer f.Close()
	em := newEmitter(f)

	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo |
			packages.NeedImports | packages.NeedDeps | packages.NeedModule,
		Dir:   absRoot,
		Tests: *incTest,
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		fatal(err)
	}

	em.node("Repo", *repo, map[string]any{"name": *repo, "lang": "go"})

	var loadErrs int
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		loadErrs += len(p.Errors)
	})

	ex := &extractor{em: em, root: absRoot, repo: *repo}
	for _, p := range pkgs {
		if p.PkgPath == "" || strings.HasSuffix(p.PkgPath, ".test") {
			continue
		}
		ex.pkg(p)
	}

	fmt.Fprintf(os.Stderr, "packages=%d nodes=%d edges=%d loadErrors=%d\n",
		len(pkgs), em.nodes, em.edges, loadErrs)
	if em.nodes == 0 {
		fatal(fmt.Errorf("no nodes extracted; check -root"))
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "gograph:", err)
	os.Exit(1)
}

// ----------------------------------------------------------------- extraction

type extractor struct {
	em   *emitter
	root string
	repo string
}

// relPath makes paths repo-relative and repo-prefixed, so File.path is stable
// and comparable across the three repos.
func (x *extractor) relPath(p string) string {
	if r, err := filepath.Rel(x.root, p); err == nil && !strings.HasPrefix(r, "..") {
		return x.repo + "/" + filepath.ToSlash(r)
	}
	return filepath.ToSlash(p)
}

func (x *extractor) pkg(p *packages.Package) {
	x.em.node("Package", p.PkgPath, map[string]any{
		"importPath": p.PkgPath, "name": p.Name, "repo": x.repo,
	})
	x.em.edge("IN", "Package", p.PkgPath, "Repo", x.repo, nil)

	for i, file := range p.Syntax {
		if i >= len(p.CompiledGoFiles) {
			continue
		}
		abs := p.CompiledGoFiles[i]
		rel := x.relPath(abs)
		lines := 0
		if fset := p.Fset.File(file.Pos()); fset != nil {
			lines = fset.LineCount()
		}
		x.em.node("File", rel, map[string]any{
			"path": rel, "repo": x.repo, "lang": "go", "loc": lines,
			"isTest": strings.HasSuffix(abs, "_test.go"),
		})
		x.em.edge("IN", "File", rel, "Package", p.PkgPath, nil)

		for _, imp := range file.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				continue
			}
			// Only record the node for third-party/stdlib; internal ones get
			// their full node when their own package is visited.
			x.em.node("Package", path, map[string]any{"importPath": path})
			x.em.edge("IMPORTS", "File", rel, "Package", path, nil)
		}

		x.walkFile(p, file, rel)
	}
}

func (x *extractor) walkFile(p *packages.Package, file *ast.File, rel string) {
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			x.typeDecls(p, d, rel)
		case *ast.FuncDecl:
			x.funcDecl(p, d, rel)
		}
	}
	// Routes are registered inside function bodies (often init/Register*), so
	// they are collected in a second pass over the whole file.
	x.routes(p, file, rel)
}

func (x *extractor) typeDecls(p *packages.Package, d *ast.GenDecl, rel string) {
	if d.Tok != token.TYPE {
		return
	}
	for _, spec := range d.Specs {
		ts, ok := spec.(*ast.TypeSpec)
		if !ok {
			continue
		}
		key := p.PkgPath + "." + ts.Name.Name
		line := p.Fset.Position(ts.Pos()).Line
		x.em.node("Type", key, map[string]any{
			"key": key, "name": ts.Name.Name, "pkg": p.PkgPath,
			"file": rel, "line": line, "exported": ts.Name.IsExported(),
		})
		x.em.edge("DECLARES", "File", rel, "Type", key, map[string]any{"line": line})
	}
}

// funcKey builds a stable identity for a function or method, including the
// receiver so (*Server).handleX never collides with a free function.
func funcKey(fn *types.Func) string {
	if fn == nil {
		return ""
	}
	sig, _ := fn.Type().(*types.Signature)
	pkgPath := ""
	if fn.Pkg() != nil {
		pkgPath = fn.Pkg().Path()
	}
	if sig != nil && sig.Recv() != nil {
		recv := sig.Recv().Type()
		if ptr, ok := recv.(*types.Pointer); ok {
			return fmt.Sprintf("%s.(*%s).%s", pkgPath, typeName(ptr.Elem()), fn.Name())
		}
		return fmt.Sprintf("%s.%s.%s", pkgPath, typeName(recv), fn.Name())
	}
	if pkgPath == "" {
		return fn.Name()
	}
	return pkgPath + "." + fn.Name()
}

func typeName(t types.Type) string {
	if named, ok := t.(*types.Named); ok {
		return named.Obj().Name()
	}
	return types.TypeString(t, nil)
}

func (x *extractor) funcDecl(p *packages.Package, d *ast.FuncDecl, rel string) {
	obj, _ := p.TypesInfo.Defs[d.Name].(*types.Func)
	key := funcKey(obj)
	if key == "" {
		return
	}
	pos := p.Fset.Position(d.Pos())
	end := p.Fset.Position(d.End())
	recv := ""
	if d.Recv != nil && len(d.Recv.List) > 0 {
		recv = types.ExprString(d.Recv.List[0].Type)
	}
	x.em.node("Func", key, map[string]any{
		"key": key, "name": d.Name.Name, "pkg": p.PkgPath, "file": rel,
		"line": pos.Line, "endLine": end.Line, "loc": end.Line - pos.Line + 1,
		"exported": d.Name.IsExported(), "recv": recv,
		"doc": firstDocLine(d.Doc),
	})
	x.em.edge("DECLARES", "File", rel, "Func", key, map[string]any{"line": pos.Line})

	if d.Body == nil {
		return
	}
	x.bodyFacts(p, d.Body, key, rel)
}

func firstDocLine(cg *ast.CommentGroup) string {
	if cg == nil {
		return ""
	}
	t := strings.TrimSpace(cg.Text())
	if i := strings.IndexByte(t, '\n'); i > 0 {
		t = t[:i]
	}
	if len(t) > 300 {
		t = t[:300]
	}
	return t
}

// bodyFacts records CALLS, SQL table access and env var usage from a function
// body, resolving callees through the type checker.
func (x *extractor) bodyFacts(p *packages.Package, body *ast.BlockStmt, fromKey, rel string) {
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		callee := x.calleeFunc(p, call)
		if callee != nil {
			if to := funcKey(callee); to != "" && to != fromKey {
				line := p.Fset.Position(call.Pos()).Line
				// Only emit a node for callees outside the analysed module; in-module
				// ones get full properties from their own declaration.
				x.em.node("Func", to, map[string]any{"key": to, "name": callee.Name()})
				x.em.edge("CALLS", "Func", fromKey, "Func", to, map[string]any{"line": line})
			}
			x.sqlFacts(p, call, callee, fromKey, rel)
			x.envFacts(p, call, callee, fromKey)
		}
		return true
	})
}

func (x *extractor) calleeFunc(p *packages.Package, call *ast.CallExpr) *types.Func {
	switch fun := ast.Unparen(call.Fun).(type) {
	case *ast.Ident:
		if fn, ok := p.TypesInfo.Uses[fun].(*types.Func); ok {
			return fn
		}
	case *ast.SelectorExpr:
		if sel, ok := p.TypesInfo.Selections[fun]; ok {
			if fn, ok := sel.Obj().(*types.Func); ok {
				return fn
			}
		}
		if fn, ok := p.TypesInfo.Uses[fun.Sel].(*types.Func); ok {
			return fn
		}
	}
	return nil
}

// ------------------------------------------------------------------ SQL facts

var (
	reFrom   = regexp.MustCompile(`(?is)\bfrom\s+` + identPat)
	reJoin   = regexp.MustCompile(`(?is)\bjoin\s+` + identPat)
	reInto   = regexp.MustCompile(`(?is)\binsert\s+(?:ignore\s+)?into\s+` + identPat)
	reUpdate = regexp.MustCompile(`(?is)\bupdate\s+` + identPat)
	// `FOR UPDATE [SKIP LOCKED|NOWAIT]` is row locking, not a write target.
	reForUpdate = regexp.MustCompile(`(?is)\bfor\s+update\b(\s+skip\s+locked|\s+nowait)?`)
	reDelete = regexp.MustCompile(`(?is)\bdelete\s+from\s+` + identPat)
	reRepl   = regexp.MustCompile(`(?is)\breplace\s+into\s+` + identPat)
)

const identPat = "`?([A-Za-z_][A-Za-z0-9_]*)`?"

// reDupKey strips `ON DUPLICATE KEY UPDATE ...` before table matching: that
// clause is followed by column names, not a table, and would otherwise be
// misread as an UPDATE target.
var reDupKey = regexp.MustCompile(`(?is)\bon\s+duplicate\s+key\s+update\b`)

// sqlDrivers are the database/sql entry points whose first string-literal
// argument is a query.
var sqlDrivers = map[string]bool{
	"QueryContext": true, "QueryRowContext": true, "ExecContext": true,
	"Query": true, "QueryRow": true, "Exec": true, "PrepareContext": true, "Prepare": true,
}

func (x *extractor) sqlFacts(p *packages.Package, call *ast.CallExpr, callee *types.Func, fromKey, rel string) {
	if !sqlDrivers[callee.Name()] {
		return
	}
	sql, ok := firstStringLit(p, call)
	if !ok || sql == "" {
		return
	}
	line := p.Fset.Position(call.Pos()).Line

	// The upsert tail lists columns, so table matching must not see it.
	scan := sql
	if m := reDupKey.FindStringIndex(scan); m != nil {
		scan = scan[:m[0]]
	}
	scan = reForUpdate.ReplaceAllString(scan, " ")

	for _, re := range []*regexp.Regexp{reFrom, reJoin} {
		for _, m := range re.FindAllStringSubmatch(scan, -1) {
			x.table(m[1], "READS", fromKey, line, rel)
		}
	}
	for _, re := range []*regexp.Regexp{reInto, reUpdate, reDelete, reRepl} {
		for _, m := range re.FindAllStringSubmatch(scan, -1) {
			x.table(m[1], "WRITES", fromKey, line, rel)
		}
	}
}

var sqlNoise = map[string]bool{
	"select": true, "dual": true, "values": true, "set": true, "where": true,
	"as": true, "on": true, "and": true, "or": true, "duplicate": true, "key": true,
}

func (x *extractor) table(name, relType, fromKey string, line int, file string) {
	t := strings.ToLower(strings.TrimSpace(name))
	if t == "" || sqlNoise[t] {
		return
	}
	x.em.node("Table", t, map[string]any{"name": t, "db": "mysql"})
	x.em.edge(relType, "Func", fromKey, "Table", t, map[string]any{"line": line, "file": file})
}

// firstStringLit returns the first argument that is a constant string, which
// covers both "..." and `...` query literals.
func firstStringLit(p *packages.Package, call *ast.CallExpr) (string, bool) {
	for _, arg := range call.Args {
		if tv, ok := p.TypesInfo.Types[arg]; ok && tv.Value != nil {
			if s, err := strconv.Unquote(tv.Value.ExactString()); err == nil {
				return s, true
			}
			if tv.Value.Kind() == 4 { // constant.String
				return strings.Trim(tv.Value.String(), `"`), true
			}
		}
		if bl, ok := arg.(*ast.BasicLit); ok && bl.Kind == token.STRING {
			if s, err := strconv.Unquote(bl.Value); err == nil {
				return s, true
			}
		}
	}
	return "", false
}

// ------------------------------------------------------------------ env facts

func (x *extractor) envFacts(p *packages.Package, call *ast.CallExpr, callee *types.Func, fromKey string) {
	if callee.Pkg() == nil || callee.Pkg().Path() != "os" {
		return
	}
	if callee.Name() != "Getenv" && callee.Name() != "LookupEnv" {
		return
	}
	name, ok := firstStringLit(p, call)
	if !ok || name == "" {
		return
	}
	x.em.node("EnvVar", name, map[string]any{"name": name})
	x.em.edge("USES_ENV", "Func", fromKey, "EnvVar", name,
		map[string]any{"line": p.Fset.Position(call.Pos()).Line})
}

// -------------------------------------------------------------- chi endpoints

var httpVerbs = map[string]string{
	"Get": "GET", "Post": "POST", "Put": "PUT", "Delete": "DELETE",
	"Patch": "PATCH", "Head": "HEAD", "Options": "OPTIONS",
}

// routes walks the file looking for chi route registrations. Correctness here
// depends on two things a regex cannot do:
//   1. confirming the receiver is really a chi.Router (r.Header.Get(...) is not
//      a route, but matches any naive /r\.Get\(/ pattern),
//   2. tracking the enclosing Route("prefix") stack to build the full path.
func (x *extractor) routes(p *packages.Package, file *ast.File, rel string) {
	var walk func(n ast.Node, prefix string, enclosing string)
	walk = func(n ast.Node, prefix, enclosing string) {
		ast.Inspect(n, func(nn ast.Node) bool {
			call, ok := nn.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr)
			if !ok || !x.isChiRouter(p, sel.X) {
				return true
			}
			name := sel.Sel.Name

			// Nested scopes: Route("/admin", func(r chi.Router){...}) and
			// Group/With(...) which inherit the current prefix.
			if name == "Route" || name == "Mount" {
				sub, _ := literalString(p, call, 0)
				if fn := lastFuncLit(call); fn != nil {
					walk(fn.Body, joinPath(prefix, sub), enclosing)
					return false
				}
				return false
			}
			if name == "Group" || name == "With" {
				if fn := lastFuncLit(call); fn != nil {
					walk(fn.Body, prefix, enclosing)
					return false
				}
				return true
			}

			verb, isVerb := httpVerbs[name]
			if !isVerb && name != "Handle" && name != "HandleFunc" {
				return true
			}
			pat, ok := literalString(p, call, 0)
			if !ok {
				return true
			}
			if !isVerb {
				verb = "ANY"
			}
			full := joinPath(prefix, pat)
			key := verb + " " + full
			line := p.Fset.Position(call.Pos()).Line

			x.em.node("Endpoint", key, map[string]any{
				"key": key, "method": verb, "path": full, "repo": x.repo,
				"file": rel, "line": line,
			})
			x.em.edge("DECLARES", "File", rel, "Endpoint", key, map[string]any{"line": line})

			// Handler: last argument that resolves to a func.
			if h := x.handlerFunc(p, call); h != "" {
				x.em.edge("HANDLED_BY", "Endpoint", key, "Func", h, map[string]any{"line": line})
			}
			return true
		})
	}
	walk(file, "", "")
}

// isChiRouter verifies via the type checker that the selector receiver is a
// chi.Router / *chi.Mux, eliminating false positives like r.Header.Get.
func (x *extractor) isChiRouter(p *packages.Package, expr ast.Expr) bool {
	tv, ok := p.TypesInfo.Types[expr]
	if !ok || tv.Type == nil {
		return false
	}
	s := tv.Type.String()
	return strings.Contains(s, "go-chi/chi") &&
		(strings.HasSuffix(s, ".Router") || strings.HasSuffix(s, ".Mux"))
}

// handlerFunc resolves the handler argument of a route registration. Three
// shapes occur in this codebase:
//   s.handleFoo                     -> method value
//   http.HandlerFunc(s.handleFoo)   -> conversion, unwrap to the inner func
//   handleListSites(db)             -> factory returning an http.Handler
func (x *extractor) handlerFunc(p *packages.Package, call *ast.CallExpr) string {
	for i := len(call.Args) - 1; i >= 0; i-- {
		arg := ast.Unparen(call.Args[i])

		if c, ok := arg.(*ast.CallExpr); ok {
			// A conversion names a type (http.HandlerFunc) rather than a func;
			// a factory names a func whose result is the handler. Only the
			// former should be unwrapped.
			if callee := x.calleeFunc(p, c); callee != nil {
				return funcKey(callee)
			}
			if len(c.Args) == 1 {
				arg = ast.Unparen(c.Args[0])
			}
		}

		switch a := arg.(type) {
		case *ast.SelectorExpr:
			if sel, ok := p.TypesInfo.Selections[a]; ok {
				if fn, ok := sel.Obj().(*types.Func); ok {
					return funcKey(fn)
				}
			}
			if fn, ok := p.TypesInfo.Uses[a.Sel].(*types.Func); ok {
				return funcKey(fn)
			}
		case *ast.Ident:
			if fn, ok := p.TypesInfo.Uses[a].(*types.Func); ok {
				return funcKey(fn)
			}
		}
	}
	return ""
}

func literalString(p *packages.Package, call *ast.CallExpr, idx int) (string, bool) {
	if idx >= len(call.Args) {
		return "", false
	}
	arg := call.Args[idx]
	if tv, ok := p.TypesInfo.Types[arg]; ok && tv.Value != nil {
		if s, err := strconv.Unquote(tv.Value.ExactString()); err == nil {
			return s, true
		}
	}
	if bl, ok := arg.(*ast.BasicLit); ok && bl.Kind == token.STRING {
		if s, err := strconv.Unquote(bl.Value); err == nil {
			return s, true
		}
	}
	return "", false
}

func lastFuncLit(call *ast.CallExpr) *ast.FuncLit {
	for i := len(call.Args) - 1; i >= 0; i-- {
		if fl, ok := ast.Unparen(call.Args[i]).(*ast.FuncLit); ok {
			return fl
		}
	}
	return nil
}

func joinPath(prefix, p string) string {
	switch {
	case prefix == "" && p == "":
		return "/"
	case prefix == "":
		return ensureLead(p)
	case p == "" || p == "/":
		return ensureLead(prefix)
	}
	return strings.TrimSuffix(ensureLead(prefix), "/") + ensureLead(p)
}

func ensureLead(p string) string {
	if p == "" {
		return ""
	}
	if p[0] != '/' {
		return "/" + p
	}
	return p
}

