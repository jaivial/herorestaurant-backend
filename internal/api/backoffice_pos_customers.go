package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/go-chi/chi/v5"
	"preactvillacarmen/internal/httpx"
)

// Guest history (pos_customers, migration 174). The record is small on purpose:
// who the guest is and what the staff chose to note. Everything about what they
// ordered and spent is derived from pos_tickets at read time, so it can never
// disagree with the tickets themselves.

// posCustomerPhone keeps digits only so formatting never splits one guest in
// two. A leading 00 or a Spanish 34 country code on an 11-digit number is
// dropped: "+34 600 11 22 33", "0034600112233" and "600112233" are one phone.
func posCustomerPhone(raw string) string {
	var b strings.Builder
	for _, r := range raw {
		if unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	digits := strings.TrimPrefix(b.String(), "00")
	if len(digits) == 11 && strings.HasPrefix(digits, "34") {
		digits = digits[2:]
	}
	return digits
}

type posCustomerInput struct {
	DisplayName string `json:"displayName"`
	Phone       string `json:"phone"`
	Email       string `json:"email"`
	TaxID       string `json:"taxId"`
	Notes       string `json:"notes"`
}

func (in *posCustomerInput) normalise() string {
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	in.Phone = posCustomerPhone(in.Phone)
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	in.Notes = strings.TrimSpace(in.Notes)
	if in.DisplayName == "" || len(in.DisplayName) > 180 {
		return "El nombre es obligatorio (máx. 180)"
	}
	if in.Phone != "" && (len(in.Phone) < 6 || len(in.Phone) > 20) {
		return "Teléfono no válido"
	}
	if in.Email != "" {
		if _, err := mail.ParseAddress(in.Email); err != nil || len(in.Email) > 255 {
			return "Email no válido"
		}
	}
	if strings.TrimSpace(in.TaxID) != "" {
		taxID, ok := normalizeSpanishCustomerTaxID(in.TaxID)
		if !ok {
			return "NIF/CIF no válido"
		}
		in.TaxID = taxID
	} else {
		in.TaxID = ""
	}
	if len(in.Notes) > 1000 {
		return "Las notas son demasiado largas (máx. 1000)"
	}
	return ""
}

// posCustomerConflict answers a duplicate phone/email with the guest who
// already owns it, so the till can offer "use this guest" instead of an error.
func (s *Server) posCustomerConflict(w http.ResponseWriter, r *http.Request, restaurantID int, in posCustomerInput, exceptID int64) bool {
	var id int64
	var name string
	err := s.db.QueryRowContext(r.Context(), `SELECT id,display_name FROM pos_customers WHERE restaurant_id=? AND id<>? AND anonymised_at IS NULL AND ((? <> '' AND phone=?) OR (? <> '' AND email=?)) LIMIT 1`, restaurantID, exceptID, in.Phone, in.Phone, in.Email, in.Email).Scan(&id, &name)
	if err != nil {
		return false
	}
	httpx.WriteJSON(w, http.StatusConflict, map[string]any{"success": false, "code": "CUSTOMER_EXISTS", "message": "Ya existe un cliente con ese teléfono o email: " + name, "customerId": id, "displayName": name})
	return true
}

func (s *Server) handleBOPOSCustomersSearch(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) > 100 {
		q = q[:100]
	}
	digits := posCustomerPhone(q)
	args := []any{a.ActiveRestaurantID}
	where := "c.restaurant_id=? AND c.anonymised_at IS NULL"
	if q != "" {
		where += " AND (c.display_name LIKE ? OR c.email LIKE ? OR c.tax_id LIKE ?"
		like := "%" + strings.NewReplacer("%", "\\%", "_", "\\_").Replace(q) + "%"
		args = append(args, like, like, like)
		if len(digits) >= 3 {
			where += " OR c.phone LIKE ?"
			args = append(args, "%"+digits+"%")
		}
		where += ")"
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT c.id,c.display_name,COALESCE(c.phone,''),COALESCE(c.email,''),COALESCE(c.tax_id,''),
		(SELECT COUNT(DISTINCT t.visit_id) FROM pos_tickets t WHERE t.restaurant_id=c.restaurant_id AND t.customer_id=c.id AND t.status IN ('PAID','PARTIALLY_REFUNDED','REFUNDED')),
		(SELECT MAX(t.paid_at) FROM pos_tickets t WHERE t.restaurant_id=c.restaurant_id AND t.customer_id=c.id AND t.status IN ('PAID','PARTIALLY_REFUNDED','REFUNDED'))
		FROM pos_customers c WHERE `+where+` ORDER BY c.updated_at DESC LIMIT 20`, args...)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error searching customers")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var name, phone, email, taxID string
		var visits int
		var last sql.NullTime
		if err = rows.Scan(&id, &name, &phone, &email, &taxID, &visits, &last); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error reading customers")
			return
		}
		item := map[string]any{"id": id, "displayName": name, "phone": phone, "email": email, "taxId": taxID, "visits": visits, "lastVisitAt": nil}
		if last.Valid {
			item["lastVisitAt"] = last.Time
		}
		out = append(out, item)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "customers": out})
}

func (s *Server) handleBOPOSCustomerCreate(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	var in posCustomerInput
	if !posDecodeBody(w, r, &in) {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid customer")
		return
	}
	if msg := in.normalise(); msg != "" {
		httpx.WriteError(w, http.StatusBadRequest, msg)
		return
	}
	if s.posCustomerConflict(w, r, a.ActiveRestaurantID, in, 0) {
		return
	}
	res, err := s.db.ExecContext(r.Context(), `INSERT INTO pos_customers (restaurant_id,display_name,phone,email,tax_id,notes,created_by) VALUES (?,?,?,?,?,?,?)`, a.ActiveRestaurantID, in.DisplayName, nullIfEmpty(in.Phone), nullIfEmpty(in.Email), nullIfEmpty(in.TaxID), nullIfEmpty(in.Notes), a.User.ID)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") && s.posCustomerConflict(w, r, a.ActiveRestaurantID, in, 0) {
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error creating customer")
		return
	}
	id, _ := res.LastInsertId()
	_, _ = s.db.ExecContext(r.Context(), `INSERT INTO pos_audit_events (restaurant_id,entity_type,entity_id,action,actor_user_id) VALUES (?,'customer',?,'CREATE',?)`, a.ActiveRestaurantID, id, a.User.ID)
	s.writePOSCustomer(w, r, a.ActiveRestaurantID, id, http.StatusCreated)
}

func (s *Server) handleBOPOSCustomerPatch(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var in posCustomerInput
	if id <= 0 || !posDecodeBody(w, r, &in) {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid customer")
		return
	}
	if msg := in.normalise(); msg != "" {
		httpx.WriteError(w, http.StatusBadRequest, msg)
		return
	}
	if s.posCustomerConflict(w, r, a.ActiveRestaurantID, in, id) {
		return
	}
	res, err := s.db.ExecContext(r.Context(), `UPDATE pos_customers SET display_name=?,phone=?,email=?,tax_id=?,notes=? WHERE restaurant_id=? AND id=? AND anonymised_at IS NULL`, in.DisplayName, nullIfEmpty(in.Phone), nullIfEmpty(in.Email), nullIfEmpty(in.TaxID), nullIfEmpty(in.Notes), a.ActiveRestaurantID, id)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error saving customer")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var exists int
		if s.db.QueryRowContext(r.Context(), `SELECT 1 FROM pos_customers WHERE restaurant_id=? AND id=? AND anonymised_at IS NULL`, a.ActiveRestaurantID, id).Scan(&exists) != nil {
			httpx.WriteError(w, http.StatusNotFound, "Customer not found")
			return
		}
	}
	s.writePOSCustomer(w, r, a.ActiveRestaurantID, id, http.StatusOK)
}

// handleBOPOSCustomerAnonymise is the GDPR erasure. The row stays because paid
// tickets reference it and a fiscal record must not change; every personal
// field goes, including the notes (where allergies get written).
func (s *Server) handleBOPOSCustomerAnonymise(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	res, err := s.db.ExecContext(r.Context(), `UPDATE pos_customers SET display_name='Cliente anonimizado',phone=NULL,email=NULL,tax_id=NULL,notes=NULL,anonymised_at=NOW() WHERE restaurant_id=? AND id=? AND anonymised_at IS NULL`, a.ActiveRestaurantID, id)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error anonymising customer")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		httpx.WriteError(w, http.StatusNotFound, "Customer not found")
		return
	}
	_, _ = s.db.ExecContext(r.Context(), `INSERT INTO pos_audit_events (restaurant_id,entity_type,entity_id,action,actor_user_id) VALUES (?,'customer',?,'ANONYMISE',?)`, a.ActiveRestaurantID, id, a.User.ID)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleBOPOSCustomerGet(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	s.writePOSCustomer(w, r, a.ActiveRestaurantID, id, http.StatusOK)
}

func (s *Server) writePOSCustomer(w http.ResponseWriter, r *http.Request, restaurantID int, id int64, status int) {
	customer, err := s.loadPOSCustomerProfile(r.Context(), restaurantID, id)
	if errors.Is(err, sql.ErrNoRows) {
		httpx.WriteError(w, http.StatusNotFound, "Customer not found")
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading customer")
		return
	}
	httpx.WriteJSON(w, status, map[string]any{"success": true, "customer": customer})
}

// loadPOSCustomerProfile returns the guest and their history. Money counts only
// what was actually paid and not given back (total - refunded), and only on
// paid checks: an open check is not yet history.
func (s *Server) loadPOSCustomerProfile(ctx context.Context, restaurantID int, id int64) (map[string]any, error) {
	var name, phone, email, taxID, notes string
	var createdAt time.Time
	var anonymised sql.NullTime
	if err := s.db.QueryRowContext(ctx, `SELECT display_name,COALESCE(phone,''),COALESCE(email,''),COALESCE(tax_id,''),COALESCE(notes,''),created_at,anonymised_at FROM pos_customers WHERE restaurant_id=? AND id=?`, restaurantID, id).Scan(&name, &phone, &email, &taxID, &notes, &createdAt, &anonymised); err != nil {
		return nil, err
	}
	out := map[string]any{"id": id, "displayName": name, "phone": phone, "email": email, "taxId": taxID, "notes": notes, "createdAt": createdAt, "anonymised": anonymised.Valid}
	var visits, checks int
	var spent int64
	var first, last sql.NullTime
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT visit_id),COUNT(*),COALESCE(SUM(total_gross_cents-refunded_cents),0),MIN(paid_at),MAX(paid_at) FROM pos_tickets WHERE restaurant_id=? AND customer_id=? AND status IN ('PAID','PARTIALLY_REFUNDED','REFUNDED')`, restaurantID, id).Scan(&visits, &checks, &spent, &first, &last); err != nil {
		return nil, err
	}
	stats := map[string]any{"visits": visits, "checks": checks, "spentCents": spent, "averageCheckCents": int64(0), "firstVisitAt": nil, "lastVisitAt": nil}
	if checks > 0 {
		stats["averageCheckCents"] = spent / int64(checks)
	}
	if first.Valid {
		stats["firstVisitAt"] = first.Time
		stats["lastVisitAt"] = last.Time
	}
	out["stats"] = stats
	// What they usually order: by quantity across paid checks, menus counted as
	// the menu (components are part of it, not separate orders).
	favRows, err := s.db.QueryContext(ctx, `SELECT l.product_name_snapshot,SUM(l.quantity) q FROM pos_ticket_lines l JOIN pos_tickets t ON t.restaurant_id=l.restaurant_id AND t.id=l.ticket_id WHERE l.restaurant_id=? AND t.customer_id=? AND t.status IN ('PAID','PARTIALLY_REFUNDED','REFUNDED') AND l.status='ACTIVE' AND l.parent_line_id IS NULL GROUP BY l.product_name_snapshot ORDER BY q DESC,l.product_name_snapshot LIMIT 5`, restaurantID, id)
	if err != nil {
		return nil, err
	}
	favourites := []map[string]any{}
	for favRows.Next() {
		var product string
		var qty float64
		if err = favRows.Scan(&product, &qty); err != nil {
			favRows.Close()
			return nil, err
		}
		favourites = append(favourites, map[string]any{"productName": product, "quantity": qty})
	}
	favRows.Close()
	out["favourites"] = favourites
	histRows, err := s.db.QueryContext(ctx, `SELECT t.id,t.ticket_number,t.status,t.total_gross_cents,t.refunded_cents,t.paid_at,v.service_date,COALESCE(rt.name,'') FROM pos_tickets t JOIN pos_visits v ON v.restaurant_id=t.restaurant_id AND v.id=t.visit_id LEFT JOIN restaurant_tables rt ON rt.restaurant_id=v.restaurant_id AND rt.id=v.table_id WHERE t.restaurant_id=? AND t.customer_id=? AND t.status<>'VOIDED' ORDER BY t.id DESC LIMIT 10`, restaurantID, id)
	if err != nil {
		return nil, err
	}
	defer histRows.Close()
	history := []map[string]any{}
	for histRows.Next() {
		var ticketID, total, refunded int64
		var number, ticketStatus, table string
		var paidAt sql.NullTime
		var serviceDate time.Time
		if err = histRows.Scan(&ticketID, &number, &ticketStatus, &total, &refunded, &paidAt, &serviceDate, &table); err != nil {
			return nil, err
		}
		item := map[string]any{"ticketId": ticketID, "ticketNumber": number, "status": ticketStatus, "totalGrossCents": total, "refundedCents": refunded, "serviceDate": serviceDate.Format("2006-01-02"), "tableName": table, "paidAt": nil}
		if paidAt.Valid {
			item["paidAt"] = paidAt.Time
		}
		history = append(history, item)
	}
	out["history"] = history
	return out, histRows.Err()
}

