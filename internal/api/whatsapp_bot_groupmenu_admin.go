package api

// Admin group-menu tool (slice 4d): booking_propose_set_group_menu proposes
// setting or changing the group menu of a booking. Same mold as the other
// admin ops: propose + exact "OK <code>" by the same actor in the same chat.
// Special-date bookings are rejected fail-closed: their food lives in the
// special_json block and the group-menu/special interaction is not verified.
// narrow column write inside the claim transaction, full-row version check,
// others-hash guard, panel-parity history and durable result.
//
// Canonical contract (panel boNormalizeAndValidateBookingInput +
// handleBOBookingPatch):
//   - menu must exist and be active (menus table, per restaurant);
//   - arroz_type/arroz_servings become the menu marker ["<title>"]/[party];
//   - principales_json is validated by buildPrincipalesSummaryAndJSON against
//     the menu's allow-list, deduped, total servings <= party_size;
//   - extras are NOT touched; the stored extras stay and their names are
//     merged into the derived commentary exactly like the panel does when a
//     patch preserves extras (mergeExtrasIntoCommentary);
//   - commentary is derived: principales summary + extras + the actor's note.
//
// Preservation rule (load-bearing): a stored commentary that is not exactly
// the derived-without-note text holds a free note (possibly from the client)
// that cannot be separated reliably. The operation is then REJECTED unless
// the actor passes replace_commentary:true, and the preview states the old
// text is discarded without showing it.
//
// Strict deviations from the panel (fail-closed, declared): unknown or
// duplicated principales names are rejected instead of silently skipped.
//
// No payment execution and no stored amounts exist on bookings; the menu
// price shown in the preview is informational, read live from the menus
// table. No customer notification, no n8n, no occupancy effect.
//
// Coordination id: wa_bot_admin_set_group_menu_v1

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

type botGMenuPrincipal struct {
	Name     string `json:"name"`
	Servings int    `json:"servings"`
}

type botGMenuArgs struct {
	MenuDeGrupoID     int                 `json:"menu_de_grupo_id"`
	Principales       []botGMenuPrincipal `json:"principales,omitempty"`
	Note              *string             `json:"note,omitempty"`
	ReplaceCommentary bool                `json:"replace_commentary,omitempty"`
	PartySize         int                 `json:"party_size,omitempty"`
}

// botGMenuColumns is the closed set of columns this op may write.
var botGMenuColumns = map[string]bool{
	"special_menu": true, "menu_de_grupo_id": true, "menu_de_grupo_assigned": true,
	"arroz_type": true, "arroz_servings": true, "principales_json": true, "commentary": true, "party_size": true,
}

type botGMenuCurrent struct {
	Name, Phone        string
	GroupMenu          bool
	MenuID             int
	Party              int
	TypeJSON, ServJSON string
	TypeSet, ServSet   bool
	PrincipalesJSON    string
	Commentary         string
	CommentarySet      bool
	ExtrasJSON         string
}

const botGMenuCurrentSelect = `SELECT customer_name, COALESCE(contact_phone,''), COALESCE(special_menu,0), COALESCE(menu_de_grupo_id,0), party_size, arroz_type, arroz_servings, COALESCE(principales_json,''), commentary, COALESCE(extras_json,'') FROM bookings WHERE restaurant_id = ? AND id = ?`

func botGMenuScanCurrent(row *sql.Row) (*botGMenuCurrent, error) {
	var c botGMenuCurrent
	var t, sv, comm sql.NullString
	var gm int64
	if err := row.Scan(&c.Name, &c.Phone, &gm, &c.MenuID, &c.Party, &t, &sv, &c.PrincipalesJSON, &comm, &c.ExtrasJSON); err != nil {
		return nil, err
	}
	c.GroupMenu = gm != 0
	c.TypeJSON, c.TypeSet = t.String, t.Valid
	c.ServJSON, c.ServSet = sv.String, sv.Valid
	c.Commentary, c.CommentarySet = comm.String, comm.Valid
	return &c, nil
}

// botGMenuQuerier is satisfied by both *sql.DB (propose) and *sql.Tx
// (execute), so the menu read rides the claim transaction when there is one.
type botGMenuQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// botGMenuFetchMenu reads the menu the panel would accept: active, of this
// restaurant. Price is informational for the preview.
func (s *Server) botGMenuFetchMenu(ctx context.Context, q botGMenuQuerier, rid, menuID int) (title, principalesRaw, price string, err error) {
	var p sql.NullString
	err = q.QueryRowContext(ctx, `SELECT menu_title, principales, COALESCE(price,'') FROM menus WHERE restaurant_id = ? AND id = ? AND active = 1 LIMIT 1`, rid, menuID).Scan(&title, &p, &price)
	if err != nil {
		return "", "", "", err
	}
	if p.Valid {
		principalesRaw = p.String
	}
	return title, principalesRaw, price, nil
}

