package api

// Admin booking flags (slice 4i): booking_propose_set_flags proposes toggling
// the two operational flags the panel PATCH exposes:
//   - is_event (booking_is_event_v1): event/negotiation marker;
//   - send_documents_to_client (booking_documents_v1): "Enviar copia al
//     cliente" — with it on, booking documents travel with FUTURE
//     notifications (the create-time confirmation attaches them); the toggle
//     itself sends nothing.
//
// Canonical contract (handleBOBookingPatch bm.go:576-579 +
// boSetBookingIsEvent/boSetBookingSendDocumentsToClient bm.go:1651-1674):
// nil keeps the stored value, a present bool overwrites it, no history entry,
// no notification, no other side effect. Same mold as the other admin ops:
// propose + exact "OK <code>", narrow column write inside the claim
// transaction, full-row version check, others-hash guard, durable result.
//
// Coordination id: wa_bot_admin_set_flags_v1

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

// botFlagsColumns is the closed set of columns this op may write.
var botFlagsColumns = []string{"is_event", "send_documents_to_client"}

func botFlagsAfterKey(bookingID int64, isEvent, sendDocs bool) string {
	return botSha("flagsv1|" + botCanon(map[string]any{"booking_id": bookingID, "is_event": isEvent, "send_documents_to_client": sendDocs}))
}

// botFlagsChangeText describes the final flag states with their CONCRETE
// consequences, verified against every consumer in the source @9333779:
//   - is_event: read by the customer bot policy (whatsapp_bot_special_policy.go),
//     the bot pipeline payload/prompt and pipeline/app.py ("event_booking": the
//     team always handles it). ON = the assistant stops modifying/cancelling
//     this booking at the customer's request and hands it to management;
//     OFF = the customer self-service flow applies again (subject to
//     special-date policy).
//   - send_documents_to_client: its ONLY reader is the backoffice booking
//     CREATE (bm.go:230-235): when on, the documents staff attached to the
//     booking travel with the create-time confirmation (customer WhatsApp +
//     confirmation emails). No reminder, n8n payload, checkout or other
//     worker reads it, so on an already-created booking the toggle changes
//     nothing today: it only stores the preference.
func botFlagsChangeText(isEvent, sendDocs bool) string {
	ev := "«evento» desactivado: deja de derivarla por motivo de evento; pueden seguir aplicando otras reglas de derivación, como fecha especial"
	if isEvent {
		ev = "«evento» activado: el asistente dejará de modificar/cancelar esta reserva a petición del cliente y la derivará siempre a la gestión"
	}
	docs := "«enviar copia al cliente» desactivado"
	if sendDocs {
		docs = "«enviar copia al cliente» activado"
	}
	return fmt.Sprintf("%s; %s", ev, docs)
}

// botFlagsDocsNote states the bounded effect of the send_documents flag.
const botFlagsDocsNote = "El indicador de documentos NO envía nada ahora: en la versión actual solo lo lee el alta de reservas del panel (los documentos que el personal adjunte viajan con la confirmación del alta al cliente). En una reserva ya creada, ningún proceso de la aplicación en esta versión lo usa; solo queda guardada la preferencia."

