// forky-admin-map builds internal/api/forky_admin_ops.json — the map of every
// backoffice `/admin` route Forky's admin trio (admin_catalog / admin_describe /
// admin_call) may reach — from the route table in internal/api/server.go, the
// sub-routers mounted inside its `/admin` block (website_builder.go,
// site_builder.go, instatic_manager.go) and the backoffice pages that call each
// route. [FORKY-ADMIN-TOOLS-S01]
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
	// Body is the exact request contract (fields, types, rules) when documented
	// in bodiesEs; admin_describe returns it so writes are not guessed.
	Body    string   `json:"body,omitempty"`
	Screens []string `json:"screens"`
}

var (
	routeRe   = regexp.MustCompile(`r\.(?:With\(([^)]*)\)\.)?(Get|Post|Put|Patch|Delete)\("([^"]+)",\s*([A-Za-z0-9_.]+(?:\([^)]*\))?)`)
	nestedRe  = regexp.MustCompile(`\br\.Route\("([^"]+)",\s*func`)
	slugRe    = regexp.MustCompile(`[^a-z0-9]+`)
	skipHdlRe = regexp.MustCompile(`WS$|WebSocket|Upload|Image|Avatar|Import|OCR`)
	// keepHdlRe are handlers caught by skipHdlRe whose bodies are plain JSON:
	// admin_call replays JSON only, so they remain operable while multipart
	// uploads, OCR scans and WS streams do not.
	keepHdlRe = regexp.MustCompile(`^(handleBOTechnicalSheetStepImageJob(Get|Create)|handleBOPOSImport(Preview|Confirm)|handleBOGroupMenusV2(DeleteSliderImage|ReorderSliderImages|DeleteSpecialSectionImage)|handleBOAdImageGenerate|handleBOAIImage(Status|ProvidersGet|ConfigGet|ConfigSet)|handleUploadAsset)$`)
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

// routeSource is a place where the `/admin` tree registers routes: the big
// block in server.go plus the sub-routers mounted inside it, whose mount gates
// gate every route they register. close is a marker line ending the block
// ("" = brace-matched func). [FORKY-ADMIN-TOOLS-S01]
type routeSource struct {
	file  string // file with the routes, relative to the server.go directory
	open  string // line that opens the registration block
	close string // marker line where the block ends ("" = brace-matched)
	mount string // gates of the mount point ("" = written on each route line)
}

var routeSources = []routeSource{
	{"server.go", `r.Route("/admin", func(r chi.Router) {`, `r.Route("/widget"`, ""},
	{"website_builder.go", "func (wb *WebsiteBuilder) RegisterRoutes", "", "s.requireBOSession, ajustesGate"},
	{"site_builder.go", "func RegisterSiteBuilderRoutes", "", "s.requireBOSession, ajustesGate"},
	{"instatic_manager.go", "func (m *instaticManager) RegisterInstaticRoutes", "", "s.requireBOSession, ajustesGate"},
}

// descriptionsEs holds the Spanish description of each operation, keyed by
// "METHOD /path". Routes without an entry fall back to the comment above them.
// [FORKY-ADMIN-TOOLS-S01]
var descriptionsEs = map[string]string{
	// website-builder (constructor de web por secciones).
	"GET /website-builder/templates":                        "Lista las plantillas disponibles del constructor de web.",
	"GET /website-builder/templates/{id}":                   "Devuelve una plantilla del constructor de web.",
	"GET /website-builder/website":                          "Devuelve la web del restaurante (modelo por secciones).",
	"POST /website-builder/website":                         "Crea la web del restaurante.",
	"PUT /website-builder/website":                          "Actualiza la web del restaurante.",
	"DELETE /website-builder/website":                       "Elimina la web del restaurante.",
	"GET /website-builder/pages":                            "Lista las páginas de la web.",
	"POST /website-builder/pages":                           "Crea una página en la web.",
	"PUT /website-builder/pages/{id}":                       "Actualiza una página de la web.",
	"DELETE /website-builder/pages/{id}":                    "Elimina una página de la web.",
	"GET /website-builder/pages/{pageId}/sections":          "Lista las secciones de una página.",
	"POST /website-builder/pages/{pageId}/sections":         "Crea una sección en una página.",
	"PUT /website-builder/sections/{id}":                    "Actualiza una sección de una página.",
	"DELETE /website-builder/sections/{id}":                 "Elimina una sección de una página.",
	"PUT /website-builder/sections/reorder":                 "Reordena las secciones de una página.",
	"GET /website-builder/components":                       "Lista los componentes disponibles para las secciones.",
	"GET /website-builder/components/{id}":                  "Devuelve un componente de sección.",
	"GET /website-builder/sections/{sectionId}/components":  "Lista los componentes de una sección.",
	"POST /website-builder/sections/{sectionId}/components": "Añade un componente a una sección.",
	"PUT /website-builder/section-components/{id}":          "Actualiza un componente de una sección.",
	"DELETE /website-builder/section-components/{id}":       "Elimina un componente de una sección.",
	"PUT /website-builder/section-components/reorder":       "Reordena los componentes de una sección.",
	"GET /website-builder/assets":                           "Lista los recursos multimedia de la web.",
	"DELETE /website-builder/assets/{id}":                   "Elimina un recurso multimedia de la web.",
	"POST /website-builder/publish":                         "Publica la web del restaurante.",
	"GET /website-builder/preview":                          "Devuelve la vista previa de la web.",
	"GET /website-builder/history":                          "Lista el historial de publicaciones de la web.",
	// site-builder (editor visual).
	"GET /site-builder/sites":                         "Lista los sitios del editor visual.",
	"POST /site-builder/sites":                        "Crea un sitio en el editor visual.",
	"GET /site-builder/sites/{siteId}":                "Devuelve un sitio del editor visual.",
	"PUT /site-builder/sites/{siteId}":                "Actualiza un sitio del editor visual.",
	"DELETE /site-builder/sites/{siteId}":             "Elimina un sitio del editor visual.",
	"GET /site-builder/sites/{siteId}/pages":          "Lista las páginas de un sitio.",
	"POST /site-builder/sites/{siteId}/pages":         "Crea una página en un sitio.",
	"GET /site-builder/pages/{pageId}":                "Devuelve una página de un sitio.",
	"PUT /site-builder/pages/{pageId}":                "Actualiza una página de un sitio.",
	"DELETE /site-builder/pages/{pageId}":             "Elimina una página de un sitio.",
	"GET /site-builder/sites/{siteId}/assets":         "Lista los recursos de un sitio.",
	"POST /site-builder/sites/{siteId}/assets":        "Registra un recurso (por URL) en un sitio.",
	"GET /site-builder/assets/{assetId}":              "Devuelve un recurso de un sitio.",
	"DELETE /site-builder/assets/{assetId}":           "Elimina un recurso de un sitio.",
	"GET /site-builder/sites/{siteId}/versions":       "Lista las versiones de un sitio.",
	"POST /site-builder/sites/{siteId}/versions":      "Crea una versión de un sitio.",
	"GET /site-builder/versions/{versionId}":          "Devuelve una versión de un sitio.",
	"GET /site-builder/components":                    "Lista los componentes del editor visual.",
	"GET /site-builder/components/{type}":             "Devuelve un componente del editor visual por tipo.",
	"GET /site-builder/sites/{siteId}/bindings":       "Lista las vinculaciones de datos de un sitio.",
	"POST /site-builder/sites/{siteId}/bindings":      "Crea una vinculación de datos en un sitio.",
	"PUT /site-builder/bindings/{bindingId}":          "Actualiza una vinculación de datos.",
	"DELETE /site-builder/bindings/{bindingId}":       "Elimina una vinculación de datos.",
	"POST /site-builder/sites/{siteId}/publish":       "Publica un sitio del editor visual.",
	"GET /site-builder/sites/{siteId}/publish-status": "Devuelve el estado de publicación de un sitio.",
	"GET /site-builder/sites/{siteId}/domains":        "Lista los dominios de un sitio.",
	"POST /site-builder/sites/{siteId}/domains":       "Añade un dominio a un sitio.",
	"DELETE /site-builder/domains/{domainId}":         "Elimina un dominio de un sitio.",
	"POST /site-builder/domains/{domainId}/verify":    "Verifica el dominio de un sitio.",
	// instatic (instancia web del restaurante).
	"GET /instatic/status/{restaurantId}":            "Devuelve el estado de la instancia web del restaurante.",
	"POST /instatic/ensure/{restaurantId}":           "Arranca la instancia web del restaurante si no está activa.",
	"POST /instatic/seed/{restaurantId}":             "Siembra la instancia web del restaurante con su plantilla.",
	"POST /instatic/publish/{restaurantId}":          "Publica la instancia web del restaurante.",
	"POST /instatic/provision-domain/{restaurantId}": "Aprovisiona el dominio de la instancia web del restaurante.",
	// operaciones JSON de server.go fuera de las familias anteriores.
	"GET /comida/technical-sheets/{id}/steps/{stepId}/image-jobs":    "Lista los trabajos de imagen de un paso de ficha técnica.",
	"POST /comida/technical-sheets/{id}/steps/{stepId}/image-jobs":   "Crea un trabajo de imagen para un paso de ficha técnica.",
	"POST /pos/products/import-preview":                              "Previsualiza la importación de productos de la carta al catálogo POS.",
	"POST /pos/products/import-confirm":                              "Confirma la importación de productos de la carta al catálogo POS.",
	"DELETE /group-menus-v2/{id}/slider/images/{imageId}":            "Elimina una imagen del carrusel de un menú de grupo.",
	"PUT /group-menus-v2/{id}/slider/images":                         "Reordena las imágenes del carrusel de un menú de grupo.",
	"DELETE /group-menus-v2/{id}/special-sections/{sectionId}/image": "Elimina la imagen de una sección especial de un menú de grupo.",
	"POST /config/ads/{adId}/image/generate":                         "Genera con IA la imagen de un anuncio.",
	"GET /comida/ai-image/status":                                    "Devuelve el estado de la configuración de imágenes IA.",
	"GET /config/ai-image/providers":                                 "Lista los proveedores de generación de imágenes IA.",
	"GET /config/ai-image":                                           "Devuelve la configuración de imágenes IA.",
	"POST /config/ai-image":                                          "Actualiza la configuración de imágenes IA.",
	"POST /campanas/email-validation":                                "Valida los correos de una campaña antes de enviarla.",
	"POST /campanas/bounces/sync":                                    "Sincroniza los rebotes de correo de las campañas.",
	"GET /bot/ai/{restaurantId}/privileged-contacts":                 "Devuelve los contactos privilegiados del bot de WhatsApp.",
	"PUT /bot/ai/{restaurantId}/privileged-contacts":                 "Actualiza los contactos privilegiados del bot de WhatsApp.",
}

func main() {
	server := flag.String("server", "internal/api/server.go", "server.go with the /admin routes")
	pages := flag.String("pages", "", "backoffice checkout (for screens)")
	out := flag.String("o", "internal/api/forky_admin_ops.json", "output file")
	flag.Parse()

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
	for _, rs := range routeSources {
		lines, err := rs.lines(filepath.Dir(*server))
		if err != nil {
			log.Fatal(err)
		}
		ops = append(ops, scanRoutes(lines, rs.mount, taken, sources)...)
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

// scanRoutes turns the route lines of one registration block into operations,
// honouring nested r.Route("prefix", ...) groups. mount gates apply to routes
// registered without their own r.With(...).
func scanRoutes(lines []string, mount string, taken map[string]bool, sources map[string]string) []op {
	ops := []op{}
	type group struct {
		depth  int
		prefix string
	}
	var stack []group
	depth := 0
	for i, line := range lines {
		m := routeRe.FindStringSubmatch(line)
		if m == nil {
			if g := nestedRe.FindStringSubmatch(line); g != nil && i > 0 {
				stack = append(stack, group{depth, g[1]})
			}
		} else {
			gates := splitGates(m[1])
			if len(gates) == 0 {
				gates = splitGates(mount)
			}
			method, path, handler := strings.ToUpper(m[2]), m[3], handlerName(m[4])
			if !contains(gates, "s.requireBOSession") || (skipHdlRe.MatchString(handler) && !keepHdlRe.MatchString(handler)) ||
				path == "/login" || path == "/logout" || strings.HasPrefix(path, "/assistant/") || strings.HasSuffix(path, "/ws") {
				continue
			}
			for _, g := range stack {
				path = g.prefix + path
			}
			desc := descriptionsEs[method+" "+path]
			if desc == "" {
				desc = docAbove(lines, i)
			}
			o := op{Method: method, Path: path, Handler: handler, Gates: gates, Section: sectionOf(gates),
				RolesAdmin: contains(gates, "rolesAdminGate"), Write: method != "GET", Risk: riskOf(method, path),
				Description: desc, Body: bodiesEs[method+" "+path], Screens: screensOf(path, sources)}
			if o.Description == "" {
				o.Description = fmt.Sprintf("%s %s (%s)", method, path, handler)
			}
			o.Name = uniqueName(method, path, taken)
			ops = append(ops, o)
		}
		depth += strings.Count(line, "{") - strings.Count(line, "}")
		for len(stack) > 0 && depth <= stack[len(stack)-1].depth {
			stack = stack[:len(stack)-1]
		}
	}
	return ops
}

// lines returns the lines of a route registration block.
func (rs routeSource) lines(dir string) ([]string, error) {
	b, err := os.ReadFile(filepath.Join(dir, rs.file))
	if err != nil {
		return nil, err
	}
	text := string(b)
	start := strings.Index(text, rs.open)
	if start < 0 {
		return nil, fmt.Errorf("%s: %q not found", rs.file, rs.open)
	}
	end := len(text)
	if rs.close != "" {
		end = strings.Index(text, rs.close)
		if end < start {
			return nil, fmt.Errorf("%s: %q not found", rs.file, rs.close)
		}
	} else {
		depth := 0
		for i := start; i < len(text); i++ {
			switch text[i] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					end = i + 1
					i = len(text)
				}
			}
		}
	}
	return strings.Split(text[start:end], "\n"), nil
}

// handlerName normalizes a handler expression to its bare function name:
// `s.handleBOMe` -> handleBOMe, `wb.ListTemplates` -> ListTemplates,
// `handleListSites(db)` -> handleListSites.
func handlerName(expr string) string {
	if i := strings.IndexByte(expr, '('); i >= 0 {
		expr = expr[:i]
	}
	if i := strings.LastIndexByte(expr, '.'); i >= 0 {
		expr = expr[i+1:]
	}
	return expr
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
		return "low"
	case method == "DELETE" || destRe.MatchString(path):
		return "high"
	}
	return "medium"
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
