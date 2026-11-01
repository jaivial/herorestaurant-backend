package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Coordination ids: wa_bot_privileged_control_v1 (control plane) and
// wa_bot_tool_grants_v1 (per-chat custom tool control).
//
// The missing control-plane custom tools of THIS version of the WhatsApp bot,
// in the spirit of a minimum common multiple: ONE parameterised tool
// (manage_bot_access) covers every similar topic — who is allowed (contacts
// and/or groups), which custom tools each allowed conversation may call, and
// the management group — through parameters, not through new tools per topic.
//
// Hard boundary: these tools only exist inside ALLOWED conversations (the
// privileged contacts and groups the operator selected). They are advertised
// only on privileged turns and refused server-side everywhere else, so no
// other chat can reach this version of the bot.
//
// Writes reuse the shared propose+OK machinery (botProposalPersist and the
// "OK <code>" approval of wa_bot_special_admin_v2): the model never applies a
// change itself, the exact preview goes to the chat and only the requester,
// in the same chat, within the TTL, applies it with the code.

// botToolGrant restricts the custom tools ONE conversation may call. It is the
// per-chat half of the tool control (the tenant-level half stays the legacy
// disable_* knobs of botTenantConfig).
//
// Semantics (fail open on absence, fail closed on presence):
//   - no grant for the chat => the full catalog it would get anyway (nothing
//     changes for a tenant that never configures grants);
//   - Tools: ["*"] => the full catalog;
//   - Tools: concrete names => ONLY those names (plus the delivery tools: a
//     conversation must always be able to get an answer).
type botToolGrant struct {
	JID   string   `json:"jid"`
	Tools []string `json:"tools"`
}

// botPrivilegedControlToolNames lists every tool of this bot version. Derived
// from the definitions so the guard and the catalog can never drift.
var botPrivilegedControlToolNames = func() map[string]bool {
	out := map[string]bool{}
	for _, d := range botToolDefsPrivilegedControl() {
		out[d.Name] = true
	}
	return out
}()

// botToolDefsPrivilegedControl is the catalog of this bot version. Registered
// ONLY for allowed (privileged) contacts and groups.
func botToolDefsPrivilegedControl() []botToolDef {
	defs := []botToolDef{
		{
			Name: "manage_bot_access",
			Description: "Gestiona el ACCESO de esta versión del bot desde el chat: quién está autorizado (contactos y/o grupos de WhatsApp), qué herramientas personalizadas puede usar cada conversación y cuál es el grupo de gestión. " +
				"resource: 'allowed_chats' (autorizados), 'tool_grants' (herramientas por conversación), 'management_group' (grupo de gestión) o 'bot_config' (configuración del bot, p.ej. privileged_direct_writes con action=set, target=privileged_direct_writes y name=true/false para que los cambios se apliquen directamente sin OK). " +
				"action: 'list' consulta el estado actual al momento; 'add'/'remove'/'set' NO aplican nada: generan una propuesta con vista previa exacta y un código que la persona que lo pidió debe confirmar escribiendo 'OK <código>' en este chat. " +
				"target: teléfono o JID de la conversación (para management_group, el nombre o JID del grupo). tools: para tool_grants, los nombres concretos o ['*'] para todo el catálogo.",
			InputSchema: botSchema(`{"type":"object","properties":{
				"resource":{"type":"string","enum":["allowed_chats","tool_grants","management_group"],"description":"Recurso a gestionar"},
				"action":{"type":"string","enum":["list","add","remove","set"],"description":"list consulta; add/remove/set generan propuesta con código"},
				"target":{"type":"string","description":"Teléfono o JID de la conversación; para management_group el nombre o JID del grupo"},
				"is_group":{"type":"boolean","description":"true cuando target es un grupo de WhatsApp (opcional si el target es un JID @g.us)"},
				"name":{"type":"string","description":"Etiqueta opcional para el listado (nombre del grupo o del contacto)"},
				"tools":{"type":"array","items":{"type":"string"},"description":"Solo para tool_grants: nombres de herramientas o ['*'] para el catálogo completo"}
			},"required":["resource","action"]}`),
		},
		{
			// Coordination id: wa_bot_dish_allergens_v1 - documented-but-missing
			// staff read: real allergen data from the technical sheets, so the
			// staff group answers allergen questions from DATA instead of the
			// customer-facing handoff (which stays unchanged for customers).
			Name:        "get_dish_allergens",
			Description: "Devuelve los ALÉRGENOS reales de una ficha técnica (plato, receta o arroz) calculados desde sus componentes: los derivados, los añadidos a mano y los efectivos, con qué ingrediente los aporta. Úsala en el grupo para responder con datos a preguntas de alergenos/intolerancias. Identifica la ficha por sheet_id o por query (nombre).",
			InputSchema: botSchema(`{"type":"object","properties":{
				"sheet_id":{"type":"integer","description":"ID de la ficha técnica (opcional si se pasa query)"},
				"query":{"type":"string","description":"Nombre del plato/receta a buscar (opcional si se pasa sheet_id)"}
			}}`),
		},
	}
	// Coordination ids: wa_bot_staff_reads_v2 / wa_bot_panel_write_v1 - the
	// rest of this bot version's surface: the dedicated staff reads and the
	// approved panel writes (the missing halves of the backoffice coverage).
	defs = append(defs, botToolDefsStaffReads()...)
	defs = append(defs, botPanelChangeToolDefs()...)
	// Coordination id: wa_bot_tool_chain_v1 - multiple custom tools and
	// result-linked pipelines through the same privileged executor.
	defs = append(defs, botToolChainDef())
	// Coordination id: campaign_tools_v1 - the campaign editor (/app/campanas)
	// as one parameterised tool with the same propose+OK writes.
	defs = append(defs, botToolDefsCampaign()...)
	// Coordination id: ads_html_editor_v1 - the web ads editor (/app/anuncios).
	defs = append(defs, botToolDefsAd()...)
	// Coordination id: wa_bot_invoice_v1 - invoices (/app/facturas): draft,
	// PDF to the chat, send to the client, with the panel's own handlers.
	defs = append(defs, botToolDefsInvoice()...)
	return defs
}

