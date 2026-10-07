package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"preactvillacarmen/internal/httpx"
)

func (s *Server) handleBOPOSCategoriesList(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,name,sort_order,is_active FROM pos_product_categories WHERE restaurant_id=? ORDER BY sort_order,name`, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, 500, "Error loading POS categories")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id int64
		var name string
		var sortOrder, active int
		if err = rows.Scan(&id, &name, &sortOrder, &active); err != nil {
			httpx.WriteError(w, 500, "Error reading POS categories")
			return
		}
		items = append(items, map[string]any{"id": id, "name": name, "sortOrder": sortOrder, "isActive": active != 0})
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "items": items})
}
func (s *Server) saveBOPOSCategory(w http.ResponseWriter, r *http.Request, id int64) {
	a, _ := boAuthFromContext(r.Context())
	var in struct {
		Name      string `json:"name"`
		SortOrder int    `json:"sortOrder"`
		IsActive  bool   `json:"isActive"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || strings.TrimSpace(in.Name) == "" {
		httpx.WriteError(w, 400, "Invalid POS category")
		return
	}
	if id == 0 {
		res, err := s.db.ExecContext(r.Context(), `INSERT INTO pos_product_categories (restaurant_id,name,sort_order,is_active) VALUES (?,?,?,?)`, a.ActiveRestaurantID, strings.TrimSpace(in.Name), in.SortOrder, stockBoolInt(in.IsActive))
		if err != nil {
			httpx.WriteError(w, 400, "POS category could not be created")
			return
		}
		id, _ = res.LastInsertId()
		httpx.WriteJSON(w, 201, map[string]any{"success": true, "id": id})
		return
	}
	res, err := s.db.ExecContext(r.Context(), `UPDATE pos_product_categories SET name=?,sort_order=?,is_active=? WHERE restaurant_id=? AND id=?`, strings.TrimSpace(in.Name), in.SortOrder, stockBoolInt(in.IsActive), a.ActiveRestaurantID, id)
	if err != nil {
		httpx.WriteError(w, 400, "POS category could not be updated")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		httpx.WriteError(w, 404, "POS category not found")
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true})
}
func (s *Server) handleBOPOSCategoryCreate(w http.ResponseWriter, r *http.Request) {
	s.saveBOPOSCategory(w, r, 0)
}
func (s *Server) handleBOPOSCategoryPatch(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	s.saveBOPOSCategory(w, r, id)
}
func (s *Server) handleBOPOSCategoryDelete(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var used int
	_ = s.db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM pos_products WHERE restaurant_id=? AND category_id=?)`, a.ActiveRestaurantID, id).Scan(&used)
	if used != 0 {
		httpx.WriteError(w, 409, "POS category is in use")
		return
	}
	res, err := s.db.ExecContext(r.Context(), `DELETE FROM pos_product_categories WHERE restaurant_id=? AND id=?`, a.ActiveRestaurantID, id)
	if err != nil {
		httpx.WriteError(w, 500, "Error deleting POS category")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		httpx.WriteError(w, 404, "POS category not found")
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true})
}

func (s *Server) handleBOPOSTicketVoid(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	ticketID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	// A closed cash day is a signed Z closure; mutating it afterwards would
	// invalidate an accounting document that has already been reported.
	if posWriteCashDayGuard(w, s.requireOpenCashDayForTicket(r.Context(), a.ActiveRestaurantID, ticketID)) {
		return
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || strings.TrimSpace(in.Reason) == "" {
		httpx.WriteError(w, 400, "Void reason is required")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, 500, "Error voiding ticket")
		return
	}
	defer tx.Rollback()
	var activeLines int
	if err = tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM pos_ticket_lines WHERE restaurant_id=? AND ticket_id=? AND status='ACTIVE'`, a.ActiveRestaurantID, ticketID).Scan(&activeLines); err != nil || activeLines > 0 {
		httpx.WriteError(w, 409, "Only empty tickets can be voided")
		return
	}
	res, err := tx.ExecContext(r.Context(), `UPDATE pos_tickets SET status='VOIDED',voided_at=NOW(),closed_by=?,version=version+1 WHERE restaurant_id=? AND id=? AND status='OPEN'`, a.User.ID, a.ActiveRestaurantID, ticketID)
	if err != nil {
		httpx.WriteError(w, 500, "Error voiding ticket")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		httpx.WriteError(w, 409, "Ticket is not open")
		return
	}
	_, _ = tx.ExecContext(r.Context(), `INSERT INTO pos_audit_events (restaurant_id,entity_type,entity_id,action,after_json,actor_user_id) VALUES (?,'ticket',?,'VOID',JSON_OBJECT('reason',?),?)`, a.ActiveRestaurantID, ticketID, strings.TrimSpace(in.Reason), a.User.ID)
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, 500, "Error voiding ticket")
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true})
}
