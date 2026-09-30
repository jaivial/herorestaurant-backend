package api

// Forky admin trio [FORKY-ADMIN-TOOLS-S01]: every backoffice CRUD operation of the
// `/admin` API (the generated map forky_admin_ops.json, built by cmd/forky-admin-map
// from server.go) reachable by the assistant with the SAME ACL as the person.
//
//   admin_catalog  -> discover operations (only the sections this user can open)
//   admin_describe -> full spec of 1..10 operations
//   admin_call     -> run ONE operation by name (never a raw path)
//
// admin_call replays the request through the real router (s.Routes()) with the
// user's own bo_session cookie and the server-side secrets, so every gate that
// protects the route in the panel (session, RBAC section, role importance,
// POS/stock permissions, plan features, superadmin) decides here too. Writes need
// the confirmation token of the existing flow. Observation points:
// forky.admin.catalog / forky.admin.describe / forky.admin.call.

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const forkyAdminCoord = "FORKY-ADMIN-TOOLS-S01"

// assistantSessionSection marks tools any backoffice session may call; the ACL of
// what they reach is enforced per operation (see assistantToolAllowed).
const assistantSessionSection = "session"

// forkyAdminMaxResult caps a tool_result: the model's context is the budget.
const forkyAdminMaxResult = 24 * 1024

//go:embed forky_admin_ops.json
var forkyAdminOpsJSON []byte

type forkyAdminOp struct {
	Name        string   `json:"name"`
	Method      string   `json:"method"`
	Path        string   `json:"path"`
	Handler     string   `json:"handler"`
	Gates       []string `json:"gates"`
	Section     string   `json:"section"`
	RolesAdmin  bool     `json:"roles_admin"`
	Write       bool     `json:"write"`
	Risk        string   `json:"risk"`
	Description string   `json:"description"`
	Screens     []string `json:"screens"`
}

