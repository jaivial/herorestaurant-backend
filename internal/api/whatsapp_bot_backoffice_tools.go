package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
)

// Coordination ids: wa_backoffice_ops_v1 (the backoffice operation surface)
// and wa_bot_privileged_control_v1 (the control plane of the bot).
//
// Both tools are the MINIMUM COMMON MULTIPLE of a whole family: one
// parameterised tool (resource x action) instead of one tool per topic.
//
//   manage_backoffice  -> one operation of the backoffice (the same catalogue
//                         the panel uses), proposed and applied through the
//                         propose->OK chain.
//   manage_bot_access  -> the control plane of the bot: which tools a
//                         conversation may call, and which group is the
//                         management group.
//
// Both are tools of the BOT (not of the Forky registry): they only exist in
// the privileged conversations of the privilege boundary
// (wa_bot_privileged_boundary_v1) and their writes only happen after a human
// approval ("OK <código>") by the SAME verified actor in the SAME chat.

// The proposal slots of the new surface. booking_id is the slot of a
// proposal (uq_wa_op_active is (restaurant_id, booking_id, active)), so a
// dedicated slot keeps a pending panel operation from being superseded by an
// unrelated booking proposal, and gives the access proposals the single slot
// they are supposed to have.
const (
	botProposalSlotBackoffice = int64(-1)
	botProposalSlotAccess     = int64(-2)
)

// botBackofficePreviewMaxBody is the biggest body that still fits in the
// preview the human reads. A bigger one is refused: the approval has to be
// informed.
const botBackofficePreviewMaxBody = 1200

// botToolDefsBackoffice is the catalogue of the two tools. They are advertised
// only on privileged turns (the webhook adds them next to the special-admin
// tools) and every call re-proves the actor.
func botToolDefsBackoffice() []botToolDef {
	return []botToolDef{
		{
			Name: "manage_backoffice",
			Description: "Gestiona una operación del BACKOFFICE del restaurante (reservas, carta, menús, miembros, horarios, stock, TPV, facturas, configuración) desde este chat. " +
				"resource = el NOMBRE de la operación del catálogo (descúbrelo con admin_catalog o admin_describe). " +
				"action = 'describe' (devuelve la especificación de la operación: método, ruta, parámetros, si escribe) o 'propose' (NO aplica nada: guarda una propuesta con la vista previa exacta y un código, y la operación se aplica solo cuando la MISMA persona responde 'OK <código>' en este chat). " +
				"path_params = los {ids} de la ruta, query = filtros, body = el cuerpo JSON de la escritura. " +
				"Tras proponer, di solo que la propuesta se ha enviado al chat y que hay que confirmar con el código: NO digas que está hecho y no inventes el código.",
			InputSchema: botSchema(`{"type":"object","properties":{
				"resource":{"type":"string","description":"Nombre de la operación del catálogo del backoffice"},
				"action":{"type":"string","enum":["describe","propose"],"description":"describe o propose (por defecto propose)"},
				"path_params":{"type":"object","description":"Valores de los {ids} de la ruta"},
				"query":{"type":"object","description":"Parámetros de filtro de la ruta"},
				"body":{"type":"object","description":"Cuerpo JSON de la escritura"}
			},"required":["resource"]}`),
		},
		{
			Name: "manage_bot_access",
			Description: "Gestiona el ACCESO de esta versión del bot desde el chat. " +
				"resource = 'tool_grants' (qué herramientas personalizadas puede usar cada conversación) o 'management_group' (grupo de gestión del bot). " +
				"action = 'list' (consulta el estado actual; exige la misma autorización que el resto de herramientas de administración) o 'set'/'remove' (NO aplican nada: generan una propuesta con vista previa exacta y un código, que se aplica cuando la misma persona responde 'OK <código>' en este chat). " +
				"target = el JID de la conversación (para tool_grants) o el JID @g.us del grupo de gestión (obligatorio: un nombre no es un identificador estable). " +
				"tools = para tool_grants, los nombres de las herramientas o ['*'] para el catálogo completo.",
			InputSchema: botSchema(`{"type":"object","properties":{
				"resource":{"type":"string","enum":["tool_grants","management_group"],"description":"Recurso a gestionar"},
				"action":{"type":"string","enum":["list","set","remove"],"description":"list consulta; set/remove generan propuesta con código"},
				"target":{"type":"string","description":"JID de la conversación o JID @g.us del grupo de gestión"},
				"name":{"type":"string","description":"Etiqueta opcional del grupo de gestión"},
				"tools":{"type":"array","items":{"type":"string"},"description":"Solo para tool_grants: nombres de herramientas o ['*'] para el catálogo completo"}
			},"required":["resource"]}`),
		},
	}
}

