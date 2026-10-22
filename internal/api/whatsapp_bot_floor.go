package api

// Admin floor change (slice 4h): booking_propose_set_floor proposes moving a
// booking to another floor (salón/planta). Same mold as the other admin ops:
// propose + exact "OK <code>" by the same actor in the same chat, narrow
// column write inside the claim transaction, full-row version check,
// others-hash guard and durable result.
//
// Canonical contract (panel boNormalizeAndValidateBookingInput +
// boUpdateBooking on PATCH):
//   - the floor must exist and be active for the booking date, with
//     loadDateFloors semantics: global floors, date-scoped rows shadowing by
//     floor_number, and per-date active overrides from
//     restaurant_floor_overrides;
//   - only bookings.preferred_floor_number changes; the panel never clears a
//     floor once set (the PATCH seeds the current value when the field is
//     absent), so this tool requires a floor and cannot clear either;
//   - the panel records NO booking_modifications entry for floor changes
//     (history tracks only date/time/party/strollers/high_chairs/rice), so
//     none is written here;
//   - the occupancy ledger moves the full party_size between floors
//     (applyBookingLocationOccupancy with sign -1/+1, canonical global floor
//     row via floorIDByNumber). The panel's reconcile also rewrites the salon
//     contribution to the same value (net zero); this op touches only the
//     floor scope, which leaves the salon rows byte-identical. Unlike the
//     panel (which reconciles outside the patch transaction), the ledger move
//     runs INSIDE the claim transaction, so a booking write and its ledger
//     move commit or roll back together;
//   - the panel does NOT check floor capacity on PATCH (aforo only gates the
//     public day-context endpoint), so neither does this op;
//   - no customer notification, no n8n.
//
// The preview never shows the current floor (privacy rule: old DB values are
// masked); the new floor is actor-provided and shown.
//
// Coordination id: wa_bot_admin_set_floor_v1

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
)

// botFloorColumns is the closed set of columns this op may write.
var botFloorColumns = []string{"preferred_floor_number"}

// botFloorCandidate mirrors the fields of boConfigFloor the bot needs. The
// custom floor name is deliberately not read: only the number participates in
// validation and in the (number-bound) preview/afterKey.
type botFloorCandidate struct {
	ID          int
	FloorNumber int
	Active      bool
}

