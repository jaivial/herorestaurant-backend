package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// Coordination id: wa_bot_panel_wsops_v1
//
// The NON-MEDIA WS-only half of the panel_change direct-op registry
// (wa_bot_panel_write_v1): every incoming backoffice WebSocket command that has
// no REST twin in the generated panel route map (forky_admin_ops.json) and
// would otherwise be unreachable from a privileged chat. Registered with
// init(), so this file is the only surface added.
//
// Audit of the 13 backoffice sockets — everything else already has a path:
//   - REST twin replayed by panel_change: ad_save (PUT /config/ads/{adId}),
//     table_edit data + day position (PUT /tables), layout_edit (PUT /tables,
//     entity=layout), template_edit (POST /tables/template/{floorNumber}),
//     template_delete (DELETE /tables/template/{floorNumber});
//   - existing direct ops: beverage_set/create/delete, weekday_set,
//     booking_document_delete (the media commands live in wa_bot_panel_media_v1);
//   - site_builder_call: unconditional replay of the site-builder REST twins
//     (see its comment: the op-map path needs the still-uncommitted map
//     regeneration);
//   - reads and subscriptions (admin_call / staff_domain_read / pure push):
//     join/sync/refresh/ping/hello frames, bookingDocumentList, sheets search,
//     global / reservas / whatsapp-connection / stripe / vinos / comida-ai push.
//
// Each entry calls the SAME domain function (or the same router replay) its WS
// handler uses. Nothing here runs outside the propose+OK machinery.

// init inserts the WS-only operations into the shared direct-op registry (the
// package map is already initialized when init runs).
func init() {
	// Coordination id: wa_bot_panel_wsops_v1
	for name, run := range botPanelWSOps {
		botPanelDirectOps[name] = run
		if d := botPanelWSOpDesc[name]; d != "" {
			botPanelDirectOpDesc[name] = d
		}
	}
}

// botPanelWSOps maps each WS-only backoffice command to its bot-executable
// operation (minimum common multiple: one entry per shared nature).
var botPanelWSOps = map[string]botPanelDirectOp{
	"editor_preview_set": botWSEditorPreviewSet,
	"table_position_set": botWSTablePositionSet,
	"ad_schedule_check":  botWSAdScheduleCheck,
	"site_builder_call":  botWSSiteBuilderCall,
	"assistant_tool":     botWSAssistantTool,
}

// botPanelWSOpDesc is the human description of each direct op for the chat
// previews (wa_bot_restaurant_days_fix_v1).
var botPanelWSOpDesc = map[string]string{
	"editor_preview_set": "cambiar la preferencia de previsualización del editor",
	"table_position_set": "mover la posición de una mesa en el plano",
	"ad_schedule_check":  "comprobar si un horario de anuncio entra en conflicto",
	"site_builder_call":  "ejecutar una operación del constructor de la web",
}

// botWSEditorPreviewSet is the editor_preview_set command of the
// group-menus-v2 socket: the same domain call handleBOMenuEditorPrefWSMessage
// makes (setMenuUserPreference + normalizeBOPreference, coordination id
// menu_editor_preview_open_v1), addressed to the user the preference belongs
// to instead of the socket session.
func botWSEditorPreviewSet(s *Server, ctx context.Context, rid int, p map[string]any) (any, error) {
	menuID, userID := botPanelInt64(p, "menu_id"), botPanelInt64(p, "user_id")
	open, ok := p["open"].(bool)
	if menuID <= 0 || userID <= 0 || !ok {
		return nil, errors.New("menu_id, user_id y open son obligatorios")
	}
	value := "0"
	if open {
		value = "1"
	}
	norm, valid := normalizeBOPreference(boMenuEditorPreviewPrefKey, value)
	if !valid {
		return nil, errors.New("open no es un valor válido")
	}
	if err := s.setMenuUserPreference(ctx, int(userID), rid, menuID, boMenuEditorPreviewPrefKey, norm); err != nil {
		return nil, errors.New("no se pudo guardar la vista del editor")
	}
	return map[string]any{"menu_id": menuID, "user_id": userID, "open": norm == "1"}, nil
}

