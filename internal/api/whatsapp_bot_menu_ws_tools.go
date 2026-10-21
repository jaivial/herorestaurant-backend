package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

// Coordination id: wa_bot_menu_ws_v1 — the four WebSocket-only menu
// operations of the backoffice menu editor (beverage_set, beverage_create,
// beverage_delete and weekday_set) exposed to the WhatsApp bot as PROTECTED
// tools.
//
// Why they need their own tools: these operations exist ONLY on the socket
// GET /admin/group-menus-v2/ws (handleBOGroupMenusV2AIWS). There is no REST
// twin in the catalogue (the only REST route of the family is the read
// GET /admin/group-menus-v2/{id}/beverage-options), so the generic replay of
// manage_backoffice cannot reach them and admin_call cannot either.
//
// They keep the SAME protection as the rest of the surface
// (docs/wa-customtools-matrix.md):
//
//   - actor gate wa_bot_privileged_boundary_v1 on EVERY call: exact
//     authorized group + fresh provider membership + verified phone;
//   - propose->OK chain: nothing is applied without the human answering
//     "OK <code>" in the same chat as the same actor (the code travels
//     straight to the chat through the gateway, never through the model);
//   - preview with method and path PINNED: the proposal stores Method/Path
//     and the apply refuses if they do not match anymore (anti-falsification);
//     the body is the exact socket envelope, in one line and never truncated;
//   - contained effects: only the menu tables the panel itself writes, plus
//     the SAME broadcast the editor emits so open panels reconcile. Nothing
//     reaches a client, nothing sends messages, no files are touched;
//   - three-valued outcome: a failure that may have written is UNCERTAIN —
//     the proposal is marked consumed (state 'uncertain'), is never retried
//     blindly and the chat is asked to verify in the panel.
//
// The stored plan carries the SAME fields as the socket envelope the panel
// editor sends (type, menu_id, option_id, …): what the human approves is what
// runs.
const (
	// The fourth proposal slot: a pending menu operation is neither superseded
	// by a pending booking, panel (-1) nor control-plane (-2) proposal, and no
	// other op can consume its code (uq_wa_op_active is (restaurant_id,
	// booking_id, active)).
	botProposalSlotMenuWS = int64(-3)

	// The transport of a socket operation, pinned exactly like Method+Path of
	// a REST operation in the backoffice surface.
	botMenuWSMethod = "WS"
	botMenuWSPath   = "/admin/group-menus-v2/ws"
)

// botMenuWSSpec is one WS-only menu operation: its tool name, the description
// the model sees and the CONCRETE consequences the human reads in the preview
// (an approval has to be informed: "risk: write" alone is not enough).
type botMenuWSSpec struct {
	Name        string
	Description string
	Consequence string
}