// botGMenuSummaryFromPrincipales rebuilds the summary string from stored
// principales_json using the panel's own formatting ("name x N, ...").
func botGMenuSummaryFromPrincipales(stored string) string {
	var rows []map[string]any
	if json.Unmarshal([]byte(strings.TrimSpace(stored)), &rows) != nil {
		return ""
	}
	parts := []string{}
	for _, row := range rows {
		name := strings.TrimSpace(anyToString(row["name"]))
		servings, _ := anyToInt(row["servings"])
		if name == "" || servings <= 0 {
			continue
		}
		parts = append(parts, name+" x "+fmt.Sprint(servings))
	}
	return strings.Join(parts, ", ")
}

type botGMenuResolved struct {
	Title, Price      string
	Summary           string
	PrincipalesVal    any // string or nil
	CommentaryVal     any // string or nil
	ArrozTypeVal      string
	ArrozServVal      string
	DerivedCommentary string
	HadFreeCommentary bool
	OldMenuTitle      string
}

// botGMenuResolve validates the request against the live menu and the current
// row and computes the exact values to write. Shared by propose and execute.
func (s *Server) botGMenuResolve(ctx context.Context, q botGMenuQuerier, rid int, cur *botGMenuCurrent, in botGMenuArgs) (*botGMenuResolved, error) {
	if in.MenuDeGrupoID <= 0 {
		return nil, errors.New("menu_de_grupo_id inválido")
	}
	title, menuPrincipalesRaw, price, err := s.botGMenuFetchMenu(ctx, q, rid, in.MenuDeGrupoID)
	if err != nil || strings.TrimSpace(title) == "" {
		return nil, errors.New("menú de grupo no válido o inactivo")
	}
	// Strict pre-validation of principales against the menu allow-list
	// (the panel silently skips unknown names; here they are an error).
	allowed := map[string]bool{}
	if strings.TrimSpace(menuPrincipalesRaw) != "" {
		var mp struct {
			Items []string `json:"items"`
		}
		if err := json.Unmarshal([]byte(menuPrincipalesRaw), &mp); err == nil {
			for _, it := range mp.Items {
				if it = strings.TrimSpace(it); it != "" {
					allowed[it] = true
				}
			}
		}
	}
	seen := map[string]bool{}
	rows := make([]map[string]any, 0, len(in.Principales))
	for _, pr := range in.Principales {
		name := strings.TrimSpace(pr.Name)
		if name == "" {
			return nil, errors.New("principal con nombre vacío")
		}
		if len(allowed) > 0 && !allowed[name] {
			return nil, fmt.Errorf("«%s» no está entre los principales de ese menú", name)
		}
		if seen[name] {
			return nil, fmt.Errorf("principal repetido: %s", name)
		}
		seen[name] = true
		if pr.Servings <= 0 {
			return nil, fmt.Errorf("raciones inválidas para %s", name)
		}
		rows = append(rows, map[string]any{"name": name, "servings": pr.Servings})
	}
	rowsRaw := "[]"
	if len(rows) > 0 {
		b, _ := json.Marshal(rows)
		rowsRaw = string(b)
	}
	summary, storedJSON, err := buildPrincipalesSummaryAndJSON(menuPrincipalesRaw, rowsRaw, cur.Party)
	if err != nil {
		return nil, err
	}
	// Extras are preserved, never touched; the derived commentary merges them
	// exactly like the panel does when a patch preserves extras.
	extrasNames := bookingExtraNames(cur.ExtrasJSON)
	note := ""
	if in.Note != nil {
		note = strings.TrimSpace(*in.Note)
	}
	derived := mergeExtrasIntoCommentary(buildGroupMenuCommentary(summary, nil, note), extrasNames)
	// Preservation: a stored commentary that is not exactly the
	// derived-without-note text holds a free note that would be lost.
	storedComm := ""
	if cur.CommentarySet {
		storedComm = strings.TrimSpace(cur.Commentary)
	}
	var expectedNoNote string
	if cur.GroupMenu {
		expectedNoNote = mergeExtrasIntoCommentary(buildGroupMenuCommentary(botGMenuSummaryFromPrincipales(cur.PrincipalesJSON), nil, ""), extrasNames)
	}
	hadFree := storedComm != strings.TrimSpace(expectedNoNote)
	if hadFree && !in.ReplaceCommentary {
		return nil, errors.New("la reserva tiene un comentario con texto libre que se perdería; si quieres sustituirlo por el generado, repite con replace_commentary:true")
	}
	bt, _ := json.Marshal([]string{title})
	bs, _ := json.Marshal([]int{cur.Party})
	r := &botGMenuResolved{
		Title:             title,
		Price:             strings.TrimSpace(price),
		Summary:           summary,
		ArrozTypeVal:      string(bt),
		ArrozServVal:      string(bs),
		DerivedCommentary: derived,
		HadFreeCommentary: hadFree,
	}
	if strings.TrimSpace(storedJSON) != "" {
		r.PrincipalesVal = storedJSON
	}
	if strings.TrimSpace(derived) != "" {
		r.CommentaryVal = derived
	}
	if cur.GroupMenu && cur.MenuID > 0 && cur.MenuID != in.MenuDeGrupoID {
		var oldTitle string
		if q.QueryRowContext(ctx, `SELECT menu_title FROM menus WHERE restaurant_id = ? AND id = ? LIMIT 1`, rid, cur.MenuID).Scan(&oldTitle) == nil && strings.TrimSpace(oldTitle) != "" {
			r.OldMenuTitle = oldTitle
		} else {
			r.OldMenuTitle = "(menú actual no disponible)"
		}
	}
	return r, nil
}