var (
	forkyAdminOpsOnce sync.Once
	forkyAdminOps     []forkyAdminOp
	forkyAdminByName  map[string]forkyAdminOp
	forkyAdminRouter  http.Handler
	forkyAdminRouterM sync.Mutex
	forkyPathParamRe  = regexp.MustCompile(`\{([A-Za-z0-9_]+)(:[^}]*)?\}`)
	forkyParamValueRe = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,100}$`)
)

func loadForkyAdminOps() {
	forkyAdminOpsOnce.Do(func() {
		var doc struct {
			Operations []forkyAdminOp `json:"operations"`
		}
		if err := json.Unmarshal(forkyAdminOpsJSON, &doc); err != nil {
			log.Printf("forky.admin.map error coord=%s err=%v", forkyAdminCoord, err)
		}
		forkyAdminOps = doc.Operations
		forkyAdminByName = make(map[string]forkyAdminOp, len(doc.Operations))
		for _, op := range doc.Operations {
			forkyAdminByName[op.Name] = op
		}
	})
}

// routerFor returns the real router, built once per process (Routes() wires hubs
// and caches, so it must not be rebuilt per call).
func (s *Server) forkyAdminRoutes() http.Handler {
	forkyAdminRouterM.Lock()
	defer forkyAdminRouterM.Unlock()
	if forkyAdminRouter == nil {
		forkyAdminRouter = s.Routes()
	}
	return forkyAdminRouter
}

// forkyAdminVisible is the catalog ACL: an operation is listed only when the
// person could open its section in the panel. The real gates still run on call.
func (s *Server) forkyAdminVisible(ctx context.Context, a boAuth, op forkyAdminOp) bool {
	switch op.Section {
	case assistantSessionSection, "":
		return true
	case "superadmin":
		return a.User.isSuperadmin
	case "root":
		imp, err := s.roleImportance(ctx, a.Role)
		return err == nil && imp >= 100
	}
	ok, err := s.boAuthCanAccessSection(ctx, a, op.Section)
	if err != nil || !ok {
		return false
	}
	if op.RolesAdmin {
		imp, err := s.roleImportance(ctx, a.Role)
		return err == nil && imp >= 90
	}
	return true
}

func (s *Server) forkyAdminVisibleOps(ctx context.Context) ([]forkyAdminOp, boAuth, error) {
	loadForkyAdminOps()
	a, ok := boAuthFromContext(ctx)
	if !ok {
		return nil, a, fmt.Errorf("autenticación requerida")
	}
	out := make([]forkyAdminOp, 0, len(forkyAdminOps))
	for _, op := range forkyAdminOps {
		if s.forkyAdminVisible(ctx, a, op) {
			out = append(out, op)
		}
	}
	return out, a, nil
}

func forkyFold(v string) string {
	r := strings.NewReplacer("á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", "ñ", "n", "ü", "u")
	return r.Replace(strings.ToLower(v))
}

func forkyAdminMatch(op forkyAdminOp, q string) bool {
	hay := forkyFold(op.Name + " " + op.Path + " " + op.Description + " " + op.Section + " " + strings.Join(op.Screens, " "))
	for _, w := range strings.Fields(forkyFold(q)) {
		if len(w) >= 3 && !strings.Contains(hay, w) {
			return false
		}
	}
	return true
}

func forkyAdminSummary(op forkyAdminOp) map[string]any {
	desc := op.Description
	if len(desc) > 200 {
		desc = desc[:200]
	}
	return map[string]any{"name": op.Name, "method": op.Method, "path": op.Path, "section": op.Section,
		"write": op.Write, "risk": op.Risk, "description": desc}
}

func (s *Server) assistantAdminCatalog(ctx context.Context, _ int, input json.RawMessage) (string, error) {
	var in struct {
		Q          string `json:"q"`
		Section    string `json:"section"`
		WritesOnly bool   `json:"writes_only"`
		Limit      int    `json:"limit"`
		Offset     int    `json:"offset"`
	}
	_ = json.Unmarshal(input, &in)
	ops, _, err := s.forkyAdminVisibleOps(ctx)
	if err != nil {
		return "", err
	}
	sections := map[string]int{}
	rows := make([]map[string]any, 0)
	for _, op := range ops {
		sections[op.Section]++
		if in.Section != "" && op.Section != in.Section || in.WritesOnly && !op.Write || !forkyAdminMatch(op, in.Q) {
			continue
		}
		rows = append(rows, forkyAdminSummary(op))
	}
	total := len(rows)
	if in.Limit <= 0 || in.Limit > 150 {
		in.Limit = 40
	}
	if in.Offset < 0 || in.Offset > total {
		in.Offset = 0
	}
	end := in.Offset + in.Limit
	if end > total {
		end = total
	}
	log.Printf("forky.admin.catalog coord=%s q=%q total=%d", forkyAdminCoord, in.Q, total)
	return botJSON(map[string]any{"tool": "admin_catalog", "total": total, "offset": in.Offset, "operations": rows[in.Offset:end],
		"sections": sections, "hint": "Usa admin_describe para ver parámetros y admin_call para ejecutar."}), nil
}

func (s *Server) assistantAdminDescribe(ctx context.Context, _ int, input json.RawMessage) (string, error) {
	var in struct {
		Names []string `json:"names"`
		Name  string   `json:"name"`
	}
	_ = json.Unmarshal(input, &in)
	if in.Name != "" {
		in.Names = append(in.Names, in.Name)
	}
	ops, _, err := s.forkyAdminVisibleOps(ctx)
	if err != nil {
		return "", err
	}
	visible := map[string]forkyAdminOp{}
	for _, op := range ops {
		visible[op.Name] = op
	}
	out, unknown := []map[string]any{}, []string{}
	for i, n := range in.Names {
		if i >= 10 {
			break
		}
		op, ok := visible[n]
		if !ok {
			unknown = append(unknown, n) // fuera del ACL == inexistente
			continue
		}
		d := forkyAdminSummary(op)
		d["path_params"] = forkyPathParams(op.Path)
		d["screens"] = op.Screens
		d["body"] = map[string]bool{"json": op.Write}
		out = append(out, d)
	}
	log.Printf("forky.admin.describe coord=%s n=%d unknown=%d", forkyAdminCoord, len(out), len(unknown))
	return botJSON(map[string]any{"tool": "admin_describe", "operations": out, "unknown": unknown}), nil
}

func forkyPathParams(path string) []string {
	out := []string{}
	for _, m := range forkyPathParamRe.FindAllStringSubmatch(path, -1) {
		out = append(out, m[1])
	}
	return out
}

func (s *Server) assistantAdminCall(ctx context.Context, rid int, input json.RawMessage) (string, error) {
	var in struct {
		Name              string          `json:"name"`
		PathParams        map[string]any  `json:"path_params"`
		Query             map[string]any  `json:"query"`
		Body              json.RawMessage `json:"body"`
		Confirmed         bool            `json:"confirmed"`
		ConfirmationToken string          `json:"confirmation_token"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("argumentos inválidos")
	}
	ops, _, err := s.forkyAdminVisibleOps(ctx)
	if err != nil {
		return "", err
	}
	var op forkyAdminOp
	for _, o := range ops {
		if o.Name == in.Name {
			op = o
		}
	}
	if op.Name == "" {
		return "", fmt.Errorf("operación desconocida para este usuario: %s (usa admin_catalog)", in.Name)
	}
	if op.Write {
		if !in.Confirmed {
			return s.assistantRequireConfirmation(rid, "admin_call", input)
		}
		// The token is bound to (restaurant, tool, canonical arguments) by confirmationArguments.
		if err := s.assistantConsumeConfirmation(in.ConfirmationToken, rid, "admin_call", input); err != nil {
			return "", err
		}
	}
	token, ok := boSessionTokenFromContext(ctx)
	if !ok {
		return "", fmt.Errorf("sesión del panel no disponible")
	}
	path, err := forkyRenderPath(op.Path, in.PathParams)
	if err != nil {
		return "", err
	}
	q := url.Values{}
	for k, v := range in.Query {
		q.Set(k, fmt.Sprint(v))
	}
	target := "/admin" + path
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	var body *strings.Reader
	if op.Write && len(in.Body) > 0 && string(in.Body) != "null" {
		body = strings.NewReader(string(in.Body))
	} else {
		body = strings.NewReader("")
	}
	callCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	req := httptest.NewRequest(op.Method, target, body).WithContext(callCtx)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.AddCookie(&http.Cookie{Name: boSessionCookieName, Value: token})
	if v := strings.TrimSpace(s.cfg.VaultKey); v != "" {
		req.Header.Set(vaultKeyHeader, v)
	}
	if v := strings.TrimSpace(s.cfg.BearerTokenKey); v != "" {
		req.Header.Set(bearerTokenHeader, v)
	}
	req.Header.Set("X-Forky-Coord", forkyAdminCoord)
	rec := httptest.NewRecorder()
	started := time.Now()
	s.forkyAdminRoutes().ServeHTTP(rec, req)
	log.Printf("forky.admin.call coord=%s op=%s status=%d ms=%d", forkyAdminCoord, op.Name, rec.Code, time.Since(started).Milliseconds())
	raw := rec.Body.Bytes()
	truncated := false
	if len(raw) > forkyAdminMaxResult {
		raw, truncated = raw[:forkyAdminMaxResult], true
	}
	out := map[string]any{"tool": "admin_call", "operation": op.Name, "status": rec.Code, "ok": rec.Code < 400}
	var parsed any
	if !truncated && json.Unmarshal(raw, &parsed) == nil {
		out["data"] = parsed
	} else {
		out["data_text"] = string(raw)
		out["truncated"] = truncated
	}
	if rec.Code == http.StatusForbidden || rec.Code == http.StatusUnauthorized {
		out["hint"] = "Sin permiso para esta operación: díselo al usuario y no reintentes."
	}
	return botJSON(out), nil
}