// botConfigRaw returns the raw stored config_json ("" when absent), the value
// the propose/execute staleness hash is computed from.
func (s *Server) botConfigRaw(ctx context.Context, restaurantID int) string {
	if s.db == nil {
		return ""
	}
	var current sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT config_json FROM whatsapp_bot_config WHERE restaurant_id = ? LIMIT 1`, restaurantID).Scan(&current); err != nil {
		return ""
	}
	return current.String
}

// ---- the backoffice operation surface --------------------------------------

// backofficeOpPlan is the LITERAL description of one operation: the
// catalogued operation, the values of its path parameters, its query and its
// body. It is what the human approves, so nothing may be implicit in it.
//
// Method and Path are pinned at propose time: if the catalogue changes
// between the proposal and the approval (a deploy inside the TTL), the
// operation is refused instead of applying something the human did not see.
type backofficeOpPlan struct {
	Op         string          `json:"op"`
	Method     string          `json:"method,omitempty"`
	Path       string          `json:"path,omitempty"`
	PathParams map[string]any  `json:"path_params,omitempty"`
	Query      map[string]any  `json:"query,omitempty"`
	Body       json.RawMessage `json:"body,omitempty"`
}

// botBackofficeOpRefusals are the catalogued operations the bot may NOT run
// from the chat, with the reason. They are the exclusions documented in
// docs/wa-customtools-matrix.md (seccion 10): retained surfaces, real money,
// real sends to clients and provider credentials.
//
// The match is a PREFIX of the path: "/campanas/" refuses every campaign
// operation, reads included, because the campaign surface sends to real
// clients.
var botBackofficeOpRefusals = []struct {
	Match  string
	Reason string
}{
	{"/bookings/cancelled", "writes-control legacy retenido: la reactivacion de reservas la hace el restaurante en el panel"},
	{"/stock/documents", "OCR document_get retenido (superficie de prompt-injection)"},
	{"/stock/ocr-scan", "OCR document_get retenido (superficie de prompt-injection)"},
	{"/stock/items/import", "importacion masiva: fuera de alcance"},
	{"/pos/products/import", "importacion masiva: fuera de alcance"},
	{"/campanas", "campanas: envian a clientes reales, fuera de alcance"},
	{"/emailAdvertising", "envio real a clientes: fuera de alcance"},
	{"/members/whatsapp/send", "envio real a clientes: fuera de alcance"},
	{"/members/whatsapp/flush-queue", "envio real a clientes: fuera de alcance"},
	{"/members/whatsapp/subscribe", "alta de suscripcion con importe: dinero real, fuera de alcance"},
	{"/members/whatsapp/cancel", "baja de suscripcion con importe: dinero real, fuera de alcance"},
	{"/members/whatsapp/connect", "provision de la conexion: se hace desde el panel"},
	{"/members/whatsapp/disconnect", "provision de la conexion: se hace desde el panel"},
	{"/members/{id}/password-reset/send", "envio real de correo: fuera de alcance"},
	{"/members/{id}/invitation/resend", "envio real de correo: fuera de alcance"},
	{"/invoices", "las facturas las emite el restaurante: fuera de alcance"},
	{"/config/stripe-connect", "dinero real: fuera de alcance"},
	{"/platform/stripe", "dinero real: fuera de alcance"},
	{"/pos/tickets/{id}/refunds", "devolucion de dinero: fuera de alcance"},
	{"/pos/tickets/{id}/fiscal-document", "documento fiscal: fuera de alcance"},
	{"/domains", "dinero real (dominio): fuera de alcance"},
	{"/site-builder/billing", "dinero real: fuera de alcance"},
	{"/site-builder/domains", "dinero real (dominio): fuera de alcance"},
	{"/site-builder/assets", "ficheros del sitio: se gestionan en el panel"},
	{"/website-builder/assets", "ficheros del sitio: se gestionan en el panel"},
	{"/instatic", "arranca el generador de webs: se hace desde el panel"},
	{"/config/chatgpt-plugin/tokens", "credencial del plugin: fuera de alcance"},
	{"/config/minimax", "clave de proveedor: fuera de alcance"},
	{"/config/bunny-storage", "clave de proveedor: fuera de alcance"},
	{"/config/ai-image", "clave de proveedor: fuera de alcance"},
	{"/config/email-provider", "clave de proveedor: fuera de alcance"},
	{"/bot/ai", "clave de proveedor: fuera de alcance"},
	{"/bot/config", "el prompt del bot (reglas e instrucciones) se cambia en el panel"},
	{"/bot/settings", "el prompt del bot se cambia en el panel"},
	{"/bot/pipeline", "el pipeline del bot se cambia en el panel"},
	{"/integrations", "credenciales y webhooks de integracion: se cambian en el panel"},
	{"/platform", "gestion de la plataforma: fuera de alcance"},
	{"/pos/roles", "permisos de los roles del TPV: se cambian en el panel"},
	{"/pos/pin", "PIN del TPV: se cambia en el panel"},
	{"/stock/roles", "permisos de los roles de stock: se cambian en el panel"},
	{"/roles", "gestion de cuentas del panel: fuera de alcance"},
	{"/users", "gestion de cuentas del panel: fuera de alcance"},
}

// botBackofficeOpRefusal returns the reason the operation is refused, or "".
//
// The match is by PATH SEGMENT: "/campanas" refuses "/campanas" and
// "/campanas/{id}/send", but not "/campanasfoo".
func botBackofficeOpRefusal(op forkyAdminOp) string {
	p := strings.TrimSpace(op.Path)
	for _, r := range botBackofficeOpRefusals {
		if p == r.Match || strings.HasPrefix(p, r.Match+"/") {
			return r.Reason
		}
	}
	return ""
}

// botBackofficePreview is the exact human summary of the proposal: what would
// happen, nothing else.
//
// The preview is NEVER truncated: a human who cannot see the whole change
// cannot approve it knowingly. A body that does not fit is refused, not cut.
//
// Every value is flattened to ONE line: a value with a newline could forge a
// second line in the preview and misrepresent the operation being approved.
func botBackofficePreview(op forkyAdminOp, plan backofficeOpPlan) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "Propuesta de operacion del panel: %s %s", op.Method, op.Path)
	if len(plan.PathParams) > 0 {
		keys := make([]string, 0, len(plan.PathParams))
		for k := range plan.PathParams {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s=%s", k, botBackofficeFlat(plan.PathParams[k])))
		}
		fmt.Fprintf(&b, " (%s)", strings.Join(parts, ", "))
	}
	if len(plan.Query) > 0 {
		keys := make([]string, 0, len(plan.Query))
		for k := range plan.Query {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s=%s", k, botBackofficeFlat(plan.Query[k])))
		}
		fmt.Fprintf(&b, " ?%s", strings.Join(parts, "&"))
	}
	if len(plan.Body) > 0 && string(plan.Body) != "null" {
		body := string(plan.Body)
		if len(body) > botBackofficePreviewMaxBody {
			return "", fmt.Errorf("el cuerpo de la operacion es demasiado grande (%d caracteres) para que se pueda revisar en la vista previa: reduce la operacion o hazla en varios pasos", len(body))
		}
		// The body has to be ONE line: a body with newlines could forge a
		// line of the preview, and the preview IS the thing the human
		// approves. Compact JSON is enough.
		if strings.ContainsAny(body, "\n\r") {
			return "", errors.New("el cuerpo de la operacion debe ir en una sola linea (JSON compacto) para que la vista previa sea exacta")
		}
		fmt.Fprintf(&b, " cuerpo: %s", body)
	}
	if op.Risk != "" {
		fmt.Fprintf(&b, " [riesgo: %s]", op.Risk)
	}
	b.WriteString(". Nada esta aplicado todavia.")
	return b.String(), nil
}

// botBackofficeFlat renders a value in ONE line: the newlines and the
// control characters become spaces, so a value can never forge a line of the
// preview.
func botBackofficeFlat(v any) string {
	s := fmt.Sprint(v)
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if r < 0x20 {
			return -1
		}
		return r
	}, s)
	if len(s) > 120 {
		s = s[:120] + "…"
	}
	return s
}

// botToolManageBackoffice executes manage_backoffice.
//
// action=describe returns the specification of the operation; action=propose
// stores the proposal and sends the preview with the code to the origin chat.
// It NEVER applies the operation: that only happens on the human approval.
func (s *Server) botToolManageBackoffice(ctx context.Context, rid int, msg botWebhookMessage, input json.RawMessage) (string, error) {
	var in struct {
		Op         string          `json:"resource"`
		Action     string          `json:"action"`
		PathParams map[string]any  `json:"path_params"`
		Query      map[string]any  `json:"query"`
		Body       json.RawMessage `json:"body"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return botJSON(map[string]any{"error": "argumentos ilegibles"}), nil
	}
	in.Op = strings.TrimSpace(in.Op)
	in.Action = strings.ToLower(strings.TrimSpace(in.Action))
	if in.Op == "" {
		return botJSON(map[string]any{"error": "falta resource (el nombre de la operacion; usa admin_catalog para descubrirlo)"}), nil
	}
	op, ok := forkyAdminOpByName(in.Op)
	if !ok {
		return botJSON(map[string]any{"error": "operacion desconocida: " + in.Op + " (usa admin_catalog o admin_describe)"}), nil
	}
	// The actor: the verified phone of a current member of the authorized
	// group (wa_bot_privileged_boundary_v1). Every action of this tool, even
	// the description of an operation, goes through the same gate.
	actor, origin, ok := s.botPrivilegedActor(ctx, rid, msg)
	if !ok {
		log.Printf("[bot] checkpoint wa_backoffice_ops_v1 restaurant_id=%d op=%s decision=denied_actor", rid, op.Name)
		return botJSON(map[string]any{"error": "no autorizado para usar las operaciones del panel desde este chat"}), nil
	}
	if in.Action == "" {
		in.Action = "propose"
	}
	// The refusal is checked BEFORE the description: an operation the chat
	// may not run is not even described, so the model does not build a plan
	// around it.
	if reason := botBackofficeOpRefusal(op); reason != "" {
		log.Printf("[bot] checkpoint wa_backoffice_ops_v1 restaurant_id=%d op=%s decision=refused", rid, op.Name)
		return botJSON(map[string]any{"error": "esta operacion no se puede hacer desde el chat: " + reason}), nil
	}
	if in.Action == "describe" {
		return botJSON(map[string]any{
			"operation": op.Name, "method": op.Method, "path": op.Path,
			"path_params": forkyPathParams(op.Path), "writes": op.Write, "risk": op.Risk,
			"section": op.Section, "description": op.Description,
		}), nil
	}
	if in.Action != "propose" {
		return botJSON(map[string]any{"error": "action invalida: usa describe o propose"}), nil
	}
	if !op.Write {
		return botJSON(map[string]any{"error": "esa operacion es de lectura: consultala con admin_call o con la herramienta dedicada"}), nil
	}
	plan := backofficeOpPlan{Op: op.Name, Method: op.Method, Path: op.Path, PathParams: in.PathParams, Query: in.Query, Body: in.Body}
	// The path parameters are validated BEFORE the proposal exists: a proposal
	// that cannot even be rendered is not a proposal.
	if _, err := forkyRenderPath(op.Path, in.PathParams); err != nil {
		return botJSON(map[string]any{"error": err.Error()}), nil
	}
	preview, err := botBackofficePreview(op, plan)
	if err != nil {
		return botJSON(map[string]any{"error": err.Error()}), nil
	}
	code, err := botSpecialNewCode()
	if err != nil {
		return botJSON(map[string]any{"error": "no se pudo generar el codigo de confirmacion"}), nil
	}
	log.Printf("[bot] checkpoint wa_backoffice_ops_v1 restaurant_id=%d op=%s proposed", rid, op.Name)
	return s.botProposalPersist(ctx, rid, msg, "backoffice", botProposalSlotBackoffice, plan, "backoffice:"+op.Name, actor, origin, "", "", preview, code)
}

