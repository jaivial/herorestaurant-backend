package api

// Admin mobility tool (slice 4f): booking_propose_set_mobility proposes
// changing the mobility-issues answers of a booking. Same mold as the other
// admin ops: propose + exact "OK <code>" by the same actor in the same chat,
// narrow column write inside the claim transaction, full-row version check,
// others-hash guard and durable result.
//
// Canonical contract (panel boNormalizeAndValidateBookingInput +
// normalizeMobilityValues): mobility_people only counts when
// has_mobility_issues is true and is clamped to 1..party_size (0 when absent
// or non-positive); when has_mobility_issues is false the row stores 0.
// Fields not passed keep their current value before normalizing, like a
// panel PATCH that omits them.
//
// Privacy: mobility answers are health-adjacent data. The preview and notice
// show only what the actor provided (or what follows from it); values read
// from the database are never shown. The panel does not record mobility
// changes in booking_modifications, so this tool does not either (parity).
// No customer notification, no n8n, no occupancy or payment effect.
//
// Coordination id: wa_bot_admin_set_mobility_v1

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

type botMobilityArgs struct {
	HasMobilityIssues *bool `json:"has_mobility_issues,omitempty"`
	MobilityPeople    *int  `json:"mobility_people,omitempty"`
}

// botMobilityColumns is the closed set of columns this op may write.
var botMobilityColumns = map[string]bool{"has_mobility_issues": true, "mobility_people": true}

type botMobilityCurrent struct {
	Has    bool
	People int
	Party  int
}

const botMobilityCurrentSelect = `SELECT COALESCE(has_mobility_issues,0), COALESCE(mobility_people,0), party_size FROM bookings WHERE restaurant_id = ? AND id = ?`

func botMobilityScanCurrent(row *sql.Row) (*botMobilityCurrent, error) {
	var c botMobilityCurrent
	var has int64
	if err := row.Scan(&has, &c.People, &c.Party); err != nil {
		return nil, err
	}
	c.Has = has != 0
	return &c, nil
}

// botMobilityResolve mirrors the panel: merge over current values, then
// normalizeMobilityValues (same function the panel uses).
func botMobilityResolve(cur *botMobilityCurrent, in botMobilityArgs) (bool, int) {
	var has *bool
	if in.HasMobilityIssues != nil {
		has = in.HasMobilityIssues
	} else if cur.Has {
		t := true
		has = &t
	}
	people := in.MobilityPeople
	if people == nil && cur.People > 0 {
		n := cur.People
		people = &n
	}
	return normalizeMobilityValues(has, people, cur.Party)
}

// botMobilityChangeText builds the shared preview/notice sentence showing
// only actor-provided (or directly implied) values.
func botMobilityChangeText(in botMobilityArgs, newHas bool, newPeople int) string {
	newParts := []string{}
	switch {
	case in.HasMobilityIssues != nil && !*in.HasMobilityIssues:
		newParts = append(newParts, "sin problemas de movilidad")
	case in.HasMobilityIssues != nil && *in.HasMobilityIssues:
		newParts = append(newParts, "con problemas de movilidad")
	}
	if in.MobilityPeople != nil && newHas {
		newParts = append(newParts, fmt.Sprintf("%d personas", newPeople))
	}
	if len(newParts) == 0 && !newHas {
		newParts = append(newParts, "sin problemas de movilidad")
	}
	return fmt.Sprintf("movilidad: «(actual oculto)» → «%s»", strings.Join(newParts, ", "))
}

func botMobilityAfterKey(bookingID int64, has bool, people int) string {
	return botSha("mobilityv1|" + botCanon(map[string]any{"booking_id": bookingID, "has_mobility_issues": has, "mobility_people": people}))
}

func (s *Server) botToolMobilityPropose(ctx context.Context, rid int, msg botWebhookMessage, input json.RawMessage) (string, error) {
	actor, origin, ok := s.botSpecialAdminAuthorize(ctx, rid, msg)
	if !ok {
		return botJSON(map[string]any{"error": "no autorizado: solo para miembros verificados del grupo de gestión"}), nil
	}
	s.botSpecialAdminFlushNotify(ctx, rid)
	var in struct {
		BookingID int64 `json:"booking_id"`
		botMobilityArgs
	}
	dec := json.NewDecoder(bytes.NewReader(input))
	dec.DisallowUnknownFields()
	if dec.Decode(&in) != nil || in.BookingID <= 0 || (in.HasMobilityIssues == nil && in.MobilityPeople == nil) {
		return botJSON(map[string]any{"error": "argumentos inválidos: booking_id y has_mobility_issues y/o mobility_people"}), nil
	}
	return s.botMobilityPropose(ctx, rid, msg, actor, origin, in.BookingID, in.botMobilityArgs)
}

