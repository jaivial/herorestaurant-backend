package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"preactvillacarmen/internal/httpx"
)

// Handlers backing the POS control-rail features that needed new schema:
// Aparcar (02), Juntar mesas (05), Cliente (08), Cajón (10), Recargo (12),
// Invita (13), Empleado (14), Tags (16) and Propina (21).

// posDecodeBody reads a JSON body with the shared POS size limit.
func posDecodeBody(w http.ResponseWriter, r *http.Request, target any) bool {
	return json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(target) == nil
}

var spanishTaxIDPattern = regexp.MustCompile(`^[XYZ0-9ABCDEFGHJNPQRSUVW][0-9]{7}[A-Z0-9]$`)

func normalizeSpanishCustomerTaxID(value string) (string, bool) {
	value = strings.ToUpper(strings.Join(strings.Fields(value), ""))
	if value == "" {
		return "", true
	}
	if !spanishTaxIDPattern.MatchString(value) {
		return value, false
	}
	letters := "TRWAGMYFPDXBNJZSQVHLCKE"
	if value[0] >= '0' && value[0] <= '9' || strings.ContainsRune("XYZ", rune(value[0])) {
		number := value[:8]
		number = strings.NewReplacer("X", "0", "Y", "1", "Z", "2").Replace(number)
		n, err := strconv.Atoi(number)
		return value, err == nil && value[8] == letters[n%23]
	}
	digits := value[1:8]
	sumEven, sumOdd := 0, 0
	for i, char := range digits {
		digit := int(char - '0')
		if i%2 == 0 {
			doubled := digit * 2
			sumOdd += doubled/10 + doubled%10
		} else {
			sumEven += digit
		}
	}
	control := (10 - (sumEven+sumOdd)%10) % 10
	controlChar := value[8]
	if strings.ContainsRune("PQRSNW", rune(value[0])) {
		return value, controlChar == "JABCDEFGHI"[control]
	}
	if strings.ContainsRune("ABEH", rune(value[0])) {
		return value, controlChar == byte('0'+control)
	}
	return value, controlChar == byte('0'+control) || controlChar == "JABCDEFGHI"[control]
}

func (s *Server) loadPOSVisit(ctx context.Context, restaurantID int, visitID int64) (map[string]any, error) {
	visits, err := s.loadPOSVisits(ctx, restaurantID, "")
	if err != nil {
		return nil, err
	}
	for _, visit := range visits {
		if visit["id"] == visitID {
			return visit, nil
		}
	}
	return nil, sql.ErrNoRows
}

// ---------------------------------------------------------------------------
// Aparcar (02): park / unpark an open visit.
// ---------------------------------------------------------------------------

func (s *Server) handleBOPOSVisitPark(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	visitID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var in struct {
		Parked bool   `json:"parked"`
		Note   string `json:"note"`
	}
	if visitID <= 0 || !posDecodeBody(w, r, &in) {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid park request")
		return
	}
	note := strings.TrimSpace(in.Note)
	if len(note) > 300 {
		httpx.WriteError(w, http.StatusBadRequest, "Park note too long")
		return
	}
	var status string
	if err := s.db.QueryRowContext(r.Context(), `SELECT status FROM pos_visits WHERE restaurant_id=? AND id=?`, a.ActiveRestaurantID, visitID).Scan(&status); err != nil {
		httpx.WriteError(w, http.StatusNotFound, "Visit not found")
		return
	}
	if status != "OPEN" {
		httpx.WriteError(w, http.StatusConflict, "Only open visits can be parked")
		return
	}
	var err error
	if in.Parked {
		_, err = s.db.ExecContext(r.Context(), `UPDATE pos_visits SET parked_at=NOW(),parked_note=?,version=version+1 WHERE restaurant_id=? AND id=? AND status='OPEN'`, stockNullableString(note), a.ActiveRestaurantID, visitID)
	} else {
		_, err = s.db.ExecContext(r.Context(), `UPDATE pos_visits SET parked_at=NULL,parked_note=NULL,version=version+1 WHERE restaurant_id=? AND id=? AND status='OPEN'`, a.ActiveRestaurantID, visitID)
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error parking visit")
		return
	}
	action := "UNPARK"
	if in.Parked {
		action = "PARK"
	}
	_, _ = s.db.ExecContext(r.Context(), `INSERT INTO pos_audit_events (restaurant_id,entity_type,entity_id,action,after_json,actor_user_id) VALUES (?,'visit',?,?,JSON_OBJECT('parked',?,'note',?),?)`, a.ActiveRestaurantID, visitID, action, in.Parked, note, a.User.ID)
	s.broadcastBOTablesEvent(a.ActiveRestaurantID, "pos_visit_updated", map[string]any{"visitId": visitID, "parked": in.Parked})
	visit, loadErr := s.loadPOSVisit(r.Context(), a.ActiveRestaurantID, visitID)
	if loadErr != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading parked visit")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "parked": in.Parked, "visit": visit})
}