func forkyRenderPath(pattern string, params map[string]any) (string, error) {
	var missing []string
	out := forkyPathParamRe.ReplaceAllStringFunc(pattern, func(ph string) string {
		name := forkyPathParamRe.FindStringSubmatch(ph)[1]
		v, ok := params[name]
		val := strings.TrimSpace(fmt.Sprint(v))
		if !ok || val == "" || !forkyParamValueRe.MatchString(val) {
			missing = append(missing, name)
			return ph
		}
		return url.PathEscape(val)
	})
	if len(missing) > 0 {
		sort.Strings(missing)
		return "", fmt.Errorf("path_params inválidos o ausentes: %s", strings.Join(missing, ", "))
	}
	return out, nil
}

// ---- session token carried from the WS handshake (never from the model) ----

type forkySessionTokenKey struct{}

func withBOSessionToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, forkySessionTokenKey{}, token)
}

func boSessionTokenFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(forkySessionTokenKey{}).(string)
	return v, ok && v != ""
}

// Registry entries appended to assistantToolRegistry (see init below).
var assistantAdminTools = []assistantTool{
	{
		Name: "admin_catalog", Section: assistantSessionSection, BackofficeOnly: true,
		Description: "Lista TODAS las operaciones del backoffice (reservas, carta, menús, miembros, horarios, fichaje, facturas, stock, TPV, campañas, anuncios, ajustes...) que ESTE usuario puede usar, de lectura y de escritura. Filtra con q (texto), section o writes_only. Úsala cuando ninguna otra herramienta cubra lo pedido.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"},"section":{"type":"string"},"writes_only":{"type":"boolean"},"limit":{"type":"integer"},"offset":{"type":"integer"}}}`),
		Handler: func(s *Server, ctx context.Context, rid int, in json.RawMessage) (string, error) {
			return s.assistantAdminCatalog(ctx, rid, in)
		},
	},
	{
		Name: "admin_describe", Section: assistantSessionSection, BackofficeOnly: true,
		Description: "Devuelve el detalle de 1..10 operaciones de admin_catalog (método, ruta, parámetros de ruta, si escribe, riesgo y pantallas donde se usa).",
		Schema:      json.RawMessage(`{"type":"object","properties":{"names":{"type":"array","items":{"type":"string"}},"name":{"type":"string"}}}`),
		Handler: func(s *Server, ctx context.Context, rid int, in json.RawMessage) (string, error) {
			return s.assistantAdminDescribe(ctx, rid, in)
		},
	},
	{
		Name: "admin_call", Section: assistantSessionSection, BackofficeOnly: true,
		Description: "Ejecuta UNA operación de admin_catalog por su name, como el usuario (mismos permisos que en el panel). path_params para los {ids} de la ruta, query para filtros, body (JSON) para escrituras. Las escrituras requieren confirmed=true y el confirmation_token que devuelve la primera llamada con confirmed=false; pide confirmación al usuario antes.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},"path_params":{"type":"object"},"query":{"type":"object"},"body":{"type":"object"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["name"]}`),
		Handler: func(s *Server, ctx context.Context, rid int, in json.RawMessage) (string, error) {
			return s.assistantAdminCall(ctx, rid, in)
		},
	},
}

func init() {
	assistantToolRegistry = append(assistantToolRegistry, assistantAdminTools...)
}