// botLoadDateFloorsFrom replicates loadDateFloors (globals merged with
// date-scoped shadows and per-date active overrides) as read-only queries
// against q. It never runs loadDefaultFloors' ensure-insert: with no global
// floors configured there is nothing valid to assign.
func (s *Server) botLoadDateFloorsFrom(ctx context.Context, q botRowQuerier, restaurantID int, date string) ([]botFloorCandidate, error) {
	readRows := func(query string, args ...any) ([]botFloorCandidate, error) {
		rows, err := q.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := make([]botFloorCandidate, 0, 4)
		for rows.Next() {
			var f botFloorCandidate
			var ground, active int
			if err := rows.Scan(&f.ID, &f.FloorNumber, &ground, &active); err != nil {
				return nil, err
			}
			f.Active = active != 0
			out = append(out, f)
		}
		return out, rows.Err()
	}
	globals, err := readRows(`SELECT id, floor_number, is_ground, is_active FROM restaurant_floors WHERE restaurant_id = ? AND specific_date IS NULL`, restaurantID)
	if err != nil {
		return nil, err
	}
	scoped, err := readRows(`SELECT id, floor_number, is_ground, is_active FROM restaurant_floors WHERE restaurant_id = ? AND specific_date = ?`, restaurantID, date)
	if err != nil {
		return nil, err
	}
	floors := globals
	if len(scoped) > 0 {
		shadowed := map[int]bool{}
		for _, f := range scoped {
			shadowed[f.FloorNumber] = true
		}
		merged := make([]botFloorCandidate, 0, len(globals)+len(scoped))
		for _, f := range globals {
			if !shadowed[f.FloorNumber] {
				merged = append(merged, f)
			}
		}
		floors = append(merged, scoped...)
	}
	rows, err := q.QueryContext(ctx, `SELECT floor_id, is_active FROM restaurant_floor_overrides WHERE restaurant_id = ? AND date = ?`, restaurantID, date)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	override := map[int]bool{}
	for rows.Next() {
		var id, active int
		if err := rows.Scan(&id, &active); err != nil {
			return nil, err
		}
		override[id] = active != 0
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range floors {
		if v, ok := override[floors[i].ID]; ok {
			floors[i].Active = v
		}
	}
	return floors, nil
}

// botFloorActiveNumber reports whether the number exists and is active for
// the date, mirroring the panel validation (bm.go:800-810).
func botFloorActiveNumber(floors []botFloorCandidate, number int64) bool {
	for _, f := range floors {
		if !f.Active {
			continue
		}
		if int64(f.FloorNumber) == number {
			return true
		}
	}
	return false
}

func botFloorAfterKey(bookingID int64, prevSet bool, prev, next int64) string {
	var prevArg any
	if prevSet {
		prevArg = prev
	}
	return botSha("floorv1|" + botCanon(map[string]any{"booking_id": bookingID, "previous_floor": prevArg, "preferred_floor_number": next}))
}

func (s *Server) botToolFloorPropose(ctx context.Context, rid int, msg botWebhookMessage, input json.RawMessage) (string, error) {
	actor, origin, ok := s.botSpecialAdminAuthorize(ctx, rid, msg)
	if !ok {
		return botJSON(map[string]any{"error": "no autorizado: solo para miembros verificados del grupo de gestión"}), nil
	}
	s.botSpecialAdminFlushNotify(ctx, rid)
	var in struct {
		BookingID int64  `json:"booking_id"`
		Floor     *int64 `json:"preferred_floor_number"`
	}
	dec := json.NewDecoder(bytes.NewReader(input))
	dec.DisallowUnknownFields()
	if dec.Decode(&in) != nil || in.BookingID <= 0 || in.Floor == nil || *in.Floor < 0 {
		return botJSON(map[string]any{"error": "argumentos inválidos: booking_id, preferred_floor_number (0 = planta baja)"}), nil
	}
	floor := *in.Floor
	row, err := s.botLoadSpecialRow(ctx, rid, in.BookingID)
	if err != nil {
		return botJSON(map[string]any{"error": "no hay una reserva con ese id"}), nil
	}
	if rerr := botSpecialRowAllowedFor(row, "editan", "edita"); rerr != nil {
		return botJSON(map[string]any{"error": rerr.Error()}), nil
	}
	var prev sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT preferred_floor_number FROM bookings WHERE restaurant_id = ? AND id = ?`, rid, in.BookingID).Scan(&prev); err != nil {
		return botJSON(map[string]any{"error": "no se pudo leer la reserva"}), nil
	}
	if prev.Valid && prev.Int64 == floor {
		return botJSON(map[string]any{"error": "la reserva ya está en esa planta"}), nil
	}
	floors, ferr := s.botLoadDateFloorsFrom(ctx, s.db, rid, row.Date)
	if ferr != nil {
		return botJSON(map[string]any{"error": "no se pudo validar el salón"}), nil
	}
	if !botFloorActiveNumber(floors, floor) {
		return botJSON(map[string]any{"error": "salón inválido para la fecha de la reserva"}), nil
	}
	// The ledger write targets the canonical GLOBAL floor row
	// (floorIDByNumber, specific_date IS NULL). A floor that exists only as a
	// date-scoped row passes the active check but its occupancy write would
	// be a silent no-op; refuse it instead of leaving the booking counted
	// nowhere.
	newFloorID, ferr2 := s.floorIDByNumber(ctx, s.db, rid, int(floor))
	if ferr2 != nil {
		return botJSON(map[string]any{"error": "no se pudo validar el salón"}), nil
	}
	if newFloorID <= 0 {
		return botJSON(map[string]any{"error": "ese salón solo existe para fechas concretas y no tiene planta general asociada; no se puede registrar la ocupación"}), nil
	}
	beforeHash, herr := botFullRowHash(ctx, s.db, rid, in.BookingID)
	if herr != nil {
		return botJSON(map[string]any{"error": "no se pudo leer la reserva"}), nil
	}
	code, cerr := botSpecialNewCode()
	if cerr != nil {
		return botJSON(map[string]any{"error": "no se pudo generar la propuesta"}), nil
	}
	// The preview shows only the actor-provided number and its canonical
	// generated name: a custom floor name is mutable menu-style config, and
	// the afterKey binds numbers only, so a rename before "OK" would leave a
	// stale name on screen. Capacity (aforo) is NOT checked, matching the
	// panel PATCH; the preview says so explicitly so the operator knows the
	// assignment can force a floor over its capacity.
	preview := fmt.Sprintf("Propuesta: CAMBIAR el salón de la reserva #%d (%s %s, %d comensales) a %s (planta %d). La planta actual no se muestra por privacidad. Los %d comensales se mueven en el registro de ocupación. SIN comprobación de aforo: la planta puede quedar por encima de su capacidad y el operador asume ese forzado. No se avisa al cliente.",
		in.BookingID, row.Date, row.Time, row.Party, floorNameForNumber(int(floor)), floor, row.Party)
	after := botFloorAfterKey(in.BookingID, prev.Valid, prev.Int64, floor)
	args := map[string]any{"booking_id": in.BookingID, "previous_floor_set": prev.Valid, "previous_floor": prev.Int64, "preferred_floor_number": floor}
	return s.botProposalPersist(ctx, rid, msg, "set_floor", in.BookingID, args, "", actor, origin, beforeHash, after, preview, code)
}

// botFloorExecute runs INSIDE the claim transaction: claim + narrow column
// write + ledger move + others-hash verification + result_json + event commit
// together.
func (s *Server) botFloorExecute(ctx context.Context, tx *sql.Tx, rid int, msg botWebhookMessage, p botSpecialProposalRow) (string, error) {
	stale := func(why string) (string, error) {
		_ = tx.Rollback()
		s.markStale(ctx, rid, p, msg)
		return "", errors.New(why)
	}
	var in struct {
		BookingID        int64 `json:"booking_id"`
		PreviousFloorSet bool  `json:"previous_floor_set"`
		PreviousFloor    int64 `json:"previous_floor"`
		Floor            int64 `json:"preferred_floor_number"`
	}
	if json.Unmarshal([]byte(p.Args), &in) != nil {
		return "", errors.New("propuesta ilegible")
	}
	row, err := botScanSpecialRow(tx.QueryRowContext(ctx, botSpecialRowSelect+" FOR UPDATE", rid, p.BookingID))
	if err != nil {
		return "", errors.New("la reserva ya no existe")
	}
	if rerr := botSpecialRowAllowedFor(row, "editan", "edita"); rerr != nil {
		return "", rerr
	}
	full, err := botFullRowHash(ctx, tx, rid, p.BookingID)
	if err != nil {
		return "", errors.New("error interno")
	}
	if full != p.Before {
		return stale(errBotSpecialStale.Error())
	}
	var prev sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT preferred_floor_number FROM bookings WHERE restaurant_id = ? AND id = ?`, rid, p.BookingID).Scan(&prev); err != nil {
		return "", errors.New("error interno")
	}
	if botFloorAfterKey(p.BookingID, prev.Valid, prev.Int64, in.Floor) != p.After {
		return stale("el resultado ya no coincide con la vista previa; pide una propuesta nueva")
	}
	if prev.Valid && prev.Int64 == in.Floor {
		return stale("la reserva ya está en esa planta; pide una propuesta nueva")
	}
	floors, ferr := s.botLoadDateFloorsFrom(ctx, tx, rid, row.Date)
	if ferr != nil {
		return "", errors.New("error interno")
	}
	if !botFloorActiveNumber(floors, in.Floor) {
		return stale("el salón ya no está disponible para la fecha de la reserva; pide una propuesta nueva")
	}
	newFloorID, ferr2 := s.floorIDByNumber(ctx, tx, rid, int(in.Floor))
	if ferr2 != nil {
		return "", errors.New("error interno")
	}
	if newFloorID <= 0 {
		return stale("el salón ya no tiene planta general asociada; pide una propuesta nueva")
	}
	if row.Party <= 0 {
		return "", errors.New("la reserva no tiene comensales registrados; no se puede mover la ocupación")
	}
	preOthers, err := botFullRowHash(ctx, tx, rid, p.BookingID, botFloorColumns...)
	if err != nil {
		return "", errors.New("error interno")
	}
	// Ledger move first: subtract the party from the old floor's canonical
	// global row, add it to the new one (applyBookingLocationOccupancy with
	// salon nil touches only the floor scope).
	var oldFloor any
	if prev.Valid {
		oldFloor = prev.Int64
	}
	if err := s.applyBookingLocationOccupancy(ctx, tx, rid, row.Date, oldFloor, nil, row.Party, -1); err != nil {
		return "", errors.New("no se pudo actualizar la ocupación; no se aplicó nada")
	}
	if err := s.applyBookingLocationOccupancy(ctx, tx, rid, row.Date, in.Floor, nil, row.Party, 1); err != nil {
		return "", errors.New("no se pudo actualizar la ocupación; no se aplicó nada")
	}
	if _, err := tx.ExecContext(ctx, "UPDATE bookings SET `preferred_floor_number` = ? WHERE restaurant_id = ? AND id = ?", in.Floor, rid, p.BookingID); err != nil {
		return "", errors.New("error al guardar; no se aplicó nada")
	}
	postOthers, err := botFullRowHash(ctx, tx, rid, p.BookingID, botFloorColumns...)
	if err != nil || postOthers != preOthers {
		return "", errors.New("verificación fallida: cambió una columna fuera de la edición; no se aplicó nada")
	}
	notice := fmt.Sprintf("Reserva #%d: salón cambiado a %s (planta %d). No se ha avisado al cliente.", p.BookingID, floorNameForNumber(int(in.Floor)), in.Floor)
	rj, _ := json.Marshal(map[string]any{"booking_id": p.BookingID, "op": p.Op, "notice": notice})
	if _, err := tx.ExecContext(ctx, `UPDATE wa_bot_op_proposals SET result_json=?, notify_state='pending' WHERE id=?`, string(rj), p.ID); err != nil {
		return "", errors.New("error interno")
	}
	s.botSpecialAdminEvent(ctx, tx, rid, p.ID, "executed", p.Actor, strings.TrimSpace(msg.ChatJID), map[string]any{"op": p.Op, "booking_id": p.BookingID, "before_hash": p.Before, "after_hash": p.After, "changed_columns": botFloorColumns, "approval_message_id": msg.MessageID})
	if err := tx.Commit(); err != nil {
		log.Printf("[bot] wa_bot_admin_set_floor_v1 commit_unknown rid=%d proposal=%d err=%v", rid, p.ID, err)
		return "", errors.New("no pude confirmar si se guardó; consulta la reserva antes de repetir")
	}
	s.broadcastBookingChanged(rid, p.BookingID, "booking_updated")
	return notice, nil
}
