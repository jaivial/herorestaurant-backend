package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Coordination id: booking_confirmation_resend_v1 (WhatsApp half)
//
// booking_propose_resend_confirmation: re-send the confirmation WhatsApp of a
// booking for the allowed (privileged) contacts and groups, over the shared
// core (booking_confirmation_resend.go). The tool NEVER sends by itself: it
// stores a proposal with the exact preview (phone, QR, regenerated receipt)
// and only the requester, in the same chat, with "OK <código>", fires the
// panel's own POST /admin/bookings/{id}/resend-confirmation. External sends
// run AFTER the claim commits (invoice pattern): a WhatsApp failure never
// resurrects the proposal, it is reported as the apply result.

const botBookingResendOp = "resend_confirmation"

// botToolBookingResendPropose validates the booking, renders the exact preview
// and persists the proposal bound to the resolved row by after-hash.
func (s *Server) botToolBookingResendPropose(ctx context.Context, rid int, msg botWebhookMessage, input json.RawMessage) (string, error) {
	actor, origin, ok := s.botSpecialAdminAuthorize(ctx, rid, msg)
	if !ok {
		return botJSON(map[string]any{"error": "no autorizado: solo para miembros verificados del grupo de gestión"}), nil
	}
	s.botSpecialAdminFlushNotify(ctx, rid)
	var in struct {
		BookingID int64 `json:"booking_id"`
	}
	// Unknown fields are REJECTED, never silently ignored.
	dec := json.NewDecoder(bytes.NewReader(input))
	dec.DisallowUnknownFields()
	if dec.Decode(&in) != nil || in.BookingID <= 0 {
		return botJSON(map[string]any{"error": "argumentos inválidos: esta herramienta solo admite booking_id"}), nil
	}
	preview, row, err := s.bookingResendConfirmationPreview(ctx, rid, in.BookingID)
	if err != nil {
		return botJSON(map[string]any{"error": err.Error()}), nil
	}
	code, cerr := botSpecialNewCode()
	if cerr != nil {
		return botJSON(map[string]any{"error": "no se pudo generar la propuesta"}), nil
	}
	after := bookingResendAfterKey(rid, in.BookingID, row)
	dedup := fmt.Sprintf("%s|%d|%d", botBookingResendOp, rid, in.BookingID)
	previewText := "Propuesta: " + preview + " Nada está enviado todavía; se envía solo al confirmar con el código."
	return s.botProposalPersist(ctx, rid, msg, botBookingResendOp, in.BookingID, in, dedup, actor, origin, "", after, previewText, code)
}

// botBookingResendExecute runs on "OK <código>": the claim commits BEFORE the
// external sends, exactly like the invoice op. A send failure is the apply
// result (reported to the chat), never a rollback of the claim.
func (s *Server) botBookingResendExecute(ctx context.Context, tx *sql.Tx, rid int, msg botWebhookMessage, p botSpecialProposalRow) (string, error) {
	var in struct {
		BookingID int64 `json:"booking_id"`
	}
	if json.Unmarshal([]byte(p.Args), &in) != nil || in.BookingID <= 0 {
		return "", errors.New("propuesta ilegible")
	}
	_, row, err := s.bookingResendConfirmationPreview(ctx, rid, in.BookingID)
	if err != nil {
		_ = tx.Rollback()
		s.markStale(ctx, rid, p, msg)
		return "", err
	}
	if bookingResendAfterKey(rid, in.BookingID, row) != p.After {
		_ = tx.Rollback()
		s.markStale(ctx, rid, p, msg)
		return "", errors.New("la reserva cambió desde la vista previa (teléfono, fecha o abonos): pide una propuesta nueva")
	}
	if err := tx.Commit(); err != nil {
		return "", errors.New("error interno")
	}
	out, aerr := s.bookingResendConfirmationApply(ctx, rid, in.BookingID)
	// The turn reply IS the group notice: record the result and close the
	// proposal's notify state so the flusher does not repeat it.
	rj, _ := json.Marshal(map[string]any{"booking_id": in.BookingID, "op": p.Op, "result": out})
	_, _ = s.db.ExecContext(ctx, `UPDATE wa_bot_op_proposals SET result_json=?, notify_state='sent' WHERE id=?`, string(rj), p.ID)
	if aerr != nil {
		botCheckpoint(bookingResendCoordinationID, "restaurant_id=%d booking_id=%d apply_failed err=%v", rid, in.BookingID, aerr)
		return "", aerr
	}
	if !toBool(out["whatsapp_sent"]) {
		return "", errors.New(firstNonEmpty(anyToString(out["message"]), "el WhatsApp no se pudo entregar ahora; ha quedado en cola para reintentar"))
	}
	summary := fmt.Sprintf("WhatsApp de confirmación de la reserva #%d reenviado al %s", in.BookingID, anyToString(out["phone"]))
	var parts []string
	if toBool(out["qr_sent"]) {
		parts = append(parts, "con el QR de la reserva")
	}
	if toBool(out["receipt_sent"]) {
		parts = append(parts, "con el comprobante de pago del adelanto regenerado")
	}
	if len(parts) > 0 {
		summary += " (" + strings.Join(parts, " y ") + ")"
	}
	return summary + ".", nil
}
