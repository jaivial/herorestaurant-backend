package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func (s *Server) assistantExecuteTool(ctx context.Context, restaurantID int, name string, input json.RawMessage) (string, error) {
	if len(input) > 64*1024 {
		return "", fmt.Errorf("tool input demasiado grande")
	}
	// Authorize before touching the database. Missing backoffice auth is denied
	// for assistant tools (public assistant requests use a separate path).
	if auth, ok := boAuthFromContext(ctx); ok {
		if !assistantToolAllowed(auth, name) {
			return "", fmt.Errorf("permiso insuficiente para %s", name)
		}
	} else {
		t, _ := assistantToolLookup(name)
		// Anonymous/public assistant sessions may only read public-safe data
		// (e.g. restaurant_info); everything else is denied before touching DB.
		if t.Write || t.BackofficeOnly {
			return "", fmt.Errorf("autenticación requerida para %s", name)
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
		// Best-effort structured audit (migration 015); deployments without the
		// migration retain the legacy audit row above.
		uid := any(nil)
		if auth, ok := boAuthFromContext(ctx); ok {
			uid = auth.User.ID
		}
		var summary any = result
		if b, e := json.Marshal(summary); e == nil {
			_, _ = s.db.ExecContext(ctx, `INSERT INTO assistant_tool_audit (user_id,restaurant_id,tool_name,tool_version,result_summary,error_message,duration_ms) VALUES (?,?,?,?,?,?,?)`, uid, restaurantID, name, "1", string(b), func() any {
				if err != nil {
					return err.Error()
				}
				return nil
			}(), time.Since(started).Milliseconds())
		}
	}
	return out, err
}

func (s *Server) assistantExecuteToolUnsafe(ctx context.Context, restaurantID int, name string, input json.RawMessage) (string, error) {
	if restaurantID <= 0 {
		return "", fmt.Errorf("restaurante activo no disponible")
	}
	t, ok := assistantToolLookup(name)
	if !ok || t.Handler == nil {
		return "", fmt.Errorf("herramienta desconocida: %s", name)
	}
	return t.Handler(s, ctx, restaurantID, input)
}

// assistantRestaurantInfo returns the active restaurant's basic profile.
// Contact fields are read only when their columns exist in the schema (the
// legacy PHP install stores them as contact_phone/contact_email/location, not
// phone/email/address).
func (s *Server) assistantRestaurantInfo(ctx context.Context, rid int) (string, error) {
	var name string
	if err := s.db.QueryRowContext(ctx, "SELECT name FROM restaurants WHERE id=?", rid).Scan(&name); err != nil {
		return "", err
	}
	out := map[string]any{"restaurant_id": rid, "name": name}
	for col, key := range map[string]string{"contact_phone": "phone", "contact_email": "email", "location": "address", "website_url": "website_url"} {
		if !s.assistantColumnExists(ctx, "restaurants", col) {
			continue
		}
		var v sql.NullString
		if err := s.db.QueryRowContext(ctx, "SELECT "+col+" FROM restaurants WHERE id=?", rid).Scan(&v); err == nil && v.Valid {
			out[key] = v.String
		}
	}
	return botJSON(out), nil
}

// assistantColumnExists reports whether a column exists on a table in the
// current schema (used to read optional fields defensively across schemas).
func (s *Server) assistantColumnExists(ctx context.Context, table, column string) bool {
	if s.db == nil {
		return false
	}
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name=? AND column_name=?`, table, column).Scan(&n)
	return err == nil && n > 0
}

// assistantBookingsSummary aggregates the active restaurant's bookings for a
// date or optional range (totals and people).
func (s *Server) assistantBookingsSummary(ctx context.Context, rid int, input json.RawMessage) (string, error) {
	var in struct {
		Date     string `json:"date"`
		DateFrom string `json:"date_from"`
		DateTo   string `json:"date_to"`
	}
	_ = json.Unmarshal(input, &in)
	var total, people int
	q := "SELECT COUNT(*), COALESCE(SUM(party_size),0) FROM bookings WHERE restaurant_id=? AND status NOT IN ('cancelled','canceled')"
	args := []any{rid}
	if strings.TrimSpace(in.Date) != "" {
		q += " AND reservation_date=?"
		args = append(args, in.Date)
	} else {
		if strings.TrimSpace(in.DateFrom) != "" {
			q += " AND reservation_date>=?"
			args = append(args, in.DateFrom)
		}
		if strings.TrimSpace(in.DateTo) != "" {
			q += " AND reservation_date<=?"
			args = append(args, in.DateTo)
		}
	}
	if err := s.db.QueryRowContext(ctx, q, args...).Scan(&total, &people); err != nil {
		return "", err
	}
	return botJSON(map[string]any{"total": total, "people": people, "date": in.Date, "date_from": in.DateFrom, "date_to": in.DateTo}), nil
}

// assistantBookingsListHandler parses the bookings_list input and returns the
// per-reservation rows for table rendering.
func (s *Server) assistantBookingsListHandler(ctx context.Context, rid int, input json.RawMessage) (string, error) {
	var in struct {
		Date     string `json:"date"`
		DateFrom string `json:"date_from"`
		DateTo   string `json:"date_to"`
		Limit    int    `json:"limit"`
	}
	_ = json.Unmarshal(input, &in)
	return s.assistantBookingList(ctx, rid, in.Date, in.DateFrom, in.DateTo, in.Limit)
}

// assistantRestaurantQuery returns safe aggregated data for the active
// restaurant: bookings series, or counts of menus / wines.
func (s *Server) assistantRestaurantQuery(ctx context.Context, rid int, input json.RawMessage) (string, error) {
	var in struct {
		Resource string `json:"resource"`
		DateFrom string `json:"date_from"`
		DateTo   string `json:"date_to"`
	}
	_ = json.Unmarshal(input, &in)
	switch in.Resource {
	case "bookings":
		return s.assistantBookingSeries(ctx, rid, in.DateFrom, in.DateTo)
	case "menus":
		return s.assistantCount(ctx, rid, "group_menus", "menus")
	case "wines":
		return s.assistantCount(ctx, rid, "wines", "wines")
	}
	return "", fmt.Errorf("resource no permitido: %s", in.Resource)
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

// assistantBookingList returns the individual reservation rows (client, date,
// time, party size, status) for the active restaurant, so the model can render a
// per-reservation table. Optional single date / range / limit are applied.
func (s *Server) assistantBookingList(ctx context.Context, rid int, date, dateFrom, dateTo string, limit int) (string, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	q := "SELECT id, customer_name, reservation_date, reservation_time, party_size, status FROM bookings WHERE restaurant_id=?"
	args := []any{rid}
	if strings.TrimSpace(date) != "" {
		q += " AND reservation_date=?"
		args = append(args, date)
	} else {
		if strings.TrimSpace(dateFrom) != "" {
			q += " AND reservation_date>=?"
			args = append(args, dateFrom)
		}
		if strings.TrimSpace(dateTo) != "" {
			q += " AND reservation_date<=?"
			args = append(args, dateTo)
		}
	}
	q += " AND status NOT IN ('cancelled','canceled') ORDER BY reservation_date, reservation_time LIMIT ?"
	args = append(args, limit)
	rows, e := s.db.QueryContext(ctx, q, args...)
	if e != nil {
		return "", e
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, people int
		var name, status string
		var dateVal time.Time // DATE column -> time.Time
		var timeStr string    // TIME column -> []byte (e.g. "14:30:00")
		if e = rows.Scan(&id, &name, &dateVal, &timeStr, &people, &status); e != nil {
			return "", e
		}
		out = append(out, map[string]any{
			"id":            id,
			"customer_name": name,
			"date":          dateVal.Format("2006-01-02"),
			"time":          timeStr,
			"people":        people,
			"status":        status,
		})
	}
	return botJSON(map[string]any{"bookings": out}), rows.Err()
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
		return s.assistantRequireConfirmation(rid, name, input)
	}
	if err := s.assistantConsumeConfirmation(in.ConfirmationToken, rid, name, input); err != nil {
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

// assistantToolAllowed maps Forky tools to the same section permissions exposed
// by boAuth, derived from the tool registry. Explicit SectionAccess is
// authoritative; otherwise role defaults are used. Writes additionally require
// the section's write capability.
func assistantToolAllowed(a boAuth, tool string) bool {
	t, ok := assistantToolLookup(tool)
	if !ok {
		return false
	}
	section, write := t.Section, t.Write
	role := strings.ToLower(strings.TrimSpace(a.Role))
	if role == "" {
		role = strings.ToLower(strings.TrimSpace(a.User.Role))
	}
	allowed := map[string]bool{}
	for _, x := range a.User.SectionAccess {
		allowed[strings.ToLower(strings.TrimSpace(x))] = true
	}
	if len(allowed) == 0 {
		if role == "root" || role == "admin" || role == "owner" {
			allowed[section] = true
		}
		if role == "metre" {
			allowed["reservas"], allowed["comida"] = true, true
		}
		if role == "jefe_cocina" {
			allowed["comida"] = true
		}
	}
	if !allowed[section] {
		return false
	}
	if write && (role == "viewer" || role == "lectura" || role == "readonly" || role == "read_only" || role == "camarero") {
		return false
	}
	return true
}