// botToolGrantsForChat returns the grant stored for one conversation, if any.
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

// botToolGranted reports whether a grant lets the chat call one tool. An empty
// grant (no entry) grants everything; an explicit list is a strict allow-list.
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
	// The delivery surface is never restricted: the conversation must always
	// be able to receive the answer it asked for.
	for t := range botDeliveryTools {
		if t == name {
			return true
		}
	}
	return false
}

// botToolDeniedForChat is the single server-side guard of the tool grants
// (wa_bot_tool_grants_v1). Empty string = allowed.
func botToolDeniedForChat(name string, tenant botTenantConfig, chatJID string) string {
	g, has := botToolGrantsForChat(tenant, chatJID)
	if botToolGranted(g, has, name) {
		return ""
	}
	return "esa herramienta no está habilitada para esta conversación. Consulta qué herramientas hay disponibles con manage_bot_access (resource=tool_grants, action=list) o pide al restaurante que la habilite."
}

// botToolGrantsFilter drops the tools a chat may not call from the catalog
// offered to the model (UX aid: the executor re-checks, see
// botToolDeniedForChat).
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
	botCheckpoint("wa_bot_tool_grants_v1", "chat=%s tools=%d/%d", chatJID, len(out), len(defs))
	return out
}

// botAccessArgs is THE parameterised payload of manage_bot_access: one shape
// for every control resource, so a new similar topic needs no new tool.
type botAccessArgs struct {
	Resource string   `json:"resource"`
	Action   string   `json:"action"`
	Target   string   `json:"target"`
	IsGroup  bool     `json:"is_group"`
	Name     string   `json:"name"`
	Tools    []string `json:"tools"`
}

