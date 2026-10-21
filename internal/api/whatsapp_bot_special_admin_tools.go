package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"math/big"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Coordination id: wa_bot_special_admin_v2
//
// Dedicated tools to manage SPECIAL-date bookings from ONE privileged group.
//
// Authority model (nothing here is decided by the model or by its arguments):
//   - Scope: the (tenant, group JID) pair must be listed in the environment
//     variable BOT_SPECIAL_ADMIN_SCOPES ("<restaurant_id>=<group jid>;..."),
//     empty by default. Tools are not even registered outside it.
//   - Actor: the verified participant JID of the inbound event, re-checked
//     against a FRESH provider participant list (no cache) when proposing and
//     again when executing. No fresh proof means no action.
//   - Writes are two steps. propose stores an exact preview (diff and hashes)
//     and sends it to the group; the model never sees the approval code. The
//     write runs only when the SAME actor, in the SAME group, writes exactly
//     "OK <code>" (normalised) within the TTL. Claim, write and result commit
//     in one transaction, so there is no persisted "claimed" state.
//   - Money: this only records an abono already received. It starts no
//     transfer, charge, refund or ticket.

const (
	botSpecialAdminTTL      = 10 * time.Minute
	botSpecialAdminCodeLen  = 8
	botSpecialAdminAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
	botSpecialAdminRateWin  = 10 * time.Minute
	botSpecialAdminRateActr = 5
	botSpecialAdminRateRest = 20
)

var botSpecialAdminOKRe = regexp.MustCompile(`^ok ([a-z0-9]{8})$`)

