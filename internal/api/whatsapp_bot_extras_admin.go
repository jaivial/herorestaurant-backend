package api

// Admin extras tool (slice 4e): booking_propose_set_extras proposes replacing
// the extras selection of a booking. Same mold as the other admin ops:
// propose + exact "OK <code>" by the same actor in the same chat, narrow
// column write inside the claim transaction, full-row version check,
// others-hash guard and durable result.
//
// Canonical contract (panel): extras are stored in extras_json as a snapshot
// array of {id, slug, name} (bookingExtrasSnapshotJSON), validated against
// the restaurant's ACTIVE extras catalog (resolveBookingExtras), in catalog
// order. Extras apply with and without a group menu. On group-menu bookings
// the derived commentary carries the extras names, so changing extras
// rebuilds the commentary exactly like a panel patch that preserves extras
// (mergeExtrasIntoCommentary over the principales summary).
//
// Preservation rule (load-bearing, same as 4d): on a group-menu booking a
// stored commentary that is not exactly the derived-without-note text holds
// a free note that cannot be separated; the operation is then REJECTED
// unless the actor passes replace_commentary:true, and the preview states
// the old text is discarded without showing it.
//
// Strict deviations from the panel (fail-closed, declared): unknown or
// inactive extra ids are rejected instead of silently dropped.
//
// The panel does NOT record extras changes in booking_modifications, so this
// tool does not either (parity). No customer notification, no n8n, no
// occupancy or payment effect.
//
// Coordination id: wa_bot_admin_set_extras_v1

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
)

type botExtrasArgs struct {
	Extras            []int64 `json:"extras"`
	ReplaceCommentary bool    `json:"replace_commentary,omitempty"`
}

// botExtrasColumns is the closed set of columns this op may write.
var botExtrasColumns = map[string]bool{"extras_json": true, "commentary": true}

type botExtrasCurrent struct {
	Name, Phone     string
	GroupMenu       bool
	Party           int
	PrincipalesJSON string
	Commentary      string
	CommentarySet   bool
	ExtrasJSON      string
}

const botExtrasCurrentSelect = `SELECT customer_name, COALESCE(contact_phone,''), COALESCE(special_menu,0), party_size, COALESCE(principales_json,''), commentary, COALESCE(extras_json,'') FROM bookings WHERE restaurant_id = ? AND id = ?`

func botExtrasScanCurrent(row *sql.Row) (*botExtrasCurrent, error) {
	var c botExtrasCurrent
	var comm sql.NullString
	var gm int64
	if err := row.Scan(&c.Name, &c.Phone, &gm, &c.Party, &c.PrincipalesJSON, &comm, &c.ExtrasJSON); err != nil {
		return nil, err
	}
	c.GroupMenu = gm != 0
	c.Commentary, c.CommentarySet = comm.String, comm.Valid
	return &c, nil
}

