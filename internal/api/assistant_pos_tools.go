package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func (s *Server) assistantPOSMutation(ctx context.Context, rid int, name string, input json.RawMessage) (string, error) {
	var in struct {
		VisitID           int    `json:"visit_id"`
		TicketID          int    `json:"ticket_id"`
		Covers            int    `json:"covers"`
		AmountCents       int    `json:"amount_cents"`
		Channel           string `json:"channel"`
		Method            string `json:"method"`
		PaymentMethod     string `json:"payment_method"`
		Reason            string `json:"reason"`
		IdempotencyKey    string `json:"idempotency_key"`
		Confirmed         bool   `json:"confirmed"`
		ConfirmationToken string `json:"confirmation_token"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return "", err
	}
	if !in.Confirmed {
		if s.confirmationStore == nil {
			s.confirmationStore = newConfirmationStore()
		}
		tok, e := s.confirmationStore.Issue("", fmt.Sprint(rid), name, "", "", 2*time.Minute)
		if e != nil {
			return "", e
		}
		return botJSON(map[string]any{"requires_confirmation": true, "confirmation_token": tok, "expires_in_seconds": 120}), nil
	}
	if s.confirmationStore == nil || strings.TrimSpace(in.ConfirmationToken) == "" {
		return "", fmt.Errorf("confirmation_token requerido")
	}
	if e := s.confirmationStore.Consume(in.ConfirmationToken, "", fmt.Sprint(rid), name, "", ""); e != nil {
		return "", e
	}
	uid := 0
	if a, ok := boAuthFromContext(ctx); ok {
		uid = a.User.ID
	}
	if uid == 0 {
		return "", fmt.Errorf("usuario no disponible")
	}
	switch name {
	case "pos_visit_create":
		if in.Covers < 1 || strings.TrimSpace(in.Channel) == "" {
			return "", fmt.Errorf("channel y covers son obligatorios")
		}
		r, e := s.db.ExecContext(ctx, `INSERT INTO pos_visits (restaurant_id,channel,service_date,service_type,covers,status,opened_by,open_idempotency_key) VALUES (?,?,CURDATE(),'OTHER',?,'OPEN',?,?)`, rid, in.Channel, in.Covers, uid, in.IdempotencyKey)
		if e != nil {
			return "", e
		}
		id, _ := r.LastInsertId()
		return botJSON(map[string]any{"created": true, "visit_id": id}), nil
	case "pos_ticket_create":
		if in.VisitID < 1 {
			return "", fmt.Errorf("visit_id inválido")
		}
		var exists int
		if e := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pos_visits WHERE restaurant_id=? AND id=?`, rid, in.VisitID).Scan(&exists); e != nil || exists != 1 {
			return "", fmt.Errorf("visita no encontrada")
		}
		r, e := s.db.ExecContext(ctx, `INSERT INTO pos_tickets (restaurant_id,visit_id,ticket_number,creation_idempotency_key,opened_by) VALUES (?,?,CONCAT('F-',UUID()),?,?)`, rid, in.VisitID, in.IdempotencyKey, uid)
		_ = r
		if e != nil {
			return "", e
		}
		id, _ := r.LastInsertId()
		return botJSON(map[string]any{"created": true, "ticket_id": id}), nil
	case "pos_payment_create":
		if in.TicketID < 1 || in.AmountCents < 1 || in.IdempotencyKey == "" {
			return "", fmt.Errorf("ticket_id, amount_cents e idempotency_key inválidos")
		}
		r, e := s.db.ExecContext(ctx, `INSERT INTO pos_payments (restaurant_id,ticket_id,method,amount_cents,idempotency_key,received_by) SELECT ?,id,?,?,?,? FROM pos_tickets WHERE restaurant_id=? AND id=?`, rid, in.TicketID, in.Method, in.AmountCents, in.IdempotencyKey, uid, rid, in.TicketID)
		if e != nil {
			return "", e
		}
		id, _ := r.LastInsertId()
		return botJSON(map[string]any{"created": true, "payment_id": id}), nil
	case "pos_refund_create":
		if in.TicketID < 1 || in.AmountCents < 1 || strings.TrimSpace(in.Reason) == "" || in.IdempotencyKey == "" {
			return "", fmt.Errorf("datos de reembolso inválidos")
		}
		r, e := s.db.ExecContext(ctx, `INSERT INTO pos_refunds (restaurant_id,ticket_id,amount_cents,reason,payment_method,idempotency_key,created_by) SELECT ?,id,?,?,?,?,? FROM pos_tickets WHERE restaurant_id=? AND id=?`, rid, in.TicketID, in.AmountCents, in.Reason, in.PaymentMethod, in.IdempotencyKey, uid, rid, in.TicketID)
		if e != nil {
			return "", e
		}
		id, _ := r.LastInsertId()
		return botJSON(map[string]any{"created": true, "refund_id": id}), nil
	}
	return "", fmt.Errorf("mutación POS inválida")
}
