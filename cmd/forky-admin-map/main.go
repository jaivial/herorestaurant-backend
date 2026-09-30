// forky-admin-map builds internal/api/forky_admin_ops.json — the map of every
// backoffice `/admin` route Forky's admin trio (admin_catalog / admin_describe /
// admin_call) may reach — from the route table in internal/api/server.go and the
// backoffice pages that call each route. [FORKY-ADMIN-TOOLS-S01]
//
//	go run ./cmd/forky-admin-map -server internal/api/server.go -pages ../backoffice -o internal/api/forky_admin_ops.json
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type op struct {
	Name        string   `json:"name"`
	Method      string   `json:"method"`
	Path        string   `json:"path"`
	Handler     string   `json:"handler"`
	Gates       []string `json:"gates"`
	Section     string   `json:"section"`
	RolesAdmin  bool     `json:"roles_admin,omitempty"`
	Write       bool     `json:"write"`
	Risk        string   `json:"risk"`
	Description string   `json:"description"`
	Screens     []string `json:"screens"`
}

var (
	routeRe   = regexp.MustCompile(`r\.(?:With\(([^)]*)\)\.)?(Get|Post|Put|Patch|Delete)\("([^"]+)",\s*s\.(\w+)`)
	slugRe    = regexp.MustCompile(`[^a-z0-9]+`)
	skipHdlRe = regexp.MustCompile(`WS$|WebSocket|Upload|Image|Avatar|Import|OCR`)
	destRe    = regexp.MustCompile(`/(cancel|delete|purge|reset|void|refund|revoke|remove)(/|$)`)
	callRe    = regexp.MustCompile(`api\.(\w+)\.(\w+)\(`)
	groupRe   = regexp.MustCompile(`^(\s*)(\w+): \{\s*$`)
	methodRe  = regexp.MustCompile(`^(\s*)(?:async )?(\w+)\(`)
	clientRe  = regexp.MustCompile("[`\"']/api/admin(/[^`\"'?$]*)")
	// Section of each gate variable; the first match wins.
	sections = []struct{ prefix, section string }{
		{"reservasGate", "reservas"}, {"menusGate", "menus"}, {"ajustesGate", "ajustes"},
		{"miembrosGate", "miembros"}, {"fichajeGate", "fichaje"}, {"horariosGate", "horarios"},
		{"facturasGate", "facturas"}, {"statisticsGate", "estadisticas"}, {"campaignsGate", "campanas"},
		{"adsGate", "anuncios"}, {"stock", "stock"}, {"sheets", "stock"}, {"productionTypeGate", "stock"},
		{"pos", "pos"}, {"s.requireBOPOS", "pos"}, {"rootOnlyGate", "root"}, {"s.requireBOSuperadmin", "superadmin"},
	}
)

func main() {
	server := flag.String("server", "internal/api/server.go", "server.go with the /admin routes")
	pages := flag.String("pages", "", "backoffice checkout (for screens)")
	out := flag.String("o", "internal/api/forky_admin_ops.json", "output file")
	flag.Parse()

	src, err := os.ReadFile(*server)
	if err != nil {
		log.Fatal(err)
	}
	text := string(src)
	start, end := strings.Index(text, `r.Route("/admin", func(r chi.Router) {`), strings.Index(text, `r.Route("/widget"`)
	if start < 0 || end < start {
		log.Fatal("admin route block not found")
	}
	lines := strings.Split(text[start:end], "\n")
	screens, sources := loadScreens(*pages)
	clientPaths := loadClientPaths(*pages)
	// A page reaches a route directly or through the api/client.ts method it calls.
	for route, src := range sources {
		var extra strings.Builder
		for _, m := range callRe.FindAllStringSubmatch(src, -1) {
			for _, p := range clientPaths[m[1]+"."+m[2]] {
				extra.WriteString(" /admin" + p)
			}
		}
		sources[route] = src + extra.String()
	}

	ops, taken := []op{}, map[string]bool{}
	for i, line := range lines {
		m := routeRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		gates := splitGates(m[1])
		method, path, handler := strings.ToUpper(m[2]), m[3], m[4]
		if !contains(gates, "s.requireBOSession") || skipHdlRe.MatchString(handler) ||
			path == "/login" || path == "/logout" || strings.HasPrefix(path, "/assistant/") || strings.HasSuffix(path, "/ws") {
			continue
		}
		o := op{Method: method, Path: path, Handler: handler, Gates: gates, Section: sectionOf(gates),
			RolesAdmin: contains(gates, "rolesAdminGate"), Write: method != "GET", Risk: riskOf(method, path),
			Description: docAbove(lines, i), Screens: screensOf(path, sources)}
		if o.Description == "" {
			o.Description = fmt.Sprintf("%s %s (%s)", method, path, handler)
		}
		o.Name = uniqueName(method, path, taken)
		ops = append(ops, o)
	}
	sort.Slice(ops, func(a, b int) bool { return ops[a].Path+ops[a].Method < ops[b].Path+ops[b].Method })
	doc := map[string]any{"version": 1, "coordination_id": "FORKY-ADMIN-TOOLS-S01", "screens": screens, "operations": ops}
	b, _ := json.MarshalIndent(doc, "", " ")
	if err := os.WriteFile(*out, append(b, '\n'), 0o644); err != nil {
		log.Fatal(err)
	}
	count := map[string]int{}
	for _, o := range ops {
		count[o.Section]++
	}
	fmt.Fprintf(os.Stderr, "forky.admin.map coord=FORKY-ADMIN-TOOLS-S01 operations=%d screens=%d sections=%v\n", len(ops), len(screens), count)
}