// botAccessNormalizeTarget accepts a phone, a phone JID or any provider JID
// and returns the canonical conversation JID. Fail closed on a kind mismatch:
// a group selection is never a contact and vice versa.
func botAccessNormalizeTarget(target string, isGroup bool) (string, error) {
	t := strings.TrimSpace(target)
	if t == "" {
		return "", errors.New("falta target (teléfono o JID de la conversación)")
	}
	if strings.Contains(t, "@") {
		if isGroup && !strings.HasSuffix(t, "@g.us") {
			return "", errors.New("target no es un JID de grupo (@g.us)")
		}
		if !isGroup && strings.HasSuffix(t, "@g.us") {
			return "", errors.New("target es un grupo: marca is_group=true")
		}
		return t, nil
	}
	if isGroup {
		return "", errors.New("un grupo se identifica por su JID (@g.us), no por teléfono")
	}
	digits := botWhatsappIDDigits(t)
	if digits == "" {
		return "", fmt.Errorf("teléfono inválido: %q", target)
	}
	return digits + "@s.whatsapp.net", nil
}

// botAccessApply is the pure state transition of every control resource. It
// mutates the tenant config and returns the human summary that reaches the
// chat as the approval notice.
func botAccessApply(tenant *botTenantConfig, a botAccessArgs) (string, error) {
	switch a.Resource {
	case "allowed_chats":
		jid, err := botAccessNormalizeTarget(a.Target, a.IsGroup)
		if err != nil {
			return "", err
		}
		kind := "contacto"
		if a.IsGroup {
			kind = "grupo"
		}
		switch a.Action {
		case "add":
			for _, c := range tenant.PrivilegedContacts {
				if strings.TrimSpace(c.JID) == jid {
					return "", fmt.Errorf("el %s ya estaba autorizado (%s)", kind, jid)
				}
			}
			tenant.PrivilegedContacts = append(tenant.PrivilegedContacts, botPrivilegedContact{JID: jid, Name: strings.TrimSpace(a.Name), IsGroup: a.IsGroup})
			return fmt.Sprintf("Aplicado: %s autorizado en esta versión del bot (%s).", kind, jid), nil
		case "remove":
			out := tenant.PrivilegedContacts[:0]
			found := false
			for _, c := range tenant.PrivilegedContacts {
				if strings.TrimSpace(c.JID) == jid {
					found = true
					continue
				}
				out = append(out, c)
			}
			if !found {
				return "", fmt.Errorf("el %s no estaba autorizado (%s)", kind, jid)
			}
			tenant.PrivilegedContacts = out
			return fmt.Sprintf("Aplicado: %s ya NO está autorizado (%s).", kind, jid), nil
		}
		return "", fmt.Errorf("action inválida para allowed_chats: %s (usa add o remove)", a.Action)
	case "tool_grants":
		jid, err := botAccessNormalizeTarget(a.Target, a.IsGroup)
		if err != nil {
			return "", err
		}
		out := make([]botToolGrant, 0, len(tenant.ToolGrants))
		for _, g := range tenant.ToolGrants {
			if strings.TrimSpace(g.JID) != jid {
				out = append(out, g)
			}
		}
		switch a.Action {
		case "remove":
			if len(out) == len(tenant.ToolGrants) {
				return "", fmt.Errorf("no había permisos de herramientas para %s", jid)
			}
			tenant.ToolGrants = out
			return fmt.Sprintf("Aplicado: %s vuelve a tener el catálogo completo de herramientas.", jid), nil
		case "set":
			if len(a.Tools) == 0 {
				if len(out) == len(tenant.ToolGrants) {
					return "", fmt.Errorf("no había permisos de herramientas para %s", jid)
				}
				tenant.ToolGrants = out
				return fmt.Sprintf("Aplicado: %s vuelve a tener el catálogo completo de herramientas.", jid), nil
			}
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
			tenant.ToolGrants = append(out, botToolGrant{JID: jid, Tools: tools})
			return fmt.Sprintf("Aplicado: %s solo puede usar las herramientas: %s.", jid, strings.Join(tools, ", ")), nil
		}
		return "", fmt.Errorf("action inválida para tool_grants: %s (usa set o remove)", a.Action)
	case "management_group":
		if a.Action != "set" {
			return "", fmt.Errorf("action inválida para management_group: %s (usa set)", a.Action)
		}
		t := strings.TrimSpace(a.Target)
		if botGroupIsGroupJID(t) {
			tenant.ManagementGroupJID = t
			if n := strings.TrimSpace(a.Name); n != "" {
				tenant.ManagementGroupName = n
			}
			return fmt.Sprintf("Aplicado: grupo de gestión fijado por JID (%s).", t), nil
		}
		if t == "" {
			return "", errors.New("falta target (nombre o JID del grupo de gestión)")
		}
		tenant.ManagementGroupName = t
		tenant.ManagementGroupJID = ""
		return fmt.Sprintf("Aplicado: grupo de gestión fijado por nombre (%s).", t), nil
	case "bot_config":
		if a.Action != "set" {
			return "", fmt.Errorf("action inválida para bot_config: %s (usa set)", a.Action)
		}
		switch strings.TrimSpace(a.Target) {
		case "privileged_direct_writes":
			// Coordination id: wa_bot_privileged_direct_writes_v1
			tenant.PrivilegedDirectWrites = strings.EqualFold(strings.TrimSpace(a.Name), "true")
			state := "DESACTIVADOS"
			if tenant.PrivilegedDirectWrites {
				state = "ACTIVADOS"
			}
			return fmt.Sprintf("Aplicado: cambios directos %s para las conversaciones autorizadas (las escrituras se ejecutan sin 'OK <código>').", state), nil
		}
		return "", fmt.Errorf("clave de configuración no reconocida: %s (usa privileged_direct_writes)", strings.TrimSpace(a.Target))
	}
	return "", fmt.Errorf("resource inválido: %s", a.Resource)
}

