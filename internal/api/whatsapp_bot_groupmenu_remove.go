package api

// Admin group-menu removal (slice 4g): booking_propose_remove_group_menu
// proposes taking a booking back to a normal, no-menu booking. Same mold as
// the other admin ops: propose + exact "OK <code>" by the same actor in the
// same chat, narrow column write inside the claim transaction, full-row
// version check, others-hash guard, panel-parity history and durable result.
//
// Canonical contract (panel boNormalizeAndValidateBookingInput +
// boUpdateBooking when a PATCH sets special_menu=false):
//   - special_menu = 0, menu_de_grupo_id = 0, menu_de_grupo_assigned = 0;
//   - arroz_type/arroz_servings become NULL (the marker is dropped; the panel
//     does NOT preserve rice across a group-menu exit);
//   - principales_json becomes NULL;
//   - commentary is NOT touched: the derived text stays as a plain literal
//     (nothing is lost; a free note inside it survives untouched);
//   - extras are NOT touched.
//
// The preview explains all of the above; the commentary content is never
// shown (it may hold a free note). Removing the menu has no payment
// execution and bookings store no amounts; no customer notification, no n8n,
// no occupancy effect. The panel records the rice marker disappearing as a
// "rice" history entry; mirrored.
//
// Coordination id: wa_bot_admin_remove_group_menu_v1

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

// botGMenuRemoveColumns is the closed set of columns this op may write.
var botGMenuRemoveColumns = []string{"special_menu", "menu_de_grupo_id", "menu_de_grupo_assigned", "arroz_type", "arroz_servings", "principales_json"}

type botGMenuRemoveCurrent struct {
	Name, Phone   string
	MenuID        int
	TypeJSON      string
	TypeSet       bool
	ServJSON      string
	ServSet       bool
	CommentarySet bool
}

const botGMenuRemoveCurrentSelect = `SELECT customer_name, COALESCE(contact_phone,''), COALESCE(menu_de_grupo_id,0), arroz_type, arroz_servings, commentary FROM bookings WHERE restaurant_id = ? AND id = ?`

func botGMenuRemoveScanCurrent(row *sql.Row) (*botGMenuRemoveCurrent, error) {
	var c botGMenuRemoveCurrent
	var t, sv, comm sql.NullString
	if err := row.Scan(&c.Name, &c.Phone, &c.MenuID, &t, &sv, &comm); err != nil {
		return nil, err
	}
	c.TypeJSON, c.TypeSet = t.String, t.Valid
	c.ServJSON, c.ServSet = sv.String, sv.Valid
	c.CommentarySet = comm.Valid && strings.TrimSpace(comm.String) != ""
	return &c, nil
}

func botGMenuRemoveAfterKey(bookingID int64, removedMenuID int) string {
	return botSha("gmenurmv1|" + botCanon(map[string]any{
		"booking_id": bookingID, "removed_menu_de_grupo_id": removedMenuID,
		"special_menu": 0, "menu_de_grupo_id": 0, "menu_de_grupo_assigned": 0,
		"arroz_type": nil, "arroz_servings": nil, "principales_json": nil,
	}))
}

// botGMenuRemoveChangeText builds the shared preview/notice sentence without
// showing the stored commentary (it may hold a free note). The menu title is
// deliberately not shown: it is mutable menu data, and the proposal is already
// bound to the exact menu id, so a renamed menu would make the preview stale.
func botGMenuRemoveChangeText(hadCommentary bool) string {
	text := "quita el menú de grupo actual: el arroz y los principales desaparecen y los extras se conservan."
	if hadCommentary {
		text += " El comentario generado queda guardado como texto fijo (contenido no mostrado por privacidad)."
	}
	return text
}

func (s *Server) botToolGMenuRemovePropose(ctx context.Context, rid int, msg botWebhookMessage, input json.RawMessage) (string, error) {
	actor, origin, ok := s.botSpecialAdminAuthorize(ctx, rid, msg)
	if !ok {
		return botJSON(map[string]any{"error": "no autorizado: solo para miembros verificados del grupo de gestión"}), nil
	}
	s.botSpecialAdminFlushNotify(ctx, rid)
	var in struct {
		BookingID int64 `json:"booking_id"`
	}
	dec := json.NewDecoder(bytes.NewReader(input))
	dec.DisallowUnknownFields()
	if dec.Decode(&in) != nil || in.BookingID <= 0 {
		return botJSON(map[string]any{"error": "argumentos inválidos: booking_id"}), nil
	}
	return s.botGMenuRemovePropose(ctx, rid, msg, actor, origin, in.BookingID)
}

