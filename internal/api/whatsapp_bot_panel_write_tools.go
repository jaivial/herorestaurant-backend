package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
)

// Coordination id: wa_bot_panel_write_v1
//
// The missing WRITE half of the backoffice surface for this bot version: ONE
// parameterised tool (panel_change) that can propose ANY panel operation the
// backoffice can perform, so the allowed (privileged) contacts and groups get
// full backoffice+backend control with the one approval mechanism bound to an
// exact preview: the shared propose+OK machinery (wa_bot_special_admin_v2).
// This is exactly the "approval bound to the exact preview" the containment
// wa_bot_admin_write_denied_v1 was waiting for.
//
// Two operation families, one tool (minimum common multiple):
//   - op-map operations (forky_admin_ops.json, the generated panel route
//     map): replayed through the REAL router with the panel service session
//     on approval (same safety net as admin_call: tenant scope, no /me, no
//     account management, no session rebinding);
//   - direct operations: the panel mutations that live ONLY on the WebSocket
//     command surface (no REST twin to replay), each one reusing the exact
//     domain function its WS handler calls.
//
// Reads keep using admin_call / staff_domain_read. Nothing here executes
// without the requester writing "OK <code>" in the same chat.

// botPanelArgs is THE parameterised payload of panel_change: one shape for
// every panel mutation, op-map or direct.
type botPanelArgs struct {
	Operation  string          `json:"operation"`
	PathParams map[string]any  `json:"path_params,omitempty"`
	Query      map[string]any  `json:"query,omitempty"`
	Body       json.RawMessage `json:"body,omitempty"`
	Params     map[string]any  `json:"params,omitempty"`
}

// botPanelDirectOp mutates panel state outside the op map (WS-only commands).
type botPanelDirectOp func(s *Server, ctx context.Context, rid int, p map[string]any) (any, error)

// botPanelDirectOps are the panel mutations with NO REST twin (WS-command
// only): without this registry they could never be performed from a chat.
// Each entry calls the same domain function the WS handler calls.
var botPanelDirectOps = map[string]botPanelDirectOp{
	"beverage_set": func(s *Server, ctx context.Context, rid int, p map[string]any) (any, error) {
		menuID, optionID := botPanelInt64(p, "menu_id"), botPanelInt64(p, "option_id")
		selected, ok := p["selected"].(bool)
		if menuID <= 0 || optionID <= 0 || !ok {
			return nil, errors.New("menu_id, option_id y selected son obligatorios")
		}
		if err := s.upsertMenuBeverageSelection(rid, menuID, optionID, selected); err != nil {
			return nil, errors.New("no se pudo guardar la bebida")
		}
		return map[string]any{"menu_id": menuID, "option_id": optionID, "selected": selected}, nil
	},
	"beverage_create": func(s *Server, ctx context.Context, rid int, p map[string]any) (any, error) {
		name := botPanelString(p, "name")
		if name == "" {
			return nil, errors.New("name es obligatorio")
		}
		opt, err := s.createRestaurantBeverageOption(rid, name)
		if err != nil {
			return nil, errors.New("no se pudo crear la bebida")
		}
		return map[string]any{"option_id": opt.ID, "name": name}, nil
	},
	"beverage_delete": func(s *Server, ctx context.Context, rid int, p map[string]any) (any, error) {
		optionID := botPanelInt64(p, "option_id")
		if optionID <= 0 {
			return nil, errors.New("option_id es obligatorio")
		}
		if err := s.deleteRestaurantBeverageOption(rid, optionID); err != nil {
			return nil, errors.New("no se pudo eliminar la bebida")
		}
		return map[string]any{"option_id": optionID, "deleted": true}, nil
	},
	"weekday_set": func(s *Server, ctx context.Context, rid int, p map[string]any) (any, error) {
		menuID := botPanelInt64(p, "menu_id")
		weekday := normalizeBOMenuWeekday(botPanelString(p, "weekday"))
		available, ok := p["available"].(bool)
		if menuID <= 0 || weekday == "" || !ok {
			return nil, errors.New("menu_id, weekday y available son obligatorios")
		}
		if err := s.saveBOMenuWeekday(ctx, rid, menuID, weekday, available); err != nil {
			return nil, errors.New("no se pudo guardar la disponibilidad")
		}
		return map[string]any{"menu_id": menuID, "weekday": weekday, "available": available}, nil
	},
	"booking_document_delete": func(s *Server, ctx context.Context, rid int, p map[string]any) (any, error) {
		id := botPanelInt64(p, "id")
		if id <= 0 {
			return nil, errors.New("id es obligatorio")
		}
		if err := s.deleteBookingDocument(ctx, rid, id); err != nil {
			return nil, errors.New("no se pudo eliminar el documento")
		}
		return map[string]any{"id": id, "deleted": true}, nil
	},
}