// botSpecialAdminScopeGroup returns the group JID authorised for the tenant.
func botSpecialAdminScopeGroup(restaurantID int) string {
	for _, part := range strings.Split(os.Getenv("BOT_SPECIAL_ADMIN_SCOPES"), ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		if strings.TrimSpace(k) == fmt.Sprintf("%d", restaurantID) {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func botSpecialAdminInScope(restaurantID int, chatJID string) bool {
	g := botSpecialAdminScopeGroup(restaurantID)
	return g != "" && strings.HasSuffix(g, "@g.us") && g == strings.TrimSpace(chatJID)
}

// botSpecialAdminDMPhoneJID returns the provider-supplied phone JID of a
// one-to-one chat. A @lid chat without a provider-supplied phone alias gives
// "" (fail closed): a LID is never converted to a phone by guessing digits.
func botSpecialAdminDMPhoneJID(chatJID, altJID string) string {
	for _, j := range []string{strings.TrimSpace(chatJID), strings.TrimSpace(altJID)} {
		if strings.HasSuffix(j, "@s.whatsapp.net") && botGroupPhoneDigits(j) != "" {
			return j
		}
	}
	return ""
}

// botSpecialAdminToolsAllowed: tools are registered for the authorised group
// itself, or for a one-to-one chat whose phone the webhook already matched to
// a member of that group. Everything is re-proved with a fresh fetch on use.
func botSpecialAdminToolsAllowed(restaurantID int, msg botWebhookMessage) bool {
	if msg.FromMe || botSpecialAdminScopeGroup(restaurantID) == "" || !msg.PrivilegedGroup {
		return false
	}
	if msg.IsGroup {
		return botSpecialAdminInScope(restaurantID, msg.ChatJID)
	}
	return msg.ActorPhoneJID != "" && strings.TrimSpace(msg.ChatJID) != ""
}

// botSpecialAdminFreshMember proves membership with a provider list fetched now.
func (s *Server) botSpecialAdminFreshMember(ctx context.Context, restaurantID int, groupJID, actorJID string) bool {
	members := s.botGroupMembersFromProvider(ctx, restaurantID, "", groupJID)
	if members.empty() {
		return false
	}
	return botGroupSenderInMembers(members, actorJID)
}

// botSpecialAdminAuthorize is the single gate for every tool of this file. It
// returns the actor identity and the chat the turn came from (the group, or the
// member's own one-to-one chat).
func (s *Server) botSpecialAdminAuthorize(ctx context.Context, restaurantID int, msg botWebhookMessage) (actor, origin string, ok bool) {
	if !botSpecialAdminToolsAllowed(restaurantID, msg) {
		return "", "", false
	}
	scope := botSpecialAdminScopeGroup(restaurantID)
	if msg.IsGroup {
		actor = strings.TrimSpace(msg.ParticipantJID)
	} else {
		actor = strings.TrimSpace(msg.ActorPhoneJID)
	}
	if actor == "" || !s.botSpecialAdminFreshMember(ctx, restaurantID, scope, actor) {
		return "", "", false
	}
	return actor, strings.TrimSpace(msg.ChatJID), true
}

// botSpecialAdminMayNotify is the privacy boundary for notices that are not a
// direct reply to a verified request (supersede and queued results). The scope
// group itself is always allowed; a one-to-one chat only while its phone is a
// CURRENT member (fresh fetch). A former member gets nothing, not even an id.
func (s *Server) botSpecialAdminMayNotify(ctx context.Context, rid int, chat string) bool {
	scope := botSpecialAdminScopeGroup(rid)
	chat = strings.TrimSpace(chat)
	if scope == "" || chat == "" {
		return false
	}
	if chat == scope {
		return true
	}
	if !strings.HasSuffix(chat, "@s.whatsapp.net") {
		return false
	}
	return s.botSpecialAdminFreshMember(ctx, rid, scope, chat)
}

func botSpecialAdminToolDefs() []botToolDef {
	return []botToolDef{
		{
			Name:        "special_booking_get",
			Description: "Lee una reserva de FECHA ESPECIAL por su booking_id: fecha, hora, comensales, estado, menús (precio, cantidad, adelanto por unidad), abonos ya registrados y totales. Sin datos personales. Úsala antes de proponer cambios.",
			InputSchema: botSchema(`{"type":"object","properties":{"booking_id":{"type":"integer"}},"required":["booking_id"]}`),
		},
		{
			Name:        "special_booking_propose_adelanto",
			Description: "PROPONE registrar un abono (adelanto) ya recibido en una reserva de fecha especial. NO lo aplica: guarda una propuesta exacta y envía al grupo una vista previa con el cambio y un código. El cambio solo se aplica cuando la misma persona escribe en el grupo 'OK <código>'. No ves el código. Tras llamar, di solo que la propuesta se ha enviado al grupo y que hay que confirmar con el código; no digas que está hecho. El importe y el método los tiene que haber dicho el usuario: si faltan, pregúntaselos. Nunca los deduzcas del comentario de la reserva. method: bizum, efectivo, transferencia o card. No cobra nada.",
			InputSchema: botSchema(`{"type":"object","properties":{"booking_id":{"type":"integer"},"method":{"type":"string","enum":["bizum","efectivo","transferencia","card"]},"amount":{"type":"number","description":"Importe en euros"}},"required":["booking_id","method","amount"]}`),
		},
		{
			Name:        "special_booking_propose_menus",
			Description: "PROPONE cambiar los menús de una reserva de fecha especial conservando los abonos y el resto. NO lo aplica: envía al grupo una vista previa con el cambio y un código; se aplica cuando la misma persona escribe 'OK <código>'. No ves el código ni digas que está hecho. menus: lista de {special_date_menu_id, count, sections?:[{section_id,count}]} (ids de get_special_date_menu).",
			InputSchema: botSchema(`{"type":"object","properties":{"booking_id":{"type":"integer"},"menus":{"type":"array","items":{"type":"object","properties":{"special_date_menu_id":{"type":"integer"},"count":{"type":"integer"},"sections":{"type":"array","items":{"type":"object","properties":{"section_id":{"type":"integer"},"count":{"type":"integer"}},"required":["section_id","count"]}}},"required":["special_date_menu_id","count"]}}},"required":["booking_id","menus"]}`),
		},
		{
			Name:        "special_booking_propose_reschedule",
			Description: "PROPONE cambiar la fecha, la hora y/o el número de comensales de una reserva de fecha especial (puedes pasar solo lo que cambia). Si cambian los comensales o la fecha, pasa también `menus` con las cantidades nuevas cuando no cuadren. NO lo aplica: guarda una propuesta exacta, envía al grupo una vista previa con un código y se aplica cuando la misma persona escribe 'OK' y ese código en el mismo chat. Conserva los abonos, el resto de datos de la reserva y actualiza la ocupación y el historial de cambios.",
			InputSchema: botSchema(`{"type":"object","properties":{"booking_id":{"type":"integer"},"date":{"type":"string","description":"Nueva fecha YYYY-MM-DD"},"time":{"type":"string","description":"Nueva hora HH:MM"},"party_size":{"type":"integer","description":"Nuevo número de comensales"},"menus":{"type":"array","items":{"type":"object","properties":{"special_date_menu_id":{"type":"integer"},"count":{"type":"integer"}},"required":["special_date_menu_id","count"]}}},"required":["booking_id"]}`),
		},
	}
}

type botSpecialRow struct {
	Date, Time, Status, Raw string
	Party                   int
	Special                 bool
	IsPre                   int
}

type botRowQuerier interface {
	QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error)
}

// botSpecialColumns are the only bookings columns a special-admin write may
// change; every other column must be bit-identical before and after.
var botSpecialColumns = []string{"is_special_booking", "is_prereserva", "special_json"}

// botFullRowHash hashes EVERY column of the booking row (NULL kept distinct),
// except the excluded ones. It does not depend on a column list in code, so a
// column added later is covered too.
func botFullRowHash(ctx context.Context, q botRowQuerier, rid int, id int64, exclude ...string) (string, error) {
	rows, err := q.QueryContext(ctx, "SELECT * FROM bookings WHERE restaurant_id = ? AND id = ?", rid, id)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return "", err
	}
	if !rows.Next() {
		return "", sql.ErrNoRows
	}
	vals := make([]sql.RawBytes, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return "", err
	}
	skip := map[string]bool{}
	for _, e := range exclude {
		skip[e] = true
	}
	h := sha256.New()
	for i, c := range cols {
		if skip[c] {
			continue
		}
		h.Write([]byte(c))
		h.Write([]byte{'='})
		if vals[i] == nil {
			h.Write([]byte{0, 'N'})
		} else {
			h.Write(vals[i])
		}
		h.Write([]byte{0x1f})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

const botSpecialRowSelect = `SELECT DATE_FORMAT(reservation_date,'%Y-%m-%d'), COALESCE(TIME_FORMAT(reservation_time,'%H:%i'),''), party_size, COALESCE(status,''), COALESCE(is_special_booking,0), COALESCE(is_prereserva,0), COALESCE(special_json,'') FROM bookings WHERE restaurant_id = ? AND id = ?`

func botScanSpecialRow(row *sql.Row) (*botSpecialRow, error) {
	var r botSpecialRow
	var isSpecial int
	if err := row.Scan(&r.Date, &r.Time, &r.Party, &r.Status, &isSpecial, &r.IsPre, &r.Raw); err != nil {
		return nil, err
	}
	r.Special = isSpecial != 0 && strings.TrimSpace(r.Raw) != ""
	return &r, nil
}

func (s *Server) botLoadSpecialRow(ctx context.Context, rid int, id int64) (*botSpecialRow, error) {
	return botScanSpecialRow(s.db.QueryRowContext(ctx, botSpecialRowSelect, rid, id))
}

// botSnapshotToReq rebuilds the full `special` request from a stored snapshot.
func botSnapshotToReq(snap specialBookingSnapshot) *specialBookingReq {
	req := &specialBookingReq{PaymentMethod: snap.PaymentMethod}
	idx := map[int64]int{}
	for _, m := range snap.Menus {
		pos, seen := idx[m.SpecialDateMenuID]
		if !seen {
			req.Menus = append(req.Menus, specialBookingMenuReq{SpecialDateMenuID: m.SpecialDateMenuID, AdelantoPaymentMethod: m.AdelantoPaymentMethod})
			pos = len(req.Menus) - 1
			idx[m.SpecialDateMenuID] = pos
		}
		items := make(specialBookingItemIDs, 0, len(m.Items))
		for _, it := range m.Items {
			items = append(items, it.DishID)
		}
		req.Menus[pos].Count += m.Count
		if m.SectionID != nil {
			req.Menus[pos].Sections = append(req.Menus[pos].Sections, specialBookingSectionReq{SectionID: *m.SectionID, Count: m.Count, Items: items})
		} else {
			req.Menus[pos].Items = items
		}
	}
	req.AdelantosPaid = append(req.AdelantosPaid, snap.AdelantosPaid...)
	return req
}

func botCanon(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func botSha(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func botSnapTotals(snap specialBookingSnapshot) map[string]any {
	var total, adel, paid float64
	for _, m := range snap.Menus {
		total += m.UnitPrice * float64(m.Count)
		adel += m.AdelantoPerUnit * float64(m.Count)
	}
	for _, p := range snap.AdelantosPaid {
		paid += p.Amount
	}
	return map[string]any{"total_eur": total, "adelanto_requerido_eur": adel, "abonado_eur": paid, "pendiente_adelanto_eur": math.Max(0, adel-paid), "pendiente_total_eur": math.Max(0, total-paid)}
}

func botEUR(v float64) string { return strings.Replace(fmt.Sprintf("%.2f€", v), ".", ",", 1) }

// botSpecialInputFromCurrent mirrors the field merge of handleBOBookingPatch for
// a patch that touches only `special`.
func botSpecialInputFromCurrent(current map[string]any) (in boNormalizeInput, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("fila de reserva con formato inesperado")
		}
	}()
	in = boNormalizeInput{
		ReservationDate:         current["reservation_date"].(string),
		ReservationTime:         current["reservation_time"].(string),
		PartySize:               int(current["party_size"].(int64)),
		CustomerName:            current["customer_name"].(string),
		ContactPhone:            anyToString(current["contact_phone"]),
		ContactPhoneCountryCode: anyToString(current["contact_phone_country_code"]),
	}
	if v := strings.TrimSpace(anyToString(current["contact_email"])); v != "" {
		in.ContactEmail = &v
	}
	if v := strings.TrimSpace(anyToString(current["table_number"])); v != "" {
		in.TableNumber = &v
	}
	if v, ok := current["preferred_floor_number"].(int64); ok {
		n := int(v)
		in.PreferredFloorNumber = &n
	}
	if v := strings.TrimSpace(anyToString(current["commentary"])); v != "" {
		in.Commentary = &v
	}
	if v, ok := current["babyStrollers"].(int64); ok {
		n := int(v)
		in.BabyStrollers = &n
	}
	if v, ok := current["highChairs"].(int64); ok {
		n := int(v)
		in.HighChairs = &n
	}
	if v, ok := current["children"].(int64); ok {
		n := int(v)
		in.Children = &n
	}
	if v, ok := current["has_mobility_issues"].(bool); ok {
		has := v
		in.HasMobilityIssues = &has
	}
	if v, ok := current["mobility_people"].(int64); ok {
		n := int(v)
		in.MobilityPeople = &n
	}
	if v, ok := current["special_menu"].(bool); ok {
		in.SpecialMenu = v
	}
	if v, ok := current["menu_de_grupo_id"].(int64); ok && v > 0 {
		n := int(v)
		in.MenuDeGrupoID = &n
	}
	if v := strings.TrimSpace(anyToString(current["principales_json"])); v != "" {
		in.PrincipalesRaw = json.RawMessage(v)
	}
	return in, nil
}

// botSpecialBuildNext validates the patched booking exactly as the canonical
// PATCH does (same normaliser) and applies the same preservation rules.
func (s *Server) botSpecialBuildNext(ctx context.Context, rid int, id int64, req *specialBookingReq) (boNormalizedBooking, map[string]any, error) {
	return s.botSpecialBuildNextOv(ctx, rid, id, req, nil)
}

// botSpecialOverride moves the booking to another date, time or party size.
type botSpecialOverride struct {
	Date, Time string
	Party      int
}

func botOvFromArgs(op string, a botSpecialProposalArgs) *botSpecialOverride {
	if op != "reschedule" {
		return nil
	}
	return &botSpecialOverride{Date: a.NewDate, Time: a.NewTime, Party: a.NewParty}
}

// botSpecialAfterKey is the exact "after" the approver saw: the special block
// plus, for a reschedule, the final date, time and party size.
func botSpecialAfterKey(op string, next boNormalizedBooking) string {
	if op == "reschedule" {
		return botSha(botCanon(next.SpecialJSON) + "|" + next.ReservationDate + "|" + next.ReservationTime + "|" + fmt.Sprint(next.PartySize) + "|" + fmt.Sprint(next.Children, next.HasMobilityIssues, next.MobilityPeople))
	}
	return botSha(botCanon(next.SpecialJSON))
}

// botSpecialColumnsFor lists the only bookings columns the op may change.
func botSpecialColumnsFor(op string) []string {
	if op == "reschedule" {
		return append([]string{"reservation_date", "reservation_time", "party_size", "children", "has_mobility_issues", "mobility_people"}, botSpecialColumns...)
	}
	return botSpecialColumns
}

func (s *Server) botSpecialBuildNextOv(ctx context.Context, rid int, id int64, req *specialBookingReq, ov *botSpecialOverride) (boNormalizedBooking, map[string]any, error) {
	current, err := s.boFetchBookingByID(ctx, rid, int(id))
	if err != nil {
		return boNormalizedBooking{}, nil, errors.New("reserva no encontrada")
	}
	in, err := botSpecialInputFromCurrent(current)
	if err != nil {
		return boNormalizedBooking{}, nil, err
	}
	in.Special = req
	in.SpecialTouched = true
	if ov != nil {
		if ov.Date != "" {
			in.ReservationDate = ov.Date
		}
		if ov.Time != "" {
			in.ReservationTime = ov.Time
		}
		if ov.Party > 0 {
			in.PartySize = ov.Party
		}
	}
	next, err := s.boNormalizeAndValidateBookingInput(ctx, rid, in)
	if err != nil {
		return next, nil, err
	}
	if !next.SpecialMenu {
		next.ArrozTypeJSON = current["arroz_type"]
		next.ArrozServingsJSON = current["arroz_servings"]
	}
	if names := bookingExtraNames(anyToString(current["extras_json"])); len(names) > 0 {
		next.ExtrasJSON = anyToString(current["extras_json"])
		next.ExtrasNames = names
	}
	return next, current, nil
}

func botSpecialAdminJSON(v map[string]any) (string, error) {
	b, _ := json.Marshal(v)
	return string(b), nil
}

type botSpecialProposalArgs struct {
	Method string                  `json:"method,omitempty"`
	Amount float64                 `json:"amount,omitempty"`
	Menus  []specialBookingMenuReq `json:"menus,omitempty"`
	// reschedule (date/time/party). Empty/zero = unchanged.
	NewDate  string `json:"new_date,omitempty"`
	NewTime  string `json:"new_time,omitempty"`
	NewParty int    `json:"new_party,omitempty"`
}

// botSpecialApplyArgs merges the proposal args into the stored snapshot.
func botSpecialApplyArgs(op string, snap specialBookingSnapshot, a botSpecialProposalArgs) (*specialBookingReq, error) {
	req := botSnapshotToReq(snap)
	switch op {
	case "adelanto":
		req.AdelantosPaid = append(req.AdelantosPaid, specialBookingAdelantoPaid{Method: a.Method, Amount: a.Amount})
	case "menus":
		req.Menus = a.Menus
	case "reschedule":
		if len(a.Menus) > 0 {
			req.Menus = a.Menus
		}
	default:
		return nil, errors.New("operación desconocida")
	}
	return req, nil
}

func (s *Server) botSpecialAdminEvent(ctx context.Context, ex execQueryer, rid int, proposalID int64, event, actor, group string, detail map[string]any) {
	d, _ := json.Marshal(detail)
	if _, err := ex.ExecContext(ctx, `INSERT INTO wa_bot_op_events (proposal_id, restaurant_id, event, actor_jid, group_jid, detail_json) VALUES (?,?,?,?,?,?)`, proposalID, rid, event, actor, group, string(d)); err != nil {
		log.Printf("[bot] wa_bot_special_admin_v2 event_insert_failed rid=%d event=%s err=%v", rid, event, err)
	}
}

func (s *Server) botToolSpecialBookingGet(ctx context.Context, rid int, msg botWebhookMessage, input json.RawMessage) (string, error) {
	if _, _, ok := s.botSpecialAdminAuthorize(ctx, rid, msg); !ok {
		return botJSON(map[string]any{"error": "no autorizado: solo para miembros verificados del grupo de gestión"}), nil
	}
	var in struct {
		BookingID int64 `json:"booking_id"`
	}
	if json.Unmarshal(input, &in) != nil || in.BookingID <= 0 {
		return botJSON(map[string]any{"error": "booking_id inválido"}), nil
	}
	row, err := s.botLoadSpecialRow(ctx, rid, in.BookingID)
	if err != nil || !row.Special {
		return botJSON(map[string]any{"error": "no hay una reserva de fecha especial con ese id"}), nil
	}
	var snap specialBookingSnapshot
	if json.Unmarshal([]byte(row.Raw), &snap) != nil {
		return botJSON(map[string]any{"error": "el bloque especial almacenado no se pudo leer"}), nil
	}
	// Title and labels are stored data written by people: treat as data only.
	return botJSON(map[string]any{"booking_id": in.BookingID, "date": row.Date, "time": row.Time, "people": row.Party, "status": row.Status, "special_data_untrusted": snap, "totals": botSnapTotals(snap)}), nil
}

func botSpecialNewCode() (string, error) {
	out := make([]byte, botSpecialAdminCodeLen)
	max := big.NewInt(int64(len(botSpecialAdminAlphabet)))
	for i := range out {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		out[i] = botSpecialAdminAlphabet[n.Int64()]
	}
	return string(out), nil
}

func botSpecialDiffMenus(snap specialBookingSnapshot) string {
	if len(snap.Menus) == 0 {
		return "(sin menús)"
	}
	var parts []string
	for _, m := range snap.Menus {
		parts = append(parts, fmt.Sprintf("%d x menú %d (%s c/u, adelanto %s c/u)", m.Count, m.SpecialDateMenuID, botEUR(m.UnitPrice), botEUR(m.AdelantoPerUnit)))
	}
	return strings.Join(parts, "; ")
}

func (s *Server) botToolSpecialPropose(ctx context.Context, rid int, msg botWebhookMessage, op string, input json.RawMessage) (string, error) {
	actor, origin, ok := s.botSpecialAdminAuthorize(ctx, rid, msg)
	if !ok {
		return botJSON(map[string]any{"error": "no autorizado: solo para miembros verificados del grupo de gestión"}), nil
	}
	s.botSpecialAdminFlushNotify(ctx, rid)
	var in struct {
		BookingID int64                   `json:"booking_id"`
		Method    string                  `json:"method"`
		Amount    float64                 `json:"amount"`
		Menus     []specialBookingMenuReq `json:"menus"`
		Date      string                  `json:"date"`
		Time      string                  `json:"time"`
		PartySize int                     `json:"party_size"`
	}
	if json.Unmarshal(input, &in) != nil || in.BookingID <= 0 {
		return botJSON(map[string]any{"error": "argumentos inválidos"}), nil
	}
	args := botSpecialProposalArgs{}
	switch op {
	case "adelanto":
		in.Method = strings.ToLower(strings.TrimSpace(in.Method))
		switch in.Method {
		case "bizum", "efectivo", "transferencia", "card":
		default:
			return botJSON(map[string]any{"error": "método inválido"}), nil
		}
		if in.Amount <= 0 || in.Amount > 100000 || math.Abs(in.Amount*100-math.Round(in.Amount*100)) > 1e-6 {
			return botJSON(map[string]any{"error": "importe inválido (euros, máximo 2 decimales)"}), nil
		}
		args = botSpecialProposalArgs{Method: in.Method, Amount: in.Amount}
	case "menus":
		if len(in.Menus) == 0 {
			return botJSON(map[string]any{"error": "menus vacío"}), nil
		}
		args = botSpecialProposalArgs{Menus: in.Menus}
	case "reschedule":
		in.Date, in.Time = strings.TrimSpace(in.Date), strings.TrimSpace(in.Time)
		if in.Date == "" && in.Time == "" && in.PartySize == 0 {
			return botJSON(map[string]any{"error": "indica la fecha, la hora o los comensales nuevos"}), nil
		}
		if in.Date != "" {
			d, derr := time.Parse("2006-01-02", in.Date)
			loc, _ := time.LoadLocation("Europe/Madrid")
			if loc == nil {
				loc = time.UTC
			}
			n := time.Now().In(loc)
			today := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
			if derr != nil || d.Before(today) {
				return botJSON(map[string]any{"error": "fecha inválida o pasada (usa YYYY-MM-DD)"}), nil
			}
		}
		if in.Time != "" {
			if _, terr := time.Parse("15:04", in.Time); terr != nil {
				return botJSON(map[string]any{"error": "hora inválida (usa HH:MM)"}), nil
			}
		}
		if in.PartySize < 0 || in.PartySize > 500 {
			return botJSON(map[string]any{"error": "comensales inválidos"}), nil
		}
		args = botSpecialProposalArgs{Menus: in.Menus, NewDate: in.Date, NewTime: in.Time, NewParty: in.PartySize}
	}
	row, err := s.botLoadSpecialRow(ctx, rid, in.BookingID)
	if err != nil || !row.Special {
		return botJSON(map[string]any{"error": "no hay una reserva de fecha especial con ese id"}), nil
	}
	var snap specialBookingSnapshot
	if json.Unmarshal([]byte(row.Raw), &snap) != nil {
		return botJSON(map[string]any{"error": "el bloque especial almacenado no se pudo leer"}), nil
	}
	if op == "reschedule" {
		if rerr := botSpecialRescheduleAllowedRow(row); rerr != nil {
			return botJSON(map[string]any{"error": rerr.Error()}), nil
		}
	}
	req, err := botSpecialApplyArgs(op, snap, args)
	if err != nil {
		return botJSON(map[string]any{"error": err.Error()}), nil
	}
	next, curMap, err := s.botSpecialBuildNextOv(ctx, rid, in.BookingID, req, botOvFromArgs(op, args))
	if err != nil {
		return botJSON(map[string]any{"error": "la validación rechaza el cambio: " + err.Error()}), nil
	}
	afterHash := botSpecialAfterKey(op, next)
	beforeHash, herr := botFullRowHash(ctx, s.db, rid, in.BookingID)
	if herr != nil {
		return botJSON(map[string]any{"error": "no se pudo leer la reserva"}), nil
	}
	var after specialBookingSnapshot
	if sj, ok := next.SpecialJSON.(string); ok {
		_ = json.Unmarshal([]byte(sj), &after)
	}
	code, err := botSpecialNewCode()
	if err != nil {
		return botJSON(map[string]any{"error": "no se pudo generar la propuesta"}), nil
	}
	bTotals, aTotals := botSnapTotals(snap), botSnapTotals(after)
	var preview string
	depsText := ""
	if op == "reschedule" {
		depsText, _ = botSpecialRescheduleDeps(curMap, next)
	}
	if op == "adelanto" {
		preview = fmt.Sprintf("Propuesta (reserva #%d, %s, %d comensales): registrar abono de %s por %s. Abonado: %s -> %s. Adelanto requerido: %s. Solo anota un pago ya recibido: no cobra ni envía nada al cliente.",
			in.BookingID, row.Date, row.Party, botEUR(args.Amount), args.Method, botEUR(bTotals["abonado_eur"].(float64)), botEUR(aTotals["abonado_eur"].(float64)), botEUR(aTotals["adelanto_requerido_eur"].(float64)))
	} else if op == "reschedule" {
		preview = fmt.Sprintf("Propuesta (reserva #%d): mover de %s %s (%d comensales) a %s %s (%d comensales). Menús: %s. Total: %s -> %s. Abonos registrados se conservan (%s). Estado actual: %s.%s Se actualizan la ocupación y el historial de cambios. SIN comprobación de aforo (se fuerza como en el panel de dirección). No se avisa al cliente.",
			in.BookingID, row.Date, row.Time, row.Party, next.ReservationDate, strings.TrimSuffix(next.ReservationTime, ":00"), next.PartySize, botSpecialDiffMenus(after), botEUR(bTotals["total_eur"].(float64)), botEUR(aTotals["total_eur"].(float64)), botEUR(aTotals["abonado_eur"].(float64)), row.Status, depsText)
	} else {
		preview = fmt.Sprintf("Propuesta (reserva #%d, %s, %d comensales): cambiar menús. Antes: %s. Después: %s. Total: %s -> %s. Abonos registrados se conservan (%s).",
			in.BookingID, row.Date, row.Party, botSpecialDiffMenus(snap), botSpecialDiffMenus(after), botEUR(bTotals["total_eur"].(float64)), botEUR(aTotals["total_eur"].(float64)), botEUR(aTotals["abonado_eur"].(float64)))
	}
	argsJSON, _ := json.Marshal(args)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return botJSON(map[string]any{"error": "no se pudo guardar la propuesta"}), nil
	}
	defer tx.Rollback()
	var superseded int64
	var oldChats []string
	if rs, qerr := tx.QueryContext(ctx, `SELECT DISTINCT origin_chat_jid FROM wa_bot_op_proposals WHERE restaurant_id=? AND booking_id=? AND active=1`, rid, in.BookingID); qerr == nil {
		for rs.Next() {
			var c string
			if rs.Scan(&c) == nil {
				oldChats = append(oldChats, c)
			}
		}
		rs.Close()
	}
	if res, err := tx.ExecContext(ctx, `UPDATE wa_bot_op_proposals SET state='superseded', active=NULL WHERE restaurant_id=? AND booking_id=? AND active=1`, rid, in.BookingID); err == nil {
		superseded, _ = res.RowsAffected()
	} else {
		return botJSON(map[string]any{"error": "no se pudo guardar la propuesta"}), nil
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO wa_bot_op_proposals (restaurant_id, scope_group_jid, origin_chat_jid, actor_jid, source_message_id, op, booking_id, args_json, before_hash, after_hash, preview_text, code_hash, state, active, expires_at, notify_state) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,'pending',1,?, 'none')`,
		rid, botSpecialAdminScopeGroup(rid), origin, actor, msg.MessageID, op, in.BookingID, string(argsJSON), beforeHash, afterHash, preview, botSha(code), time.Now().UTC().Add(botSpecialAdminTTL).Format("2006-01-02 15:04:05"))
	if err != nil {
		return botJSON(map[string]any{"error": "no se pudo guardar la propuesta"}), nil
	}
	pid, _ := res.LastInsertId()
	s.botSpecialAdminEvent(ctx, tx, rid, pid, "proposed", actor, origin, map[string]any{"op": op, "booking_id": in.BookingID, "before_hash": beforeHash, "after_hash": afterHash, "superseded": superseded})
	if err := tx.Commit(); err != nil {
		return botJSON(map[string]any{"error": "no se pudo guardar la propuesta"}), nil
	}

	for _, c := range oldChats {
		if !s.botSpecialAdminMayNotify(ctx, rid, c) {
			continue
		}
		if gw, okGw := s.botGatewayFor(ctx, rid); okGw {
			_ = gw.SendText(ctx, c, fmt.Sprintf("La propuesta pendiente sobre la reserva #%d se ha sustituido por otra más reciente. El código anterior ya no vale.", in.BookingID))
		}
	}
	// The code is delivered straight to the group by the gateway: it is not
	// returned to the model and not recorded in the conversation history.
	text := preview + fmt.Sprintf("\n\nPara aplicarlo, quien lo ha pedido responde en ESTE chat exactamente: OK %s (caduca en 10 minutos).", code)
	gw, okGw := s.botGatewayFor(ctx, rid)
	if !okGw || gw.SendText(ctx, origin, text) != nil {
		s.botSpecialAdminEvent(ctx, s.db, rid, pid, "preview_send_failed", actor, origin, nil)
		return botJSON(map[string]any{"ok": false, "error": "no se pudo enviar la vista previa al grupo; la propuesta queda inactiva y no se aplicará. Pide al usuario que lo repita."}), nil
	}
	// accepted by the provider (NOT proven delivered): the proposal becomes live.
	if r, err := s.db.ExecContext(ctx, `UPDATE wa_bot_op_proposals SET state='sent' WHERE id=? AND state='pending' AND active=1`, pid); err != nil {
		return botJSON(map[string]any{"ok": false, "error": "la propuesta no pudo activarse"}), nil
	} else if n, _ := r.RowsAffected(); n != 1 {
		return botJSON(map[string]any{"ok": false, "error": "la propuesta fue sustituida por otra más reciente"}), nil
	}
	s.botSpecialAdminEvent(ctx, s.db, rid, pid, "preview_accepted", actor, origin, nil)
	return botJSON(map[string]any{"ok": true, "status": "propuesta enviada al grupo, pendiente de confirmación con código", "instruction": "Di solo que has enviado la propuesta al grupo y que quien la pidió debe responder con el código. NO digas que está aplicado."}), nil
}

func botSpecialNormalizeOK(text string) (string, bool) {
	t := strings.ToLower(strings.TrimSpace(text))
	if len(t) > 24 {
		return "", false
	}
	t = strings.Join(strings.Fields(t), " ")
	m := botSpecialAdminOKRe.FindStringSubmatch(t)
	if m == nil {
		return "", false
	}
	return strings.ToUpper(m[1]), true
}

func (s *Server) botSpecialAdminRateOK(ctx context.Context, rid int, actor string) bool {
	const bad = `event IN ('bad_code','approve_rejected_other_actor') AND created_at >= DATE_SUB(NOW(), INTERVAL 10 MINUTE)`
	var a, t int
	if s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM wa_bot_op_events WHERE restaurant_id=? AND actor_jid=? AND `+bad, rid, actor).Scan(&a) != nil {
		return false
	}
	if s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM wa_bot_op_events WHERE restaurant_id=? AND `+bad, rid).Scan(&t) != nil {
		return false
	}
	return a < botSpecialAdminRateActr && t < botSpecialAdminRateRest
}

type botSpecialProposalRow struct {
	ID                            int64
	Op                            string
	BookingID                     int64
	Args, Before, After, CodeHash string
	Actor                         string
}

// botSpecialAdminTryApprove handles an inbound "OK <code>" BEFORE any model
// turn. It returns true when the message was an approval attempt and has been
// fully handled (the model must not see it).
func (s *Server) botSpecialAdminTryApprove(ctx context.Context, rid int, msg botWebhookMessage) bool {
	if !botSpecialAdminToolsAllowed(rid, msg) {
		return false
	}
	s.botSpecialAdminFlushNotifyThrottled(ctx, rid)
	code, isOK := botSpecialNormalizeOK(msg.Text)
	if !isOK {
		return false
	}
	scope := botSpecialAdminScopeGroup(rid)
	origin := strings.TrimSpace(msg.ChatJID)
	actor := strings.TrimSpace(msg.ParticipantJID)
	if !msg.IsGroup {
		actor = strings.TrimSpace(msg.ActorPhoneJID)
	}
	reply := func(t string) {
		if gw, ok := s.botGatewayFor(ctx, rid); ok {
			_ = gw.SendText(ctx, origin, t)
		}
	}
	if actor == "" || !s.botSpecialAdminFreshMember(ctx, rid, scope, actor) {
		s.botSpecialAdminEvent(ctx, s.db, rid, 0, "approve_rejected_actor", actor, origin, nil)
		return true
	}
	if !s.botSpecialAdminRateOK(ctx, rid, actor) {
		s.botSpecialAdminEvent(ctx, s.db, rid, 0, "approve_rate_limited", actor, origin, nil)
		return true
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, op, booking_id, args_json, before_hash, after_hash, code_hash, actor_jid FROM wa_bot_op_proposals WHERE restaurant_id=? AND scope_group_jid=? AND origin_chat_jid=? AND state='sent' AND active=1 AND expires_at > UTC_TIMESTAMP()`, rid, scope, origin)
	if err != nil {
		return true
	}
	var match *botSpecialProposalRow
	want := botSha(code)
	for rows.Next() {
		var p botSpecialProposalRow
		if rows.Scan(&p.ID, &p.Op, &p.BookingID, &p.Args, &p.Before, &p.After, &p.CodeHash, &p.Actor) != nil {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(p.CodeHash), []byte(want)) == 1 {
			cp := p
			match = &cp
		}
	}
	rows.Close()
	if match == nil {
		s.botSpecialAdminEvent(ctx, s.db, rid, 0, "bad_code", actor, origin, nil)
		reply("Código no válido o caducado. Pide una propuesta nueva.")
		return true
	}
	if match.Actor != actor {
		// A valid code from somebody else never executes: only the requester can approve.
		s.botSpecialAdminEvent(ctx, s.db, rid, match.ID, "approve_rejected_other_actor", actor, origin, nil)
		reply("Código no válido o caducado. Pide una propuesta nueva.")
		return true
	}
	out, err := s.botSpecialAdminExecute(ctx, rid, msg, *match)
	if err != nil {
		s.botSpecialAdminEvent(ctx, s.db, rid, match.ID, "execute_failed", actor, origin, map[string]any{"reason": err.Error()})
		reply("No se aplicó la propuesta: " + err.Error())
		return true
	}
	// The notice stays 'pending' until the provider accepts it.
	if gw, okGw := s.botGatewayFor(ctx, rid); okGw && gw.SendText(ctx, origin, out) == nil {
		_, _ = s.db.ExecContext(ctx, `UPDATE wa_bot_op_proposals SET notify_state='sent' WHERE id=? AND notify_state='pending'`, match.ID)
	}
	return true
}

