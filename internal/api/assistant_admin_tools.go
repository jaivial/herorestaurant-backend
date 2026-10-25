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
	"strconv"
	"strings"
	"sync"
	"time"
)

const forkyAdminCoord = "FORKY-ADMIN-TOOLS-S01"

// assistantSessionSection marks tools any backoffice session may call; the ACL of
// what they reach is enforced per operation (see assistantToolAllowed).
const assistantSessionSection = "session"

// assistantToolTimeout is the per-tool budget of a WS turn: admin_call replays panel
// routes (exports, POS/stock reports) that legitimately take longer than the 5s the
// hand-written tools get.
func assistantToolTimeout(name string) time.Duration {
	if name == "admin_call" {
		return 20 * time.Second
	}
	return 5 * time.Second
}

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

// forkyAdminAliasGroups: Spanish/English synonym groups for catalog discovery.
// The ops index mixes English names (admin_get_pos_cash_days_current) with
// Spanish descriptions, so a bare substring match is nearly blind in one of
// the two languages ("caja" found 0 of the 13 cash ops). Query-side expansion
// only: the generated ops map is not touched, and a word matches when ANY of
// its aliases appears in the op haystack. Keep groups small and domain-real:
// an over-broad alias turns discovery into noise.
var forkyAdminAliasGroups = [][]string{
	{"caja", "cash"},
	{"caduc", "expir", "vencim", "shelf"},
	{"almacen", "warehouse"},
	{"turno", "shift"},
	{"miembro", "member", "staff", "equipo", "personal", "team"},
	{"cierre", "closure", "close"},
	{"articulo", "item"},
	{"reserva", "booking"},
	{"factura", "invoice"},
	{"campana", "campaign"},
	{"anuncio", "ads"},
	{"carta", "menu"},
	{"horario", "schedule"},
	{"fichaje", "clock"},
	{"mesa", "table"},
	{"pedido", "order"},
	{"cocina", "kitchen"},
	{"precio", "price"},
	{"cliente", "customer"},
	{"usuario", "user"},
	{"informe", "report"},
}

var forkyAdminAliases = func() map[string][]string {
	m := map[string][]string{}
	for _, g := range forkyAdminAliasGroups {
		for _, w := range g {
			m[w] = g
		}
	}
	return m
}()