func (s *Server) botToolFlagsPropose(ctx context.Context, rid int, msg botWebhookMessage, input json.RawMessage) (string, error) {
	actor, origin, ok := s.botSpecialAdminAuthorize(ctx, rid, msg)
	if !ok {
		return botJSON(map[string]any{"error": "no autorizado: solo para miembros verificados del grupo de gestión"}), nil
	}
	s.botSpecialAdminFlushNotify(ctx, rid)
	var in struct {
		BookingID int64 `json:"booking_id"`
		IsEvent   *bool `json:"is_event"`
		SendDocs  *bool `json:"send_documents_to_client"`
	}
	dec := json.NewDecoder(bytes.NewReader(input))
	dec.DisallowUnknownFields()
	if dec.Decode(&in) != nil || in.BookingID <= 0 || (in.IsEvent == nil && in.SendDocs == nil) {
		return botJSON(map[string]any{"error": "argumentos inválidos: booking_id y al menos uno de is_event, send_documents_to_client"}), nil
	}
	row, err := s.botLoadSpecialRow(ctx, rid, in.BookingID)
	if err != nil {
		return botJSON(map[string]any{"error": "no hay una reserva con ese id"}), nil
	}
	if rerr := botSpecialRowAllowedFor(row, "editan", "edita"); rerr != nil {
		return botJSON(map[string]any{"error": rerr.Error()}), nil
	}
	var curEvent, curDocs int
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(is_event,0), COALESCE(send_documents_to_client,0) FROM bookings WHERE restaurant_id = ? AND id = ?`, rid, in.BookingID).Scan(&curEvent, &curDocs); err != nil {
		return botJSON(map[string]any{"error": "no se pudo leer la reserva"}), nil
	}
	finalEvent := curEvent != 0
	if in.IsEvent != nil {
		finalEvent = *in.IsEvent
	}
	finalDocs := curDocs != 0
	if in.SendDocs != nil {
		finalDocs = *in.SendDocs
	}
	if finalEvent == (curEvent != 0) && finalDocs == (curDocs != 0) {
		return botJSON(map[string]any{"error": "la reserva ya tiene esos indicadores así"}), nil
	}
	beforeHash, herr := botFullRowHash(ctx, s.db, rid, in.BookingID)
	if herr != nil {
		return botJSON(map[string]any{"error": "no se pudo leer la reserva"}), nil
	}
	code, cerr := botSpecialNewCode()
	if cerr != nil {
		return botJSON(map[string]any{"error": "no se pudo generar la propuesta"}), nil
	}
	preview := fmt.Sprintf("Propuesta: CAMBIAR indicadores de la reserva #%d (%s %s, %d comensales): %s. %s No se avisa al cliente.",
		in.BookingID, row.Date, row.Time, row.Party, botFlagsChangeText(finalEvent, finalDocs), botFlagsDocsNote)
	after := botFlagsAfterKey(in.BookingID, finalEvent, finalDocs)
	args := map[string]any{"booking_id": in.BookingID, "is_event": finalEvent, "send_documents_to_client": finalDocs}
	return s.botProposalPersist(ctx, rid, msg, "set_flags", in.BookingID, args, "", actor, origin, beforeHash, after, preview, code)
}

// botFlagsExecute runs INSIDE the claim transaction.
func (s *Server) botFlagsExecute(ctx context.Context, tx *sql.Tx, rid int, msg botWebhookMessage, p botSpecialProposalRow) (string, error) {
	stale := func(why string) (string, error) {
		_ = tx.Rollback()
		s.markStale(ctx, rid, p, msg)
		return "", errors.New(why)
	}
	var in struct {
		BookingID int64 `json:"booking_id"`
		IsEvent   bool  `json:"is_event"`
		SendDocs  bool  `json:"send_documents_to_client"`
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
	full, err := botFullRowHash(ctx, tx, rid, p.BookingID)
	if err != nil {
		return "", errors.New("error interno")
	}
	if full != p.Before {
		return stale(errBotSpecialStale.Error())
	}
	if botFlagsAfterKey(p.BookingID, in.IsEvent, in.SendDocs) != p.After {
		return stale("el resultado ya no coincide con la vista previa; pide una propuesta nueva")
	}
	preOthers, err := botFullRowHash(ctx, tx, rid, p.BookingID, botFlagsColumns...)
	if err != nil {
		return "", errors.New("error interno")
	}
	if _, err := tx.ExecContext(ctx, "UPDATE bookings SET `is_event` = ?, `send_documents_to_client` = ? WHERE restaurant_id = ? AND id = ?", boolToTinyint(in.IsEvent), boolToTinyint(in.SendDocs), rid, p.BookingID); err != nil {
		return "", errors.New("error al guardar; no se aplicó nada")
	}
	postOthers, err := botFullRowHash(ctx, tx, rid, p.BookingID, botFlagsColumns...)
	if err != nil || postOthers != preOthers {
		return "", errors.New("verificación fallida: cambió una columna fuera de la edición; no se aplicó nada")
	}
	notice := fmt.Sprintf("Reserva #%d: %s. No se ha enviado nada ni avisado al cliente.", p.BookingID, botFlagsChangeText(in.IsEvent, in.SendDocs))
	rj, _ := json.Marshal(map[string]any{"booking_id": p.BookingID, "op": p.Op, "notice": notice})
	if _, err := tx.ExecContext(ctx, `UPDATE wa_bot_op_proposals SET result_json=?, notify_state='pending' WHERE id=?`, string(rj), p.ID); err != nil {
		return "", errors.New("error interno")
	}
	s.botSpecialAdminEvent(ctx, tx, rid, p.ID, "executed", p.Actor, strings.TrimSpace(msg.ChatJID), map[string]any{"op": p.Op, "booking_id": p.BookingID, "before_hash": p.Before, "after_hash": p.After, "changed_columns": botFlagsColumns, "approval_message_id": msg.MessageID})
	if err := tx.Commit(); err != nil {
		log.Printf("[bot] wa_bot_admin_set_flags_v1 commit_unknown rid=%d proposal=%d err=%v", rid, p.ID, err)
		return "", errors.New("no pude confirmar si se guardó; consulta la reserva antes de repetir")
	}
	s.broadcastBookingChanged(rid, p.BookingID, "booking_updated")
	return notice, nil
}