// botGMenuChanges computes the actual column writes (only effective changes).
func botGMenuChanges(cur *botGMenuCurrent, menuID int, r *botGMenuResolved) []botEditChange {
	var out []botEditChange
	add := func(col string, val any) {
		if !botGMenuColumns[col] {
			return
		}
		out = append(out, botEditChange{Col: col, Val: val})
	}
	if !cur.GroupMenu {
		add("special_menu", 1)
		add("menu_de_grupo_assigned", 1)
	}
	if cur.MenuID != menuID {
		add("menu_de_grupo_id", menuID)
	}
	if !cur.TypeSet || cur.TypeJSON != r.ArrozTypeVal {
		add("arroz_type", r.ArrozTypeVal)
	}
	if !cur.ServSet || cur.ServJSON != r.ArrozServVal {
		add("arroz_servings", r.ArrozServVal)
	}
	curPrin := strings.TrimSpace(cur.PrincipalesJSON)
	newPrin := ""
	if s, ok := r.PrincipalesVal.(string); ok {
		newPrin = strings.TrimSpace(s)
	}
	if curPrin != newPrin {
		add("principales_json", r.PrincipalesVal)
	}
	curComm := ""
	if cur.CommentarySet {
		curComm = cur.Commentary
	}
	newComm := ""
	if s, ok := r.CommentaryVal.(string); ok {
		newComm = s
	}
	if strings.TrimSpace(curComm) != strings.TrimSpace(newComm) {
		add("commentary", r.CommentaryVal)
	}
	return out
}

// botGMenuAfterKey binds the shown menu price too: it is a mutable reference
// (menus.price, never written to bookings), so a price change between propose
// and OK must stale the proposal instead of executing under a false preview.
func botGMenuAfterKey(bookingID int64, menuID int, price string, changes []botEditChange) string {
	m := map[string]any{"booking_id": bookingID, "menu_de_grupo_id": menuID, "menu_price": price}
	for _, c := range changes {
		m[c.Col] = c.Val
	}
	return botSha("gmenuv1|" + botCanon(m))
}

// botGMenuChangeText builds the shared preview/notice sentence. The new menu
// title, price, principales summary and derived commentary are not personal
// data; the old commentary is never shown.
func botGMenuChangeText(bookingID int64, cur *botGMenuCurrent, menuID int, r *botGMenuResolved) string {
	menuPart := ""
	switch {
	case !cur.GroupMenu:
		menuPart = "pone menú de grupo"
	case cur.MenuID != menuID:
		menuPart = fmt.Sprintf("cambia el menú de grupo «%s»", r.OldMenuTitle)
	default:
		menuPart = "actualiza el menú de grupo"
	}
	price := ""
	if r.Price != "" {
		price = " (precio: " + r.Price + ", solo informativo: no se cobra nada)"
	}
	prin := "(sin principales)"
	if strings.TrimSpace(r.Summary) != "" {
		prin = r.Summary
	}
	comm := "(sin comentario)"
	if strings.TrimSpace(r.DerivedCommentary) != "" {
		comm = "«" + r.DerivedCommentary + "»"
	}
	text := fmt.Sprintf("%s: menú «%s»%s. Principales: %s. El arroz pasa a ser el marcador del menú. El comentario se genera solo: %s. Los extras se conservan.",
		menuPart, r.Title, price, prin, comm)
	if r.HadFreeCommentary {
		text += " El comentario anterior se descarta (contenido no mostrado por privacidad)."
	}
	return text
}

