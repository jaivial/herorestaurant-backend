package api

import (
	"database/sql"
	"encoding/json"
	"math"
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

func (s *Server) handleBOPOSLineMove(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	sourceTicketID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	lineID, _ := strconv.ParseInt(chi.URLParam(r, "lineId"), 10, 64)
	var in struct {
		TargetTicketID int64   `json:"targetTicketId"`
		Quantity       float64 `json:"quantity"`
		IdempotencyKey string  `json:"idempotencyKey"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || sourceTicketID <= 0 || lineID <= 0 || in.TargetTicketID <= 0 || in.TargetTicketID == sourceTicketID || in.Quantity <= 0 || strings.TrimSpace(in.IdempotencyKey) == "" {
		httpx.WriteError(w, 400, "Invalid line move")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, 500, "Error moving line")
		return
	}
	defer tx.Rollback()
	var existingTargetTicket int64
	if duplicateErr := tx.QueryRowContext(r.Context(), `SELECT ticket_id FROM pos_ticket_lines WHERE restaurant_id=? AND idempotency_key=?`, a.ActiveRestaurantID, in.IdempotencyKey).Scan(&existingTargetTicket); duplicateErr == nil {
		tx.Rollback()
		source, _ := s.loadPOSTicket(r.Context(), a.ActiveRestaurantID, sourceTicketID)
		target, _ := s.loadPOSTicket(r.Context(), a.ActiveRestaurantID, existingTargetTicket)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "duplicate": true, "sourceTicket": source, "targetTicket": target})
		return
	}
	ids := []int64{sourceTicketID, in.TargetTicketID}
	if ids[0] > ids[1] {
		ids[0], ids[1] = ids[1], ids[0]
	}
	type ticketState struct {
		visitID  int64
		status   string
		discount int64
	}
	states := map[int64]ticketState{}
	for _, ticketID := range ids {
		var state ticketState
		if err = tx.QueryRowContext(r.Context(), `SELECT visit_id,status,ticket_discount_cents FROM pos_tickets WHERE restaurant_id=? AND id=? FOR UPDATE`, a.ActiveRestaurantID, ticketID).Scan(&state.visitID, &state.status, &state.discount); err != nil || state.status != "OPEN" {
			httpx.WriteError(w, 409, "Both tickets must be open")
			return
		}
		states[ticketID] = state
	}
	if states[sourceTicketID].visitID != states[in.TargetTicketID].visitID {
		httpx.WriteError(w, 409, "Tickets must belong to same visit")
		return
	}
	var productID sql.NullInt64
	var name string
	var sku sql.NullString
	var quantity float64
	var unitPrice int64
	var vat float64
	var discount int64
	var notes, course string
	if err = tx.QueryRowContext(r.Context(), `SELECT pos_product_id,product_name_snapshot,product_sku_snapshot,quantity,unit_price_gross_cents,vat_rate_snapshot,discount_cents,COALESCE(notes,''),COALESCE(course,'') FROM pos_ticket_lines WHERE restaurant_id=? AND ticket_id=? AND id=? AND status='ACTIVE' FOR UPDATE`, a.ActiveRestaurantID, sourceTicketID, lineID).Scan(&productID, &name, &sku, &quantity, &unitPrice, &vat, &discount, &notes, &course); err != nil || in.Quantity > quantity {
		httpx.WriteError(w, 409, "Move quantity exceeds source line")
		return
	}
	movedDiscount := discount
	var newLineID int64
	if in.Quantity == quantity {
		_, err = tx.ExecContext(r.Context(), `UPDATE pos_ticket_lines SET status='VOIDED',void_reason='MOVED',voided_by=?,voided_at=NOW() WHERE restaurant_id=? AND id=?`, a.User.ID, a.ActiveRestaurantID, lineID)
		if err == nil {
			// Move stock tracking records to target ticket
			_, _ = tx.ExecContext(r.Context(), `UPDATE pos_ticket_line_stock SET ticket_id=? WHERE restaurant_id=? AND ticket_line_id=?`, in.TargetTicketID, a.ActiveRestaurantID, lineID)
		}
	} else {
		remaining := quantity - in.Quantity
		sourceDiscount := int64(math.Round(float64(discount) * (remaining / quantity)))
		movedDiscount = discount - sourceDiscount
		_, err = tx.ExecContext(r.Context(), `UPDATE pos_ticket_lines SET quantity=?,discount_cents=?,line_total_gross_cents=ROUND(?*unit_price_gross_cents)-? WHERE restaurant_id=? AND id=?`, remaining, sourceDiscount, remaining, sourceDiscount, a.ActiveRestaurantID, lineID)
		// For partial moves with LIVE stock mode: adjust stock tracking for quantity change
		if err == nil {
			settings, settingsErr := s.loadPOSSettings(r.Context(), a.ActiveRestaurantID)
			if settingsErr == nil && settings.StockMode == "LIVE" && productID.Valid {
				// Restore the moved quantity's stock from the source line
				// The new line will deduct it when created
				idempotencyKey := "pos-line-move:" + in.IdempotencyKey + ":source"
				_ = s.adjustStockForQuantityChange(r.Context(), tx, a.ActiveRestaurantID, a.User.ID, sourceTicketID, lineID, productID.Int64, quantity, remaining, idempotencyKey)
			}
		}
	}
	if err == nil {
		res, insertErr := tx.ExecContext(r.Context(), `INSERT INTO pos_ticket_lines (restaurant_id,ticket_id,pos_product_id,product_name_snapshot,product_sku_snapshot,quantity,unit_price_gross_cents,vat_rate_snapshot,discount_cents,line_total_gross_cents,notes,course,idempotency_key,created_by) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, a.ActiveRestaurantID, in.TargetTicketID, productID, name, sku, in.Quantity, unitPrice, vat, movedDiscount, int64(math.Round(float64(unitPrice)*in.Quantity))-movedDiscount, stockNullableString(notes), stockNullableString(course), in.IdempotencyKey, a.User.ID)
		err = insertErr
		if insertErr == nil {
			newLineID, _ = res.LastInsertId()
			// For partial moves: deduct stock for the new line
			if in.Quantity != quantity {
				settings, settingsErr := s.loadPOSSettings(r.Context(), a.ActiveRestaurantID)
				if settingsErr == nil && settings.StockMode == "LIVE" && productID.Valid {
					_, _ = s.deductStockForLine(r.Context(), tx, a.ActiveRestaurantID, a.User.ID, in.TargetTicketID, newLineID, productID.Int64, in.Quantity, "pos-line-move:"+in.IdempotencyKey+":target")
				}
			} else {
				// Full move: update stock tracking to reference new line
				_, _ = tx.ExecContext(r.Context(), `UPDATE pos_ticket_line_stock SET ticket_line_id=? WHERE restaurant_id=? AND ticket_line_id=?`, newLineID, a.ActiveRestaurantID, lineID)
			}
		}
	}
	if err != nil {
		httpx.WriteError(w, 400, "Line could not be moved")
		return
	}
	_ = newLineID
	if _, err = s.recalculatePOSTicket(r.Context(), tx, a.ActiveRestaurantID, sourceTicketID, states[sourceTicketID].discount); err != nil {
		httpx.WriteError(w, 500, "Error calculating source ticket")
		return
	}
	if _, err = s.recalculatePOSTicket(r.Context(), tx, a.ActiveRestaurantID, in.TargetTicketID, states[in.TargetTicketID].discount); err != nil {
		httpx.WriteError(w, 500, "Error calculating target ticket")
		return
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, 500, "Error moving line")
		return
	}
	source, _ := s.loadPOSTicket(r.Context(), a.ActiveRestaurantID, sourceTicketID)
	target, _ := s.loadPOSTicket(r.Context(), a.ActiveRestaurantID, in.TargetTicketID)
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "sourceTicket": source, "targetTicket": target})
}

func (s *Server) handleBOPOSTicketVoid(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	ticketID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
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