func forkyAdminMatch(op forkyAdminOp, q string) bool {
	hay := forkyFold(op.Name + " " + op.Path + " " + op.Description + " " + op.Section + " " + strings.Join(op.Screens, " "))
	for _, w := range strings.Fields(forkyFold(q)) {
		if len(w) < 3 {
			continue
		}
		candidates := forkyAdminAliases[w]
		if candidates == nil {
			candidates = []string{w}
		}
		ok := false
		for _, c := range candidates {
			if strings.Contains(hay, c) {
				ok = true
				break
			}
		}
		if !ok {
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

// forkyPathIsAccountManagement reports whether the path manages the ACCOUNTS
// of the panel (who can log in and with what role) rather than the business
// data of a restaurant.
//
// The bot is a restaurant operator: the business data is its business
// (reservations, menus, members, tables, stock, POS). The people behind the
// panel are not: the service session it borrows is a superadmin, so these
// operations would let a message change who can log into the backoffice.
func forkyPathIsAccountManagement(path string) bool {
	p := strings.TrimSpace(path)
	if p == "" {
		return false
	}
	// The panel's own accounts and their roles.
	if p == "/users" || strings.HasPrefix(p, "/users/") {
		return true
	}
	if p == "/roles" || strings.HasPrefix(p, "/roles/") {
		return true
	}
	// The platform-level user administration (superadmin section): create,
	// patch, assign a restaurant, set a password, revoke sessions.
	if p == "/platform/users" || strings.HasPrefix(p, "/platform/users/") {
		return true
	}
	return false
}

// forkyPathIsSessionScoped reports whether the path acts on the session's
// own account (its password, its preferences) rather than on the business
// data.
//
// The service session the bot uses belongs to a real superadmin, so these
// operations would act on that person's account: the bot must not be able to
// change the credentials of the account it borrows.
func forkyPathIsSessionScoped(path string) bool {
	p := strings.TrimSpace(path)
	if p == "" {
		return false
	}
	// "/me" and "/me/..." are the account's own routes. "/members/..." are
	// the other people's accounts and are legitimate backoffice operations.
	if p == "/me" {
		return true
	}
	return strings.HasPrefix(p, "/me/")
}

// forkyPathIsActiveRestaurant reports whether the path is the one that
// rebinds the session to another restaurant.
func forkyPathIsActiveRestaurant(path string) bool {
	p := strings.TrimSpace(path)
	return p == "/active-restaurant" || strings.HasSuffix(p, "/active-restaurant")
}

// forkyPathHasParam reports whether the path pattern carries the given
// parameter, so a caller can enforce what the parameter may be.
func forkyPathHasParam(path string, name string) bool {
	for _, m := range forkyPathParamRe.FindAllStringSubmatch(path, -1) {
		if m[1] == name {
			return true
		}
	}
	return false
}

func forkyPathParams(path string) []string {
	out := []string{}
	for _, m := range forkyPathParamRe.FindAllStringSubmatch(path, -1) {
		out = append(out, m[1])
	}
	return out
}

// adminCallInput is one admin_call request: the operation name plus its
// path/query/body arguments and the write confirmation fields.
type adminCallInput struct {
	Name              string          `json:"name"`
	PathParams        map[string]any  `json:"path_params"`
	Query             map[string]any  `json:"query"`
	Body              json.RawMessage `json:"body"`
	Confirmed         bool            `json:"confirmed"`
	ConfirmationToken string          `json:"confirmation_token"`
}

// adminCallMaxBatch caps the number of READ operations one batched admin_call
// may carry (wa_bot_admin_call_batch_v1).
const adminCallMaxBatch = 20

func (s *Server) assistantAdminCall(ctx context.Context, rid int, input json.RawMessage) (string, error) {
	var in adminCallInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("argumentos inválidos")
	}

	// wa_bot_admin_call_batch_v1: an optional "calls" array runs several READ
	// operations in ONE model call and returns one result per item. The Bot
	// Alquería incident of 2026-10-23 (18 iter / 37 tools) spent ~8 iterations
	// calling admin_get_bookings_id once per reservation; batching collapses
	// them into a single iteration. READS only: a batched write is refused
	// (writes keep their dedicated confirmation path, one operation at a time).
	var probe struct {
		Calls []adminCallInput `json:"calls"`
	}
	if json.Unmarshal(input, &probe) == nil && len(probe.Calls) > 0 {
		return s.assistantAdminCallBatch(ctx, rid, probe.Calls)
	}
	return s.assistantAdminCallSingle(ctx, rid, in, input)
}

// assistantAdminCallBatch runs each READ operation of a batch and returns one
// result per item, in order. An item that fails (unknown op, write, scope,
// replay error) yields its error text in that slot and does NOT stop the rest.
func (s *Server) assistantAdminCallBatch(ctx context.Context, rid int, calls []adminCallInput) (string, error) {
	if len(calls) > adminCallMaxBatch {
		return "", fmt.Errorf("demasiadas operaciones en batch: %d (máximo %d)", len(calls), adminCallMaxBatch)
	}
	type item struct {
		Index int    `json:"index"`
		Name  string `json:"name"`
		OK    bool   `json:"ok"`
		Data  string `json:"data,omitempty"`
		Error string `json:"error,omitempty"`
	}
	out := make([]item, 0, len(calls))
	for i, c := range calls {
		// Writes are refused item-by-item: the batch is read-only by design.
		rawItem, _ := json.Marshal(c)
		raw, err := s.assistantAdminCallSingle(ctx, rid, c, rawItem)
		it := item{Index: i, Name: c.Name}
		if err != nil {
			it.Error = err.Error()
		} else {
			it.OK = true
			it.Data = raw
		}
		out = append(out, it)
	}
	return botJSON(map[string]any{"tool": "admin_call_batch", "count": len(out), "results": out}), nil
}

// assistantAdminCallSingle resolves and executes ONE admin_call operation
// (read or write), applying the bot-turn write denial and the write
// confirmation token. It is the shared core of single calls and batch items.
func (s *Server) assistantAdminCallSingle(ctx context.Context, rid int, in adminCallInput, rawInput json.RawMessage) (string, error) {
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
	// A bot turn is a model acting on a chat message: it may read through the
	// catalogue, but a generic panel write is refused here, in the server. The
	// confirmation token is issued to and redeemed by the same model, so it is
	// not a human decision; the dedicated bot tools carry their own checks.
	if op.Write && isBotPrivilegedTurn(ctx) {
		botCheckpoint("wa_bot_admin_write_denied_v1", "restaurant_id=%d", rid)
		// wa_bot_terminal_tool_v1: final refusal for this turn (the model wrote
		// the "confirmation" itself; a retry can never change the answer).
		// NOT terminal (wa_bot_terminal_tool_v1 regression 2026-10-23 17:30):
		// this refusal points the model at panel_change, so the turn must stay
		// open for that call. With terminal:true the loop closed before the
		// model could follow the advice and the customer got a summary instead
		// of the proposal. (Supersedes the terminal+retry_with steer of
		// wa_bot_terminal_steer_v1; the steer machinery in botSteerTool stays
		// available for other terminal refusals with retry_with.)
		return botJSON(map[string]any{"error": "el bot no puede ejecutar escrituras genéricas del panel (admin_call) sin aprobación humana. No lo reintentes por admin_call: llama ahora a panel_change para PROPONER el cambio (se aplica cuando el usuario responde 'OK <código>' en el chat)."}), nil
	}
	if op.Write {
		if !in.Confirmed {
			return s.assistantRequireConfirmation(rid, "admin_call", rawInput)
		}
		// The token is bound to (restaurant, tool, canonical arguments) by confirmationArguments.
		if err := s.assistantConsumeConfirmation(in.ConfirmationToken, rid, "admin_call", rawInput); err != nil {
			return "", err
		}
	}
	token, ok := boSessionTokenFromContext(ctx)
	if !ok {
		return "", fmt.Errorf("sesión del panel no disponible")
	}
	if err := forkyAdminCheckOp(rid, op, in.PathParams); err != nil {
		return "", err
	}
	return s.forkyAdminRunOp(rid, token, op, in.PathParams, in.Query, in.Body)
}

// forkyAdminCheckOp is the safety net every panel replay shares (admin_call
// and the approved-write path panel_change, wa_bot_panel_write_v1): tenant
// scope, no session rebinding, no session-scoped ops, no account management.
func forkyAdminCheckOp(rid int, op forkyAdminOp, pathParams map[string]any) error {
	// Tenant scope: a path that addresses a restaurant must address THIS
	// restaurant. Without this the model could put another tenant's id in the
	// path and the replay would read or write that tenant's data with the
	// service session's authority.
	//
	// The bot's service session is bound to one restaurant, so a mismatch is
	// never a legitimate call: it is either a mistake or an attempt to reach
	// another tenant.
	if rid > 0 && forkyPathHasParam(op.Path, "restaurantId") {
		got := strings.TrimSpace(fmt.Sprint(pathParams["restaurantId"]))
		if got == "" {
			got = strings.TrimSpace(fmt.Sprint(pathParams["restaurant_id"]))
		}
		if got != "" && got != strconv.Itoa(rid) {
			return fmt.Errorf("operación fuera del restaurante del bot: la ruta apunta a %s y este bot opera en %d", got, rid)
		}
	}
	// The same scope applies to the operation that REBINDS the session to
	// another restaurant (/active-restaurant): it persists
	// active_restaurant_id on the session row, and the replay authenticates
	// on that very session. Letting the bot rebind itself would move the
	// whole privileged turn to another tenant.
	//
	// The bot has exactly one restaurant, so the operation has no meaning
	// for it: the restaurant is already the right one.
	if rid > 0 && forkyPathIsActiveRestaurant(op.Path) {
		return fmt.Errorf("el bot no puede cambiar de restaurante: ya opera en el %d", rid)
	}
	// The operations that act on the SESSION ITSELF are refused for the same
	// reason, and with a sharper edge: the service session belongs to a real
	// superadmin, so /me/password would change THAT PERSON's password and
	// /me/preferences would change their preferences. The bot has no
	// identity of its own to change, and it must never be able to touch the
	// credentials of the account it borrows.
	if forkyPathIsSessionScoped(op.Path) {
		return fmt.Errorf("el bot no puede ejecutar %s: son operaciones sobre la cuenta de la sesion, no sobre el restaurante", op.Path)
	}
	// The ACCOUNT MANAGEMENT operations are refused too. The service session
	// is a superadmin, so the catalog exposes operations that create panel
	// users, assign them to restaurants, change their roles and revoke their
	// sessions. The bot manages a restaurant, not the people who work in the
	// panel: giving it those operations would let a message in a group
	// change who can log into the backoffice, or lock a colleague out.
	//
	// The bot is a restaurant operator: the business data is its business
	// (reservations, menus, members, tables, stock, POS). The people behind
	// the panel are not.
	if forkyPathIsAccountManagement(op.Path) {
		return fmt.Errorf("el bot no puede ejecutar %s: es una operacion de gestion de cuentas del panel, no de datos del restaurante", op.Path)
	}
	return nil
}

// forkyAdminRunOp replays ONE already-checked operation through the real
// router with the panel session identity. It is the shared replay core of
// admin_call (reads) and panel_change (approved writes).
func (s *Server) forkyAdminRunOp(rid int, token string, op forkyAdminOp, pathParams, query map[string]any, body json.RawMessage) (string, error) {
	path, err := forkyRenderPath(op.Path, pathParams)
	if err != nil {
		return "", err
	}
	q := url.Values{}
	for k, v := range query {
		q.Set(k, fmt.Sprint(v))
	}
	// The admin subrouter is mounted at /admin, and the /api prefix rewrite runs
	// after chi has already matched the route, so the replay must address the
	// mounted path directly. Addressing /api/admin here is matched against the
	// tree as-is and answers 404.
	target := "/admin" + path
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	var bodyReader *strings.Reader
	if op.Write && len(body) > 0 && string(body) != "null" {
		bodyReader = strings.NewReader(string(body))
	} else {
		bodyReader = strings.NewReader("")
	}
	// The replay must not inherit the caller's request context: it carries the
	// inbound connection's routing state and cancellation, which make the router
	// answer 404 instead of dispatching. The panel session identity travels
	// through the cookie, and the deadline keeps the call bounded.
	callCtx, cancel := context.WithTimeout(context.Background(), assistantToolTimeout("admin_call"))
	defer cancel()
	req := httptest.NewRequest(op.Method, target, bodyReader).WithContext(callCtx)
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
		Description: "Ejecuta UNA operación de admin_catalog por su name, como el usuario (mismos permisos que en el panel). path_params para los {ids} de la ruta, query para filtros, body (JSON) para escrituras. Las escrituras requieren confirmed=true y el confirmation_token que devuelve la primera llamada con confirmed=false; pide confirmación al usuario antes. " +
			"Para varias LECTURAS distintas usa 'calls' (array de {name, path_params, query}): las ejecuta todas en UNA llamada y devuelve un resultado por cada una, evitando una iteración por operación (máximo 20, solo lectura).",
		Schema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},"path_params":{"type":"object"},"query":{"type":"object"},"body":{"type":"object"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"},"calls":{"type":"array","description":"Varias lecturas en una sola llamada (máx 20)","items":{"type":"object","properties":{"name":{"type":"string"},"path_params":{"type":"object"},"query":{"type":"object"}},"required":["name"]}}}}`),
		Handler: func(s *Server, ctx context.Context, rid int, in json.RawMessage) (string, error) {
			return s.assistantAdminCall(ctx, rid, in)
		},
	},
}

func init() {
	assistantToolRegistry = append(assistantToolRegistry, assistantAdminTools...)
}