// handleBOPOSTicketCustomer links a check to a guest (or unlinks it with
// customerId 0). Allowed after payment too: who ate is not part of the amount,
// the VAT or the fiscal hash, and "add this to my history" is usually asked
// at the end of the meal.
func (s *Server) handleBOPOSTicketCustomer(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	ticketID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var in struct {
		CustomerID int64 `json:"customerId"`
	}
	if ticketID <= 0 || !posDecodeBody(w, r, &in) || in.CustomerID < 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid customer link")
		return
	}
	var customer any
	if in.CustomerID > 0 {
		var ok int
		if s.db.QueryRowContext(r.Context(), `SELECT 1 FROM pos_customers WHERE restaurant_id=? AND id=? AND anonymised_at IS NULL`, a.ActiveRestaurantID, in.CustomerID).Scan(&ok) != nil {
			httpx.WriteError(w, http.StatusNotFound, "Customer not found")
			return
		}
		customer = in.CustomerID
	}
	res, err := s.db.ExecContext(r.Context(), `UPDATE pos_tickets SET customer_id=? WHERE restaurant_id=? AND id=? AND status<>'VOIDED'`, customer, a.ActiveRestaurantID, ticketID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error linking customer")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var exists int
		if s.db.QueryRowContext(r.Context(), `SELECT 1 FROM pos_tickets WHERE restaurant_id=? AND id=? AND status<>'VOIDED'`, a.ActiveRestaurantID, ticketID).Scan(&exists) != nil {
			httpx.WriteError(w, http.StatusNotFound, "Ticket not found")
			return
		}
	}
	_, _ = s.db.ExecContext(r.Context(), `INSERT INTO pos_audit_events (restaurant_id,entity_type,entity_id,action,after_json,actor_user_id) VALUES (?,'ticket',?,'CUSTOMER_LINK',JSON_OBJECT('customerId',?),?)`, a.ActiveRestaurantID, ticketID, in.CustomerID, a.User.ID)
	ticket, err := s.loadPOSTicket(r.Context(), a.ActiveRestaurantID, ticketID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading ticket")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "ticket": ticket})
}
