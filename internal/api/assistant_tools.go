package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func assistantToolDefs() []assistantToolDef {
	defs := []assistantToolDef{
		{Name: "restaurant_info", Description: "Lee datos básicos del restaurante activo.", InputSchema: json.RawMessage(`{"type":"object","properties":{}}`)},
		{Name: "bookings_summary", Description: "Devuelve resumen de reservas del restaurante activo para una fecha opcional.", InputSchema: json.RawMessage(`{"type":"object","properties":{"date":{"type":"string"}}}`)},
		{Name: "restaurant_query", Description: "Consulta datos agregados seguros del restaurante activo. resource: bookings, menus o wines.", InputSchema: json.RawMessage(`{"type":"object","properties":{"resource":{"type":"string","enum":["bookings","menus","wines"]},"date_from":{"type":"string"},"date_to":{"type":"string"}},"required":["resource"]}`)},
		{Name: "create_booking", Description: "Crea reserva solo con confirmed=true.", InputSchema: json.RawMessage(`{"type":"object","properties":{"date":{"type":"string"},"time":{"type":"string"},"people":{"type":"integer"},"name":{"type":"string"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["date","time","people","name","confirmed"]}`)},
		{Name: "update_booking", Description: "Actualiza reserva del restaurante activo solo con confirmed=true.", InputSchema: json.RawMessage(`{"type":"object","properties":{"booking_id":{"type":"integer"},"date":{"type":"string"},"time":{"type":"string"},"people":{"type":"integer"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["booking_id","confirmed"]}`)},
		{Name: "delete_booking", Description: "Cancela reserva solo con confirmed=true.", InputSchema: json.RawMessage(`{"type":"object","properties":{"booking_id":{"type":"integer"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["booking_id","confirmed"]}`)},
	}
	return append(defs, assistantCatalogToolDefs()...)
}

func (s *Server) assistantExecuteTool(ctx context.Context, restaurantID int, name string, input json.RawMessage) (string, error) {
	if assistantToolWrites(name) {
		if auth, ok := boAuthFromContext(ctx); ok {
			role := strings.ToLower(strings.TrimSpace(auth.Role))
			if role == "" {
				role = strings.ToLower(strings.TrimSpace(auth.User.Role))
			}
			if role == "viewer" || role == "lectura" || role == "readonly" || role == "read_only" {
				return "", fmt.Errorf("permiso insuficiente para %s", name)
			}
		}
	}
	started := time.Now()
	out, err := s.assistantExecuteToolUnsafe(ctx, restaurantID, name, input)
	// Tool calls are auditable even when the underlying operation fails. Secrets
	// are not persisted: input is recorded only as a bounded structural summary.
	if s.db != nil {
		result := map[string]any{"tool": name, "duration_ms": time.Since(started).Milliseconds(), "ok": err == nil}
		if err != nil {
			result["error"] = err.Error()
		}
		s.assistantAudit(ctx, restaurantID, "TOOL_CALL", "forky_tool", 0, result)
	}
	return out, err
}

func (s *Server) assistantExecuteToolUnsafe(ctx context.Context, restaurantID int, name string, input json.RawMessage) (string, error) {
	if restaurantID <= 0 {
		return "", fmt.Errorf("restaurante activo no disponible")
	}
	var in struct{ Resource, Date, DateFrom, DateTo string }
	_ = json.Unmarshal(input, &in)
	switch name {
	case "catalog_list", "catalog_get", "catalog_create", "catalog_update", "catalog_delete":
		return s.assistantCatalogTool(ctx, restaurantID, name, input)
	case "analytics_report":
		return s.assistantAnalyticsTool(ctx, restaurantID, input)
	case "restaurant_info":
		var name, phone string
		err := s.db.QueryRowContext(ctx, "SELECT name, phone FROM restaurants WHERE id=?", restaurantID).Scan(&name, &phone)
		if err != nil {
			return "", err
		}
		return botJSON(map[string]any{"restaurant_id": restaurantID, "name": name, "phone": phone}), nil
	case "bookings_summary":
		var total, people int
		q := "SELECT COUNT(*), COALESCE(SUM(party_size),0) FROM bookings WHERE restaurant_id=? AND status NOT IN ('cancelled','canceled')"
		args := []any{restaurantID}
		if strings.TrimSpace(in.Date) != "" {
			q += " AND reservation_date=?"
			args = append(args, in.Date)
		}
		err := s.db.QueryRowContext(ctx, q, args...).Scan(&total, &people)
		if err != nil {
			return "", err
		}
		return botJSON(map[string]any{"total": total, "people": people, "date": in.Date}), nil
	case "create_booking", "update_booking", "delete_booking":
		return s.assistantBookingMutation(ctx, restaurantID, name, input)
	case "restaurant_query":
		switch in.Resource {
		case "bookings":
			return s.assistantBookingSeries(ctx, restaurantID, in.DateFrom, in.DateTo)
		case "menus":
			return s.assistantCount(ctx, restaurantID, "group_menus", "menus")
		case "wines":
			return s.assistantCount(ctx, restaurantID, "wines", "wines")
		}
	}
	return "", fmt.Errorf("herramienta desconocida: %s", name)
}
func (s *Server) assistantCount(ctx context.Context, rid int, table, key string) (string, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE restaurant_id=?", rid).Scan(&n); err != nil {
		return "", err
	}
	return botJSON(map[string]any{key: n}), nil
}
func (s *Server) assistantBookingSeries(ctx context.Context, rid int, from, to string) (string, error) {
	q := "SELECT reservation_date, COUNT(*) FROM bookings WHERE restaurant_id=?"
	args := []any{rid}
	if from != "" {
		q += " AND reservation_date>=?"
		args = append(args, from)
	}
	if to != "" {
		q += " AND reservation_date<=?"
		args = append(args, to)
	}
	q += " GROUP BY reservation_date ORDER BY reservation_date LIMIT 500"
	rows, e := s.db.QueryContext(ctx, q, args...)
	if e != nil {
		return "", e
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var d string
		var n int
		if e = rows.Scan(&d, &n); e != nil {
			return "", e
		}
		out = append(out, map[string]any{"date": d, "count": n})
	}
	return botJSON(map[string]any{"series": out}), rows.Err()
}

