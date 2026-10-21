package api

// Admin rice tool (slice 4c): booking_propose_set_rice proposes setting,
// changing or clearing the rice of a NORMAL booking (no group menu; there the
// rice columns are the menu marker). Same mold as the other admin ops:
// propose + exact "OK <code>" by the same actor in the same chat, narrow
// two-column write inside the claim transaction, full-row version check,
// others-hash guard, panel-parity history and durable result.
// Special-date bookings are rejected fail-closed: their food lives in the
// special_json block and the rice/special interaction is not verified yet.
//
// Canonical contract (panel): arroz_type/arroz_servings are paired JSON
// arrays, deduped, sum of servings <= party_size (parseArrozFromArrays). The
// panel does not check type names (its UI supplies them); this tool validates
// every name against the active rices of the booking date's menu
// (loadRiceTypesForMenu: menu del día Mon-Fri, fin de semana Sat-Sun) and
// stores the canonical casing. An empty items list clears the rice and the
// preview says so explicitly. No customer notification, no n8n, no occupancy
// or payment effect.
//
// Coordination id: wa_bot_admin_set_rice_v1

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
)

type botRiceItem struct {
	Type     string `json:"type"`
	Servings int    `json:"servings"`
}

// botRiceArgs carries the requested rice of a booking_propose_set_rice
// proposal. Items is nil-vs-empty significant at the API boundary; stored in
// args it round-trips as the JSON array (null = clear).
type botRiceArgs struct {
	Items []botRiceItem `json:"items"`
}

// botRiceColumns is the closed set of columns this op may write.
var botRiceColumns = []string{"arroz_type", "arroz_servings"}

type botRiceCurrent struct {
	Name, Phone        string
	TypeJSON, ServJSON string
	TypeSet, ServSet   bool
	GroupMenu          bool
	Party              int
}

const botRiceCurrentSelect = `SELECT customer_name, COALESCE(contact_phone,''), special_menu, party_size, arroz_type, arroz_servings FROM bookings WHERE restaurant_id = ? AND id = ?`

func botRiceScanCurrent(row *sql.Row) (*botRiceCurrent, error) {
	var c botRiceCurrent
	var t, sv sql.NullString
	var gm sql.NullInt64
	if err := row.Scan(&c.Name, &c.Phone, &gm, &c.Party, &t, &sv); err != nil {
		return nil, err
	}
	c.GroupMenu = gm.Int64 != 0
	c.TypeJSON, c.TypeSet = t.String, t.Valid
	c.ServJSON, c.ServSet = sv.String, sv.Valid
	return &c, nil
}

// botRicePairsFromJSON parses the stored paired JSON into items; malformed
// stored data yields nil (treated as "no readable rice", never a crash).
func botRicePairsFromJSON(typeJSON, servJSON string) []botRiceItem {
	var types []string
	var servs []int
	if json.Unmarshal([]byte(typeJSON), &types) != nil || json.Unmarshal([]byte(servJSON), &servs) != nil {
		return nil
	}
	out := make([]botRiceItem, 0, len(types))
	for i, t := range types {
		sv := 0
		if i < len(servs) {
			sv = servs[i]
		}
		if strings.TrimSpace(t) == "" || sv <= 0 {
			continue
		}
		out = append(out, botRiceItem{Type: t, Servings: sv})
	}
	return out
}

// botRiceDisplay renders rice for previews and notices. Rice selections are
// menu data, not personal data, so both old and new values may be shown.
func botRiceDisplay(items []botRiceItem) string {
	if len(items) == 0 {
		return "(sin arroz)"
	}
	parts := make([]string, 0, len(items))
	for _, it := range items {
		parts = append(parts, fmt.Sprintf("%d x %s", it.Servings, it.Type))
	}
	return strings.Join(parts, ", ")
}

// botRiceSamePairs compares two rice selections as unordered sets of
// type+servings pairs.
func botRiceSamePairs(a, b []botRiceItem) bool {
	key := func(items []botRiceItem) map[string]bool {
		m := map[string]bool{}
		for _, it := range items {
			m[it.Type+"|"+fmt.Sprint(it.Servings)] = true
		}
		return m
	}
	ka, kb := key(a), key(b)
	if len(ka) != len(kb) {
		return false
	}
	for k := range ka {
		if !kb[k] {
			return false
		}
	}
	return true
}