// botExtrasQuerier is satisfied by both *sql.DB (propose) and *sql.Tx
// (execute), so the catalog read rides the claim transaction when there is one.
type botExtrasQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// botLoadBookingExtras mirrors loadRestaurantBookingExtras (same SELECT,
// verbatim) with a context and a querier so it can ride the claim
// transaction. Parity: internal/api/booking_extras.go:30.
func botLoadBookingExtras(ctx context.Context, q botExtrasQuerier, restaurantID int) ([]bookingExtra, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT id, slug, name, is_custom, active
		FROM restaurant_booking_extras
		WHERE restaurant_id = ? AND active = 1
		ORDER BY is_custom ASC, id ASC
	`, restaurantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]bookingExtra, 0, 12)
	for rows.Next() {
		var (
			extra  bookingExtra
			custom int
			active int
		)
		if err := rows.Scan(&extra.ID, &extra.Slug, &extra.Name, &custom, &active); err != nil {
			return nil, err
		}
		extra.Custom = custom != 0
		extra.Active = active != 0
		out = append(out, extra)
	}
	return out, rows.Err()
}

type botExtrasResolved struct {
	ExtrasVal         any // snapshot JSON string or nil
	ExtrasNames       []string
	CommentaryVal     any    // only when GroupMenu: derived commentary (string or nil)
	DerivedCommentary string // display form when GroupMenu
	HadFreeCommentary bool
}

// botExtrasResolve validates the requested ids against the live catalog and
// computes the exact values to write. Shared by propose and execute.
func (s *Server) botExtrasResolve(ctx context.Context, q botExtrasQuerier, rid int, cur *botExtrasCurrent, in botExtrasArgs) (*botExtrasResolved, error) {
	all, err := botLoadBookingExtras(ctx, q, rid)
	if err != nil {
		return nil, errors.New("no se pudieron consultar los extras")
	}
	// Strict: every requested id must exist and be active (the panel silently
	// drops unknown ids; here that is an error).
	wanted := make(map[int64]bool, len(in.Extras))
	for _, id := range in.Extras {
		if id <= 0 {
			return nil, fmt.Errorf("id de extra inválido: %d", id)
		}
		wanted[id] = true
	}
	catalog := map[int64]bool{}
	picked := make([]bookingExtra, 0, len(wanted))
	for _, extra := range all {
		catalog[extra.ID] = true
		if wanted[extra.ID] {
			picked = append(picked, extra)
		}
	}
	for id := range wanted {
		if !catalog[id] {
			return nil, fmt.Errorf("el extra %d no existe o no está activo; usa los ids de la lista de extras", id)
		}
	}
	r := &botExtrasResolved{}
	for _, e := range picked {
		r.ExtrasNames = append(r.ExtrasNames, e.Name)
	}
	if v := bookingExtrasSnapshotJSON(picked); v != nil {
		if s, ok := v.(string); ok {
			r.ExtrasVal = s
		} else {
			b, _ := json.Marshal(v)
			r.ExtrasVal = string(b)
		}
	}
	if cur.GroupMenu {
		summary := botGMenuSummaryFromPrincipales(cur.PrincipalesJSON)
		derived := mergeExtrasIntoCommentary(buildGroupMenuCommentary(summary, nil, ""), r.ExtrasNames)
		storedComm := ""
		if cur.CommentarySet {
			storedComm = strings.TrimSpace(cur.Commentary)
		}
		expectedNoNote := mergeExtrasIntoCommentary(buildGroupMenuCommentary(summary, nil, ""), bookingExtraNames(cur.ExtrasJSON))
		r.HadFreeCommentary = storedComm != strings.TrimSpace(expectedNoNote)
		if r.HadFreeCommentary && !in.ReplaceCommentary {
			return nil, errors.New("la reserva tiene un comentario con texto libre que se perdería; si quieres sustituirlo por el generado, repite con replace_commentary:true")
		}
		r.DerivedCommentary = derived
		if strings.TrimSpace(derived) != "" {
			r.CommentaryVal = derived
		}
	}
	return r, nil
}

// botExtrasChanges computes the actual column writes (only effective changes).
func botExtrasChanges(cur *botExtrasCurrent, r *botExtrasResolved) []botEditChange {
	var out []botEditChange
	newExtras := ""
	if s, ok := r.ExtrasVal.(string); ok {
		newExtras = strings.TrimSpace(s)
	}
	if strings.TrimSpace(cur.ExtrasJSON) != newExtras {
		out = append(out, botEditChange{Col: "extras_json", Val: r.ExtrasVal})
	}
	if cur.GroupMenu {
		curComm := ""
		if cur.CommentarySet {
			curComm = cur.Commentary
		}
		if strings.TrimSpace(curComm) != strings.TrimSpace(r.DerivedCommentary) {
			out = append(out, botEditChange{Col: "commentary", Val: r.CommentaryVal})
		}
	}
	return out
}

func botExtrasAfterKey(bookingID int64, changes []botEditChange, in botExtrasArgs) string {
	ids := append([]int64(nil), in.Extras...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	m := map[string]any{"booking_id": bookingID, "extras": ids}
	for _, c := range changes {
		m[c.Col] = c.Val
	}
	return botSha("extrasv1|" + botCanon(m))
}

// botExtrasChangeText builds the shared preview/notice sentence. Extras names
// are catalog data, not personal data; the old commentary is never shown.
func botExtrasChangeText(cur *botExtrasCurrent, r *botExtrasResolved) string {
	oldNames := bookingExtraNames(cur.ExtrasJSON)
	oldDisp := "(sin extras)"
	if len(oldNames) > 0 {
		oldDisp = strings.Join(oldNames, ", ")
	}
	newDisp := "(sin extras)"
	if len(r.ExtrasNames) > 0 {
		newDisp = strings.Join(r.ExtrasNames, ", ")
	}
	text := fmt.Sprintf("extras: «%s» → «%s»", oldDisp, newDisp)
	if len(r.ExtrasNames) == 0 && len(oldNames) > 0 {
		text = fmt.Sprintf("extras: «%s» → (sin extras): QUITA todos los extras", oldDisp)
	}
	if cur.GroupMenu {
		comm := "(sin comentario)"
		if strings.TrimSpace(r.DerivedCommentary) != "" {
			comm = "«" + r.DerivedCommentary + "»"
		}
		text += fmt.Sprintf(". El comentario generado se actualiza: %s", comm)
		if r.HadFreeCommentary {
			text += " El comentario anterior se descarta (contenido no mostrado por privacidad)."
		}
	}
	return text
}

func (s *Server) botToolExtrasPropose(ctx context.Context, rid int, msg botWebhookMessage, input json.RawMessage) (string, error) {
	actor, origin, ok := s.botSpecialAdminAuthorize(ctx, rid, msg)
	if !ok {
		return botJSON(map[string]any{"error": "no autorizado: solo para miembros verificados del grupo de gestión"}), nil
	}
	s.botSpecialAdminFlushNotify(ctx, rid)
	var in struct {
		BookingID         int64    `json:"booking_id"`
		Extras            *[]int64 `json:"extras"`
		ReplaceCommentary bool     `json:"replace_commentary,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(input))
	dec.DisallowUnknownFields()
	// extras must be present as an array: absent or null is NOT a clear-all,
	// only an explicit [] clears the selection.
	if dec.Decode(&in) != nil || in.BookingID <= 0 || in.Extras == nil {
		return botJSON(map[string]any{"error": "argumentos inválidos: booking_id y extras (lista de ids; vacía [] para quitarlos; ausente o null no vale)"}), nil
	}
	return s.botExtrasPropose(ctx, rid, msg, actor, origin, in.BookingID, botExtrasArgs{Extras: *in.Extras, ReplaceCommentary: in.ReplaceCommentary})
}