// botWSTablePositionSet is the position half of the WS table_edit command
// (backoffice_premium.go): upsertBOPremiumTablePositionScoped, the very same
// domain call. Day positions also exist as REST (PUT /tables) but the template
// scope is socket-only, so without this entry scope=template was unreachable.
func botWSTablePositionSet(s *Server, ctx context.Context, rid int, p map[string]any) (any, error) {
	tableID := botPanelInt64(p, "id")
	floor := botPanelInt64(p, "floor_number")
	date, scope := botPanelString(p, "date"), botPanelString(p, "scope")
	x, y := botPanelInt64(p, "x_pos"), botPanelInt64(p, "y_pos")
	if tableID <= 0 || p["floor_number"] == nil || floor < 0 || p["x_pos"] == nil || p["y_pos"] == nil {
		return nil, errors.New("id, floor_number, x_pos e y_pos son obligatorios")
	}
	if scope != "template" && !isDateISO(date) {
		return nil, errors.New("date (YYYY-MM-DD) es obligatoria salvo scope=template")
	}
	if _, err := s.upsertBOPremiumTablePositionScoped(ctx, rid, date, int(floor), tableID, x, y, scope); err != nil {
		return nil, errors.New("no se pudo guardar la posición de la mesa")
	}
	return map[string]any{"id": tableID, "date": date, "floor_number": floor, "x_pos": x, "y_pos": y, "scope": scope}, nil
}