func (s *Server) botGMenuRemovePropose(ctx context.Context, rid int, msg botWebhookMessage, actor, origin string, bookingID int64) (string, error) {
	row, err := s.botLoadSpecialRow(ctx, rid, bookingID)
	if err != nil {
		return botJSON(map[string]any{"error": "no hay una reserva con ese id"}), nil
	}
	if rerr := botSpecialRowAllowedFor(row, "editan", "edita"); rerr != nil {
		return botJSON(map[string]any{"error": rerr.Error()}), nil
	}
	if row.Special {
		return botJSON(map[string]any{"error": "la reserva es de fecha especial: sus menús van en el bloque especial y se cambian desde el panel"}), nil
	}
	cur, cerr := botGMenuRemoveScanCurrent(s.db.QueryRowContext(ctx, botGMenuRemoveCurrentSelect, rid, bookingID))
	if cerr != nil {
		return botJSON(map[string]any{"error": "no se pudo leer la reserva"}), nil
	}
	if cur.MenuID <= 0 {
		return botJSON(map[string]any{"error": "la reserva no tiene menú de grupo"}), nil
	}
	beforeHash, herr := botFullRowHash(ctx, s.db, rid, bookingID)
	if herr != nil {
		return botJSON(map[string]any{"error": "no se pudo leer la reserva"}), nil
	}
	code, cerr := botSpecialNewCode()
	if cerr != nil {
		return botJSON(map[string]any{"error": "no se pudo generar la propuesta"}), nil
	}
	preview := fmt.Sprintf("Propuesta: QUITAR el menú de grupo de la reserva #%d (%s %s, %d comensales): %s. No cambian fecha, hora, comensales, salón ni ocupación. No se avisa al cliente.",
		bookingID, row.Date, row.Time, row.Party, botGMenuRemoveChangeText(cur.CommentarySet))
	after := botGMenuRemoveAfterKey(bookingID, cur.MenuID)
	args := map[string]any{"booking_id": bookingID, "removed_menu_de_grupo_id": cur.MenuID}
	return s.botProposalPersist(ctx, rid, msg, "remove_group_menu", bookingID, args, "", actor, origin, beforeHash, after, preview, code)
}

// botGMenuRemoveExecute runs INSIDE the claim transaction: claim + narrow
// column write + others-hash verification + panel-parity rice history +
// result_json + event commit together.
func (s *Server) botGMenuRemoveExecute(ctx context.Context, tx *sql.Tx, rid int, msg botWebhookMessage, p botSpecialProposalRow) (string, error) {
	stale := func(why string) (string, error) {
		_ = tx.Rollback()
		s.markStale(ctx, rid, p, msg)
		return "", errors.New(why)
	}
	var in struct {
		BookingID            int64 `json:"booking_id"`
		RemovedMenuDeGrupoID int   `json:"removed_menu_de_grupo_id"`
	}
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
	cur, cerr := botGMenuRemoveScanCurrent(tx.QueryRowContext(ctx, botGMenuRemoveCurrentSelect, rid, p.BookingID))
	if cerr != nil {
		return "", errors.New("error interno")
	}
	if cur.MenuID <= 0 {
		return stale("la reserva ya no tiene menú de grupo; pide una propuesta nueva")
	}
	if botGMenuRemoveAfterKey(p.BookingID, cur.MenuID) != p.After {
		return stale("el resultado ya no coincide con la vista previa; pide una propuesta nueva")
	}
	preOthers, err := botFullRowHash(ctx, tx, rid, p.BookingID, botGMenuRemoveColumns...)
	if err != nil {
		return "", errors.New("error interno")
	}
	if _, err := tx.ExecContext(ctx, "UPDATE bookings SET `special_menu` = 0, `menu_de_grupo_id` = 0, `menu_de_grupo_assigned` = 0, `arroz_type` = NULL, `arroz_servings` = NULL, `principales_json` = NULL WHERE restaurant_id = ? AND id = ?", rid, p.BookingID); err != nil {
		return "", errors.New("error al guardar; no se aplicó nada")
	}
	postOthers, err := botFullRowHash(ctx, tx, rid, p.BookingID, botGMenuRemoveColumns...)
	if err != nil || postOthers != preOthers {
		return "", errors.New("verificación fallida: cambió una columna fuera de la edición; no se aplicó nada")
	}
	// History: the panel records the rice marker disappearing as "rice" with
	// the raw type|servings values; mirror it.
	var oldType, oldServ any
	if cur.TypeSet {
		oldType = cur.TypeJSON
	}
	if cur.ServSet {
		oldServ = cur.ServJSON
	}
	oldHist := fmt.Sprintf("%v|%v", oldType, oldServ)
	if oldHist != "<nil>|<nil>" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO booking_modifications (restaurant_id, booking_id, original_reservation_date, field_modified, old_value, new_value, modified_by, modified_by_name, customer_name, contact_phone, modification_date) VALUES (?, ?, ?, 'rice', ?, '<nil>|<nil>', 'whatsapp', 'Bot WhatsApp (dirección)', ?, ?, NOW())`,
			rid, p.BookingID, row.Date, oldHist, cur.Name, cur.Phone); err != nil {
			return "", errors.New("no se pudo registrar el historial; no se aplicó nada")
		}
	}
	notice := fmt.Sprintf("Reserva #%d: %s. No se ha avisado al cliente.", p.BookingID, botGMenuRemoveChangeText(cur.CommentarySet))
	rj, _ := json.Marshal(map[string]any{"booking_id": p.BookingID, "op": p.Op, "notice": notice})
	if _, err := tx.ExecContext(ctx, `UPDATE wa_bot_op_proposals SET result_json=?, notify_state='pending' WHERE id=?`, string(rj), p.ID); err != nil {
		return "", errors.New("error interno")
	}
	s.botSpecialAdminEvent(ctx, tx, rid, p.ID, "executed", p.Actor, strings.TrimSpace(msg.ChatJID), map[string]any{"op": p.Op, "booking_id": p.BookingID, "before_hash": p.Before, "after_hash": p.After, "changed_columns": botGMenuRemoveColumns, "approval_message_id": msg.MessageID})
	if err := tx.Commit(); err != nil {
		log.Printf("[bot] wa_bot_admin_remove_group_menu_v1 commit_unknown rid=%d proposal=%d err=%v", rid, p.ID, err)
		return "", errors.New("no pude confirmar si se guardó; consulta la reserva antes de repetir")
	}
	s.broadcastBookingChanged(rid, p.BookingID, "booking_updated")
	return notice, nil
}
