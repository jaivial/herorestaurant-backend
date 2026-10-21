package api

// Admin booking notification settings (slice 4k): booking_propose_notif_settings
// proposes changing the restaurant's booking WhatsApp CONFIRMATION toggle
// (booking_notification_settings, bkg-wa-notif), mirroring the panel's
// handleBOBookingNotificationsPut (booking_notification_settings.go:89-108).
//
// Bounded consequence (verified against every consumer @9333779):
//   - send_confirmation=false: sendBookingWhatsAppToCustomer
//     (booking_whatsapp.go:179-183) silently SKIPS the create-time customer
//     WhatsApp confirmation (logged, reported as success). The confirmation
//     EMAILS are NOT gated by this flag and keep going out.
//   - send_confirmation=true restores the WhatsApp confirmation for FUTURE
//     bookings. Already-created bookings are never re-notified.
//
// NOT EXPOSED (declared gap, fail-closed): send_reconfirmation and
// reconfirmation_days_before have NO consumer anywhere in this repository
// (grep @9333779: only booking_notification_settings.go itself). An external
// reader (e.g. an n8n flow against the table) cannot be ruled out from
// source, so the bot never writes those two fields; the upsert preserves the
// stored values (or the panel defaults) verbatim.
//
// Concurrency: the settings row is read FOR UPDATE inside the claim
// transaction and the final row must match the bound after-state
// (confirmation as proposed, reconfirmation fields preserved). The upsert
// mirrors saveBookingNotificationSettings. No customer is notified by the
// toggle itself. Shares the single booking_id=0 proposal slot per tenant.
//
// Coordination id: wa_bot_admin_notif_settings_v1

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

// botNotifSettingsRow is the full settings row, with the panel defaults
// applied when no row exists (confirmation on, reconfirmation off, 2 days).
type botNotifSettingsRow struct {
	Found        bool
	Confirmation bool
	Reconfirm    bool
	Days         int
}

func botNotifDefaultRow() botNotifSettingsRow {
	return botNotifSettingsRow{Confirmation: true, Reconfirm: false, Days: bookingNotifDefaultDaysBefo}
}

func botScanNotifSettings(row *sql.Row) (botNotifSettingsRow, error) {
	r := botNotifDefaultRow()
	err := row.Scan(&r.Confirmation, &r.Reconfirm, &r.Days)
	if err == sql.ErrNoRows {
		return r, nil
	}
	if err != nil {
		return r, err
	}
	r.Found = true
	r.Days = clampReconfirmationDays(r.Days)
	return r, nil
}

func botNotifKey(prefix string, r botNotifSettingsRow) string {
	return botSha(prefix + botCanon(map[string]any{"send_confirmation": r.Confirmation, "send_reconfirmation": r.Reconfirm, "reconfirmation_days_before": r.Days}))
}

func (s *Server) botToolNotifSettingsPropose(ctx context.Context, rid int, msg botWebhookMessage, input json.RawMessage) (string, error) {
	actor, origin, ok := s.botSpecialAdminAuthorize(ctx, rid, msg)
	if !ok {
		return botJSON(map[string]any{"error": "no autorizado: solo para miembros verificados del grupo de gestión"}), nil
	}
	s.botSpecialAdminFlushNotify(ctx, rid)
	var in struct {
		SendConfirmation *bool `json:"send_confirmation"`
	}
	dec := json.NewDecoder(bytes.NewReader(input))
	dec.DisallowUnknownFields()
	if dec.Decode(&in) != nil || in.SendConfirmation == nil {
		return botJSON(map[string]any{"error": "argumentos inválidos: send_confirmation (obligatorio)"}), nil
	}
	cur, err := botScanNotifSettings(s.db.QueryRowContext(ctx, `SELECT send_confirmation, send_reconfirmation, reconfirmation_days_before FROM booking_notification_settings WHERE restaurant_id = ? LIMIT 1`, rid))
	if err != nil {
		return botJSON(map[string]any{"error": "no se pudo leer la configuración"}), nil
	}
	if cur.Confirmation == *in.SendConfirmation {
		return botJSON(map[string]any{"error": "la confirmación por WhatsApp ya está así"}), nil
	}
	next := cur
	next.Confirmation = *in.SendConfirmation
	code, cerr := botSpecialNewCode()
	if cerr != nil {
		return botJSON(map[string]any{"error": "no se pudo generar la propuesta"}), nil
	}
	var effect string
	if *in.SendConfirmation {
		effect = "ACTIVA el WhatsApp de confirmación: las PRÓXIMAS reservas que se creen volverán a enviarlo."
	} else {
		effect = "DESACTIVA el WhatsApp de confirmación: las PRÓXIMAS reservas que se creen NO lo recibirán (queda registrado como omitido, sin error). Los emails de confirmación SIGUEN enviándose igual."
	}
	preview := fmt.Sprintf("Propuesta: NOTIFICACIONES DE RESERVA — %s Las reservas ya creadas no reciben nada nuevo. La reconfirmación programada no se toca.", effect)
	args := map[string]any{"booking_id": 0, "send_confirmation": *in.SendConfirmation}
	return s.botProposalPersist(ctx, rid, msg, "notif_settings", 0, args, "notifsettings", actor, origin, botNotifKey("notif-before|", cur), botNotifKey("notif-after|", next), preview, code)
}