func (s *Server) botMobilityPropose(ctx context.Context, rid int, msg botWebhookMessage, actor, origin string, bookingID int64, in botMobilityArgs) (string, error) {
	row, err := s.botLoadSpecialRow(ctx, rid, bookingID)
	if err != nil {
		return botJSON(map[string]any{"error": "no hay una reserva con ese id"}), nil
	}
	if rerr := botSpecialRowAllowedFor(row, "editan", "edita"); rerr != nil {
		return botJSON(map[string]any{"error": rerr.Error()}), nil
	}
	cur, cerr := botMobilityScanCurrent(s.db.QueryRowContext(ctx, botMobilityCurrentSelect, rid, bookingID))
	if cerr != nil {
		return botJSON(map[string]any{"error": "no se pudo leer la reserva"}), nil
	}
	newHas, newPeople := botMobilityResolve(cur, in)
	if newHas == cur.Has && newPeople == cur.People {
		return botJSON(map[string]any{"error": "ningún campo cambiaría: la reserva ya tiene esos valores"}), nil
	}
	beforeHash, herr := botFullRowHash(ctx, s.db, rid, bookingID)
	if herr != nil {
		return botJSON(map[string]any{"error": "no se pudo leer la reserva"}), nil
	}
	code, cerr := botSpecialNewCode()
	if cerr != nil {
		return botJSON(map[string]any{"error": "no se pudo generar la propuesta"}), nil
	}
	preview := fmt.Sprintf("Propuesta: MOVILIDAD de la reserva #%d (%s %s, %d comensales): %s. No cambian fecha, hora, comensales, salón, menús ni ocupación. No se avisa al cliente.",
		bookingID, row.Date, row.Time, row.Party, botMobilityChangeText(in, newHas, newPeople))
	after := botMobilityAfterKey(bookingID, newHas, newPeople)
	return s.botProposalPersist(ctx, rid, msg, "set_mobility", bookingID, in, "", actor, origin, beforeHash, after, preview, code)
}

// botMobilityExecute runs INSIDE the claim transaction: claim + narrow column
// write + others-hash verification + result_json + event commit together.
func (s *Server) botMobilityExecute(ctx context.Context, tx *sql.Tx, rid int, msg botWebhookMessage, p botSpecialProposalRow) (string, error) {
	stale := func(why string) (string, error) {
		_ = tx.Rollback()
		s.markStale(ctx, rid, p, msg)
		return "", errors.New(why)
	}
	var in botMobilityArgs
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
	cur, cerr := botMobilityScanCurrent(tx.QueryRowContext(ctx, botMobilityCurrentSelect, rid, p.BookingID))
	if cerr != nil {
		return "", errors.New("error interno")
	}
	newHas, newPeople := botMobilityResolve(cur, in)
	if newHas == cur.Has && newPeople == cur.People {
		return stale("los campos ya tienen los valores pedidos; pide una propuesta nueva")
	}
	if botMobilityAfterKey(p.BookingID, newHas, newPeople) != p.After {
		return stale("el resultado ya no coincide con la vista previa; pide una propuesta nueva")
	}
	changes := []botEditChange{}
	if newHas != cur.Has {
		changes = append(changes, botEditChange{Col: "has_mobility_issues", Val: boolToTinyint(newHas)})
	}
	if newPeople != cur.People {
		changes = append(changes, botEditChange{Col: "mobility_people", Val: newPeople})
	}
	cols := make([]string, 0, len(changes))
	for _, c := range changes {
		if !botMobilityColumns[c.Col] {
			return "", errors.New("columna no permitida; no se aplicó nada")
		}
		cols = append(cols, c.Col)
	}
	preOthers, err := botFullRowHash(ctx, tx, rid, p.BookingID, cols...)
	if err != nil {
		return "", errors.New("error interno")
	}
	sets := make([]string, len(changes))
	vals := make([]any, len(changes))
	for i, c := range changes {
		sets[i] = "`" + c.Col + "` = ?"
		vals[i] = c.Val
	}
	vals = append(vals, rid, p.BookingID)
	if _, err := tx.ExecContext(ctx, "UPDATE bookings SET "+strings.Join(sets, ", ")+" WHERE restaurant_id = ? AND id = ?", vals...); err != nil {
		return "", errors.New("error al guardar; no se aplicó nada")
	}
	postOthers, err := botFullRowHash(ctx, tx, rid, p.BookingID, cols...)
	if err != nil || postOthers != preOthers {
		return "", errors.New("verificación fallida: cambió una columna fuera de la edición; no se aplicó nada")
	}
	// The panel does not record mobility changes in booking_modifications; parity.
	notice := fmt.Sprintf("Reserva #%d: %s. No se ha avisado al cliente.", p.BookingID, botMobilityChangeText(in, newHas, newPeople))
	rj, _ := json.Marshal(map[string]any{"booking_id": p.BookingID, "op": p.Op, "notice": notice})
	if _, err := tx.ExecContext(ctx, `UPDATE wa_bot_op_proposals SET result_json=?, notify_state='pending' WHERE id=?`, string(rj), p.ID); err != nil {
		return "", errors.New("error interno")
	}
	s.botSpecialAdminEvent(ctx, tx, rid, p.ID, "executed", p.Actor, strings.TrimSpace(msg.ChatJID), map[string]any{"op": p.Op, "booking_id": p.BookingID, "before_hash": p.Before, "after_hash": p.After, "changed_columns": cols, "approval_message_id": msg.MessageID})
	if err := tx.Commit(); err != nil {
		log.Printf("[bot] wa_bot_admin_set_mobility_v1 commit_unknown rid=%d proposal=%d err=%v", rid, p.ID, err)
		return "", errors.New("no pude confirmar si se guardó; consulta la reserva antes de repetir")
	}
	s.broadcastBookingChanged(rid, p.BookingID, "booking_updated")
	return notice, nil
}