// botBackofficeExecute applies an approved backoffice proposal.
//
// The claim of the code (single use, atomic) is committed BEFORE the
// operation runs: the operation itself goes through the real route, with its
// own transaction, so it cannot share one. A failure of the operation leaves
// the proposal consumed (fail closed): the human has to propose again.
func (s *Server) botBackofficeExecute(ctx context.Context, rid int, msg botWebhookMessage, p botSpecialProposalRow) (string, error) {
	var plan backofficeOpPlan
	if err := json.Unmarshal([]byte(p.Args), &plan); err != nil {
		return "", errors.New("propuesta ilegible")
	}
	op, ok := forkyAdminOpByName(plan.Op)
	if !ok {
		return "", errors.New("la operacion ya no esta en el catalogo")
	}
	// The catalogue may have changed between the proposal and the approval
	// (a deploy inside the TTL). The human approved a concrete method and
	// path: if they are not the ones of the catalogue now, the operation is
	// refused instead of applying something nobody saw.
	if plan.Method != "" && (plan.Method != op.Method || plan.Path != op.Path) {
		return "", errors.New("la operacion cambio en el catalogo desde la vista previa; pide una propuesta nueva")
	}
	if !op.Write {
		return "", errors.New("la propuesta no es una escritura")
	}
	if reason := botBackofficeOpRefusal(op); reason != "" {
		return "", errors.New("la operacion no se puede hacer desde el chat: " + reason)
	}
	// The replay runs with the SERVICE SESSION of the bot (a real panel
	// session bound to the restaurant) and the root auth: the gates of the
	// route decide, exactly as they do for a human. The context carries NO
	// "privileged turn" marker: this is not a model turn, it is the human
	// approval of a proposal.
	out, err := s.assistantAdminReplay(s.botServiceContext(ctx, rid), rid, op, plan.PathParams, plan.Query, plan.Body)
	if err != nil {
		return "", err
	}
	// The replay answers 200 with a payload even when the route refused the
	// operation (403, 409, 500...). Announcing that as "applied" would tell
	// the human the opposite of the truth, so the status decides.
	if applied, reason := botBackofficeReplayApplied(out); !applied {
		log.Printf("[bot] checkpoint wa_backoffice_ops_v1 restaurant_id=%d op=%s decision=refused_by_route", rid, op.Name)
		return "", errors.New("la operacion no se aplico: " + reason)
	}
	log.Printf("[bot] checkpoint wa_backoffice_ops_v1 restaurant_id=%d op=%s applied", rid, op.Name)
	notice := "Aplicado: " + op.Method + " " + op.Path + ". " + out
	if rj, mErr := json.Marshal(map[string]any{"op": p.Op, "operation": op.Name, "notice": notice}); mErr == nil {
		_, _ = s.db.ExecContext(ctx, `UPDATE wa_bot_op_proposals SET result_json=?, notify_state='pending' WHERE id=?`, string(rj), p.ID)
	}
	return notice, nil
}