// ---------------------------------------------------------------------------
// Juntar mesas (05): merge source visits into a target visit.
// ---------------------------------------------------------------------------

func (s *Server) handleBOPOSVisitMerge(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	targetID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var in struct {
		SourceVisitIDs []int64 `json:"sourceVisitIds"`
		IdempotencyKey string  `json:"idempotencyKey"`
	}
	if targetID <= 0 || !posDecodeBody(w, r, &in) || len(in.SourceVisitIDs) == 0 || strings.TrimSpace(in.IdempotencyKey) == "" {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid merge request")
		return
	}
	uniqueSources := make([]int64, 0, len(in.SourceVisitIDs))
	seenSources := make(map[int64]struct{}, len(in.SourceVisitIDs))
	for _, sourceID := range in.SourceVisitIDs {
		if _, seen := seenSources[sourceID]; !seen {
			seenSources[sourceID] = struct{}{}
			uniqueSources = append(uniqueSources, sourceID)
		}
	}
	in.SourceVisitIDs = uniqueSources
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error merging visits")
		return
	}
	defer tx.Rollback()
	mergeKey := strings.TrimSpace(in.IdempotencyKey)
	sourceJSON, _ := json.Marshal(in.SourceVisitIDs)
	if _, err = tx.ExecContext(r.Context(), `INSERT INTO pos_visit_merges (restaurant_id,target_visit_id,idempotency_key,source_visit_ids_json,created_by) VALUES (?,?,?,?,?)`, a.ActiveRestaurantID, targetID, mergeKey, string(sourceJSON), a.User.ID); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			var existingTarget int64
			if lookupErr := tx.QueryRowContext(r.Context(), `SELECT target_visit_id FROM pos_visit_merges WHERE restaurant_id=? AND idempotency_key=?`, a.ActiveRestaurantID, mergeKey).Scan(&existingTarget); lookupErr != nil || existingTarget != targetID {
				httpx.WriteError(w, http.StatusConflict, "Merge key belongs to another visit")
				return
			}
			tx.Rollback()
			visit, visitErr := s.loadPOSVisit(r.Context(), a.ActiveRestaurantID, targetID)
			var existingTicketID int64
			ticketIDErr := s.db.QueryRowContext(r.Context(), `SELECT id FROM pos_tickets WHERE restaurant_id=? AND visit_id=? AND status='OPEN' ORDER BY id LIMIT 1`, a.ActiveRestaurantID, targetID).Scan(&existingTicketID)
			ticket, ticketErr := s.loadPOSTicket(r.Context(), a.ActiveRestaurantID, existingTicketID)
			if visitErr != nil || ticketIDErr != nil || ticketErr != nil {
				httpx.WriteError(w, http.StatusInternalServerError, "Error loading merged visit")
				return
			}
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "duplicate": true, "visit": visit, "ticket": ticket, "tickets": []map[string]any{ticket}})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error recording merge intent")
		return
	}

	var targetStatus, targetChannel string
	var targetCovers int
	if err = tx.QueryRowContext(r.Context(), `SELECT status,channel,covers FROM pos_visits WHERE restaurant_id=? AND id=? FOR UPDATE`, a.ActiveRestaurantID, targetID).Scan(&targetStatus, &targetChannel, &targetCovers); err != nil || targetStatus != "OPEN" || targetChannel != "DINE_IN" {
		httpx.WriteError(w, http.StatusConflict, "Target visit is not open")
		return
	}
	var targetTicketID int64
	if err = tx.QueryRowContext(r.Context(), `SELECT id FROM pos_tickets WHERE restaurant_id=? AND visit_id=? AND status='OPEN' ORDER BY id LIMIT 1`, a.ActiveRestaurantID, targetID).Scan(&targetTicketID); err != nil {
		httpx.WriteError(w, http.StatusConflict, "Target visit has no open ticket")
		return
	}
	var mergedTicketDiscount, mergedSurcharge int64
	if err = tx.QueryRowContext(r.Context(), `SELECT ticket_discount_cents,surcharge_cents FROM pos_tickets WHERE restaurant_id=? AND id=? FOR UPDATE`, a.ActiveRestaurantID, targetTicketID).Scan(&mergedTicketDiscount, &mergedSurcharge); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading target adjustments")
		return
	}

	movedLines := 0
	mergedCovers := targetCovers
	for _, sourceID := range in.SourceVisitIDs {
		if sourceID == targetID {
			httpx.WriteError(w, http.StatusBadRequest, "Cannot merge a visit into itself")
			return
		}
		var sourceStatus, sourceChannel string
		var sourceCovers int
		var parkedAt sql.NullTime
		if err = tx.QueryRowContext(r.Context(), `SELECT status,channel,covers,parked_at FROM pos_visits WHERE restaurant_id=? AND id=? FOR UPDATE`, a.ActiveRestaurantID, sourceID).Scan(&sourceStatus, &sourceChannel, &sourceCovers, &parkedAt); err != nil || sourceStatus != "OPEN" || sourceChannel != "DINE_IN" || parkedAt.Valid {
			httpx.WriteError(w, http.StatusConflict, "Source visit is not open")
			return
		}
		// A source that already took money must not be folded into another bill.
		var settled int
		if err = tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM pos_tickets WHERE restaurant_id=? AND visit_id=? AND status NOT IN ('OPEN','VOIDED')`, a.ActiveRestaurantID, sourceID).Scan(&settled); err != nil || settled > 0 {
			httpx.WriteError(w, http.StatusConflict, "Paid visit cannot be merged")
			return
		}
		var sourceDiscount, sourceSurcharge int64
		if err = tx.QueryRowContext(r.Context(), `SELECT COALESCE(SUM(ticket_discount_cents),0),COALESCE(SUM(surcharge_cents),0) FROM pos_tickets WHERE restaurant_id=? AND visit_id=? AND status='OPEN'`, a.ActiveRestaurantID, sourceID).Scan(&sourceDiscount, &sourceSurcharge); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error loading source adjustments")
			return
		}
		mergedTicketDiscount += sourceDiscount
		mergedSurcharge += sourceSurcharge
		res, execErr := tx.ExecContext(r.Context(), `UPDATE pos_ticket_lines l JOIN pos_tickets t ON t.restaurant_id=l.restaurant_id AND t.id=l.ticket_id SET l.ticket_id=? WHERE l.restaurant_id=? AND t.visit_id=? AND t.status='OPEN' AND l.status='ACTIVE'`, targetTicketID, a.ActiveRestaurantID, sourceID)
		if execErr != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error moving merged lines")
			return
		}
		affected, _ := res.RowsAffected()
		// Update pos_ticket_line_stock to reference the new target ticket
		_, _ = tx.ExecContext(r.Context(), `UPDATE pos_ticket_line_stock s JOIN pos_tickets t ON t.restaurant_id=s.restaurant_id AND t.id=s.ticket_id SET s.ticket_id=? WHERE s.restaurant_id=? AND t.visit_id=?`, targetTicketID, a.ActiveRestaurantID, sourceID)
		movedLines += int(affected)
		if _, err = tx.ExecContext(r.Context(), `UPDATE pos_ticket_adjustments a JOIN pos_tickets t ON t.restaurant_id=a.restaurant_id AND t.id=a.ticket_id SET a.ticket_id=? WHERE a.restaurant_id=? AND t.visit_id=? AND t.status='OPEN'`, targetTicketID, a.ActiveRestaurantID, sourceID); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error moving source adjustments")
			return
		}
		if _, err = tx.ExecContext(r.Context(), `INSERT IGNORE INTO pos_ticket_tags (restaurant_id,ticket_id,tag_id,created_by) SELECT restaurant_id,?,tag_id,created_by FROM pos_ticket_tags WHERE restaurant_id=? AND ticket_id IN (SELECT id FROM pos_tickets WHERE restaurant_id=? AND visit_id=? AND status='OPEN')`, targetTicketID, a.ActiveRestaurantID, a.ActiveRestaurantID, sourceID); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error moving source tags")
			return
		}
		if _, err = tx.ExecContext(r.Context(), `UPDATE pos_tickets SET status='VOIDED',voided_at=NOW(),closed_by=? WHERE restaurant_id=? AND visit_id=? AND status='OPEN'`, a.User.ID, a.ActiveRestaurantID, sourceID); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error closing merged tickets")
			return
		}
		if _, err = tx.ExecContext(r.Context(), `UPDATE pos_visits SET status='MERGED',merged_into_visit_id=?,closed_by=?,closed_at=NOW(),version=version+1 WHERE restaurant_id=? AND id=?`, targetID, a.User.ID, a.ActiveRestaurantID, sourceID); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error merging visit")
			return
		}
		mergedCovers += sourceCovers
		_, _ = tx.ExecContext(r.Context(), `INSERT INTO pos_audit_events (restaurant_id,entity_type,entity_id,action,after_json,actor_user_id) VALUES (?,'visit',?,'MERGE',JSON_OBJECT('mergedInto',?,'movedLines',?),?)`, a.ActiveRestaurantID, sourceID, targetID, affected, a.User.ID)
	}

	if _, err = tx.ExecContext(r.Context(), `UPDATE pos_visits SET covers=?,version=version+1 WHERE restaurant_id=? AND id=?`, mergedCovers, a.ActiveRestaurantID, targetID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error updating merged covers")
		return
	}
	if _, err = tx.ExecContext(r.Context(), `UPDATE pos_tickets SET surcharge_cents=? WHERE restaurant_id=? AND id=?`, mergedSurcharge, a.ActiveRestaurantID, targetTicketID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error preserving merged surcharge")
		return
	}
	if _, err = s.recalculatePOSTicket(r.Context(), tx, a.ActiveRestaurantID, targetTicketID, mergedTicketDiscount); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error merging visits")
		return
	}
	s.broadcastBOTablesEvent(a.ActiveRestaurantID, "pos_visits_merged", map[string]any{"visitId": targetID, "sourceVisitIds": in.SourceVisitIDs})
	ticket, _ := s.loadPOSTicket(r.Context(), a.ActiveRestaurantID, targetTicketID)
	visit, _ := s.loadPOSVisit(r.Context(), a.ActiveRestaurantID, targetID)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "visit": visit, "tickets": []map[string]any{ticket}, "ticket": ticket, "covers": mergedCovers, "movedLines": movedLines})
}

// ---------------------------------------------------------------------------
// Cliente (08): attach customer billing data to a visit.
// ---------------------------------------------------------------------------

func (s *Server) handleBOPOSVisitCustomer(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	visitID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var in struct {
		CustomerName    string `json:"customerName"`
		CustomerTaxID   string `json:"customerTaxId"`
		CustomerAddress string `json:"customerAddress"`
	}
	if visitID <= 0 || !posDecodeBody(w, r, &in) {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid customer data")
		return
	}
	name := strings.TrimSpace(in.CustomerName)
	taxID, validTaxID := normalizeSpanishCustomerTaxID(in.CustomerTaxID)
	address := strings.TrimSpace(in.CustomerAddress)
	if name == "" || !validTaxID || len(name) > 180 || len(taxID) > 40 || len(address) > 300 {
		httpx.WriteError(w, http.StatusBadRequest, "Customer data too long")
		return
	}
	res, err := s.db.ExecContext(r.Context(), `UPDATE pos_visits SET customer_name=?,customer_tax_id=?,customer_address=?,version=version+1 WHERE restaurant_id=? AND id=? AND status='OPEN'`, stockNullableString(name), stockNullableString(taxID), stockNullableString(address), a.ActiveRestaurantID, visitID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error saving customer")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		httpx.WriteError(w, http.StatusConflict, "Open visit not found")
		return
	}
	_, _ = s.db.ExecContext(r.Context(), `INSERT INTO pos_audit_events (restaurant_id,entity_type,entity_id,action,after_json,actor_user_id) VALUES (?,'visit',?,'CUSTOMER',JSON_OBJECT('customerName',?,'customerTaxId',?),?)`, a.ActiveRestaurantID, visitID, name, taxID, a.User.ID)
	visit, loadErr := s.loadPOSVisit(r.Context(), a.ActiveRestaurantID, visitID)
	if loadErr != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading customer visit")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "visit": visit, "customerName": name, "customerTaxId": taxID, "customerAddress": address})
}

// ---------------------------------------------------------------------------
// Cajón (10): record a no-sale drawer opening against the current shift.
// ---------------------------------------------------------------------------

func (s *Server) handleBOPOSDrawerOpen(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	var in struct {
		Reason         string `json:"reason"`
		Note           string `json:"note"`
		IdempotencyKey string `json:"idempotencyKey"`
	}
	if !posDecodeBody(w, r, &in) || strings.TrimSpace(in.IdempotencyKey) == "" {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid drawer request")
		return
	}
	in.IdempotencyKey = strings.TrimSpace(in.IdempotencyKey)
	in.Note = strings.TrimSpace(in.Note)
	if len(in.IdempotencyKey) > 120 || len(in.Note) > 300 {
		httpx.WriteError(w, http.StatusBadRequest, "Drawer request too long")
		return
	}
	var existingEventID, existingShiftID int64
	var existingReason string
	if err := s.db.QueryRowContext(r.Context(), `SELECT id,shift_id,reason FROM pos_drawer_events WHERE restaurant_id=? AND idempotency_key=?`, a.ActiveRestaurantID, in.IdempotencyKey).Scan(&existingEventID, &existingShiftID, &existingReason); err == nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "duplicate": true, "drawerEventId": existingEventID, "shiftId": existingShiftID, "reason": existingReason})
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		httpx.WriteError(w, http.StatusInternalServerError, "Error checking drawer event")
		return
	}
	reason := strings.ToUpper(strings.TrimSpace(in.Reason))
	if reason == "" {
		reason = "NO_SALE"
	}
	if !validPOSMode(reason, "NO_SALE", "CHANGE", "COUNT", "OTHER") {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid drawer reason")
		return
	}
	var shiftID int64
	if err := s.db.QueryRowContext(r.Context(), `SELECT id FROM pos_shifts WHERE restaurant_id=? AND status='OPEN' ORDER BY opened_at DESC LIMIT 1`, a.ActiveRestaurantID).Scan(&shiftID); err != nil {
		httpx.WriteJSON(w, http.StatusConflict, map[string]any{"success": false, "message": "Open a cash shift before using the drawer", "code": "POS_SHIFT_REQUIRED"})
		return
	}
	res, err := s.db.ExecContext(r.Context(), `INSERT INTO pos_drawer_events (restaurant_id,shift_id,reason,note,idempotency_key,opened_by) VALUES (?,?,?,?,?,?)`, a.ActiveRestaurantID, shiftID, reason, stockNullableString(in.Note), in.IdempotencyKey, a.User.ID)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			var existing int64
			_ = s.db.QueryRowContext(r.Context(), `SELECT id FROM pos_drawer_events WHERE restaurant_id=? AND idempotency_key=?`, a.ActiveRestaurantID, strings.TrimSpace(in.IdempotencyKey)).Scan(&existing)
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "duplicate": true, "drawerEventId": existing})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error recording drawer event")
		return
	}
	eventID, _ := res.LastInsertId()
	_, _ = s.db.ExecContext(r.Context(), `INSERT INTO pos_audit_events (restaurant_id,entity_type,entity_id,action,after_json,actor_user_id) VALUES (?,'drawer',?,'OPEN',JSON_OBJECT('shiftId',?,'reason',?),?)`, a.ActiveRestaurantID, eventID, shiftID, reason, a.User.ID)
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"success": true, "drawerEventId": eventID, "shiftId": shiftID, "reason": reason})
}

// ---------------------------------------------------------------------------
// Recargo (12): signed ticket adjustment. Discount and surcharge coexist.
// ---------------------------------------------------------------------------

func (s *Server) handleBOPOSTicketAdjustment(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	ticketID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var in struct {
		Type            string  `json:"type"`
		Mode            string  `json:"mode"`
		AmountCents     int64   `json:"amountCents"`
		Percent         float64 `json:"percent"`
		Reason          string  `json:"reason"`
		IdempotencyKey  string  `json:"idempotencyKey"`
		ExpectedVersion int     `json:"expectedVersion"`
	}
	if ticketID <= 0 || !posDecodeBody(w, r, &in) || strings.TrimSpace(in.IdempotencyKey) == "" {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid adjustment")
		return
	}
	kind := strings.ToUpper(strings.TrimSpace(in.Type))
	mode := strings.ToUpper(strings.TrimSpace(in.Mode))
	if mode == "" {
		mode = "AMOUNT"
	}
	reason := strings.TrimSpace(in.Reason)
	if !validPOSMode(kind, "DISCOUNT", "SURCHARGE") || !validPOSMode(mode, "AMOUNT", "PERCENT") || reason == "" || len(reason) > 500 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid adjustment")
		return
	}
	if mode == "PERCENT" && (in.Percent <= 0 || in.Percent > 100) {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid adjustment percent")
		return
	}
	if mode == "AMOUNT" && (in.AmountCents <= 0 || in.AmountCents > 100000000) {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid adjustment amount")
		return
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error applying adjustment")
		return
	}
	defer tx.Rollback()
	var existingAdjustment, existingTicketID int64
	if err = tx.QueryRowContext(r.Context(), `SELECT id,ticket_id FROM pos_ticket_adjustments WHERE restaurant_id=? AND idempotency_key=?`, a.ActiveRestaurantID, strings.TrimSpace(in.IdempotencyKey)).Scan(&existingAdjustment, &existingTicketID); err == nil {
		if existingTicketID != ticketID {
			httpx.WriteJSON(w, http.StatusConflict, map[string]any{"success": false, "message": "Idempotency key belongs to another ticket", "code": "IDEMPOTENCY_CONFLICT"})
			return
		}
		tx.Rollback()
		ticket, _ := s.loadPOSTicket(r.Context(), a.ActiveRestaurantID, ticketID)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "duplicate": true, "ticket": ticket})
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		httpx.WriteError(w, http.StatusInternalServerError, "Error checking adjustment")
		return
	}
	var status string
	var version int
	var subtotal, lineDiscounts, currentDiscount, currentSurcharge int64
	if err = tx.QueryRowContext(r.Context(), `SELECT status,version,subtotal_gross_cents,discount_cents,ticket_discount_cents,surcharge_cents FROM pos_tickets WHERE restaurant_id=? AND id=? FOR UPDATE`, a.ActiveRestaurantID, ticketID).Scan(&status, &version, &subtotal, &lineDiscounts, &currentDiscount, &currentSurcharge); err != nil || status != "OPEN" {
		httpx.WriteError(w, http.StatusConflict, "Ticket is not open")
		return
	}
	if in.ExpectedVersion > 0 && in.ExpectedVersion != version {
		httpx.WriteJSON(w, http.StatusConflict, map[string]any{"success": false, "message": "Ticket changed", "code": "STALE_TICKET"})
		return
	}
	// Percent always applies to the line base, never to an already adjusted total,
	// so applying 10% twice cannot compound unpredictably.
	base := subtotal - (lineDiscounts - currentDiscount)
	amount := in.AmountCents
	if mode == "PERCENT" {
		amount = int64(math.Round(float64(base) * in.Percent / 100))
	}
	if amount <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Adjustment resolves to zero")
		return
	}

	nextDiscount, nextSurcharge := currentDiscount, currentSurcharge
	signed := amount
	if kind == "DISCOUNT" {
		nextDiscount += amount
		signed = -amount
	} else {
		nextSurcharge += amount
	}
	if _, err = tx.ExecContext(r.Context(), `UPDATE pos_tickets SET surcharge_cents=? WHERE restaurant_id=? AND id=?`, nextSurcharge, a.ActiveRestaurantID, ticketID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error applying adjustment")
		return
	}
	if _, err = s.recalculatePOSTicket(r.Context(), tx, a.ActiveRestaurantID, ticketID, nextDiscount); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	var percentValue any
	if mode == "PERCENT" {
		percentValue = in.Percent
	}
	if _, err = tx.ExecContext(r.Context(), `INSERT INTO pos_ticket_adjustments (restaurant_id,ticket_id,type,mode,percent_value,amount_cents,reason,idempotency_key,created_by) VALUES (?,?,?,?,?,?,?,?,?)`, a.ActiveRestaurantID, ticketID, kind, mode, percentValue, signed, reason, strings.TrimSpace(in.IdempotencyKey), a.User.ID); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			tx.Rollback()
			ticket, _ := s.loadPOSTicket(r.Context(), a.ActiveRestaurantID, ticketID)
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "duplicate": true, "ticket": ticket})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error recording adjustment")
		return
	}
	_, _ = tx.ExecContext(r.Context(), `INSERT INTO pos_audit_events (restaurant_id,entity_type,entity_id,action,after_json,actor_user_id) VALUES (?,'ticket',?,?,JSON_OBJECT('amountCents',?,'mode',?,'reason',?),?)`, a.ActiveRestaurantID, ticketID, kind, signed, mode, reason, a.User.ID)
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error applying adjustment")
		return
	}
	ticket, _ := s.loadPOSTicket(r.Context(), a.ActiveRestaurantID, ticketID)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "ticket": ticket})
}

// ---------------------------------------------------------------------------
// Invita (13): comp a line. Revenue drops to zero, the line and its stock stay.
// ---------------------------------------------------------------------------

func (s *Server) handleBOPOSLineComp(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	ticketID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	lineID, _ := strconv.ParseInt(chi.URLParam(r, "lineId"), 10, 64)
	var in struct {
		Comped          bool   `json:"comped"`
		Reason          string `json:"reason"`
		ExpectedVersion int    `json:"expectedVersion"`
	}
	if ticketID <= 0 || lineID <= 0 || !posDecodeBody(w, r, &in) {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid comp request")
		return
	}
	reason := strings.TrimSpace(in.Reason)
	if in.Comped && reason == "" {
		httpx.WriteError(w, http.StatusBadRequest, "Comp reason is required")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error comping line")
		return
	}
	defer tx.Rollback()
	var status string
	var version int
	if err = tx.QueryRowContext(r.Context(), `SELECT status,version FROM pos_tickets WHERE restaurant_id=? AND id=? FOR UPDATE`, a.ActiveRestaurantID, ticketID).Scan(&status, &version); err != nil || status != "OPEN" {
		httpx.WriteError(w, http.StatusConflict, "Ticket is not open")
		return
	}
	if in.ExpectedVersion > 0 && in.ExpectedVersion != version {
		httpx.WriteJSON(w, http.StatusConflict, map[string]any{"success": false, "message": "Ticket changed", "code": "STALE_TICKET"})
		return
	}
	var quantity float64
	var unitPrice int64
	var lineStatus string
	if err = tx.QueryRowContext(r.Context(), `SELECT quantity,unit_price_gross_cents,status FROM pos_ticket_lines WHERE restaurant_id=? AND id=? AND ticket_id=? FOR UPDATE`, a.ActiveRestaurantID, lineID, ticketID).Scan(&quantity, &unitPrice, &lineStatus); err != nil || lineStatus != "ACTIVE" {
		httpx.WriteError(w, http.StatusConflict, "Line is not active")
		return
	}
	// Comping discounts the whole line: the dish is still served and still
	// deducts stock, but the customer is charged nothing for it.
	if in.Comped {
		gross := int64(math.Round(quantity * float64(unitPrice)))
		_, err = tx.ExecContext(r.Context(), `UPDATE pos_ticket_lines SET discount_cents=?,comped_at=NOW(),comp_reason=?,comped_by=? WHERE restaurant_id=? AND id=?`, gross, reason, a.User.ID, a.ActiveRestaurantID, lineID)
	} else {
		_, err = tx.ExecContext(r.Context(), `UPDATE pos_ticket_lines SET discount_cents=0,comped_at=NULL,comp_reason=NULL,comped_by=NULL WHERE restaurant_id=? AND id=?`, a.ActiveRestaurantID, lineID)
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error comping line")
		return
	}
	var discount int64
	if err = tx.QueryRowContext(r.Context(), `SELECT ticket_discount_cents FROM pos_tickets WHERE restaurant_id=? AND id=?`, a.ActiveRestaurantID, ticketID).Scan(&discount); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading ticket")
		return
	}
	if _, err = s.recalculatePOSTicket(r.Context(), tx, a.ActiveRestaurantID, ticketID, discount); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	_, _ = tx.ExecContext(r.Context(), `INSERT INTO pos_audit_events (restaurant_id,entity_type,entity_id,action,after_json,actor_user_id) VALUES (?,'ticket_line',?,'COMP',JSON_OBJECT('comped',?,'reason',?),?)`, a.ActiveRestaurantID, lineID, in.Comped, reason, a.User.ID)
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error comping line")
		return
	}
	ticket, _ := s.loadPOSTicket(r.Context(), a.ActiveRestaurantID, ticketID)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "ticket": ticket})
}

// ---------------------------------------------------------------------------
// Empleado (14): attribute a ticket to a restaurant member.
// ---------------------------------------------------------------------------

func (s *Server) handleBOPOSTicketOperator(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	ticketID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var in struct {
		OperatorMemberID int64 `json:"operatorMemberId"`
		ExpectedVersion  int   `json:"expectedVersion"`
	}
	if ticketID <= 0 || !posDecodeBody(w, r, &in) {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid operator")
		return
	}
	var operator any
	if in.OperatorMemberID > 0 {
		var exists int
		if err := s.db.QueryRowContext(r.Context(), `SELECT 1 FROM restaurant_members WHERE restaurant_id=? AND id=? AND is_active=1`, a.ActiveRestaurantID, in.OperatorMemberID).Scan(&exists); err != nil {
			httpx.WriteError(w, http.StatusNotFound, "Member not found")
			return
		}
		operator = in.OperatorMemberID
	}
	query := `UPDATE pos_tickets SET operator_member_id=?,version=version+1 WHERE restaurant_id=? AND id=? AND status='OPEN'`
	args := []any{operator, a.ActiveRestaurantID, ticketID}
	if in.ExpectedVersion > 0 {
		query += ` AND version=?`
		args = append(args, in.ExpectedVersion)
	}
	res, err := s.db.ExecContext(r.Context(), query, args...)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error setting operator")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		httpx.WriteError(w, http.StatusConflict, "Open ticket not found")
		return
	}
	_, _ = s.db.ExecContext(r.Context(), `INSERT INTO pos_audit_events (restaurant_id,entity_type,entity_id,action,after_json,actor_user_id) VALUES (?,'ticket',?,'OPERATOR',JSON_OBJECT('operatorMemberId',?),?)`, a.ActiveRestaurantID, ticketID, operator, a.User.ID)
	ticket, loadErr := s.loadPOSTicket(r.Context(), a.ActiveRestaurantID, ticketID)
	if loadErr != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading operator ticket")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "operatorMemberId": operator, "ticket": ticket})
}

// ---------------------------------------------------------------------------
// Tags (16): tenant catalogue plus ticket/line attachment.
// ---------------------------------------------------------------------------

func (s *Server) handleBOPOSTagsList(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,name,COALESCE(color,''),scope,sort_order,is_active FROM pos_tags WHERE restaurant_id=? ORDER BY sort_order,name`, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading tags")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id int64
		var name, color, scope string
		var sortOrder, active int
		if err = rows.Scan(&id, &name, &color, &scope, &sortOrder, &active); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error reading tags")
			return
		}
		items = append(items, map[string]any{"id": id, "name": name, "color": color, "scope": scope, "sortOrder": sortOrder, "isActive": active != 0})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "items": items})
}

