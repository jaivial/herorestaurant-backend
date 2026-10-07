package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"preactvillacarmen/internal/httpx"
)

// POS recall ("traer la cuenta de la mesa 7"): copy the dishes of a finished
// ticket into a new one so a returning guest gets their usual without the waiter
// retyping it. Prices, modifiers and notes are copied as they were sold, not
// re-read from the catalogue: the guest is owed the dish they had last time,
// and the recipe may have changed since.
//
// Copying onto an open ticket is the common case (the same table asks for the
// same thing again); copying onto a brand new ticket is also supported so a
// takeaway can recall a dine-in.
//
// Coordination id: pos_recall_v1

// maxRecallLines bounds a recall. A ticket with hundreds of lines is a
// mis-selection, not a returning guest, and copying it would freeze the till.
const maxRecallLines = 120

// posRecallLine is one line of a finished ticket, with the modifiers that rode
// along with it.
type posRecallLine struct {
	productID sql.NullInt64
	name      string
	quantity  float64
	unitPrice int64
	vatRate   float64
	lineTotal int64
	notes     sql.NullString
	// id is the original line id, used to re-point children at the copy.
	id int64
	// Pack provenance: a recalled menu must keep its dishes under it, not
	// become one paid line plus a pile of parentless 0,00 € plates.
	packID       sql.NullInt64
	parentLineID sql.NullInt64
	// course travels with the line: a recalled menu still belongs to the same
	// service, so firing course 2 does not leave it stranded in course 1.
	course string
	// name/option/delta/qty per modifier, snapshotted from the source so the
	// copy reads exactly like the original even if the catalogue changed.
	modifiers []posRecallModifier
}

type posRecallModifier struct {
	optionID sql.NullInt64
	name     string
	delta    int64
	quantity float64
}