// botRiceResolve validates the requested items against the active rices of
// the booking date's menu and returns the canonical stored JSON values
// (nil, nil clears the rice), the normalized items and an error. Unlike the
// panel, unknown or duplicated types are rejected instead of silently
// dropped (fail-closed; the model must send exact rice_menu names).
func (s *Server) botRiceResolve(ctx context.Context, rid int, date string, party int, in []botRiceItem) (typeVal, servVal any, items []botRiceItem, err error) {
	if len(in) == 0 {
		return nil, nil, nil, nil
	}
	menuKey, _, _ := botRiceMenuForDate(date)
	allowed, _, aerr := s.loadRiceTypesForMenu(ctx, rid, menuKey)
	if aerr != nil {
		return nil, nil, nil, errors.New("no se pudieron consultar los arroces de ese día")
	}
	byLower := map[string]string{}
	for _, a := range allowed {
		byLower[strings.ToLower(a)] = a
	}
	seen := map[string]bool{}
	sum := 0
	for _, it := range in {
		name := strings.TrimSpace(it.Type)
		if name == "" {
			return nil, nil, nil, errors.New("tipo de arroz vacío")
		}
		canon, ok := byLower[strings.ToLower(name)]
		if !ok {
			return nil, nil, nil, fmt.Errorf("«%s» no está entre los arroces disponibles para esa fecha; usa los nombres de rice_menu", name)
		}
		if seen[canon] {
			return nil, nil, nil, fmt.Errorf("arroz repetido: %s", canon)
		}
		seen[canon] = true
		if it.Servings <= 0 {
			return nil, nil, nil, fmt.Errorf("raciones inválidas para %s", canon)
		}
		sum += it.Servings
		items = append(items, botRiceItem{Type: canon, Servings: it.Servings})
	}
	if sum > party {
		return nil, nil, nil, errors.New("Las raciones de arroz superan el número de comensales")
	}
	types := make([]string, len(items))
	servs := make([]int, len(items))
	for i, it := range items {
		types[i] = it.Type
		servs[i] = it.Servings
	}
	bt, _ := json.Marshal(types)
	bs, _ := json.Marshal(servs)
	return string(bt), string(bs), items, nil
}

func botRiceAfterKey(bookingID int64, typeVal, servVal any) string {
	return botSha("ricev1|" + botCanon(map[string]any{"booking_id": bookingID, "arroz_type": typeVal, "arroz_servings": servVal}))
}

// botRiceChangeText builds the shared preview/notice change sentence.
func botRiceChangeText(oldItems, newItems []botRiceItem) string {
	switch {
	case len(newItems) == 0 && len(oldItems) > 0:
		return fmt.Sprintf("arroz: «%s» → (sin arroz): QUITA el arroz actual", botRiceDisplay(oldItems))
	case len(oldItems) == 0 && len(newItems) > 0:
		return fmt.Sprintf("arroz: (sin arroz) → «%s»", botRiceDisplay(newItems))
	default:
		return fmt.Sprintf("arroz: «%s» → «%s»", botRiceDisplay(oldItems), botRiceDisplay(newItems))
	}
}

func (s *Server) botToolRicePropose(ctx context.Context, rid int, msg botWebhookMessage, input json.RawMessage) (string, error) {
	actor, origin, ok := s.botSpecialAdminAuthorize(ctx, rid, msg)
	if !ok {
		return botJSON(map[string]any{"error": "no autorizado: solo para miembros verificados del grupo de gestión"}), nil
	}
	s.botSpecialAdminFlushNotify(ctx, rid)
	var in struct {
		BookingID int64          `json:"booking_id"`
		Items     *[]botRiceItem `json:"items"`
	}
	dec := json.NewDecoder(bytes.NewReader(input))
	dec.DisallowUnknownFields()
	if dec.Decode(&in) != nil || in.BookingID <= 0 || in.Items == nil {
		return botJSON(map[string]any{"error": "argumentos inválidos: booking_id e items (lista de {type, servings}; vacía para quitar el arroz)"}), nil
	}
	return s.botRicePropose(ctx, rid, msg, actor, origin, in.BookingID, *in.Items)
}