func (s *Server) handleBOPOSTagCreate(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	var in struct {
		Name      string `json:"name"`
		Color     string `json:"color"`
		Scope     string `json:"scope"`
		SortOrder int    `json:"sortOrder"`
	}
	if !posDecodeBody(w, r, &in) || strings.TrimSpace(in.Name) == "" {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid tag")
		return
	}
	scope := strings.ToUpper(strings.TrimSpace(in.Scope))
	if scope == "" {
		scope = "BOTH"
	}
	if !validPOSMode(scope, "LINE", "TICKET", "BOTH") {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid tag scope")
		return
	}
	res, err := s.db.ExecContext(r.Context(), `INSERT INTO pos_tags (restaurant_id,name,color,scope,sort_order) VALUES (?,?,?,?,?)`, a.ActiveRestaurantID, strings.TrimSpace(in.Name), stockNullableString(strings.TrimSpace(in.Color)), scope, in.SortOrder)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			httpx.WriteError(w, http.StatusConflict, "Tag already exists")
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error creating tag")
		return
	}
	id, _ := res.LastInsertId()
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"success": true, "id": id})
}

func (s *Server) handleBOPOSTicketTagAttach(w http.ResponseWriter, r *http.Request) {
	s.attachPOSTag(w, r, "ticket")
}