// botBackofficeReplayApplied reads the answer of the replay and tells whether
// the operation really happened.
func botBackofficeReplayApplied(out string) (bool, string) {
	var res struct {
		OK     bool            `json:"ok"`
		Status int             `json:"status"`
		Data   json.RawMessage `json:"data"`
		Text   string          `json:"data_text"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		// An unreadable answer is not a proof of success.
		return false, "la operacion no devolvio una respuesta legible"
	}
	if res.OK {
		return true, ""
	}
	reason := ""
	if len(res.Data) > 0 {
		var payload map[string]any
		if json.Unmarshal(res.Data, &payload) == nil {
			for _, k := range []string{"message", "error"} {
				if v, ok := payload[k].(string); ok && strings.TrimSpace(v) != "" {
					reason = strings.TrimSpace(v)
					break
				}
			}
		}
	}
	if reason == "" {
		reason = strings.TrimSpace(res.Text)
	}
	if len(reason) > 300 {
		reason = reason[:300]
	}
	if reason == "" {
		reason = fmt.Sprintf("la ruta respondio %d", res.Status)
	}
	return false, reason
}

// ---- the control plane of the bot ------------------------------------------

// botToolGrant restricts the custom tools ONE conversation may call.
//
// Semantics (fail open on absence, fail closed on presence):
//   - no grant for the chat => the full catalog it would get anyway;
//   - Tools: ["*"] => the full catalog;
//   - Tools: concrete names => ONLY those names (plus the delivery tools: a
//     conversation must always be able to get an answer).
type botToolGrant struct {
	JID   string   `json:"jid"`
	Tools []string `json:"tools"`
}

// botToolGrantsForChat returns the grant stored for one conversation.
func botToolGrantsForChat(tenant botTenantConfig, chatJID string) (botToolGrant, bool) {
	chatJID = strings.TrimSpace(chatJID)
	if chatJID == "" {
		return botToolGrant{}, false
	}
	for _, g := range tenant.ToolGrants {
		if strings.TrimSpace(g.JID) == chatJID {
			return g, true
		}
	}
	return botToolGrant{}, false
}

// botToolGranted reports whether a grant lets the chat call one tool.
func botToolGranted(g botToolGrant, hasGrant bool, name string) bool {
	if !hasGrant {
		return true
	}
	for _, t := range g.Tools {
		t = strings.TrimSpace(t)
		if t == "*" || t == name {
			return true
		}
	}
	for t := range botDeliveryTools {
		if t == name {
			return true
		}
	}
	return false
}

// botToolDeniedForChat is the server-side guard of the per-chat tool grants.
// Empty string = allowed.
func botToolDeniedForChat(name string, tenant botTenantConfig, chatJID string) string {
	g, has := botToolGrantsForChat(tenant, chatJID)
	if botToolGranted(g, has, name) {
		return ""
	}
	return "esa herramienta no esta habilitada para esta conversacion. Consulta las herramientas disponibles con admin_catalog."
}

// botToolGrantsFilter drops the tools a chat may not call from the catalogue
// offered to the model (a UX aid: the executor re-checks).
func botToolGrantsFilter(defs []botToolDef, tenant botTenantConfig, chatJID string) []botToolDef {
	if _, has := botToolGrantsForChat(tenant, chatJID); !has {
		return defs
	}
	out := make([]botToolDef, 0, len(defs))
	for _, d := range defs {
		if botToolDeniedForChat(d.Name, tenant, chatJID) == "" {
			out = append(out, d)
		}
	}
	return out
}

// botAccessArgs is THE parameterised payload of manage_bot_access.
type botAccessArgs struct {
	Resource string   `json:"resource"`
	Action   string   `json:"action"`
	Target   string   `json:"target"`
	Name     string   `json:"name"`
	Tools    []string `json:"tools"`
}

// botAccessApply is the pure state transition of every control resource: it
// patches the RAW config document, so the fields this code does not know
// survive untouched (re-serialising the parsed struct would drop them, and a
// corrupt config would be silently replaced by a valid one).
//
// scopeGroups are the authorized groups of the environment: the management
// group can only be pointed at one of them, because the bot answers with the
// staff tools there and a group the operator has not authorized must never
// receive staff traffic.
//
// Coordination id: wa_bot_privileged_control_v1: the authorized conversations
// are NOT managed here: the privilege comes from the environment and from the
// current membership of the group (wa_bot_privileged_boundary_v1), never from
// a list stored in the database.
func botAccessApply(doc map[string]any, a botAccessArgs, scopeGroups []string) (string, error) {
	switch a.Resource {
	case "tool_grants":
		jid := strings.TrimSpace(a.Target)
		if jid == "" {
			return "", errors.New("falta target (el JID de la conversacion)")
		}
		grants := botAccessGrants(doc)
		out := make([]map[string]any, 0, len(grants))
		for _, g := range grants {
			if botAccessGrantJID(g) != jid {
				out = append(out, g)
			}
		}
		switch a.Action {
		case "remove":
			if len(out) == len(grants) {
				return "", fmt.Errorf("no habia permisos de herramientas para %s", jid)
			}
			doc["tool_grants"] = out
			return fmt.Sprintf("Aplicado: %s vuelve a tener el catalogo completo de herramientas.", jid), nil
		case "set":
			tools := make([]string, 0, len(a.Tools))
			seen := map[string]struct{}{}
			for _, t := range a.Tools {
				t = strings.TrimSpace(t)
				if t == "" {
					continue
				}
				if _, dup := seen[t]; dup {
					continue
				}
				seen[t] = struct{}{}
				tools = append(tools, t)
			}
			// An empty list means "the full catalogue", never "nothing": a
			// grant with no tools would leave the conversation without a
			// single read tool, which is not what an empty list asks for.
			if len(tools) == 0 {
				if len(out) == len(grants) {
					return "", fmt.Errorf("no habia permisos de herramientas para %s", jid)
				}
				doc["tool_grants"] = out
				return fmt.Sprintf("Aplicado: %s vuelve a tener el catalogo completo de herramientas.", jid), nil
			}
			values := make([]any, 0, len(tools))
			for _, t := range tools {
				values = append(values, t)
			}
			doc["tool_grants"] = append(out, map[string]any{"jid": jid, "tools": values})
			return fmt.Sprintf("Aplicado: %s solo puede usar las herramientas: %s.", jid, strings.Join(tools, ", ")), nil
		}
		return "", fmt.Errorf("action invalida para tool_grants: %s (usa set o remove)", a.Action)
	case "management_group":
		if a.Action != "set" {
			return "", fmt.Errorf("action invalida para management_group: %s (usa set)", a.Action)
		}
		t := strings.TrimSpace(a.Target)
		// The management group is pinned by its JID: a name is not a stable
		// identifier and two groups can share it.
		if !botGroupIsGroupJID(t) {
			return "", errors.New("el grupo de gestion se fija por su JID (@g.us), no por su nombre")
		}
		// And it can only be one of the AUTHORIZED groups: the bot answers
		// there with the staff tools, so a group the operator has not
		// authorized must never become the target of the staff traffic.
		if !botScopeHasGroup(scopeGroups, t) {
			return "", errors.New("el grupo de gestion solo puede fijarse a un grupo autorizado del entorno; el resto se cambia en el panel")
		}
		doc["management_group_jid"] = t
		if n := strings.TrimSpace(a.Name); n != "" {
			doc["management_group_name"] = n
		}
		return fmt.Sprintf("Aplicado: grupo de gestion fijado por JID (%s).", t), nil
	}
	return "", fmt.Errorf("resource invalido: %s (usa tool_grants o management_group)", a.Resource)
}

// botAccessGrants reads the per-chat tool grants of the raw config document.
func botAccessGrants(doc map[string]any) []map[string]any {
	raw, ok := doc["tool_grants"].([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if g, ok := item.(map[string]any); ok {
			out = append(out, g)
		}
	}
	return out
}

// botAccessGrantJID returns the jid of a raw grant.
func botAccessGrantJID(g map[string]any) string {
	jid, _ := g["jid"].(string)
	return strings.TrimSpace(jid)
}

// botScopeHasGroup reports whether the list holds the group.
func botScopeHasGroup(list []string, value string) bool {
	for _, v := range list {
		if strings.TrimSpace(v) == value {
			return true
		}
	}
	return false
}

// botAccessPreview is the exact human summary of the access proposal.
func botAccessPreview(a botAccessArgs, scopeGroups []string) (string, error) {
	probe := map[string]any{}
	if _, err := botAccessApply(probe, a, scopeGroups); err != nil {
		return "", err
	}
	verb := map[string]string{"set": "FIJAR", "remove": "QUITAR"}[a.Action]
	extra := ""
	if len(a.Tools) > 0 {
		extra = " tools=[" + strings.Join(a.Tools, ", ") + "]"
	}
	return fmt.Sprintf("Propuesta de acceso del bot (%s / %s): %s — target=%s%s. Nada esta aplicado todavia.",
		a.Resource, a.Action, verb, strings.TrimSpace(a.Target), extra), nil
}

// botToolManageBotAccess executes manage_bot_access.
func (s *Server) botToolManageBotAccess(ctx context.Context, rid int, msg botWebhookMessage, input json.RawMessage) (string, error) {
	var a botAccessArgs
	if err := json.Unmarshal(input, &a); err != nil {
		return botJSON(map[string]any{"error": "argumentos ilegibles"}), nil
	}
	a.Resource = strings.ToLower(strings.TrimSpace(a.Resource))
	a.Action = strings.ToLower(strings.TrimSpace(a.Action))
	if a.Resource == "" || a.Action == "" {
		return botJSON(map[string]any{"error": "faltan resource y action"}), nil
	}
	// The listing is an access operation too: it needs the SAME authorization
	// as the rest of the administration surface.
	actor, origin, ok := s.botPrivilegedActor(ctx, rid, msg)
	if !ok {
		log.Printf("[bot] checkpoint wa_bot_privileged_control_v1 restaurant_id=%d action=%s decision=denied_actor", rid, a.Action)
		return botJSON(map[string]any{"error": "no autorizado para gestionar el acceso desde este chat"}), nil
	}
	if a.Action == "list" {
		tenant := s.botStoredTenantConfig(ctx, rid)
		log.Printf("[bot] checkpoint wa_bot_privileged_control_v1 restaurant_id=%d action=list", rid)
		return botJSON(map[string]any{
			"tool_grants":      tenant.ToolGrants,
			"management_group": map[string]any{"name": tenant.ManagementGroupName, "jid": tenant.ManagementGroupJID},
			"note":             "Las conversaciones autorizadas no se gestionan desde aqui: las decide el grupo autorizado del entorno y la pertenencia actual a ese grupo.",
		}), nil
	}
	if a.Action != "set" && a.Action != "remove" {
		return botJSON(map[string]any{"error": "action invalida: usa list, set o remove"}), nil
	}
	preview, err := botAccessPreview(a, s.botPrivilegedScopeGroups(rid))
	if err != nil {
		return botJSON(map[string]any{"error": err.Error()}), nil
	}
	code, err := botSpecialNewCode()
	if err != nil {
		return botJSON(map[string]any{"error": "no se pudo generar el codigo de confirmacion"}), nil
	}
	// before_hash is the config this preview was computed against: if the
	// config moves before the code arrives, the apply is refused as stale.
	before := botSha(s.botConfigRaw(ctx, rid))
	log.Printf("[bot] checkpoint wa_bot_privileged_control_v1 restaurant_id=%d resource=%s action=%s proposed", rid, a.Resource, a.Action)
	return s.botProposalPersist(ctx, rid, msg, "access", botProposalSlotAccess, a, "access:"+a.Resource+":"+a.Action, actor, origin, before, "", preview, code)
}

// botAccessExecute applies an approved access proposal inside the approval
// transaction (the claim and this write commit together).
func (s *Server) botAccessExecute(ctx context.Context, tx *sql.Tx, rid int, p botSpecialProposalRow) (string, error) {
	var a botAccessArgs
	if err := json.Unmarshal([]byte(p.Args), &a); err != nil {
		return "", errors.New("propuesta ilegible")
	}
	var current sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT config_json FROM whatsapp_bot_config WHERE restaurant_id=? FOR UPDATE`, rid).Scan(&current); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", errors.New("error interno")
	}
	if p.Before != "" && p.Before != botSha(current.String) {
		return "", errors.New("la configuracion cambio desde la vista previa; pide una propuesta nueva")
	}
	// The config is patched SURGICALLY: the document is never re-serialised
	// from the parsed struct, so the fields this code does not know survive
	// and a corrupt config is refused instead of being silently replaced.
	doc := map[string]any{}
	if strings.TrimSpace(current.String) != "" {
		if err := json.Unmarshal([]byte(current.String), &doc); err != nil {
			return "", errors.New("la configuracion del bot no se pudo leer; pide una propuesta nueva")
		}
	}
	if doc == nil {
		doc = map[string]any{}
	}
	summary, err := botAccessApply(doc, a, s.botPrivilegedScopeGroups(rid))
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return "", errors.New("error interno")
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO whatsapp_bot_config (restaurant_id, config_json) VALUES (?, ?)
		ON DUPLICATE KEY UPDATE config_json = VALUES(config_json)
	`, rid, string(raw)); err != nil {
		return "", errors.New("error interno")
	}
	if err := tx.Commit(); err != nil {
		return "", errors.New("error interno")
	}
	log.Printf("[bot] checkpoint wa_bot_privileged_control_v1 restaurant_id=%d applied resource=%s action=%s", rid, a.Resource, a.Action)
	// A changed grant set makes the old panel service session unnecessary.
	botServiceSessionReset(rid)
	if rj, mErr := json.Marshal(map[string]any{"op": p.Op, "resource": a.Resource, "action": a.Action, "notice": summary}); mErr == nil {
		_, _ = s.db.ExecContext(ctx, `UPDATE wa_bot_op_proposals SET result_json=?, notify_state='pending' WHERE id=?`, string(rj), p.ID)
	}
	return summary, nil
}