func (s *Server) botRicePropose(ctx context.Context, rid int, msg botWebhookMessage, actor, origin string, bookingID int64, items []botRiceItem) (string, error) {
	row, err := s.botLoadSpecialRow(ctx, rid, bookingID)
	if err != nil {
		return botJSON(map[string]any{"error": "no hay una reserva con ese id"}), nil
	}
	if rerr := botSpecialRowAllowedFor(row, "editan", "edita"); rerr != nil {
		return botJSON(map[string]any{"error": rerr.Error()}), nil
	}
	if row.Special {
		return botJSON(map[string]any{"error": "la reserva es de fecha especial: su arroz y menús van en el bloque especial y se cambian desde el panel"}), nil
	}
	cur, cerr := botRiceScanCurrent(s.db.QueryRowContext(ctx, botRiceCurrentSelect, rid, bookingID))
	if cerr != nil {
		return botJSON(map[string]any{"error": "no se pudo leer la reserva"}), nil
	}
	if cur.GroupMenu {
		return botJSON(map[string]any{"error": "la reserva tiene menú de grupo: su arroz es el propio menú y se cambia desde el panel"}), nil
	}
	typeVal, servVal, resolved, rerr := s.botRiceResolve(ctx, rid, row.Date, cur.Party, items)
	if rerr != nil {
		return botJSON(map[string]any{"error": rerr.Error()}), nil
	}
	curPairs := botRicePairsFromJSON(cur.TypeJSON, cur.ServJSON)
	if botRiceSamePairs(curPairs, resolved) {
		return botJSON(map[string]any{"error": "la reserva ya tiene ese arroz"}), nil
	}
	beforeHash, herr := botFullRowHash(ctx, s.db, rid, bookingID)
	if herr != nil {
		return botJSON(map[string]any{"error": "no se pudo leer la reserva"}), nil
	}
	code, cerr := botSpecialNewCode()
	if cerr != nil {
		return botJSON(map[string]any{"error": "no se pudo generar la propuesta"}), nil
	}
	preview := fmt.Sprintf("Propuesta: CAMBIAR el arroz de la reserva #%d (%s %s, %d comensales): %s. No cambian fecha, hora, comensales, salón, menús ni ocupación. No se avisa al cliente.",
		bookingID, row.Date, row.Time, row.Party, botRiceChangeText(curPairs, resolved))
	after := botRiceAfterKey(bookingID, typeVal, servVal)
	return s.botProposalPersist(ctx, rid, msg, "set_rice", bookingID, botRiceArgs{Items: items}, "", actor, origin, beforeHash, after, preview, code)
}