// botAccessApplyNow applies an access change directly, without the OK flow:
// only for tenants with privileged direct writes enabled
// (wa_bot_privileged_direct_writes_v1).
func (s *Server) botAccessApplyNow(ctx context.Context, restaurantID int, a botAccessArgs) (string, error) {
	if s.db == nil {
		return "", errors.New("base de datos no disponible")
	}
	tenant := s.botStoredTenantConfig(ctx, restaurantID)
	summary, err := botAccessApply(&tenant, a)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(tenant)
	if err != nil {
		return "", errors.New("error interno")
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO whatsapp_bot_config (restaurant_id, config_json) VALUES (?, ?)
		ON DUPLICATE KEY UPDATE config_json = VALUES(config_json)
	`, restaurantID, string(raw)); err != nil {
		return "", errors.New("error interno")
	}
	botServiceSessionReset(restaurantID)
	botCheckpoint("wa_bot_privileged_control_v1", "restaurant_id=%d applied-direct resource=%s action=%s", restaurantID, a.Resource, a.Action)
	return summary, nil
}

// botAccessPreview is the exact human summary sent to the chat before any
// change exists: what would happen, nothing else.
func botAccessPreview(a botAccessArgs) (string, error) {
	var probe botTenantConfig
	if _, err := botAccessApply(&probe, a); err != nil {
		return "", err
	}
	// botAccessApply already validated the args against a probe tenant; the
	// preview states the intent verbosely so the code is confirmed knowingly.
	verb := map[string]string{"add": "AÑADIR", "remove": "QUITAR", "set": "FIJAR"}[a.Action]
	return fmt.Sprintf("Propuesta de acceso (%s / %s): %s — target=%s%s. Nada está aplicado todavía.",
		a.Resource, a.Action, verb, strings.TrimSpace(a.Target),
		func() string {
			if len(a.Tools) > 0 {
				return " tools=[" + strings.Join(a.Tools, ", ") + "]"
			}
			return ""
		}()), nil
}

// botPrivilegedControlAuthorize is the actor gate of this bot version: only an
// allowed (privileged) conversation, from its proven actor. A privileged group
// re-proves membership against a FRESH provider participant list; a privileged
// contact IS the credential (the conversation itself was selected).
func (s *Server) botPrivilegedControlAuthorize(ctx context.Context, restaurantID int, msg botWebhookMessage) (actor, origin string, ok bool) {
	if msg.FromMe || !msg.PrivilegedGroup {
		return "", "", false
	}
	if msg.IsGroup {
		actor = strings.TrimSpace(msg.ParticipantJID)
	} else {
		actor = strings.TrimSpace(msg.ActorPhoneJID)
		if actor == "" {
			// A one-to-one chat the provider only addresses by its LID: the
			// conversation itself identifies the requester.
			actor = strings.TrimSpace(msg.ChatJID)
		}
	}
	origin = strings.TrimSpace(msg.ChatJID)
	if actor == "" || origin == "" {
		return "", "", false
	}
	if msg.IsGroup && !s.botSpecialAdminFreshMember(ctx, restaurantID, origin, actor) {
		return "", "", false
	}
	return actor, origin, true
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

// botToolManageAccess executes manage_bot_access. list answers immediately;
// every mutation becomes a propose+OK proposal the model cannot approve.
func (s *Server) botToolManageAccess(ctx context.Context, restaurantID int, msg botWebhookMessage, input json.RawMessage) (string, error) {
	var a botAccessArgs
	if err := json.Unmarshal(input, &a); err != nil {
		return botJSON(map[string]any{"error": "argumentos ilegibles"}), nil
	}
	a.Resource = strings.ToLower(strings.TrimSpace(a.Resource))
	a.Action = strings.ToLower(strings.TrimSpace(a.Action))
	if a.Resource == "" || a.Action == "" {
		return botJSON(map[string]any{"error": "faltan resource y action"}), nil
	}
	if a.Action == "list" {
		tenant := s.botStoredTenantConfig(ctx, restaurantID)
		botCheckpoint("wa_bot_privileged_control_v1", "restaurant_id=%d action=list", restaurantID)
		return botJSON(map[string]any{
			"allowed_chats":            botPrivilegedContactsFromConfig(tenant),
			"tool_grants":              tenant.ToolGrants,
			"management_group":         map[string]any{"name": tenant.ManagementGroupName, "jid": tenant.ManagementGroupJID},
			"privileged_direct_writes": tenant.PrivilegedDirectWrites,
		}), nil
	}
	actor, origin, ok := s.botPrivilegedControlAuthorize(ctx, restaurantID, msg)
	if !ok {
		botCheckpoint("wa_bot_privileged_control_v1", "restaurant_id=%d denied=actor", restaurantID)
		return botJSON(map[string]any{"error": "no autorizado para gestionar el acceso desde este chat"}), nil
	}
	// Coordination id: wa_bot_privileged_direct_writes_v1: the owner group
	// applies access changes directly, without the OK flow.
	if s.botStoredTenantConfig(ctx, restaurantID).PrivilegedDirectWrites {
		summary, err := s.botAccessApplyNow(ctx, restaurantID, a)
		if err != nil {
			return botJSON(map[string]any{"error": err.Error()}), nil
		}
		return botJSON(map[string]any{"ok": true, "status": summary}), nil
	}
	preview, err := botAccessPreview(a)
	if err != nil {
		return botJSON(map[string]any{"error": err.Error()}), nil
	}
	code, err := botSpecialNewCode()
	if err != nil {
		return botJSON(map[string]any{"error": "no se pudo generar el código de confirmación"}), nil
	}
	// before_hash is the config this preview was computed against: if the
	// config moves before the code arrives, the apply is refused as stale.
	before := botSha(s.botConfigRaw(ctx, restaurantID))
	out, err := s.botProposalPersist(ctx, restaurantID, msg, "access", 0, a, "access:"+a.Resource+":"+a.Action, actor, origin, before, "", preview, code)
	if err != nil {
		return botJSON(map[string]any{"error": "no se pudo guardar la propuesta"}), nil
	}
	botCheckpoint("wa_bot_privileged_control_v1", "restaurant_id=%d proposed resource=%s action=%s", restaurantID, a.Resource, a.Action)
	return out, nil
}

// botAccessExecute applies an approved access proposal inside the approval
// transaction (claim + write + commit are atomic). Dispatched by
// botSpecialAdminExecute for op "access".
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
		return "", errors.New("la configuración cambió desde la vista previa; pide una propuesta nueva")
	}
	tenant := parseBotTenantConfig(current.String)
	summary, err := botAccessApply(&tenant, a)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(tenant)
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
	botCheckpoint("wa_bot_privileged_control_v1", "restaurant_id=%d applied resource=%s action=%s", rid, a.Resource, a.Action)
	// A changed selection or grant set makes the old panel service session
	// unnecessary (same rule as botSavePrivilegedContacts).
	botServiceSessionReset(rid)
	return summary, nil
}