// loadPOSRecallLines reads the active lines of a finished ticket plus their
// modifiers, in two queries regardless of how many lines there are.
func (s *Server) loadPOSRecallLines(ctx context.Context, tx *sql.Tx, restaurantID int, sourceTicketID int64) ([]posRecallLine, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,pos_product_id,product_name_snapshot,quantity,unit_price_gross_cents,vat_rate_snapshot,line_total_gross_cents,notes,pack_id,parent_line_id,COALESCE(NULLIF(course,''),'1') FROM pos_ticket_lines WHERE restaurant_id=? AND ticket_id=? AND status='ACTIVE' ORDER BY id`, restaurantID, sourceTicketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	lines := []posRecallLine{}
	index := map[int64]int{}
	for rows.Next() {
		var line posRecallLine
		var id int64
		if err = rows.Scan(&id, &line.productID, &line.name, &line.quantity, &line.unitPrice, &line.vatRate, &line.lineTotal, &line.notes, &line.packID, &line.parentLineID, &line.course); err != nil {
			return nil, err
		}
		line.id = id
		index[id] = len(lines)
		lines = append(lines, line)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(lines) == 0 {
		return lines, nil
	}
	modRows, err := tx.QueryContext(ctx, `SELECT m.ticket_line_id,m.modifier_option_id,m.name_snapshot,m.price_delta_cents,m.quantity FROM pos_ticket_line_modifiers m JOIN pos_ticket_lines l ON l.restaurant_id=m.restaurant_id AND l.id=m.ticket_line_id WHERE m.restaurant_id=? AND l.ticket_id=? ORDER BY m.ticket_line_id,m.id`, restaurantID, sourceTicketID)
	if err != nil {
		return nil, err
	}
	defer modRows.Close()
	for modRows.Next() {
		var lineID int64
		var mod posRecallModifier
		if err = modRows.Scan(&lineID, &mod.optionID, &mod.name, &mod.delta, &mod.quantity); err != nil {
			return nil, err
		}
		if i, ok := index[lineID]; ok {
			lines[i].modifiers = append(lines[i].modifiers, mod)
		}
	}
	return lines, modRows.Err()
}

// handleBOPOSRecall copies a finished ticket's lines onto an open ticket.
func (s *Server) handleBOPOSRecall(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	targetID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var in struct {
		SourceTicketID int64 `json:"sourceTicketId"`
	}
	if targetID <= 0 || json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in) != nil || in.SourceTicketID <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid recall")
		return
	}
	// A closed cash day is a signed Z closure; nothing can be added to it.
	if posWriteCashDayGuard(w, s.requireOpenCashDayForTicket(r.Context(), a.ActiveRestaurantID, targetID)) {
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error recalling ticket")
		return
	}
	defer tx.Rollback()
	var status string
	var ticketDiscount int64
	if err = tx.QueryRowContext(r.Context(), `SELECT status,ticket_discount_cents FROM pos_tickets WHERE restaurant_id=? AND id=? FOR UPDATE`, a.ActiveRestaurantID, targetID).Scan(&status, &ticketDiscount); err != nil || status != "OPEN" {
		httpx.WriteError(w, http.StatusConflict, "Ticket is not open")
		return
	}
	// The source must belong to this restaurant. Reading its lines is enough to
	// prove ownership; recalling from another restaurant's ticket must 404, not
	// silently copy nothing.
	var sourceExists int64
	if err = tx.QueryRowContext(r.Context(), `SELECT id FROM pos_tickets WHERE restaurant_id=? AND id=?`, a.ActiveRestaurantID, in.SourceTicketID).Scan(&sourceExists); err != nil {
		httpx.WriteError(w, http.StatusNotFound, "Source ticket not found")
		return
	}
	lines, err := s.loadPOSRecallLines(r.Context(), tx, a.ActiveRestaurantID, in.SourceTicketID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error reading source ticket")
		return
	}
	if len(lines) == 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Source ticket has no active lines")
		return
	}
	if len(lines) > maxRecallLines {
		httpx.WriteError(w, http.StatusBadRequest, "Source ticket has too many lines to recall")
		return
	}
	// Parents are written before their children so a child can point at the
	// recalled parent rather than at the original line. Component lines are
	// copied too: the kitchen still has to cook the dishes, and their zero
	// price keeps the money on the menu exactly as it was sold.
	baseKey := "recall:" + strconv.FormatInt(targetID, 10) + ":" + strconv.FormatInt(in.SourceTicketID, 10)
	idRemap := map[int64]int64{}
	copied := 0
	for _, line := range lines {
		if line.parentLineID.Valid {
			continue
		}
		key := baseKey + ":p" + strconv.Itoa(copied)
		var productID, packID any
		if line.productID.Valid {
			productID = line.productID.Int64
		}
		if line.packID.Valid {
			packID = line.packID.Int64
		}
		var notes any
		if line.notes.Valid {
			notes = line.notes.String
		}
		res, insErr := tx.ExecContext(r.Context(), `INSERT INTO pos_ticket_lines (restaurant_id,ticket_id,pos_product_id,product_name_snapshot,quantity,unit_price_gross_cents,vat_rate_snapshot,line_total_gross_cents,notes,idempotency_key,pack_id,course,created_by) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`, a.ActiveRestaurantID, targetID, productID, line.name, line.quantity, line.unitPrice, line.vatRate, line.lineTotal, notes, key, packID, line.course, a.User.ID)
		if insErr != nil {
			if strings.Contains(strings.ToLower(insErr.Error()), "duplicate") {
				// This recall already applied: skip rather than charge twice.
				continue
			}
			httpx.WriteError(w, http.StatusBadRequest, "Ticket line could not be recalled")
			return
		}
		newID, _ := res.LastInsertId()
		idRemap[line.id] = newID
		for _, mod := range line.modifiers {
			var optionID any
			if mod.optionID.Valid {
				optionID = mod.optionID.Int64
			}
			if _, modErr := tx.ExecContext(r.Context(), `INSERT INTO pos_ticket_line_modifiers (restaurant_id,ticket_line_id,modifier_option_id,name_snapshot,price_delta_cents,quantity) VALUES (?,?,?,?,?,?)`, a.ActiveRestaurantID, newID, optionID, mod.name, mod.delta, mod.quantity); modErr != nil {
				httpx.WriteError(w, http.StatusInternalServerError, "Error recalling modifiers")
				return
			}
		}
		copied++
	}
	childIndex := 0
	for _, line := range lines {
		if !line.parentLineID.Valid {
			continue
		}
		newParent, ok := idRemap[line.parentLineID.Int64]
		if !ok {
			// Its parent was not copied (an already-applied recall): skip the
			// orphan rather than write a 0,00 € plate nobody paid for.
			continue
		}
		// The index keeps the key unique when a menu has several components:
		// keying on the parent alone would let all but the first collide and be
		// silently skipped as a "duplicate".
		key := baseKey + ":c" + strconv.Itoa(childIndex)
		var productID any
		if line.productID.Valid {
			productID = line.productID.Int64
		}
		var notes any
		if line.notes.Valid {
			notes = line.notes.String
		}
		res, insErr := tx.ExecContext(r.Context(), `INSERT INTO pos_ticket_lines (restaurant_id,ticket_id,pos_product_id,product_name_snapshot,quantity,unit_price_gross_cents,vat_rate_snapshot,line_total_gross_cents,notes,idempotency_key,parent_line_id,course,created_by) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`, a.ActiveRestaurantID, targetID, productID, line.name, line.quantity, line.unitPrice, line.vatRate, line.lineTotal, notes, key, newParent, line.course, a.User.ID)
		if insErr != nil {
			if strings.Contains(strings.ToLower(insErr.Error()), "duplicate") {
				continue
			}
			httpx.WriteError(w, http.StatusBadRequest, "Ticket line could not be recalled")
			return
		}
		newID, _ := res.LastInsertId()
		for _, mod := range line.modifiers {
			var optionID any
			if mod.optionID.Valid {
				optionID = mod.optionID.Int64
			}
			if _, modErr := tx.ExecContext(r.Context(), `INSERT INTO pos_ticket_line_modifiers (restaurant_id,ticket_line_id,modifier_option_id,name_snapshot,price_delta_cents,quantity) VALUES (?,?,?,?,?,?)`, a.ActiveRestaurantID, newID, optionID, mod.name, mod.delta, mod.quantity); modErr != nil {
				httpx.WriteError(w, http.StatusInternalServerError, "Error recalling modifiers")
				return
			}
		}
		copied++
		childIndex++
	}
	if copied == 0 {
		// Everything was a duplicate: the recall already happened.
		if err = tx.Commit(); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error recalling ticket")
			return
		}
		ticket, _ := s.loadPOSTicket(r.Context(), a.ActiveRestaurantID, targetID)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "copied": 0, "ticket": ticket})
		return
	}
	if _, err = s.recalculatePOSTicket(r.Context(), tx, a.ActiveRestaurantID, targetID, ticketDiscount); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error calculating ticket")
		return
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error recalling ticket")
		return
	}
	ticket, err := s.loadPOSTicket(r.Context(), a.ActiveRestaurantID, targetID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading ticket")
		return
	}
	s.broadcastBOFichajeRevenue(a.ActiveRestaurantID, boTodayDate())
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"success": true, "copied": copied, "ticket": ticket})
}