func (s *Server) botExtrasPropose(ctx context.Context, rid int, msg botWebhookMessage, actor, origin string, bookingID int64, in botExtrasArgs) (string, error) {
	row, err := s.botLoadSpecialRow(ctx, rid, bookingID)
	if err != nil {
		return botJSON(map[string]any{"error": "no hay una reserva con ese id"}), nil
	}
	if rerr := botSpecialRowAllowedFor(row, "editan", "edita"); rerr != nil {
		return botJSON(map[string]any{"error": rerr.Error()}), nil
	}
	if row.Special {
		return botJSON(map[string]any{"error": "la reserva es de fecha especial: sus extras van en el bloque especial y se cambian desde el panel"}), nil
	}
	cur, cerr := botExtrasScanCurrent(s.db.QueryRowContext(ctx, botExtrasCurrentSelect, rid, bookingID))
	if cerr != nil {
		return botJSON(map[string]any{"error": "no se pudo leer la reserva"}), nil
	}
	r, rerr := s.botExtrasResolve(ctx, s.db, rid, cur, in)
	if rerr != nil {
		return botJSON(map[string]any{"error": rerr.Error()}), nil
	}
	changes := botExtrasChanges(cur, r)
	if len(changes) == 0 {
		return botJSON(map[string]any{"error": "ningún campo cambiaría: la reserva ya tiene esos extras"}), nil
	}
	beforeHash, herr := botFullRowHash(ctx, s.db, rid, bookingID)
	if herr != nil {
		return botJSON(map[string]any{"error": "no se pudo leer la reserva"}), nil
	}
	code, cerr := botSpecialNewCode()
	if cerr != nil {
		return botJSON(map[string]any{"error": "no se pudo generar la propuesta"}), nil
	}
	preview := fmt.Sprintf("Propuesta: EXTRAS de la reserva #%d (%s %s, %d comensales): %s. No cambian fecha, hora, comensales, salón, menús ni ocupación. No se avisa al cliente.",
		bookingID, row.Date, row.Time, row.Party, botExtrasChangeText(cur, r))
	after := botExtrasAfterKey(bookingID, changes, in)
	return s.botProposalPersist(ctx, rid, msg, "set_extras", bookingID, in, "", actor, origin, beforeHash, after, preview, code)
}