func (s *Server) botToolGMenuPropose(ctx context.Context, rid int, msg botWebhookMessage, input json.RawMessage) (string, error) {
	actor, origin, ok := s.botSpecialAdminAuthorize(ctx, rid, msg)
	if !ok {
		return botJSON(map[string]any{"error": "no autorizado: solo para miembros verificados del grupo de gestión"}), nil
	}
	s.botSpecialAdminFlushNotify(ctx, rid)
	var in struct {
		BookingID int64 `json:"booking_id"`
		botGMenuArgs
	}
	dec := json.NewDecoder(bytes.NewReader(input))
	dec.DisallowUnknownFields()
	if dec.Decode(&in) != nil || in.BookingID <= 0 {
		return botJSON(map[string]any{"error": "argumentos inválidos: booking_id y menu_de_grupo_id"}), nil
	}
	return s.botGMenuPropose(ctx, rid, msg, actor, origin, in.BookingID, in.botGMenuArgs)
}

func (s *Server) botGMenuPropose(ctx context.Context, rid int, msg botWebhookMessage, actor, origin string, bookingID int64, in botGMenuArgs) (string, error) {
	row, err := s.botLoadSpecialRow(ctx, rid, bookingID)
	if err != nil {
		return botJSON(map[string]any{"error": "no hay una reserva con ese id"}), nil
	}
	if rerr := botSpecialRowAllowedFor(row, "editan", "edita"); rerr != nil {
		return botJSON(map[string]any{"error": rerr.Error()}), nil
	}
	if row.Special {
		return botJSON(map[string]any{"error": "la reserva es de fecha especial: sus menús van en el bloque especial y se cambian desde el panel"}), nil
	}
	cur, cerr := botGMenuScanCurrent(s.db.QueryRowContext(ctx, botGMenuCurrentSelect, rid, bookingID))
	if cerr != nil {
		return botJSON(map[string]any{"error": "no se pudo leer la reserva"}), nil
	}
	if in.MenuDeGrupoID <= 0 && cur.GroupMenu && cur.MenuID > 0 {
		in.MenuDeGrupoID = cur.MenuID
	}
	if in.PartySize < 0 || in.PartySize > 500 {
		return botJSON(map[string]any{"error": "party_size inválido (1-500)"}), nil
	}
	origParty := cur.Party
	cur = botGMenuWithParty(cur, in.PartySize)
	r, rerr := s.botGMenuResolve(ctx, s.db, rid, cur, in)
	if rerr != nil {
		return botJSON(map[string]any{"error": rerr.Error()}), nil
	}
	changes := botGMenuChanges(cur, in.MenuDeGrupoID, r)
	changes = botGMenuAddParty(changes, origParty, in.PartySize)
	if len(changes) == 0 {
		return botJSON(map[string]any{"error": "ningún campo cambiaría: la reserva ya tiene ese menú"}), nil
	}
	beforeHash, herr := botFullRowHash(ctx, s.db, rid, bookingID)
	if herr != nil {
		return botJSON(map[string]any{"error": "no se pudo leer la reserva"}), nil
	}
	code, cerr := botSpecialNewCode()
	if cerr != nil {
		return botJSON(map[string]any{"error": "no se pudo generar la propuesta"}), nil
	}
	preview := fmt.Sprintf("Propuesta: MENÚ DE GRUPO en la reserva #%d (%s %s, %d comensales): %s. No cambian fecha, hora, comensales, salón ni ocupación. No se avisa al cliente.",
		bookingID, row.Date, row.Time, row.Party, botGMenuChangeText(bookingID, cur, in.MenuDeGrupoID, r))
	if pt := botGMenuPartyText(origParty, in.PartySize); pt != "" {
		preview = strings.Replace(preview, "No cambian fecha, hora, comensales,", "No cambian fecha, hora,", 1) + pt
	}
	after := botGMenuAfterKey(bookingID, in.MenuDeGrupoID, r.Price, changes)
	return s.botProposalPersist(ctx, rid, msg, "set_group_menu", bookingID, in, "", actor, origin, beforeHash, after, preview, code)
}

