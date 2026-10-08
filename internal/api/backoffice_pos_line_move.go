package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"preactvillacarmen/internal/httpx"
)

// Moving a line between the checks of one table is how a comensal gets their own
// bill: "the two beers and the arroz are Ana's". The money, the kitchen and the
// stock all have to come out the same as if the dish had been rung on that
// check in the first place.
//
// A FULL move re-points the line (and, for a menu, its component dishes) to the
// target check. Its id does not change, so its modifiers, tags, invitation,
// stock records and kitchen history simply travel with it.
//
// A PARTIAL move ("1 of the 3 beers") splits the row: the source keeps the
// rest, a new row carries the moved quantity with a copy of everything that
// describes the dish (modifiers, tags, invitation, pack, course, operator), and
// the quantity the kitchen already knows about is handed over with a
// pos_kitchen_line_transfers row so the next comanda neither voids nor re-cooks.

type posMovableLine struct {
	id                   int64
	productID            sql.NullInt64
	name                 string
	sku                  sql.NullString
	quantity             float64
	unitPrice, discount  int64
	vat                  float64
	notes, course        sql.NullString
	compedAt             sql.NullTime
	compReason           sql.NullString
	compedBy, operatorID sql.NullInt64
	packID, parentLineID sql.NullInt64
}

func loadPOSMovableLine(ctx context.Context, tx *sql.Tx, restaurantID int, ticketID, lineID int64) (posMovableLine, error) {
	var l posMovableLine
	err := tx.QueryRowContext(ctx, `SELECT id,pos_product_id,product_name_snapshot,product_sku_snapshot,quantity,unit_price_gross_cents,vat_rate_snapshot,discount_cents,notes,course,comped_at,comp_reason,comped_by,operator_member_id,pack_id,parent_line_id FROM pos_ticket_lines WHERE restaurant_id=? AND ticket_id=? AND id=? AND status='ACTIVE' FOR UPDATE`, restaurantID, ticketID, lineID).
		Scan(&l.id, &l.productID, &l.name, &l.sku, &l.quantity, &l.unitPrice, &l.vat, &l.discount, &l.notes, &l.course, &l.compedAt, &l.compReason, &l.compedBy, &l.operatorID, &l.packID, &l.parentLineID)
	return l, err
}