// botMenuWSOps is the closed catalogue of the four operations. A tool name is
// the socket message type, so the operation a tool proposes is the very
// message the editor would emit.
var botMenuWSOps = []botMenuWSSpec{
	{
		Name: "beverage_set",
		Description: "Marca o desmarca una bebida en la carta de un menú (operación del panel que SOLO existe por WebSocket, sin gemelo REST). " +
			"NO aplica nada: genera una propuesta con la vista previa exacta y un código, y la operación se aplica solo cuando la MISMA persona responde 'OK <código>' en este chat. " +
			"Campos: menu_id (obligatorio), option_id (la bebida) y selected (true para seleccionarla, false para quitarla). " +
			"Tras proponer, di solo que la propuesta se ha enviado al chat y que hay que confirmar con el código: NO digas que está hecho y no inventes el código.",
		Consequence: "marca o desmarca una bebida en la carta de este menú (menu_beverage_options, solo este menú); no avisa a ningún cliente, no envía mensajes ni toca ficheros, y refresca los editores del panel abiertos en este menú.",
	},
	{
		Name: "beverage_create",
		Description: "Crea una bebida personalizada en la carta del restaurante (operación del panel que SOLO existe por WebSocket, sin gemelo REST). " +
			"NO aplica nada: genera una propuesta con la vista previa exacta y un código, y la operación se aplica solo cuando la MISMA persona responde 'OK <código>' en este chat. " +
			"Campos: menu_id (obligatorio) y name (nombre de la bebida). La crea sin seleccionarla en el menú: marcarla es beverage_set. " +
			"Tras proponer, di solo que la propuesta se ha enviado al chat y que hay que confirmar con el código: NO digas que está hecho y no inventes el código.",
		Consequence: "crea una bebida personalizada en la carta del restaurante (restaurant_beverage_options, is_custom=1) sin seleccionarla en el menú; no avisa a ningún cliente, no envía mensajes ni toca ficheros, y refresca los editores del panel abiertos en este menú.",
	},
	{
		Name: "beverage_delete",
		Description: "Desactiva (borrado lógico) una bebida personalizada de la carta del restaurante (operación del panel que SOLO existe por WebSocket, sin gemelo REST). " +
			"NO aplica nada: genera una propuesta con la vista previa exacta y un código, y la operación se aplica solo cuando la MISMA persona responde 'OK <código>' en este chat. " +
			"Campos: menu_id (obligatorio) y option_id. Solo se pueden borrar bebidas personalizadas (is_custom=1): las de la carta predeterminada no se borran desde aquí. " +
			"Tras proponer, di solo que la propuesta se ha enviado al chat y que hay que confirmar con el código: NO digas que está hecho y no inventes el código.",
		Consequence: "desactiva una bebida personalizada (borrado lógico active=0, recuperable en la base de datos; solo is_custom=1); no borra ficheros ni envía nada a clientes, y refresca los editores del panel abiertos en este menú.",
	},
	{
		Name: "weekday_set",
		Description: "Fija la disponibilidad de un día de la semana en el calendario de un menú (operación del panel que SOLO existe por WebSocket, sin gemelo REST). " +
			"NO aplica nada: genera una propuesta con la vista previa exacta y un código, y la operación se aplica solo cuando la MISMA persona responde 'OK <código>' en este chat. " +
			"Campos: menu_id (obligatorio), weekday (monday..sunday, lunes..domingo o 1..7) y available (true/false). " +
			"Tras proponer, di solo que la propuesta se ha enviado al chat y que hay que confirmar con el código: NO digas que está hecho y no inventes el código.",
		Consequence: "fija la disponibilidad de un día en el calendario semanal del menú (menu_weekday_availability): decide los días en que este menú se puede reservar; no avisa a ningún cliente ni envía mensajes, y refresca los editores del panel abiertos en este menú.",
	},
}

// botMenuWSByTool resolves one of the four operations by its tool name (which
// IS the socket message type).
func botMenuWSByTool(name string) (botMenuWSSpec, bool) {
	for _, spec := range botMenuWSOps {
		if spec.Name == name {
			return spec, true
		}
	}
	return botMenuWSSpec{}, false
}

// botMenuWSSchema is the shared input schema of the four tools: the fields of
// the socket envelope, no more.
func botMenuWSSchema() json.RawMessage {
	return botSchema(`{"type":"object","properties":{
		"menu_id":{"type":"integer","description":"Id del menú de grupo"},
		"option_id":{"type":"integer","description":"Id de la opción de bebida (beverage_set y beverage_delete)"},
		"selected":{"type":"boolean","description":"Seleccionar (true) o no (false) la bebida (beverage_set)"},
		"name":{"type":"string","description":"Nombre de la bebida a crear (beverage_create)"},
		"weekday":{"type":"string","description":"Día: monday..sunday, lunes..domingo o 1..7 (weekday_set)"},
		"available":{"type":"boolean","description":"Si el día queda disponible (true) o no (false) (weekday_set)"}
	},"required":["menu_id"]}`)
}