var errBotSpecialStale = errors.New("la reserva cambió desde la vista previa; pide una propuesta nueva")

func botSpecialLockRefs(ctx context.Context, tx *sql.Tx, rid int, date string) error {
	// Reference rows that price and validate the block are share-locked for the
	// life of the tx, so an admin edit of them waits for the commit and cannot
	// move between the validation reads and the write. Dish rows (names and
	// existence of items) are not locked; the after_hash re-check covers them.
	for _, q := range []string{
		`SELECT id FROM special_dates WHERE restaurant_id = ? AND date = ? ORDER BY id FOR SHARE`,
		`SELECT m.id FROM special_date_menus m JOIN special_dates d ON d.id = m.special_date_id WHERE d.restaurant_id = ? AND d.date = ? ORDER BY m.id FOR SHARE`,
		`SELECT sec.id FROM special_date_menu_sections sec JOIN special_date_menus m ON m.id = sec.special_date_menu_id JOIN special_dates d ON d.id = m.special_date_id WHERE d.restaurant_id = ? AND d.date = ? ORDER BY sec.id FOR SHARE`,
	} {
		rs, err := tx.QueryContext(ctx, q, rid, date)
		if err != nil {
			return err
		}
		for rs.Next() {
		}
		rs.Close()
	}
	return nil
}