// posKitchenSentByStation is what each station has already been told about a
// line: dispatched deltas plus the quantity handed in or out by earlier splits.
func posKitchenSentByStation(ctx context.Context, tx *sql.Tx, restaurantID int, lineID int64) (map[int64]float64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT station_id,SUM(qty) FROM (
		SELECT d.station_id,dl.quantity_delta qty FROM pos_kitchen_dispatch_lines dl JOIN pos_kitchen_dispatches d ON d.restaurant_id=dl.restaurant_id AND d.id=dl.dispatch_id WHERE dl.restaurant_id=? AND dl.ticket_line_id=? AND d.status<>'CANCELLED'
		UNION ALL SELECT station_id,quantity FROM pos_kitchen_line_transfers WHERE restaurant_id=? AND to_line_id=?
		UNION ALL SELECT station_id,-quantity FROM pos_kitchen_line_transfers WHERE restaurant_id=? AND from_line_id=?
	) x GROUP BY station_id`, restaurantID, lineID, restaurantID, lineID, restaurantID, lineID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]float64{}
	for rows.Next() {
		var station int64
		var qty float64
		if err = rows.Scan(&station, &qty); err != nil {
			return nil, err
		}
		out[station] = qty
	}
	return out, rows.Err()
}

// splitPOSLine moves `moved` units of line l (on sourceTicket) into a new row on
// targetTicket and returns the new row's id. newParent is set for the
// component dishes of a menu, which follow their parent.
func (s *Server) splitPOSLine(ctx context.Context, tx *sql.Tx, restaurantID, userID int, l posMovableLine, sourceTicket, targetTicket int64, moved float64, newParent sql.NullInt64, key string, stockLive bool) (int64, error) {
	remaining := l.quantity - moved
	sourceDiscount := int64(math.Round(float64(l.discount) * (remaining / l.quantity)))
	movedDiscount := l.discount - sourceDiscount
	if _, err := tx.ExecContext(ctx, `UPDATE pos_ticket_lines SET quantity=?,discount_cents=?,line_total_gross_cents=ROUND(?*unit_price_gross_cents)-? WHERE restaurant_id=? AND id=?`, remaining, sourceDiscount, remaining, sourceDiscount, restaurantID, l.id); err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO pos_ticket_lines (restaurant_id,ticket_id,pos_product_id,product_name_snapshot,product_sku_snapshot,quantity,unit_price_gross_cents,vat_rate_snapshot,discount_cents,line_total_gross_cents,notes,course,idempotency_key,created_by,comped_at,comp_reason,comped_by,operator_member_id,pack_id,parent_line_id) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		restaurantID, targetTicket, l.productID, l.name, l.sku, moved, l.unitPrice, l.vat, movedDiscount, int64(math.Round(float64(l.unitPrice)*moved))-movedDiscount, l.notes, l.course, key, userID, l.compedAt, l.compReason, l.compedBy, l.operatorID, l.packID, newParent)
	if err != nil {
		return 0, err
	}
	newID, _ := res.LastInsertId()
	// Modifiers are per unit (the price delta is already inside unit price), so
	// the copy is identical, not prorated.
	if _, err = tx.ExecContext(ctx, `INSERT INTO pos_ticket_line_modifiers (restaurant_id,ticket_line_id,modifier_option_id,name_snapshot,price_delta_cents,quantity,source_product_id) SELECT restaurant_id,?,modifier_option_id,name_snapshot,price_delta_cents,quantity,source_product_id FROM pos_ticket_line_modifiers WHERE restaurant_id=? AND ticket_line_id=? ORDER BY id`, newID, restaurantID, l.id); err != nil {
		return 0, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO pos_ticket_line_tags (restaurant_id,ticket_line_id,tag_id,created_by) SELECT restaurant_id,?,tag_id,created_by FROM pos_ticket_line_tags WHERE restaurant_id=? AND ticket_line_id=?`, newID, restaurantID, l.id); err != nil {
		return 0, err
	}
	sent, err := posKitchenSentByStation(ctx, tx, restaurantID, l.id)
	if err != nil {
		return 0, err
	}
	for station, qty := range sent {
		handed := math.Min(moved, qty)
		if handed <= 0 {
			continue
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO pos_kitchen_line_transfers (restaurant_id,station_id,from_line_id,to_line_id,quantity,created_by) VALUES (?,?,?,?,?,?)`, restaurantID, station, l.id, newID, handed, userID); err != nil {
			return 0, err
		}
	}
	if stockLive && l.productID.Valid {
		if err = s.adjustStockForQuantityChange(ctx, tx, restaurantID, userID, sourceTicket, l.id, l.productID.Int64, l.quantity, remaining, "pos-line-move:"+key+":source"); err != nil {
			return 0, err
		}
		if _, err = s.deductStockForLine(ctx, tx, restaurantID, userID, targetTicket, newID, l.productID.Int64, moved, "pos-line-move:"+key+":target"); err != nil {
			return 0, err
		}
	}
	return newID, nil
}

func (s *Server) handleBOPOSLineMove(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	ctx := r.Context()
	sourceTicketID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	lineID, _ := strconv.ParseInt(chi.URLParam(r, "lineId"), 10, 64)
	// A closed cash day is a signed Z closure; mutating it afterwards would
	// invalidate an accounting document that has already been reported.
	if posWriteCashDayGuard(w, s.requireOpenCashDayForTicket(ctx, a.ActiveRestaurantID, sourceTicketID)) {
		return
	}
	var in struct {
		TargetTicketID int64   `json:"targetTicketId"`
		Quantity       float64 `json:"quantity"`
		IdempotencyKey string  `json:"idempotencyKey"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || sourceTicketID <= 0 || lineID <= 0 || in.TargetTicketID <= 0 || in.TargetTicketID == sourceTicketID || in.Quantity <= 0 || strings.TrimSpace(in.IdempotencyKey) == "" || len(in.IdempotencyKey) > 100 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid line move")
		return
	}
	in.IdempotencyKey = strings.TrimSpace(in.IdempotencyKey)
	if posWriteCashDayGuard(w, s.requireOpenCashDayForTicket(ctx, a.ActiveRestaurantID, in.TargetTicketID)) {
		return
	}
	writeTickets := func(status int, extra map[string]any) {
		source, _ := s.loadPOSTicket(ctx, a.ActiveRestaurantID, sourceTicketID)
		target, _ := s.loadPOSTicket(ctx, a.ActiveRestaurantID, in.TargetTicketID)
		body := map[string]any{"success": true, "sourceTicket": source, "targetTicket": target}
		for k, v := range extra {
			body[k] = v
		}
		httpx.WriteJSON(w, status, body)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error moving line")
		return
	}
	defer tx.Rollback()
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
		var st ticketState
		if err = tx.QueryRowContext(ctx, `SELECT visit_id,status,ticket_discount_cents FROM pos_tickets WHERE restaurant_id=? AND id=? FOR UPDATE`, a.ActiveRestaurantID, ticketID).Scan(&st.visitID, &st.status, &st.discount); err != nil || st.status != "OPEN" {
			httpx.WriteError(w, http.StatusConflict, "Both tickets must be open")
			return
		}
		states[ticketID] = st
	}
	// A retried move must not move a second unit. The audit row is the record
	// of the move for both kinds (a full move creates no new row to collide on),
	// and it is read AFTER both checks are locked so two copies of the same
	// request cannot both get past it.
	var priorMoved sql.NullInt64
	if err = tx.QueryRowContext(ctx, `SELECT CAST(JSON_UNQUOTE(JSON_EXTRACT(after_json,'$.movedLineId')) AS UNSIGNED) FROM pos_audit_events WHERE restaurant_id=? AND entity_type='ticket_line' AND entity_id=? AND action='MOVE' AND JSON_UNQUOTE(JSON_EXTRACT(after_json,'$.idempotencyKey'))=? LIMIT 1`, a.ActiveRestaurantID, lineID, in.IdempotencyKey).Scan(&priorMoved); err == nil {
		tx.Rollback()
		writeTickets(http.StatusOK, map[string]any{"duplicate": true, "movedLineId": priorMoved.Int64})
		return
	}
	if states[sourceTicketID].visitID != states[in.TargetTicketID].visitID {
		httpx.WriteError(w, http.StatusConflict, "Tickets must belong to same visit")
		return
	}
	line, err := loadPOSMovableLine(ctx, tx, a.ActiveRestaurantID, sourceTicketID, lineID)
	if err != nil || in.Quantity > line.quantity+1e-9 {
		httpx.WriteError(w, http.StatusConflict, "Move quantity exceeds source line")
		return
	}
	// The money of a menu lives on its parent line. Moving one dish of it alone
	// would give the guest a free plate and leave the menu price on the other
	// check, so a component only moves with its menu.
	if line.parentLineID.Valid {
		httpx.WriteError(w, http.StatusConflict, "Un plato de un menú se mueve con su menú: mueve el menú")
		return
	}
	settings, settingsErr := s.loadPOSSettings(ctx, a.ActiveRestaurantID)
	stockLive := settingsErr == nil && settings.StockMode == "LIVE"
	children := []posMovableLine{}
	childRows, err := tx.QueryContext(ctx, `SELECT id FROM pos_ticket_lines WHERE restaurant_id=? AND ticket_id=? AND parent_line_id=? AND status='ACTIVE' ORDER BY id`, a.ActiveRestaurantID, sourceTicketID, lineID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading menu dishes")
		return
	}
	childIDs := []int64{}
	for childRows.Next() {
		var id int64
		if err = childRows.Scan(&id); err != nil {
			childRows.Close()
			httpx.WriteError(w, http.StatusInternalServerError, "Error loading menu dishes")
			return
		}
		childIDs = append(childIDs, id)
	}
	childRows.Close()
	for _, id := range childIDs {
		child, cErr := loadPOSMovableLine(ctx, tx, a.ActiveRestaurantID, sourceTicketID, id)
		if cErr != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error loading menu dishes")
			return
		}
		children = append(children, child)
	}
	full := math.Abs(in.Quantity-line.quantity) < 1e-9
	movedLineID := lineID
	if full {
		all := append([]int64{lineID}, childIDs...)
		for _, id := range all {
			if _, err = tx.ExecContext(ctx, `UPDATE pos_ticket_lines SET ticket_id=? WHERE restaurant_id=? AND id=?`, in.TargetTicketID, a.ActiveRestaurantID, id); err != nil {
				break
			}
			if _, err = tx.ExecContext(ctx, `UPDATE pos_ticket_line_stock SET ticket_id=? WHERE restaurant_id=? AND ticket_line_id=?`, in.TargetTicketID, a.ActiveRestaurantID, id); err != nil {
				break
			}
		}
	} else {
		movedLineID, err = s.splitPOSLine(ctx, tx, a.ActiveRestaurantID, a.User.ID, line, sourceTicketID, in.TargetTicketID, in.Quantity, sql.NullInt64{}, in.IdempotencyKey, stockLive)
		for i, child := range children {
			if err != nil {
				break
			}
			childMoved := child.quantity * in.Quantity / line.quantity
			_, err = s.splitPOSLine(ctx, tx, a.ActiveRestaurantID, a.User.ID, child, sourceTicketID, in.TargetTicketID, childMoved, sql.NullInt64{Int64: movedLineID, Valid: true}, in.IdempotencyKey+":c"+strconv.Itoa(i), stockLive)
		}
	}
	if err != nil {
		log.Printf("pos line move %d -> ticket %d: %v", lineID, in.TargetTicketID, err)
		httpx.WriteError(w, http.StatusInternalServerError, "Line could not be moved")
		return
	}
	// A check-level discount stays on its check. If what is left on the source
	// no longer covers it, say so instead of answering a bare 500.
	if _, err = s.recalculatePOSTicket(ctx, tx, a.ActiveRestaurantID, sourceTicketID, states[sourceTicketID].discount); err != nil {
		httpx.WriteError(w, http.StatusConflict, "El descuento de esta cuenta supera lo que quedaría en ella: quítalo o redúcelo antes de mover")
		return
	}
	if _, err = s.recalculatePOSTicket(ctx, tx, a.ActiveRestaurantID, in.TargetTicketID, states[in.TargetTicketID].discount); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error calculating target ticket")
		return
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO pos_audit_events (restaurant_id,entity_type,entity_id,action,after_json,actor_user_id) VALUES (?,'ticket_line',?,'MOVE',JSON_OBJECT('fromTicketId',?,'toTicketId',?,'quantity',?,'full',?,'movedLineId',?,'idempotencyKey',?),?)`, a.ActiveRestaurantID, lineID, sourceTicketID, in.TargetTicketID, in.Quantity, full, movedLineID, in.IdempotencyKey, a.User.ID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error moving line")
		return
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error moving line")
		return
	}
	writeTickets(http.StatusOK, map[string]any{"movedLineId": movedLineID, "full": full})
}
