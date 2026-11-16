package api

import (
	"context"
	"encoding/json"
	"fmt"
	"preactvillacarmen/internal/sqldb"
	"strings"
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
		return s.assistantRequireConfirmation(rid, name, input)
	}
	if err := s.assistantConsumeConfirmation(in.ConfirmationToken, rid, name, input); err != nil {
		return "", err
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
		// CURDATE() = session (UTC) date per statement. go-sql-driver v1.9.3
		// LastInsertId never errors, so InsertID keeps the MySQL path identical.
		id, e := sqldb.InsertID(ctx, sqlDialect, s.db, nativeSQL(`INSERT INTO pos_visits (restaurant_id,channel,service_date,service_type,covers,status,opened_by,open_idempotency_key) VALUES (?,?,CURDATE(),'OTHER',?,'OPEN',?,?)`, `INSERT INTO pos_visits (restaurant_id,channel,service_date,service_type,covers,status,opened_by,open_idempotency_key) VALUES (?,?,CAST(statement_timestamp() AT TIME ZONE 'UTC' AS date),'OTHER',?,'OPEN',?,?)`), "id", rid, in.Channel, in.Covers, uid, in.IdempotencyKey)
		if e != nil {
			return "", e
		}
		return botJSON(map[string]any{"created": true, "visit_id": id}), nil
	case "pos_ticket_create":
		if in.VisitID < 1 {
			return "", fmt.Errorf("visit_id inválido")
		}
		var exists int
		if e := s.db.QueryRowContext(ctx, portableSQL(`SELECT COUNT(*) FROM pos_visits WHERE restaurant_id=? AND id=?`), rid, in.VisitID).Scan(&exists); e != nil || exists != 1 {
			return "", fmt.Errorf("visita no encontrada")
		}
		// ticket_number = 'F-' + MySQL UUID() (v1). PG binds a Go v1 UUID
		// (sql_uuid_v1_pg_v1); MySQL keeps UUID() in SQL. id via RETURNING.
		args := []any{rid, in.VisitID}
		if sqlDialect == sqldb.Postgres {
			u, ue := sqlUUIDv1()
			if ue != nil {
				return "", fmt.Errorf("pos_ticket_create: uuid: %w", ue)
			}
			args = append(args, "F-"+u)
		}
		args = append(args, in.IdempotencyKey, uid)
		id, e := sqldb.InsertID(ctx, sqlDialect, s.db, nativeSQL(`INSERT INTO pos_tickets (restaurant_id,visit_id,ticket_number,creation_idempotency_key,opened_by) VALUES (?,?,CONCAT('F-',UUID()),?,?)`, `INSERT INTO pos_tickets (restaurant_id,visit_id,ticket_number,creation_idempotency_key,opened_by) VALUES (?,?,?,?,?)`), "id", args...)
		if e != nil {
			return "", e
		}
		return botJSON(map[string]any{"created": true, "ticket_id": id}), nil
	case "pos_payment_create":
		if in.TicketID < 1 || in.AmountCents < 1 || in.IdempotencyKey == "" {
			return "", fmt.Errorf("ticket_id, amount_cents e idempotency_key inválidos")
		}
		// No ticket -> 0 rows: MySQL OK packet id 0, PG InsertSelectID (0,0,nil).
		// method is an enum: an unknown value fails on both (strict mode).
		id, _, e := sqldb.InsertSelectID(ctx, sqlDialect, s.db, nativeSQL(`INSERT INTO pos_payments (restaurant_id,ticket_id,method,amount_cents,idempotency_key,received_by) SELECT ?,id,?,?,?,? FROM pos_tickets WHERE restaurant_id=? AND id=?`, `INSERT INTO pos_payments (restaurant_id,ticket_id,method,amount_cents,idempotency_key,received_by) SELECT CAST(? AS integer),id,CAST(CAST(? AS text) AS enum__pos_payments__method),CAST(? AS bigint),CAST(? AS text),CAST(? AS integer) FROM pos_tickets WHERE restaurant_id=? AND id=?`), "id", rid, in.TicketID, mysqlUpperEnumArg(in.Method), in.AmountCents, in.IdempotencyKey, uid, rid, in.TicketID)
		if e != nil {
			return "", e
		}
		return botJSON(map[string]any{"created": true, "payment_id": id}), nil
	case "pos_refund_create":
		if in.TicketID < 1 || in.AmountCents < 1 || strings.TrimSpace(in.Reason) == "" || in.IdempotencyKey == "" {
			return "", fmt.Errorf("datos de reembolso inválidos")
		}
		id, _, e := sqldb.InsertSelectID(ctx, sqlDialect, s.db, nativeSQL(`INSERT INTO pos_refunds (restaurant_id,ticket_id,amount_cents,reason,payment_method,idempotency_key,created_by) SELECT ?,id,?,?,?,?,? FROM pos_tickets WHERE restaurant_id=? AND id=?`, `INSERT INTO pos_refunds (restaurant_id,ticket_id,amount_cents,reason,payment_method,idempotency_key,created_by) SELECT CAST(? AS integer),id,CAST(? AS bigint),CAST(? AS text),CAST(CAST(? AS text) AS enum__pos_refunds__payment_method),CAST(? AS text),CAST(? AS integer) FROM pos_tickets WHERE restaurant_id=? AND id=?`), "id", rid, in.TicketID, in.AmountCents, in.Reason, mysqlUpperEnumArg(in.PaymentMethod), in.IdempotencyKey, uid, rid, in.TicketID)
		if e != nil {
			return "", e
		}
		return botJSON(map[string]any{"created": true, "refund_id": id}), nil
	}
	return "", fmt.Errorf("mutación POS inválida")
}