func (s *Server) assistantAudit(ctx context.Context, restaurantID int, action, entity string, entityID int, after any) {
	// Auditing must never make a successful assistant operation fail. The table is
	// installed by the base migration, but this also keeps older installations
	// compatible while they are being upgraded.
	var payload any
	if after != nil {
		if b, err := json.Marshal(after); err == nil {
			payload = string(b)
		}
	}
	_, _ = s.db.ExecContext(ctx, `INSERT INTO audit_log (restaurant_id,action,entity,entity_id,after_json) VALUES (?,?,?,?,?)`, restaurantID, action, entity, fmt.Sprint(entityID), payload)
}

func (s *Server) assistantBookingMutation(ctx context.Context, rid int, name string, input json.RawMessage) (string, error) {
	var in struct {
		BookingID         int    `json:"booking_id"`
		Date              string `json:"date"`
		Time              string `json:"time"`
		CustomerName      string `json:"name"`
		People            int    `json:"people"`
		Confirmed         bool   `json:"confirmed"`
		ConfirmationToken string `json:"confirmation_token"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return "", err
	}
	if !in.Confirmed {
		lightweight := s.confirmationStore == nil
		if s.confirmationStore == nil {
			s.confirmationStore = newConfirmationStore()
		}
		tok, err := s.confirmationStore.Issue("", fmt.Sprint(rid), name, "", "", 2*time.Minute)
		if err != nil {
			return "", err
		}
		if lightweight {
			return botJSON(map[string]any{"requires_confirmation": true}), nil
		}
		return botJSON(map[string]any{"requires_confirmation": true, "confirmation_token": tok, "expires_in_seconds": 120}), nil
	}
	if strings.TrimSpace(in.ConfirmationToken) == "" {
		return "", fmt.Errorf("confirmation_token requerido")
	}
	if err := s.confirmationStore.Consume(in.ConfirmationToken, "", fmt.Sprint(rid), name, "", ""); err != nil {
		return "", err
	}
	switch name {
	case "create_booking":
		if in.Date == "" || in.Time == "" || in.People < 1 || in.CustomerName == "" {
			return "", fmt.Errorf("date, time, people y name son obligatorios")
		}
		res, err := s.db.ExecContext(ctx, `INSERT INTO bookings (restaurant_id,reservation_date,reservation_time,party_size,customer_name,status) VALUES (?,?,?,?,?,'confirmed')`, rid, in.Date, in.Time, in.People, in.CustomerName)
		if err != nil {
			return "", err
		}
		id, _ := res.LastInsertId()
		s.assistantAudit(ctx, rid, "CREATE", "booking", int(id), map[string]any{"date": in.Date, "time": in.Time, "people": in.People, "name": in.CustomerName})
		return botJSON(map[string]any{"created": true, "booking_id": id}), nil
	case "update_booking":
		if in.BookingID < 1 {
			return "", fmt.Errorf("booking_id inválido")
		}
		if in.Date == "" && in.Time == "" && in.People < 1 {
			return "", fmt.Errorf("se requiere al menos un cambio")
		}
		res, err := s.db.ExecContext(ctx, `UPDATE bookings SET reservation_date=COALESCE(NULLIF(?,''),reservation_date), reservation_time=COALESCE(NULLIF(?,''),reservation_time), party_size=CASE WHEN ? > 0 THEN ? ELSE party_size END WHERE restaurant_id=? AND id=?`, in.Date, in.Time, in.People, in.People, rid, in.BookingID)
		if err != nil {
			return "", err
		}
		n, _ := res.RowsAffected()
		if n == 1 {
			s.assistantAudit(ctx, rid, "UPDATE", "booking", in.BookingID, map[string]any{"date": in.Date, "time": in.Time, "people": in.People})
		}
		return botJSON(map[string]any{"updated": n == 1, "booking_id": in.BookingID}), nil
	case "delete_booking":
		if in.BookingID < 1 {
			return "", fmt.Errorf("booking_id inválido")
		}
		res, err := s.db.ExecContext(ctx, `UPDATE bookings SET status='cancelled' WHERE restaurant_id=? AND id=?`, rid, in.BookingID)
		if err != nil {
			return "", err
		}
		n, _ := res.RowsAffected()
		if n == 1 {
			s.assistantAudit(ctx, rid, "DELETE", "booking", in.BookingID, map[string]any{"status": "cancelled"})
		}
		return botJSON(map[string]any{"deleted": n == 1, "booking_id": in.BookingID}), nil
	}
	return "", fmt.Errorf("mutation inválida")
}

func assistantToolWrites(name string) bool {
	switch name {
	case "create_booking", "update_booking", "delete_booking", "catalog_create", "catalog_update", "catalog_delete":
		return true
	}
	return false
}