// botRiceExecute runs INSIDE the claim transaction: claim + two-column write
// + others-hash verification + panel-parity rice history + result_json +
// event commit together.
func (s *Server) botRiceExecute(ctx context.Context, tx *sql.Tx, rid int, msg botWebhookMessage, p botSpecialProposalRow) (string, error) {
	stale := func(why string) (string, error) {
		_ = tx.Rollback()
		s.markStale(ctx, rid, p, msg)
		return "", errors.New(why)
	}
	var in botRiceArgs
	if json.Unmarshal([]byte(p.Args), &in) != nil {
		return "", errors.New("propuesta ilegible")
	}
	row, err := botScanSpecialRow(tx.QueryRowContext(ctx, botSpecialRowSelect+" FOR UPDATE", rid, p.BookingID))
	if err != nil {
		return "", errors.New("la reserva ya no existe")
	}
	if rerr := botSpecialRowAllowedFor(row, "editan", "edita"); rerr != nil {
		return "", rerr
	}
	if row.Special {
		return stale("la reserva ahora es de fecha especial; pide una propuesta nueva")
	}
	full, err := botFullRowHash(ctx, tx, rid, p.BookingID)
	if err != nil {
		return "", errors.New("error interno")
	}
	if full != p.Before {
		return stale(errBotSpecialStale.Error())
	}
	cur, cerr := botRiceScanCurrent(tx.QueryRowContext(ctx, botRiceCurrentSelect, rid, p.BookingID))
	if cerr != nil {
		return "", errors.New("error interno")
	}
	if cur.GroupMenu {
		return stale("la reserva ahora tiene menú de grupo; pide una propuesta nueva")
	}
	typeVal, servVal, resolved, rerr := s.botRiceResolve(ctx, rid, row.Date, cur.Party, in.Items)
	if rerr != nil {
		return stale("la validación ya no pasa: " + rerr.Error())
	}
	curPairs := botRicePairsFromJSON(cur.TypeJSON, cur.ServJSON)
	if botRiceSamePairs(curPairs, resolved) {
		return stale("el arroz ya está así; pide una propuesta nueva")
	}
	if botRiceAfterKey(p.BookingID, typeVal, servVal) != p.After {
		return stale("el resultado ya no coincide con la vista previa; pide una propuesta nueva")
	}
	preOthers, err := botFullRowHash(ctx, tx, rid, p.BookingID, botRiceColumns...)
	if err != nil {
		return "", errors.New("error interno")
	}
	if _, err := tx.ExecContext(ctx, "UPDATE bookings SET `arroz_type` = ?, `arroz_servings` = ? WHERE restaurant_id = ? AND id = ?", typeVal, servVal, rid, p.BookingID); err != nil {
		return "", errors.New("error al guardar; no se aplicó nada")
	}
	postOthers, err := botFullRowHash(ctx, tx, rid, p.BookingID, botRiceColumns...)
	if err != nil || postOthers != preOthers {
		return "", errors.New("verificación fallida: cambió una columna fuera de la edición; no se aplicó nada")
	}
	// History: the panel records rice changes as "rice" with the raw
	// type|servings values; mirror it inside the same transaction.
	var oldType, oldServ any
	if cur.TypeSet {
		oldType = cur.TypeJSON
	}
	if cur.ServSet {
		oldServ = cur.ServJSON
	}
	oldHist := fmt.Sprintf("%v|%v", oldType, oldServ)
	newHist := fmt.Sprintf("%v|%v", typeVal, servVal)
	if oldHist != newHist {
		if _, err := tx.ExecContext(ctx, `INSERT INTO booking_modifications (restaurant_id, booking_id, original_reservation_date, field_modified, old_value, new_value, modified_by, modified_by_name, customer_name, contact_phone, modification_date) VALUES (?, ?, ?, 'rice', ?, ?, 'whatsapp', 'Bot WhatsApp (dirección)', ?, ?, NOW())`,
			rid, p.BookingID, row.Date, oldHist, newHist, cur.Name, cur.Phone); err != nil {
			return "", errors.New("no se pudo registrar el historial; no se aplicó nada")
		}
	}
	notice := fmt.Sprintf("Reserva #%d: %s. No se ha avisado al cliente.", p.BookingID, botRiceChangeText(curPairs, resolved))
	rj, _ := json.Marshal(map[string]any{"booking_id": p.BookingID, "op": p.Op, "notice": notice})
	if _, err := tx.ExecContext(ctx, `UPDATE wa_bot_op_proposals SET result_json=?, notify_state='pending' WHERE id=?`, string(rj), p.ID); err != nil {
		return "", errors.New("error interno")
	}
	s.botSpecialAdminEvent(ctx, tx, rid, p.ID, "executed", p.Actor, strings.TrimSpace(msg.ChatJID), map[string]any{"op": p.Op, "booking_id": p.BookingID, "before_hash": p.Before, "after_hash": p.After, "changed_columns": botRiceColumns, "approval_message_id": msg.MessageID})
	if err := tx.Commit(); err != nil {
		log.Printf("[bot] wa_bot_admin_set_rice_v1 commit_unknown rid=%d proposal=%d err=%v", rid, p.ID, err)
		return "", errors.New("no pude confirmar si se guardó; consulta la reserva antes de repetir")
	}
	s.broadcastBookingChanged(rid, p.BookingID, "booking_updated")
	return notice, nil
}