// botWSAdScheduleCheck is the ad_schedule_check command of the fichaje socket
// (backoffice_fichaje.go): the same overlap probe handleWSAdScheduleCheck runs
// inline over restaurant_ads. It only reads, but the probe has no REST twin and
// answers on the socket bus, so this entry is the only way to ask it from a
// chat.
func botWSAdScheduleCheck(s *Server, ctx context.Context, rid int, p map[string]any) (any, error) {
	adID := botPanelInt64(p, "ad_id")
	start, end := botPanelString(p, "starts_at"), botPanelString(p, "ends_at")
	if !isDateISO(start) || !isDateISO(end) {
		return nil, errors.New("starts_at y ends_at (YYYY-MM-DD) son obligatorios")
	}
	if end < start {
		return nil, errors.New("ends_at no puede ser anterior a starts_at")
	}
	var (
		otherID              int64
		name                 string
		otherStart, otherEnd time.Time
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, starts_at, ends_at FROM restaurant_ads WHERE restaurant_id = ? AND active = 1 AND id <> ? AND starts_at IS NOT NULL AND ends_at IS NOT NULL AND starts_at <= ? AND ends_at >= ? ORDER BY starts_at LIMIT 1`,
		rid, adID, end, start).Scan(&otherID, &name, &otherStart, &otherEnd)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return map[string]any{"ad_id": adID, "conflict": false}, nil
	case err != nil:
		return nil, errors.New("no se pudo comprobar el horario del anuncio")
	}
	return map[string]any{"ad_id": adID, "conflict": true, "name": name,
		"starts_at": otherStart.Format("2006-01-02"), "ends_at": otherEnd.Format("2006-01-02")}, nil
}

// botWSSiteBuilderCall executes one site_builder_ws.go frame. Every frame is a
// REST twin (siteBuilderRoute -> the RegisterSiteBuilderRoutes handlers), and
// the regenerated forky_admin_ops.json in this tree (extended
// cmd/forky-admin-map) now maps those routes too — but that regeneration is a
// separate, still-uncommitted stream. This entry keeps all 17 socket message
// types executable UNCONDITIONALLY by replaying the same route through the real
// router with the panel session (the same core and safety net admin_call
// uses). It can retire once the regenerated map is committed.
func botWSSiteBuilderCall(s *Server, ctx context.Context, rid int, p map[string]any) (any, error) {
	typ := strings.ToLower(botPanelString(p, "type"))
	method, path, ok := siteBuilderRoute(typ)
	if !ok {
		return nil, fmt.Errorf("tipo de mensaje desconocido para el site builder: %s", typ)
	}
	payload, _ := p["payload"].(map[string]any)
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.New("payload ilegible")
	}
	pathParams := map[string]any{}
	for _, key := range []string{"siteId", "pageId", "domainId"} {
		if v := botPanelString(payload, key); v != "" {
			pathParams[key] = v
		}
	}
	op := forkyAdminOp{Name: "site_builder." + typ, Method: method, Path: path, Write: method != http.MethodGet}
	if err := forkyAdminCheckOp(rid, op, pathParams); err != nil {
		return nil, err
	}
	token, err := s.botServiceSession(ctx, rid)
	if err != nil || token == "" {
		return nil, errors.New("sesión del panel no disponible")
	}
	log.Printf("[bot] checkpoint wa_bot_panel_wsops_v1 restaurant_id=%d op=site_builder_call type=%s", rid, typ)
	out, err := s.forkyAdminRunOp(rid, token, op, pathParams, nil, body)
	if err != nil {
		return nil, err
	}
	var res struct {
		Status   int    `json:"status"`
		OK       bool   `json:"ok"`
		Data     any    `json:"data"`
		DataText string `json:"data_text"`
	}
	if json.Unmarshal([]byte(out), &res) != nil {
		return nil, errors.New("respuesta ilegible del panel")
	}
	if !res.OK {
		return nil, fmt.Errorf("el site builder no aplicó el cambio (status %d): %s", res.Status, res.DataText)
	}
	if res.Data != nil {
		return res.Data, nil
	}
	return map[string]any{"status": res.Status, "data_text": res.DataText}, nil
}

// botWSAssistantTool is the deterministic effect of the assistant_ws.go
// "message" frame: the SAME dispatcher every tool call of that turn runs
// (assistantExecuteTool), signed with the service session identity the same way
// the media ops sign their uploads. The conversational turn itself is an LLM
// loop and is deliberately NOT proxied: an approval must bind to an exact
// preview, and tool name + input is exactly that.
func botWSAssistantTool(s *Server, ctx context.Context, rid int, p map[string]any) (any, error) {
	name := botPanelString(p, "name")
	if name == "" {
		return nil, errors.New("name es obligatorio")
	}
	input, err := json.Marshal(p["input"])
	if err != nil {
		return nil, errors.New("input ilegible")
	}
	userID, err := s.botServiceSessionUser(ctx)
	if err != nil {
		return nil, errors.New("sesión del panel no disponible")
	}
	// Root synthetic auth scoped to the bot restaurant: the assistant tools
	// resolve their tenant and actor from this context.
	toolCtx, cancel := context.WithTimeout(ctx, assistantToolTimeout(name))
	defer cancel()
	toolCtx = withBOAuth(toolCtx, boAuth{
		Role:               "root",
		User:               boUser{ID: userID, Role: "root", isSuperadmin: true},
		ActiveRestaurantID: rid,
	})
	log.Printf("[bot] checkpoint wa_bot_panel_wsops_v1 restaurant_id=%d op=assistant_tool tool=%s", rid, name)
	out, err := s.assistantExecuteTool(toolCtx, rid, name, input)
	if err != nil {
		return nil, err
	}
	// Same failure heuristic the assistant_ws turn uses: an embedded error
	// object must not commit the approval claim as a successful write.
	if strings.Contains(out, `"error"`) {
		return nil, fmt.Errorf("la herramienta no aplicó el cambio: %s", out)
	}
	var res any
	if json.Unmarshal([]byte(out), &res) != nil {
		return map[string]any{"raw": out}, nil
	}
	return res, nil
}