func (s *Server) handleBOPOSLineTagAttach(w http.ResponseWriter, r *http.Request) {
	s.attachPOSTag(w, r, "line")
}

// attachPOSTag attaches or detaches a tag on a ticket or a ticket line.
func (s *Server) attachPOSTag(w http.ResponseWriter, r *http.Request, target string) {
	a, _ := boAuthFromContext(r.Context())
	ticketID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	entityID := ticketID
	if target == "line" {
		entityID, _ = strconv.ParseInt(chi.URLParam(r, "lineId"), 10, 64)
	}
	var in struct {
		TagID  int64 `json:"tagId"`
		Attach *bool `json:"attach"`
	}
	if ticketID <= 0 || entityID <= 0 || !posDecodeBody(w, r, &in) || in.TagID <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid tag request")
		return
	}
	attach := in.Attach == nil || *in.Attach
	var status string
	if err := s.db.QueryRowContext(r.Context(), `SELECT status FROM pos_tickets WHERE restaurant_id=? AND id=?`, a.ActiveRestaurantID, ticketID).Scan(&status); err != nil || status != "OPEN" {
		httpx.WriteError(w, http.StatusConflict, "Ticket is not open")
		return
	}
	if target == "line" {
		var owned int
		if err := s.db.QueryRowContext(r.Context(), `SELECT 1 FROM pos_ticket_lines WHERE restaurant_id=? AND ticket_id=? AND id=? AND status='ACTIVE'`, a.ActiveRestaurantID, ticketID, entityID).Scan(&owned); err != nil {
			httpx.WriteError(w, http.StatusNotFound, "Active ticket line not found")
			return
		}
	}
	if attach {
		var validTag int
		tagQuery := `SELECT 1 FROM pos_tags WHERE restaurant_id=? AND id=? AND is_active=1 AND scope IN ('TICKET','BOTH')`
		if target == "line" {
			tagQuery = `SELECT 1 FROM pos_tags WHERE restaurant_id=? AND id=? AND is_active=1 AND scope IN ('LINE','BOTH')`
		}
		if err := s.db.QueryRowContext(r.Context(), tagQuery, a.ActiveRestaurantID, in.TagID).Scan(&validTag); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Tag is not valid for this target")
			return
		}
	}
	var err error
	switch {
	case target == "line" && attach:
		_, err = s.db.ExecContext(r.Context(), `INSERT IGNORE INTO pos_ticket_line_tags (restaurant_id,ticket_line_id,tag_id,created_by) VALUES (?,?,?,?)`, a.ActiveRestaurantID, entityID, in.TagID, a.User.ID)
	case target == "line":
		_, err = s.db.ExecContext(r.Context(), `DELETE FROM pos_ticket_line_tags WHERE restaurant_id=? AND ticket_line_id=? AND tag_id=?`, a.ActiveRestaurantID, entityID, in.TagID)
	case attach:
		_, err = s.db.ExecContext(r.Context(), `INSERT IGNORE INTO pos_ticket_tags (restaurant_id,ticket_id,tag_id,created_by) VALUES (?,?,?,?)`, a.ActiveRestaurantID, entityID, in.TagID, a.User.ID)
	default:
		_, err = s.db.ExecContext(r.Context(), `DELETE FROM pos_ticket_tags WHERE restaurant_id=? AND ticket_id=? AND tag_id=?`, a.ActiveRestaurantID, entityID, in.TagID)
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error updating tags")
		return
	}
	ticket, loadErr := s.loadPOSTicket(r.Context(), a.ActiveRestaurantID, ticketID)
	if loadErr != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading tagged ticket")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "tagId": in.TagID, "attached": attach, "ticket": ticket})
}