// botPanelChangeToolDefs is the catalog of the approved-write surface.
func botPanelChangeToolDefs() []botToolDef {
	return []botToolDef{
		{
			Name: "panel_change",
			Description: "PROPONE un cambio del panel de administración (una operación del backoffice) para aplicarlo desde el chat. NO lo aplica: guarda la propuesta exacta y envía la vista previa con un código; solo se aplica cuando la persona que lo pidió escribe 'OK <código>' en este chat. " +
				"operation: nombre de la operación tal cual aparece en admin_catalog, o una operación directa para lo que no tiene ruta REST: " +
				"beverage_set {menu_id, option_id, selected} | beverage_create {name} | beverage_delete {option_id} | weekday_set {menu_id, weekday, available} | " +
				"booking_document_delete {id} | editor_preview_set {menu_id, user_id, open} | table_position_set {id, floor_number, x_pos, y_pos, date?, scope?} | ad_schedule_check {ad_id, starts_at, ends_at} | " +
				"media_upload {kind: booking_document|special_section|vino|ads, data_base64, filename, mime_type, ...ids} | media_ai {kind: vino|vino_cutout|comida|section_dish|menu_preview|slider|ads_enhance|ads_generate, target_id..., data_base64?, prompt?} | " +
				"site_builder_call {type: sites.*|pages.*|components.list|publish.*|domains.*, payload} | assistant_tool {name, input}. " +
				"path_params, query y body: los mismos argumentos que indica admin_describe; params: solo para operaciones directas. " +
				"Las lecturas NO van por aquí: usa admin_call o staff_domain_read.",
			InputSchema: botSchema(`{"type":"object","properties":{
				"operation":{"type":"string","description":"Nombre de la operación del panel (admin_catalog) o directa"},
				"path_params":{"type":"object","description":"Parámetros de ruta (admin_describe)"},
				"query":{"type":"object","description":"Parámetros de consulta (admin_describe)"},
				"body":{"type":"object","description":"Cuerpo JSON de la escritura (admin_describe)"},
				"params":{"type":"object","description":"Solo operaciones directas"}
			},"required":["operation"]}`),
		},
	}
}

// botPanelVisibleOp resolves one op with the SAME ACL admin_call uses (the
// synthetic root auth: superadmin and account ops are never reachable).
func (s *Server) botPanelVisibleOp(ctx context.Context, rid int, name string) (forkyAdminOp, error) {
	ops, _, err := s.forkyAdminVisibleOps(s.botPrivilegedForkyContext(ctx, rid))
	if err != nil {
		return forkyAdminOp{}, err
	}
	for _, o := range ops {
		if o.Name == name {
			return o, nil
		}
	}
	return forkyAdminOp{}, fmt.Errorf("operación desconocida para este usuario: %s (usa admin_catalog)", name)
}

// botToolPanelChange executes panel_change: validation now, proposal always.
func (s *Server) botToolPanelChange(ctx context.Context, restaurantID int, msg botWebhookMessage, input json.RawMessage) (string, error) {
	var a botPanelArgs
	if err := json.Unmarshal(input, &a); err != nil {
		return botJSON(map[string]any{"error": "argumentos ilegibles"}), nil
	}
	a.Operation = strings.TrimSpace(a.Operation)
	if a.Operation == "" {
		return botJSON(map[string]any{"error": "falta operation"}), nil
	}
	actor, origin, ok := s.botPrivilegedControlAuthorize(ctx, restaurantID, msg)
	if !ok {
		log.Printf("[bot] checkpoint wa_bot_panel_write_v1 restaurant_id=%d denied=actor", restaurantID)
		return botJSON(map[string]any{"error": "no autorizado para proponer cambios del panel desde este chat"}), nil
	}
	if _, direct := botPanelDirectOps[a.Operation]; !direct {
		op, err := s.botPanelVisibleOp(ctx, restaurantID, a.Operation)
		if err != nil {
			return botJSON(map[string]any{"error": err.Error()}), nil
		}
		if !op.Write {
			return botJSON(map[string]any{"error": "esa operación es de lectura: usa admin_call, no panel_change"}), nil
		}
		// Refuse BEFORE proposing what could never be applied (same safety
		// net as admin_call).
		if err := forkyAdminCheckOp(restaurantID, op, a.PathParams); err != nil {
			return botJSON(map[string]any{"error": err.Error()}), nil
		}
	}
	preview := s.botPanelPreview(ctx, restaurantID, a)
	code, err := botSpecialNewCode()
	if err != nil {
		return botJSON(map[string]any{"error": "no se pudo generar el código de confirmación"}), nil
	}
	out, err := s.botProposalPersist(ctx, restaurantID, msg, "panel", 0, a, "panel:"+a.Operation, actor, origin, "", "", preview, code)
	if err != nil {
		return botJSON(map[string]any{"error": "no se pudo guardar la propuesta"}), nil
	}
	log.Printf("[bot] checkpoint wa_bot_panel_write_v1 restaurant_id=%d proposed op=%s", restaurantID, a.Operation)
	return out, nil
}