// botGMenuExecute runs INSIDE the claim transaction: claim + narrow column
// write + others-hash verification + panel-parity rice history + result_json
// + event commit together.
func (s *Server) botGMenuExecute(ctx context.Context, tx *sql.Tx, rid int, msg botWebhookMessage, p botSpecialProposalRow) (string, error) {
	stale := func(why string) (string, error) {
		_ = tx.Rollback()
		s.markStale(ctx, rid, p, msg)
		return "", errors.New(why)
	}
	var in botGMenuArgs
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
	cur, cerr := botGMenuScanCurrent(tx.QueryRowContext(ctx, botGMenuCurrentSelect, rid, p.BookingID))
	if cerr != nil {
		return "", errors.New("error interno")
	}
	origParty := cur.Party
	cur = botGMenuWithParty(cur, in.PartySize)
	r, rerr := s.botGMenuResolve(ctx, tx, rid, cur, in)
	if rerr != nil {
		return stale("la validación ya no pasa: " + rerr.Error())
	}
	changes := botGMenuChanges(cur, in.MenuDeGrupoID, r)
	changes = botGMenuAddParty(changes, origParty, in.PartySize)
	if len(changes) == 0 {
		return stale("la reserva ya tiene ese menú; pide una propuesta nueva")
	}
	if botGMenuAfterKey(p.BookingID, in.MenuDeGrupoID, r.Price, changes) != p.After {
		return stale("el resultado ya no coincide con la vista previa; pide una propuesta nueva")
	}
	cols := make([]string, 0, len(changes))
	for _, c := range changes {
		if !botGMenuColumns[c.Col] {
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
	// History: the panel records the rice columns becoming the menu marker as
	// "rice" with the raw type|servings values; mirror it.
	var oldType, oldServ any
	if cur.TypeSet {
		oldType = cur.TypeJSON
	}
	if cur.ServSet {
		oldServ = cur.ServJSON
	}
	oldHist := fmt.Sprintf("%v|%v", oldType, oldServ)
	newHist := fmt.Sprintf("%v|%v", r.ArrozTypeVal, r.ArrozServVal)
	if oldHist != newHist {
		if _, err := tx.ExecContext(ctx, `INSERT INTO booking_modifications (restaurant_id, booking_id, original_reservation_date, field_modified, old_value, new_value, modified_by, modified_by_name, customer_name, contact_phone, modification_date) VALUES (?, ?, ?, 'rice', ?, ?, 'whatsapp', 'Bot WhatsApp (dirección)', ?, ?, NOW())`,
			rid, p.BookingID, row.Date, oldHist, newHist, cur.Name, cur.Phone); err != nil {
			return "", errors.New("no se pudo registrar el historial; no se aplicó nada")
		}
	}
	if in.PartySize > 0 && in.PartySize != origParty {
		if err := s.botGMenuApplyPartyDelta(ctx, tx, rid, p.BookingID, row.Date, origParty, in.PartySize, cur.Name, cur.Phone); err != nil {
			return "", err
		}
	}
	notice := fmt.Sprintf("Reserva #%d: %s. No se ha avisado al cliente.", p.BookingID, botGMenuChangeText(p.BookingID, cur, in.MenuDeGrupoID, r))
	notice += botGMenuPartyText(origParty, in.PartySize)
	rj, _ := json.Marshal(map[string]any{"booking_id": p.BookingID, "op": p.Op, "notice": notice})
	if _, err := tx.ExecContext(ctx, `UPDATE wa_bot_op_proposals SET result_json=?, notify_state='pending' WHERE id=?`, string(rj), p.ID); err != nil {
		return "", errors.New("error interno")
	}
	s.botSpecialAdminEvent(ctx, tx, rid, p.ID, "executed", p.Actor, strings.TrimSpace(msg.ChatJID), map[string]any{"op": p.Op, "booking_id": p.BookingID, "before_hash": p.Before, "after_hash": p.After, "changed_columns": cols, "approval_message_id": msg.MessageID})
	if err := tx.Commit(); err != nil {
		log.Printf("[bot] wa_bot_admin_set_group_menu_v1 commit_unknown rid=%d proposal=%d err=%v", rid, p.ID, err)
		return "", errors.New("no pude confirmar si se guardó; consulta la reserva antes de repetir")
	}
	s.broadcastBookingChanged(rid, p.BookingID, "booking_updated")
	return notice, nil
}