func (s *Server) botSpecialAdminExecute(ctx context.Context, rid int, msg botWebhookMessage, p botSpecialProposalRow) (string, error) {
	var args botSpecialProposalArgs
	if json.Unmarshal([]byte(p.Args), &args) != nil {
		return "", errors.New("propuesta ilegible")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", errors.New("error interno")
	}
	defer tx.Rollback()
	stale := func(why string) (string, error) {
		_ = tx.Rollback() // release the claim lock before marking outside the tx
		s.markStale(ctx, rid, p, msg)
		return "", errors.New(why)
	}
	// Claim: atomic, single use. It commits (or not) together with the write.
	res, err := tx.ExecContext(ctx, `UPDATE wa_bot_op_proposals SET state='done', active=NULL, done_at=UTC_TIMESTAMP() WHERE id=? AND state='sent' AND active=1 AND expires_at > UTC_TIMESTAMP()`, p.ID)
	if err != nil {
		return "", errors.New("error interno")
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return "", errors.New("la propuesta ya no está vigente")
	}
	row, err := botScanSpecialRow(tx.QueryRowContext(ctx, botSpecialRowSelect+" FOR UPDATE", rid, p.BookingID))
	if err != nil || !row.Special {
		return "", errors.New("la reserva ya no es una reserva especial")
	}
	// Version check over the WHOLE row (every column), not a subset.
	full, err := botFullRowHash(ctx, tx, rid, p.BookingID)
	if err != nil {
		return "", errors.New("error interno")
	}
	if full != p.Before {
		return stale(errBotSpecialStale.Error())
	}
	preOthers, err := botFullRowHash(ctx, tx, rid, p.BookingID, botSpecialColumnsFor(p.Op)...)
	if err != nil {
		return "", errors.New("error interno")
	}
	if err := botSpecialLockRefs(ctx, tx, rid, row.Date); err != nil {
		return "", errors.New("error interno")
	}
	if p.Op == "reschedule" {
		if rerr := botSpecialRescheduleAllowedRow(row); rerr != nil {
			return "", rerr
		}
	}
	if p.Op == "reschedule" && args.NewDate != "" && args.NewDate != row.Date {
		if err := botSpecialLockRefs(ctx, tx, rid, args.NewDate); err != nil {
			return "", errors.New("error interno")
		}
	}
	var snap specialBookingSnapshot
	if json.Unmarshal([]byte(row.Raw), &snap) != nil {
		return "", errors.New("bloque especial ilegible")
	}
	req, err := botSpecialApplyArgs(p.Op, snap, args)
	if err != nil {
		return "", err
	}
	next, _, err := s.botSpecialBuildNextOv(ctx, rid, p.BookingID, req, botOvFromArgs(p.Op, args))
	if err != nil {
		return "", errors.New("la validación rechaza el cambio: " + err.Error())
	}
	if botSpecialAfterKey(p.Op, next) != p.After {
		return stale("el resultado ya no coincide con la vista previa (menús o precios cambiados); pide una propuesta nueva")
	}
	// Special-only write: the three special columns and nothing else.
	spJSON, _ := next.SpecialJSON.(string)
	if p.Op == "reschedule" {
		if err := s.botSpecialRescheduleWrite(ctx, tx, rid, p, row, next, spJSON); err != nil {
			return "", err
		}
	} else if _, err := tx.ExecContext(ctx, `UPDATE bookings SET is_special_booking = ?, is_prereserva = ?, special_json = ? WHERE restaurant_id = ? AND id = ?`,
		boolToTinyint(next.IsSpecialBooking), boolToTinyint(next.IsPrereserva), spJSON, rid, p.BookingID); err != nil {
		return "", errors.New("error al guardar")
	}
	postOthers, err := botFullRowHash(ctx, tx, rid, p.BookingID, botSpecialColumnsFor(p.Op)...)
	if err != nil || postOthers != preOthers {
		return "", errors.New("verificación fallida: cambió una columna fuera del bloque especial; no se aplicó nada")
	}
	// Second validation just before commit: reference data read again.
	if again, _, err := s.botSpecialBuildNextOv(ctx, rid, p.BookingID, req, botOvFromArgs(p.Op, args)); err != nil || botSpecialAfterKey(p.Op, again) != p.After {
		return stale("los datos de referencia cambiaron durante la aplicación; no se aplicó nada")
	}
	var after specialBookingSnapshot
	_ = json.Unmarshal([]byte(spJSON), &after)
	totals := botSnapTotals(after)
	notice := fmt.Sprintf("Aplicado en la reserva #%d. Abonado: %s. Adelanto requerido: %s. Total: %s.", p.BookingID, botEUR(totals["abonado_eur"].(float64)), botEUR(totals["adelanto_requerido_eur"].(float64)), botEUR(totals["total_eur"].(float64)))
	rj, _ := json.Marshal(map[string]any{"booking_id": p.BookingID, "op": p.Op, "notice": notice})
	if _, err := tx.ExecContext(ctx, `UPDATE wa_bot_op_proposals SET result_json=?, notify_state='pending' WHERE id=?`, string(rj), p.ID); err != nil {
		return "", errors.New("error interno")
	}
	s.botSpecialAdminEvent(ctx, tx, rid, p.ID, "executed", p.Actor, strings.TrimSpace(msg.ChatJID), map[string]any{"op": p.Op, "booking_id": p.BookingID, "before_hash": p.Before, "after_hash": p.After, "approval_message_id": msg.MessageID})
	if err := tx.Commit(); err != nil {
		// Outcome unknown to us: never re-apply by guessing.
		log.Printf("[bot] wa_bot_special_admin_v2 commit_unknown rid=%d proposal=%d err=%v", rid, p.ID, err)
		return "", errors.New("no pude confirmar si se guardó; consulta la reserva antes de repetir")
	}
	s.broadcastBookingChanged(rid, p.BookingID, "booking_updated")
	return notice, nil
}