// botPanelPreview is the exact change summary the requester confirms.
func (s *Server) botPanelPreview(ctx context.Context, rid int, a botPanelArgs) string {
	if _, direct := botPanelDirectOps[a.Operation]; direct {
		return fmt.Sprintf("Propuesta de cambio del panel (%s, operación directa): params=%s. Nada está aplicado todavía.",
			a.Operation, botJSON(a.Params))
	}
	method, path := "", a.Operation
	if op, err := s.botPanelVisibleOp(ctx, rid, a.Operation); err == nil {
		method, path = op.Method, op.Path
	}
	body := strings.TrimSpace(string(a.Body))
	if body == "" {
		body = "{}"
	}
	return fmt.Sprintf("Propuesta de cambio del panel (%s): %s %s body=%s. Nada está aplicado todavía.",
		a.Operation, method, path, body)
}

// botPanelExecute applies an approved panel proposal: dispatches a direct op
// or replays the op-map operation through the real router. Claimed atomically
// by botSpecialAdminExecute before reaching here; the claim commits ONLY with
// a successful write, so a failed panel answer leaves the code retryable.
func (s *Server) botPanelExecute(ctx context.Context, tx *sql.Tx, rid int, p botSpecialProposalRow) (string, error) {
	var a botPanelArgs
	if err := json.Unmarshal([]byte(p.Args), &a); err != nil {
		return "", errors.New("propuesta ilegible")
	}
	var summary string
	if run, direct := botPanelDirectOps[a.Operation]; direct {
		res, err := run(s, ctx, rid, a.Params)
		if err != nil {
			return "", err
		}
		summary = "Aplicado (" + a.Operation + "): " + botJSON(res)
	} else {
		op, err := s.botPanelVisibleOp(ctx, rid, a.Operation)
		if err != nil {
			return "", err
		}
		if err := forkyAdminCheckOp(rid, op, a.PathParams); err != nil {
			return "", err
		}
		token, err := s.botServiceSession(ctx, rid)
		if err != nil || token == "" {
			return "", errors.New("sesión del panel no disponible")
		}
		out, err := s.forkyAdminRunOp(rid, token, op, a.PathParams, a.Query, a.Body)
		if err != nil {
			return "", err
		}
		var res struct {
			Status   int    `json:"status"`
			OK       bool   `json:"ok"`
			DataText string `json:"data_text"`
		}
		_ = json.Unmarshal([]byte(out), &res)
		if !res.OK {
			return "", fmt.Errorf("el panel no aplicó el cambio (status %d): %s", res.Status, res.DataText)
		}
		summary = "Aplicado (" + a.Operation + "): " + out
	}
	if err := tx.Commit(); err != nil {
		return "", errors.New("error interno")
	}
	log.Printf("[bot] checkpoint wa_bot_panel_write_v1 restaurant_id=%d applied op=%s", rid, a.Operation)
	return summary, nil
}

// botPanelInt64 / botPanelString read direct-op parameters fail-safe.
func botPanelInt64(p map[string]any, key string) int64 {
	switch v := p[key].(type) {
	case float64:
		return int64(v)
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		return n
	}
	return 0
}

func botPanelString(p map[string]any, key string) string {
	v, ok := p[key]
	if !ok || v == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(v))
}