func splitGates(raw string) []string {
	out := []string{}
	for _, g := range strings.Split(raw, ",") {
		if g = strings.TrimSpace(g); g != "" {
			out = append(out, g)
		}
	}
	return out
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func sectionOf(gates []string) string {
	for _, g := range gates {
		for _, s := range sections {
			if strings.HasPrefix(g, s.prefix) {
				return s.section
			}
		}
	}
	return "session"
}

func riskOf(method, path string) string {
	switch {
	case method == "GET":
		return "read"
	case method == "DELETE" || destRe.MatchString(path):
		return "destructive"
	}
	return "write"
}

// docAbove joins the `//` comment lines right above a route line (skipping coordination ids).
func docAbove(lines []string, i int) string {
	var parts []string
	for j := i - 1; j >= 0; j-- {
		t := strings.TrimSpace(lines[j])
		if !strings.HasPrefix(t, "//") {
			break
		}
		t = strings.TrimSpace(strings.TrimPrefix(t, "//"))
		if !strings.HasPrefix(strings.ToLower(t), "coordination id") {
			parts = append([]string{t}, parts...)
		}
	}
	return strings.Join(parts, " ")
}

func uniqueName(method, path string, taken map[string]bool) string {
	slug := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(path), "_"), "_")
	base := "admin_" + strings.ToLower(method) + "_" + slug
	if len(base) > 60 {
		base = base[:60]
	}
	name := base
	for n := 2; taken[name]; n++ {
		name = fmt.Sprintf("%s_%d", base, n)
	}
	taken[name] = true
	return name
}

// loadScreens returns the page routes and, per route, the text of its page directory.
func loadScreens(root string) ([]string, map[string]string) {
	screens, sources := []string{}, map[string]string{}
	if root == "" {
		return screens, sources
	}
	for _, base := range []string{"pages/app", "pages/m/app"} {
		dir := filepath.Join(root, base)
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || d.Name() != "+Page.tsx" {
				return nil
			}
			pageDir := filepath.Dir(p)
			rel, _ := filepath.Rel(filepath.Join(root, "pages"), pageDir)
			route := "/" + strings.ReplaceAll(filepath.ToSlash(rel), "@", ":")
			screens = append(screens, route)
			var sb strings.Builder
			files, _ := os.ReadDir(pageDir)
			for _, f := range files {
				if !f.IsDir() && (strings.HasSuffix(f.Name(), ".ts") || strings.HasSuffix(f.Name(), ".tsx")) {
					b, _ := os.ReadFile(filepath.Join(pageDir, f.Name()))
					sb.Write(b)
				}
			}
			sources[route] = sb.String()
			return nil
		})
	}
	sort.Strings(screens)
	return screens, sources
}

// screensOf lists the pages whose own files call the route's static prefix.
func screensOf(path string, sources map[string]string) []string {
	prefix := path
	if i := strings.Index(prefix, "{"); i >= 0 {
		prefix = prefix[:i]
	}
	prefix = strings.TrimRight(prefix, "/")
	out := []string{}
	if len(prefix) < 3 {
		return out
	}
	for route, src := range sources {
		if strings.Contains(src, "/admin"+prefix) {
			out = append(out, route)
		}
	}
	sort.Strings(out)
	return out
}

// loadClientPaths maps each `api.<group>.<method>` of the backoffice api/client.ts to
// the /api/admin paths it calls (indentation tells the group and the method).
func loadClientPaths(root string) map[string][]string {
	out := map[string][]string{}
	b, err := os.ReadFile(filepath.Join(root, "api", "client.ts"))
	if err != nil {
		return out
	}
	type frame struct {
		indent int
		name   string
	}
	var groups []frame
	method, methodIndent := "", 0
	for _, line := range strings.Split(string(b), "\n") {
		if m := groupRe.FindStringSubmatch(line); m != nil {
			ind := len(m[1])
			for len(groups) > 0 && groups[len(groups)-1].indent >= ind {
				groups = groups[:len(groups)-1]
			}
			groups = append(groups, frame{ind, m[2]})
		}
		if m := methodRe.FindStringSubmatch(line); m != nil {
			method, methodIndent = m[2], len(m[1])
		}
		for _, p := range clientRe.FindAllStringSubmatch(line, -1) {
			group := ""
			for _, g := range groups {
				if g.indent < methodIndent {
					group = g.name
				}
			}
			if group != "" && method != "" {
				key := group + "." + method
				out[key] = append(out[key], p[1])
			}
		}
	}
	return out
}