// botExtrasExecute runs INSIDE the claim transaction: claim + narrow column
// write + others-hash verification + result_json + event commit together.
func (s *Server) botExtrasExecute(ctx context.Context, tx *sql.Tx, rid int, msg botWebhookMessage, p botSpecialProposalRow) (string, error) {
	stale := func(why string) (string, error) {
		_ = tx.Rollback()
		s.markStale(ctx, rid, p, msg)
		return "", errors.New(why)
	}
	var in botExtrasArgs
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
	if row.Special {
		return stale("la reserva ahora es de fecha especial; pide una propuesta nueva")
	}
	full, err := botFullRowHash(ctx, tx, rid, p.BookingID)
	if err != nil {
		return "", errors.New("error interno")
	}
	if full != p.Before {
		return stale(errBotSpecialStale.Error())
	}
	cur, cerr := botExtrasScanCurrent(tx.QueryRowContext(ctx, botExtrasCurrentSelect, rid, p.BookingID))
	if cerr != nil {
		return "", errors.New("error interno")
	}
	r, rerr := s.botExtrasResolve(ctx, tx, rid, cur, in)
	if rerr != nil {
		return stale("la validación ya no pasa: " + rerr.Error())
	}
	changes := botExtrasChanges(cur, r)
	if len(changes) == 0 {
		return stale("la reserva ya tiene esos extras; pide una propuesta nueva")
	}
	if botExtrasAfterKey(p.BookingID, changes, in) != p.After {
		return stale("el resultado ya no coincide con la vista previa; pide una propuesta nueva")
	}
	cols := make([]string, 0, len(changes))
	for _, c := range changes {
		if !botExtrasColumns[c.Col] {
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
	// The panel does not record extras changes in booking_modifications; parity.
	notice := fmt.Sprintf("Reserva #%d: %s. No se ha avisado al cliente.", p.BookingID, botExtrasChangeText(cur, r))
	rj, _ := json.Marshal(map[string]any{"booking_id": p.BookingID, "op": p.Op, "notice": notice})
	if _, err := tx.ExecContext(ctx, `UPDATE wa_bot_op_proposals SET result_json=?, notify_state='pending' WHERE id=?`, string(rj), p.ID); err != nil {
		return "", errors.New("error interno")
	}
	s.botSpecialAdminEvent(ctx, tx, rid, p.ID, "executed", p.Actor, strings.TrimSpace(msg.ChatJID), map[string]any{"op": p.Op, "booking_id": p.BookingID, "before_hash": p.Before, "after_hash": p.After, "changed_columns": cols, "approval_message_id": msg.MessageID})
	if err := tx.Commit(); err != nil {
		log.Printf("[bot] wa_bot_admin_set_extras_v1 commit_unknown rid=%d proposal=%d err=%v", rid, p.ID, err)
		return "", errors.New("no pude confirmar si se guardó; consulta la reserva antes de repetir")
	}
	s.broadcastBookingChanged(rid, p.BookingID, "booking_updated")
	return notice, nil
}