// botToolDefsMenuWS advertises the four WS-only operations. Like
// botToolDefsBackoffice they only exist on privileged turns and every call
// re-proves the actor; the executor dispatches all four to the shared
// propose handler.
func botToolDefsMenuWS() []botToolDef {
	schema := botMenuWSSchema()
	defs := make([]botToolDef, 0, len(botMenuWSOps))
	for _, spec := range botMenuWSOps {
		defs = append(defs, botToolDef{Name: spec.Name, Description: spec.Description, InputSchema: schema})
	}
	return defs
}

// botMenuWSPayload is the socket envelope of the panel editor for these four
// operations. The same JSON is previewed, approved and applied: nothing is
// implicit in what the human reads.
//
// Type is NEVER taken from the model: the propose handler overwrites it with
// the tool name, so beverage_set can never store a weekday_set envelope.
type botMenuWSPayload struct {
	Type      string `json:"type"`
	MenuID    int64  `json:"menu_id"`
	OptionID  int64  `json:"option_id,omitempty"`
	Selected  *bool  `json:"selected,omitempty"`
	Name      string `json:"name,omitempty"`
	Weekday   string `json:"weekday,omitempty"`
	Available *bool  `json:"available,omitempty"`
}

// botMenuWSValidate checks every precondition BEFORE the proposal exists (and
// again before the apply, because the data may have moved inside the TTL). It
// canonicalizes the payload in place (weekday key, trimmed name), so what
// applies is exactly what was validated.
//
// A failure here is CERTAIN: nothing has been written yet.
func (s *Server) botMenuWSValidate(ctx context.Context, rid int, p *botMenuWSPayload) error {
	if p.MenuID <= 0 {
		return errors.New("falta menu_id (el menú sobre el que se aplica)")
	}
	// Tenant containment: the panel socket is scoped by the session; here the
	// scope has to be proven, so a foreign menu id is refused before writing.
	var menus int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM menus WHERE id = ? AND restaurant_id = ?`, p.MenuID, rid).Scan(&menus); err != nil {
		return errors.New("no se pudo verificar el menú")
	}
	if menus == 0 {
		return errors.New("no hay ningún menú con ese id en este restaurante")
	}
	switch p.Type {
	case "beverage_set":
		if p.OptionID <= 0 || p.Selected == nil {
			return errors.New("option_id y selected son obligatorios")
		}
		if !s.beverageOptionBelongs(rid, p.OptionID) {
			return errors.New("opción de bebida no válida para este restaurante")
		}
	case "beverage_create":
		p.Name = strings.TrimSpace(p.Name)
		if p.Name == "" || len(p.Name) > 255 {
			return errors.New("name obligatorio (máximo 255 caracteres)")
		}
		if beverageOptionSlug(p.Name) == "" {
			return errors.New("ese name no genera una bebida válida")
		}
	case "beverage_delete":
		if p.OptionID <= 0 {
			return errors.New("option_id obligatorio")
		}
		var custom, active int
		err := s.db.QueryRowContext(ctx, `SELECT is_custom, active FROM restaurant_beverage_options WHERE id = ? AND restaurant_id = ?`, p.OptionID, rid).Scan(&custom, &active)
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("opción de bebida no válida para este restaurante")
		}
		if err != nil {
			return errors.New("no se pudo verificar la opción de bebida")
		}
		if active == 0 {
			return errors.New("esa bebida ya está desactivada")
		}
		if custom == 0 {
			return errors.New("solo se pueden borrar bebidas personalizadas: las de la carta predeterminada no se borran desde aquí")
		}
	case "weekday_set":
		key := normalizeBOMenuWeekday(p.Weekday)
		if key == "" || p.Available == nil {
			return errors.New("weekday y available son obligatorios (weekday: monday..sunday, lunes..domingo o 1..7)")
		}
		p.Weekday = key
	default:
		return errors.New("operación desconocida")
	}
	return nil
}

// botMenuWSPreview is the exact human summary of a WS-only menu proposal.
// Same guarantees as botBackofficePreview: the FIRST line pins method and
// path, the body is one line and is never truncated (a body that does not fit
// is refused), every consequence is named concretely and the proposal states
// that nothing is applied yet.
func botMenuWSPreview(spec botMenuWSSpec, plan backofficeOpPlan) (string, error) {
	body := strings.TrimSpace(string(plan.Body))
	if body == "" || body == "null" {
		return "", errors.New("la operación no tiene cuerpo")
	}
	if len(body) > botBackofficePreviewMaxBody {
		return "", fmt.Errorf("el cuerpo de la operacion es demasiado grande (%d caracteres) para que se pueda revisar en la vista previa: reduce la operacion o hazla en varios pasos", len(body))
	}
	// The body is compact JSON (json.Marshal escapes control characters), but
	// the guarantee is checked anyway: a newline in the preview could forge a
	// second line and misrepresent what is being approved.
	if strings.ContainsAny(body, "\n\r") {
		return "", errors.New("el cuerpo de la operacion debe ir en una sola linea (JSON compacto) para que la vista previa sea exacta")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Propuesta de operacion del panel (%s): %s %s", spec.Name, plan.Method, plan.Path)
	fmt.Fprintf(&b, " cuerpo: %s", body)
	fmt.Fprintf(&b, " [riesgo: write]")
	fmt.Fprintf(&b, ". Efectos: %s", spec.Consequence)
	b.WriteString(". Nada esta aplicado todavia.")
	return b.String(), nil
}

// botToolMenuWSPropose executes one of the four tools: it NEVER applies the
// operation — it validates, stores the proposal (own slot, pinned transport)
// and sends the preview + code straight to the origin chat through the
// gateway. The apply only happens on the human approval.
func (s *Server) botToolMenuWSPropose(ctx context.Context, rid int, msg botWebhookMessage, name string, input json.RawMessage) (string, error) {
	spec, ok := botMenuWSByTool(name)
	if !ok {
		return botJSON(map[string]any{"error": "operacion desconocida: " + name}), nil
	}
	var payload botMenuWSPayload
	if err := json.Unmarshal(input, &payload); err != nil {
		return botJSON(map[string]any{"error": "argumentos ilegibles"}), nil
	}
	// The message type comes from the TOOL, never from the arguments.
	payload.Type = spec.Name
	// The actor: the verified phone of a current member of the authorized
	// group (wa_bot_privileged_boundary_v1). Every action of this tool goes
	// through the same gate as manage_backoffice, before any validation is
	// revealed to the caller.
	actor, origin, ok := s.botPrivilegedActor(ctx, rid, msg)
	if !ok {
		log.Printf("[bot] checkpoint wa_bot_menu_ws_v1 restaurant_id=%d op=%s decision=denied_actor", rid, spec.Name)
		return botJSON(map[string]any{"error": "no autorizado para usar las operaciones del panel desde este chat"}), nil
	}
	if verr := s.botMenuWSValidate(ctx, rid, &payload); verr != nil {
		log.Printf("[bot] checkpoint wa_bot_menu_ws_v1 restaurant_id=%d op=%s decision=invalid_args", rid, spec.Name)
		return botJSON(map[string]any{"error": verr.Error()}), nil
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return botJSON(map[string]any{"error": "argumentos ilegibles"}), nil
	}
	plan := backofficeOpPlan{Op: spec.Name, Method: botMenuWSMethod, Path: botMenuWSPath, Body: body}
	preview, err := botMenuWSPreview(spec, plan)
	if err != nil {
		return botJSON(map[string]any{"error": err.Error()}), nil
	}
	code, err := botSpecialNewCode()
	if err != nil {
		return botJSON(map[string]any{"error": "no se pudo generar el codigo de confirmacion"}), nil
	}
	log.Printf("[bot] checkpoint wa_bot_menu_ws_v1 restaurant_id=%d op=%s proposed", rid, spec.Name)
	return s.botProposalPersist(ctx, rid, msg, "menu_ws", botProposalSlotMenuWS, plan, "menu_ws:"+spec.Name, actor, origin, "", "", preview, code)
}

// botMenuWSExecute applies an approved WS-only menu proposal.
//
// The claim of the code (single use, atomic) is committed BEFORE this runs
// (botSpecialAdminExecute): the helpers write with the server connection, so
// they cannot share the claim's transaction. The outcome is THREE-valued, as
// in the backoffice replay: validation refusals are certain (nothing written),
// a helper failure may have written and is therefore UNCERTAIN — the
// proposal is marked consumed and never retried blindly.
func (s *Server) botMenuWSExecute(ctx context.Context, rid int, msg botWebhookMessage, p botSpecialProposalRow) (out string, err error) {
	// The claim is already committed, so a panic anywhere below is an
	// UNKNOWN state: mark the proposal consumed ('uncertain') and report it
	// as uncertain, never as "not applied".
	defer func() {
		if r := recover(); r != nil {
			reason := fmt.Sprintf("el panel ha fallado de forma inesperada (%v)", r)
			log.Printf("[bot] checkpoint wa_bot_menu_ws_v1 restaurant_id=%d op=%s decision=uncertain reason=%s", rid, p.Op, reason)
			s.botBackofficeMarkUncertain(ctx, rid, msg, p, reason)
			out, err = "", &botBackofficeUncertainError{reason: reason}
		}
	}()
	var plan backofficeOpPlan
	if jerr := json.Unmarshal([]byte(p.Args), &plan); jerr != nil {
		return "", errors.New("propuesta ilegible")
	}
	spec, ok := botMenuWSByTool(plan.Op)
	if !ok {
		return "", errors.New("la operacion ya no esta en el catalogo")
	}
	// The transport was pinned at propose time: if it does not match the
	// catalogue anymore (a deploy inside the TTL), the operation is refused
	// instead of applying something the human did not see.
	if plan.Method != botMenuWSMethod || plan.Path != botMenuWSPath {
		return "", errors.New("la operacion cambio en el catalogo desde la vista previa; pide una propuesta nueva")
	}
	var payload botMenuWSPayload
	if jerr := json.Unmarshal(plan.Body, &payload); jerr != nil || payload.Type != spec.Name {
		return "", errors.New("la propuesta no corresponde a esta operacion")
	}
	// Re-validated against the CURRENT data: the menu may be gone, the bebida
	// may already be deactivated. All of it happens before any write.
	if verr := s.botMenuWSValidate(ctx, rid, &payload); verr != nil {
		log.Printf("[bot] checkpoint wa_bot_menu_ws_v1 restaurant_id=%d op=%s decision=refused_validation", rid, spec.Name)
		return "", errors.New("la operacion no se aplico: " + verr.Error())
	}
	detail, aerr := s.botMenuWSApply(ctx, rid, payload, fmt.Sprintf("wa_bot_menu_ws:%d", p.ID))
	if aerr != nil {
		// The write was attempted and could not be confirmed: the proposal is
		// consumed (state 'uncertain'), the chat is told to VERIFY and nothing
		// is proposed again automatically.
		log.Printf("[bot] checkpoint wa_bot_menu_ws_v1 restaurant_id=%d op=%s decision=uncertain reason=%s", rid, spec.Name, aerr)
		s.botBackofficeMarkUncertain(ctx, rid, msg, p, aerr.Error())
		return "", &botBackofficeUncertainError{reason: aerr.Error()}
	}
	log.Printf("[bot] checkpoint wa_bot_menu_ws_v1 restaurant_id=%d op=%s applied", rid, spec.Name)
	notice := "Aplicado: " + botMenuWSMethod + " " + botMenuWSPath + " (" + spec.Name + "). " + detail
	// Best-effort trace: the operation already happened, so a failure here
	// must not change what the chat is told — it is logged, not swallowed.
	if rj, mErr := json.Marshal(map[string]any{"op": p.Op, "operation": spec.Name, "notice": notice}); mErr == nil {
		if _, wErr := s.db.ExecContext(ctx, `UPDATE wa_bot_op_proposals SET result_json=?, notify_state='pending' WHERE id=?`, string(rj), p.ID); wErr != nil {
			log.Printf("[bot] wa_bot_menu_ws_v1 rid=%d proposal=%d result_write_failed err=%v", rid, p.ID, wErr)
		}
	}
	return notice, nil
}

// botMenuWSApply performs the write and mirrors the panel's own broadcast so
// every editor watching the menu reconciles (beverage_options / weekday_saved
// frames, with the proposal correlation id as observational point across
// backend and panel). Any error here is UNCERTAIN: the helper may have
// written before failing.
func (s *Server) botMenuWSApply(ctx context.Context, rid int, p botMenuWSPayload, correlation string) (string, error) {
	switch p.Type {
	case "beverage_set":
		if err := s.upsertMenuBeverageSelection(rid, p.MenuID, p.OptionID, *p.Selected); err != nil {
			return "", err
		}
		options, _ := s.loadMenuBeverageOptions(rid, p.MenuID)
		s.botMenuWSBroadcast(rid, p.MenuID, map[string]any{
			"type": "beverage_options", "menu_id": p.MenuID, "options": options,
			"correlation_id": correlation, "observed_at": time.Now().UTC().Format(time.RFC3339),
		})
		state := "quitada de la seleccion"
		if *p.Selected {
			state = "seleccionada"
		}
		return fmt.Sprintf("menu %d: bebida (opcion %d) %s.", p.MenuID, p.OptionID, state), nil
	case "beverage_create":
		option, err := s.createRestaurantBeverageOption(rid, p.Name)
		if err != nil {
			return "", err
		}
		options, _ := s.loadMenuBeverageOptions(rid, p.MenuID)
		s.botMenuWSBroadcast(rid, p.MenuID, map[string]any{
			"type": "beverage_options", "menu_id": p.MenuID, "options": options,
			"correlation_id": correlation, "observed_at": time.Now().UTC().Format(time.RFC3339),
		})
		return fmt.Sprintf("menu %d: bebida personalizada %q creada (id %d), sin seleccionar en el menu.", p.MenuID, option.Name, option.ID), nil
	case "beverage_delete":
		if err := s.deleteRestaurantBeverageOption(rid, p.OptionID); err != nil {
			return "", err
		}
		options, _ := s.loadMenuBeverageOptions(rid, p.MenuID)
		s.botMenuWSBroadcast(rid, p.MenuID, map[string]any{
			"type": "beverage_options", "menu_id": p.MenuID, "options": options,
			"correlation_id": correlation, "observed_at": time.Now().UTC().Format(time.RFC3339),
		})
		return fmt.Sprintf("menu %d: bebida personalizada (opcion %d) desactivada.", p.MenuID, p.OptionID), nil
	case "weekday_set":
		if err := s.saveBOMenuWeekday(ctx, rid, p.MenuID, p.Weekday, *p.Available); err != nil {
			return "", err
		}
		weekdays, werr := s.loadBOMenuWeekdays(ctx, rid, p.MenuID)
		if werr != nil {
			weekdays = emptyBOMenuWeekdays()
		}
		s.botMenuWSBroadcast(rid, p.MenuID, map[string]any{
			"type": "weekday_saved", "restaurant_id": rid, "menu_id": p.MenuID,
			"weekday": p.Weekday, "available": *p.Available,
			"menu_weekdays": weekdays, "weekdays": weekdays,
			"correlation_id": correlation, "at": time.Now().UTC().Format(time.RFC3339),
		})
		state := "no disponible"
		if *p.Available {
			state = "disponible"
		}
		return fmt.Sprintf("menu %d: %s %s.", p.MenuID, p.Weekday, state), nil
	}
	return "", errors.New("operacion desconocida")
}

// botMenuWSBroadcast mirrors the panel's own broadcast (the WS handlers of
// beverage_options.go / menu_weekday_availability.go), so the bot write leaves
// open editors exactly as a human edit would.
func (s *Server) botMenuWSBroadcast(rid int, menuID int64, frame map[string]any) {
	if s.groupMenusV2AIHub == nil {
		return
	}
	s.groupMenusV2AIHub.broadcast(rid, menuID, frame)
}