// botNotifSettingsExecute runs INSIDE the claim transaction.
func (s *Server) botNotifSettingsExecute(ctx context.Context, tx *sql.Tx, rid int, msg botWebhookMessage, p botSpecialProposalRow) (string, error) {
	stale := func(why string) (string, error) {
		_ = tx.Rollback()
		s.markStale(ctx, rid, p, msg)
		return "", errors.New(why)
	}
	var in struct {
		SendConfirmation bool `json:"send_confirmation"`
	}
	if json.Unmarshal([]byte(p.Args), &in) != nil {
		return "", errors.New("propuesta ilegible")
	}
	cur, err := botScanNotifSettings(tx.QueryRowContext(ctx, `SELECT send_confirmation, send_reconfirmation, reconfirmation_days_before FROM booking_notification_settings WHERE restaurant_id = ? LIMIT 1 FOR UPDATE`, rid))
	if err != nil {
		return "", errors.New("error interno")
	}
	if botNotifKey("notif-before|", cur) != p.Before {
		return stale("la configuración cambió desde la vista previa; pide una propuesta nueva")
	}
	next := cur
	next.Confirmation = in.SendConfirmation
	if botNotifKey("notif-after|", next) != p.After {
		return stale("el resultado ya no coincide con la vista previa; pide una propuesta nueva")
	}
	// Mirror saveBookingNotificationSettings, preserving the stored
	// reconfirmation fields verbatim (never written by the bot).
	if _, err := tx.ExecContext(ctx, `INSERT INTO booking_notification_settings (restaurant_id, send_confirmation, send_reconfirmation, reconfirmation_days_before) VALUES (?, ?, ?, ?) ON DUPLICATE KEY UPDATE send_confirmation = VALUES(send_confirmation), send_reconfirmation = VALUES(send_reconfirmation), reconfirmation_days_before = VALUES(reconfirmation_days_before)`, rid, next.Confirmation, next.Reconfirm, next.Days); err != nil {
		return "", errors.New("error al guardar; no se aplicó nada")
	}
	final, err := botScanNotifSettings(tx.QueryRowContext(ctx, `SELECT send_confirmation, send_reconfirmation, reconfirmation_days_before FROM booking_notification_settings WHERE restaurant_id = ? LIMIT 1`, rid))
	if err != nil {
		return "", errors.New("error interno")
	}
	if botNotifKey("notif-after|", final) != p.After {
		return "", errors.New("verificación fallida: la configuración no quedó como se propuso; no se aplicó nada")
	}
	estado := "desactivado"
	if in.SendConfirmation {
		estado = "activado"
	}
	notice := fmt.Sprintf("Notificaciones de reserva: WhatsApp de confirmación %s. Los emails de confirmación no cambian; la reconfirmación programada no se ha tocado.", estado)
	rj, _ := json.Marshal(map[string]any{"op": p.Op, "notice": notice})
	if _, err := tx.ExecContext(ctx, `UPDATE wa_bot_op_proposals SET result_json=?, notify_state='pending' WHERE id=?`, string(rj), p.ID); err != nil {
		return "", errors.New("error interno")
	}
	s.botSpecialAdminEvent(ctx, tx, rid, p.ID, "executed", p.Actor, strings.TrimSpace(msg.ChatJID), map[string]any{"op": p.Op, "send_confirmation": in.SendConfirmation, "before_hash": p.Before, "after_hash": p.After, "approval_message_id": msg.MessageID})
	if err := tx.Commit(); err != nil {
		log.Printf("[bot] wa_bot_admin_notif_settings_v1 commit_unknown rid=%d proposal=%d err=%v", rid, p.ID, err)
		return "", errors.New("no pude confirmar si se guardó; revisa la configuración antes de repetir")
	}
	return notice, nil
}