func (s *Server) markStale(ctx context.Context, rid int, p botSpecialProposalRow, msg botWebhookMessage) {
	// Runs outside the (rolled back) tx: the claim did not commit.
	_, _ = s.db.ExecContext(context.Background(), `UPDATE wa_bot_op_proposals SET state='stale', active=NULL WHERE id=? AND state='sent'`, p.ID)
	s.botSpecialAdminEvent(context.Background(), s.db, rid, p.ID, "stale", p.Actor, strings.TrimSpace(msg.ChatJID), nil)
}

var botSpecialFlushAt sync.Map

func (s *Server) botSpecialAdminFlushNotifyThrottled(ctx context.Context, rid int) {
	now := time.Now()
	if v, ok := botSpecialFlushAt.Load(rid); ok && now.Sub(v.(time.Time)) < 30*time.Second {
		return
	}
	botSpecialFlushAt.Store(rid, now)
	s.botSpecialAdminFlushNotify(ctx, rid)
}

// botSpecialAdminFlushNotify retries the notice of executed proposals whose
// notification the provider did not accept. It never writes the booking. It
// runs when a message arrives in an authorised chat or a proposal is made
// (there is no background worker), so a notice can wait for the next such event.
func (s *Server) botSpecialAdminFlushNotify(ctx context.Context, rid int) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, origin_chat_jid, result_json FROM wa_bot_op_proposals WHERE restaurant_id=? AND state='done' AND notify_state='pending' AND done_at > DATE_SUB(UTC_TIMESTAMP(), INTERVAL 1 DAY) LIMIT 5`, rid)
	if err != nil {
		return
	}
	type n struct {
		id          int64
		chat, notes string
	}
	var list []n
	for rows.Next() {
		var x n
		var rj sql.NullString
		if rows.Scan(&x.id, &x.chat, &rj) == nil {
			var r struct {
				Notice string `json:"notice"`
			}
			_ = json.Unmarshal([]byte(rj.String), &r)
			x.notes = r.Notice
			list = append(list, x)
		}
	}
	rows.Close()
	for _, x := range list {
		if x.notes == "" {
			continue
		}
		if !s.botSpecialAdminMayNotify(ctx, rid, x.chat) {
			continue
		}
		gw, ok := s.botGatewayFor(ctx, rid)
		if !ok || gw.SendText(ctx, x.chat, x.notes) != nil {
			continue
		}
		_, _ = s.db.ExecContext(ctx, `UPDATE wa_bot_op_proposals SET notify_state='sent' WHERE id=? AND notify_state='pending'`, x.id)
	}
}

// botSpecialRescheduleWrite moves a special booking to a new date, time or
// party size inside the caller's transaction. It writes ONLY the date, time,
// party size, the party-dependent normalised columns and the special block;
// then it reconciles the occupancy ledger and records the change history, the
// same side effects the backoffice PATCH has. The caller verifies afterwards
// that no other column of the row moved.
func (s *Server) botSpecialRescheduleWrite(ctx context.Context, tx *sql.Tx, rid int, p botSpecialProposalRow, old *botSpecialRow, next boNormalizedBooking, spJSON string) error {
	var floor, salon sql.NullInt64
	var custName, phone string
	var oldChildren, oldMobPeople int
	if err := tx.QueryRowContext(ctx, `SELECT preferred_floor_number, preferred_salon_id, customer_name, COALESCE(contact_phone,''), COALESCE(children,0), COALESCE(mobility_people,0) FROM bookings WHERE restaurant_id = ? AND id = ?`, rid, p.BookingID).Scan(&floor, &salon, &custName, &phone, &oldChildren, &oldMobPeople); err != nil {
		return errors.New("error interno")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE bookings SET reservation_date = ?, reservation_time = ?, party_size = ?, children = ?, has_mobility_issues = ?, mobility_people = ?, is_special_booking = ?, is_prereserva = ?, special_json = ? WHERE restaurant_id = ? AND id = ?`,
		next.ReservationDate, next.ReservationTime, next.PartySize, next.Children, boolToTinyint(next.HasMobilityIssues), next.MobilityPeople,
		boolToTinyint(next.IsSpecialBooking), boolToTinyint(next.IsPrereserva), spJSON, rid, p.BookingID); err != nil {
		return errors.New("error al guardar")
	}
	if next.ReservationDate != old.Date || next.PartySize != old.Party {
		var f, sl any
		if floor.Valid {
			f = floor.Int64
		}
		if salon.Valid {
			sl = salon.Int64
		}
		if err := s.applyBookingLocationDelta(ctx, tx, rid, old.Date, f, sl, old.Party, next.ReservationDate, f, sl, next.PartySize); err != nil {
			return errors.New("no se pudo actualizar la ocupación; no se aplicó nada")
		}
	}
	rec := func(field, oldV, newV string) error {
		if oldV == newV {
			return nil
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO booking_modifications (restaurant_id, booking_id, original_reservation_date, field_modified, old_value, new_value, modified_by, modified_by_name, customer_name, contact_phone, modification_date) VALUES (?, ?, ?, ?, ?, ?, 'whatsapp', 'Bot WhatsApp (dirección)', ?, ?, NOW())`,
			rid, p.BookingID, old.Date, field, oldV, newV, custName, phone)
		return err
	}
	oldTime := old.Time
	newTime := strings.TrimSuffix(next.ReservationTime, ":00")
	for _, e := range [][3]string{{"date", old.Date, next.ReservationDate}, {"time", oldTime, newTime}, {"party_size", fmt.Sprint(old.Party), fmt.Sprint(next.PartySize)}, {"children", fmt.Sprint(oldChildren), fmt.Sprint(next.Children)}, {"mobility_people", fmt.Sprint(oldMobPeople), fmt.Sprint(next.MobilityPeople)}} {
		if err := rec(e[0], e[1], e[2]); err != nil {
			return errors.New("no se pudo registrar el historial; no se aplicó nada")
		}
	}
	return nil
}

// botSpecialRescheduleAllowedRow rejects a reschedule of a cancelled/closed
// booking (no hidden reactivation) or of one whose original date already passed.
func botSpecialRescheduleAllowedRow(row *botSpecialRow) error {
	switch strings.ToLower(strings.TrimSpace(row.Status)) {
	case "pending", "confirmed":
	default:
		return errors.New("la reserva está en estado '" + row.Status + "': solo se mueven reservas pendientes o confirmadas")
	}
	loc, _ := time.LoadLocation("Europe/Madrid")
	if loc == nil {
		loc = time.UTC
	}
	n := time.Now().In(loc)
	today := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
	if d, err := time.Parse("2006-01-02", row.Date); err != nil || d.Before(today) {
		return errors.New("la reserva original es de una fecha pasada: no se mueve desde aquí")
	}
	return nil
}

// botSpecialRescheduleDeps summarises children/mobility values that the
// normaliser recomputes from the party size, old vs new, for the preview.
func botSpecialRescheduleDeps(current map[string]any, next boNormalizedBooking) (text string, changed bool) {
	oc, _ := current["children"].(int64)
	om, _ := current["mobility_people"].(int64)
	oh, _ := current["has_mobility_issues"].(bool)
	var parts []string
	if int(oc) != next.Children {
		parts = append(parts, fmt.Sprintf("niños %d -> %d", oc, next.Children))
	}
	if int(om) != next.MobilityPeople || oh != next.HasMobilityIssues {
		parts = append(parts, fmt.Sprintf("personas con movilidad reducida %d -> %d", om, next.MobilityPeople))
	}
	if len(parts) == 0 {
		return "", false
	}
	return " Se ajustan por el nuevo número de comensales: " + strings.Join(parts, ", ") + ".", true
}
