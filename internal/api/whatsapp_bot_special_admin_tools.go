package api

import (
	"bytes"
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
	"sort"
	"strconv"
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
		{
			Name:        "booking_propose_reschedule",
			Description: "PROPONE cambiar la fecha, la hora y/o los comensales de una reserva NORMAL (sin menú de grupo, sin arroces, no de fecha especial; para esas usa special_booking_propose_reschedule o el panel). NO lo aplica: guarda una propuesta exacta, envía al grupo una vista previa con un código y se aplica cuando la misma persona escribe 'OK' y ese código en el mismo chat. Solo cambia fecha, hora, comensales (y niños/movilidad si dependen de ellos), actualiza ocupación e historial y no avisa al cliente.",
			InputSchema: botSchema(`{"type":"object","properties":{"booking_id":{"type":"integer"},"date":{"type":"string","description":"Nueva fecha YYYY-MM-DD"},"time":{"type":"string","description":"Nueva hora HH:MM"},"party_size":{"type":"integer","description":"Nuevo número de comensales"}},"required":["booking_id"]}`),
		},
		{
			Name:        "booking_propose_cancel",
			Description: "PROPONE cancelar una reserva (normal o de fecha especial) por su booking_id. NO la cancela: guarda una propuesta exacta y envía al grupo una vista previa con un código; se aplica cuando la misma persona escribe 'OK' y ese código en el mismo chat. Al aplicarse la reserva se archiva en Canceladas y se libera la ocupación. No avisa al cliente. Pide siempre el booking_id y confirma con el usuario cuál es antes de proponer.",
			InputSchema: botSchema(`{"type":"object","properties":{"booking_id":{"type":"integer"}},"required":["booking_id"]}`),
		},
		{
			Name:        "booking_propose_edit",
			Description: "PROPONE editar datos básicos de una reserva existente (normal o de fecha especial): nombre, teléfono, email, mesa, comentario, niños, carritos o tronas. NO lo aplica: guarda una propuesta exacta y envía al grupo una vista previa con los cambios y un código; se aplica cuando la misma persona escribe 'OK' y ese código en el mismo chat. Solo cambia lo que pases; fecha, hora, comensales, salón, menús y ocupación no se tocan (para eso están booking_propose_reschedule o el panel). El comentario no se puede editar en reservas con menú de grupo (se genera solo). No avisa al cliente. Pide siempre el booking_id y confirma con el usuario qué cambia antes de proponer.",
			InputSchema: botSchema(`{"type":"object","additionalProperties":false,"properties":{"booking_id":{"type":"integer"},"customer_name":{"type":"string"},"contact_phone":{"type":"string"},"contact_phone_country_code":{"type":"string","description":"Prefijo sin +"},"contact_email":{"type":"string","description":"Vacío = email por defecto del restaurante"},"table_number":{"type":"string","description":"Vacío = sin mesa"},"commentary":{"type":"string","description":"Vacío = sin comentario"},"children":{"type":"integer"},"baby_strollers":{"type":"integer"},"high_chairs":{"type":"integer"}},"required":["booking_id"]}`),
		},
		{
			Name:        "booking_propose_set_rice",
			Description: "PROPONE poner, cambiar o quitar el arroz de una reserva NORMAL sin menú de grupo (con menú de grupo el arroz es el propio menú: se cambia desde el panel). NO lo aplica: guarda una propuesta exacta y envía al grupo una vista previa con un código; se aplica cuando la misma persona escribe 'OK' y ese código en el mismo chat. items: lista de {type, servings}; usa SOLO nombres exactos de rice_menu para la fecha de la reserva; la suma de raciones no puede superar los comensales; una lista vacía quita el arroz (la vista previa lo dice claramente). No cambia fecha, hora, comensales, menús ni ocupación. No avisa al cliente. Pide siempre el booking_id y confirma con el usuario el cambio antes de proponer.",
			InputSchema: botSchema(`{"type":"object","additionalProperties":false,"properties":{"booking_id":{"type":"integer"},"items":{"type":"array","items":{"type":"object","additionalProperties":false,"properties":{"type":{"type":"string","description":"Nombre exacto del arroz según rice_menu"},"servings":{"type":"integer","description":"Raciones (mínimo 1)"}},"required":["type","servings"]}}},"required":["booking_id","items"]}`),
		},
		{
			Name:        "booking_propose_create",
			Description: "PROPONE crear una reserva NORMAL nueva (sin menú de grupo, arroces, extras ni fecha especial: esas se crean desde el panel). NO la crea: guarda una propuesta exacta y envía al grupo una vista previa con un código; se crea cuando la misma persona escribe 'OK' y ese código en el mismo chat. Pide antes fecha, hora, comensales, nombre y teléfono (no los inventes). Rechaza fechas pasadas, días cerrados y fechas especiales. Si ya existe una reserva con los mismos datos (fecha, hora, teléfono y comensales), lo dice y solo sigue si el usuario confirma expresamente el duplicado con allow_duplicate_of. No avisa al cliente ni emite avisos externos.",
			InputSchema: botSchema(`{"type":"object","additionalProperties":false,"properties":{"date":{"type":"string","description":"YYYY-MM-DD"},"time":{"type":"string","description":"HH:MM"},"party_size":{"type":"integer"},"customer_name":{"type":"string"},"contact_phone":{"type":"string"},"contact_phone_country_code":{"type":"string","description":"Prefijo sin + (por defecto 34)"},"contact_email":{"type":"string"},"commentary":{"type":"string"},"children":{"type":"integer"},"baby_strollers":{"type":"integer"},"high_chairs":{"type":"integer"},"preferred_floor_number":{"type":"integer"},"allow_duplicate_of":{"type":"integer","description":"Solo para confirmar expresamente un duplicado legítimo: booking_id de la reserva existente igual"}},"required":["date","time","party_size","customer_name","contact_phone"]}`),
		},
		{
			Name:        "booking_propose_restore",
			Description: "PROPONE restaurar una reserva que cancelaste con booking_propose_cancel, tal como estaba (mismo id, todos los datos, menú especial y abonos incluidos; no usa la reactivación del panel, que pierde datos). NO la restaura: envía al grupo una vista previa con un código; se aplica cuando la misma persona escribe 'OK' y ese código en el mismo chat. Solo vale para reservas canceladas por el bot con copia completa guardada. No avisa al cliente.",
			InputSchema: botSchema(`{"type":"object","properties":{"booking_id":{"type":"integer"}},"required":["booking_id"]}`),
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

func botIsResched(op string) bool { return op == "reschedule" || op == "reschedule_normal" }

func botOvFromArgs(op string, a botSpecialProposalArgs) *botSpecialOverride {
	if !botIsResched(op) {
		return nil
	}
	return &botSpecialOverride{Date: a.NewDate, Time: a.NewTime, Party: a.NewParty}
}

// botSpecialAfterKey is the exact "after" the approver saw: the special block
// plus, for a reschedule, the final date, time and party size.
func botSpecialAfterKey(op string, next boNormalizedBooking) string {
	if op == "reschedule_normal" {
		return botSha("normal|" + next.ReservationDate + "|" + next.ReservationTime + "|" + fmt.Sprint(next.PartySize) + "|" + fmt.Sprint(next.Children, next.HasMobilityIssues, next.MobilityPeople))
	}
	if op == "reschedule" {
		return botSha(botCanon(next.SpecialJSON) + "|" + next.ReservationDate + "|" + next.ReservationTime + "|" + fmt.Sprint(next.PartySize) + "|" + fmt.Sprint(next.Children, next.HasMobilityIssues, next.MobilityPeople))
	}
	return botSha(botCanon(next.SpecialJSON))
}

// botSpecialColumnsFor lists the only bookings columns the op may change.
func botSpecialColumnsFor(op string) []string {
	if op == "reschedule_normal" {
		return []string{"reservation_date", "reservation_time", "party_size", "children", "has_mobility_issues", "mobility_people"}
	}
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
	in.SpecialTouched = req != nil
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
	if op == "reschedule_normal" {
		return nil, nil
	}
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
	case "reschedule", "reschedule_normal":
		if op == "reschedule_normal" && len(in.Menus) > 0 {
			return botJSON(map[string]any{"error": "esta herramienta no admite menús"}), nil
		}
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
	if op == "cancel" {
		return s.botCancelPropose(ctx, rid, msg, actor, origin, in.BookingID)
	}
	if op == "restore" {
		return s.botRestorePropose(ctx, rid, msg, actor, origin, in.BookingID)
	}
	row, err := s.botLoadSpecialRow(ctx, rid, in.BookingID)
	if op == "reschedule_normal" {
		if err != nil {
			return botJSON(map[string]any{"error": "no hay una reserva con ese id"}), nil
		}
		if row.Special {
			return botJSON(map[string]any{"error": "esa reserva es de fecha especial: usa special_booking_propose_reschedule"}), nil
		}
	} else if err != nil || !row.Special {
		return botJSON(map[string]any{"error": "no hay una reserva de fecha especial con ese id"}), nil
	}
	var snap specialBookingSnapshot
	if op != "reschedule_normal" && json.Unmarshal([]byte(row.Raw), &snap) != nil {
		return botJSON(map[string]any{"error": "el bloque especial almacenado no se pudo leer"}), nil
	}
	if botIsResched(op) {
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
	if op == "reschedule_normal" {
		if nerr := s.botNormalRescheduleGuard(ctx, rid, curMap, next); nerr != nil {
			return botJSON(map[string]any{"error": nerr.Error()}), nil
		}
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
	if botIsResched(op) {
		depsText, _ = botSpecialRescheduleDeps(curMap, next)
	}
	if op == "adelanto" {
		preview = fmt.Sprintf("Propuesta (reserva #%d, %s, %d comensales): registrar abono de %s por %s. Abonado: %s -> %s. Adelanto requerido: %s. Solo anota un pago ya recibido: no cobra ni envía nada al cliente.",
			in.BookingID, row.Date, row.Party, botEUR(args.Amount), args.Method, botEUR(bTotals["abonado_eur"].(float64)), botEUR(aTotals["abonado_eur"].(float64)), botEUR(aTotals["adelanto_requerido_eur"].(float64)))
	} else if op == "reschedule_normal" {
		preview = fmt.Sprintf("Propuesta (reserva #%d, sin menú especial): mover de %s %s (%d comensales) a %s %s (%d comensales). Estado actual: %s.%s Se actualizan la ocupación y el historial de cambios. SIN comprobación de aforo (se fuerza como en el panel de dirección). No se avisa al cliente.",
			in.BookingID, row.Date, row.Time, row.Party, next.ReservationDate, strings.TrimSuffix(next.ReservationTime, ":00"), next.PartySize, row.Status, depsText)
	} else if op == "reschedule" {
		preview = fmt.Sprintf("Propuesta (reserva #%d): mover de %s %s (%d comensales) a %s %s (%d comensales). Menús: %s. Total: %s -> %s. Abonos registrados se conservan (%s). Estado actual: %s.%s Se actualizan la ocupación y el historial de cambios. SIN comprobación de aforo (se fuerza como en el panel de dirección). No se avisa al cliente.",
			in.BookingID, row.Date, row.Time, row.Party, next.ReservationDate, strings.TrimSuffix(next.ReservationTime, ":00"), next.PartySize, botSpecialDiffMenus(after), botEUR(bTotals["total_eur"].(float64)), botEUR(aTotals["total_eur"].(float64)), botEUR(aTotals["abonado_eur"].(float64)), row.Status, depsText)
	} else {
		preview = fmt.Sprintf("Propuesta (reserva #%d, %s, %d comensales): cambiar menús. Antes: %s. Después: %s. Total: %s -> %s. Abonos registrados se conservan (%s).",
			in.BookingID, row.Date, row.Party, botSpecialDiffMenus(snap), botSpecialDiffMenus(after), botEUR(bTotals["total_eur"].(float64)), botEUR(aTotals["total_eur"].(float64)), botEUR(aTotals["abonado_eur"].(float64)))
	}
	return s.botProposalPersist(ctx, rid, msg, op, in.BookingID, args, "", actor, origin, beforeHash, afterHash, preview, code)
}

// botProposalPersist stores a proposal, supersedes the previous one for the
// booking, sends the preview + code straight to the origin chat and activates
// it only after the gateway accepted the send. Shared by every op.
func (s *Server) botProposalPersist(ctx context.Context, rid int, msg botWebhookMessage, op string, bookingID int64, args any, dedupKey string, actor, origin, beforeHash, afterHash, preview, code string) (string, error) {
	in := struct{ BookingID int64 }{bookingID}
	argsJSON, _ := json.Marshal(args)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return botJSON(map[string]any{"error": "no se pudo guardar la propuesta"}), nil
	}
	defer tx.Rollback()
	var superseded int64
	var oldChats []string
	chatsQ := `SELECT DISTINCT origin_chat_jid FROM wa_bot_op_proposals WHERE restaurant_id=? AND booking_id=? AND active=1`
	supQ := `UPDATE wa_bot_op_proposals SET state='superseded', active=NULL WHERE restaurant_id=? AND booking_id=? AND active=1`
	supArgs := []any{rid, in.BookingID}
	if dedupKey != "" {
		// Create proposals carry booking_id=0 and uq_wa_op_active is
		// (restaurant_id, booking_id, active): two active creates would collide.
		// So at most one create pends per tenant and a new one supersedes ANY
		// pending create, whatever its natural key. The dedup key still travels
		// in args for correlation and the execute re-check.
		chatsQ = `SELECT DISTINCT origin_chat_jid FROM wa_bot_op_proposals WHERE restaurant_id=? AND op='create' AND active=1`
		supQ = `UPDATE wa_bot_op_proposals SET state='superseded', active=NULL WHERE restaurant_id=? AND op='create' AND active=1`
		supArgs = []any{rid}
	}
	if rs, qerr := tx.QueryContext(ctx, chatsQ, supArgs...); qerr == nil {
		for rs.Next() {
			var c string
			if rs.Scan(&c) == nil {
				oldChats = append(oldChats, c)
			}
		}
		rs.Close()
	}
	if res, err := tx.ExecContext(ctx, supQ, supArgs...); err == nil {
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

	supNotice := fmt.Sprintf("La propuesta pendiente sobre la reserva #%d se ha sustituido por otra más reciente. El código anterior ya no vale.", in.BookingID)
	if dedupKey != "" {
		supNotice = "La propuesta pendiente de crear una reserva se ha sustituido por otra más reciente. El código anterior ya no vale."
	}
	for _, c := range oldChats {
		if !s.botSpecialAdminMayNotify(ctx, rid, c) {
			continue
		}
		if gw, okGw := s.botGatewayFor(ctx, rid); okGw {
			_ = gw.SendText(ctx, c, supNotice)
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
	if p.Op == "edit" {
		return s.botEditExecute(ctx, tx, rid, msg, p)
	}
	if p.Op == "set_rice" {
		return s.botRiceExecute(ctx, tx, rid, msg, p)
	}
	if p.Op == "create" {
		return s.botCreateExecute(ctx, tx, rid, msg, p)
	}
	if p.Op == "restore" {
		return s.botRestoreExecute(ctx, tx, rid, msg, p)
	}
	row, err := botScanSpecialRow(tx.QueryRowContext(ctx, botSpecialRowSelect+" FOR UPDATE", rid, p.BookingID))
	if err != nil {
		return "", errors.New("la reserva ya no existe")
	}
	if p.Op == "reschedule_normal" {
		if row.Special {
			return "", errors.New("la reserva ahora es de fecha especial; pide una propuesta nueva")
		}
	} else if p.Op != "cancel" && !row.Special {
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
	if p.Op == "cancel" {
		return s.botCancelExecute(ctx, tx, rid, msg, p, row)
	}
	preOthers, err := botFullRowHash(ctx, tx, rid, p.BookingID, botSpecialColumnsFor(p.Op)...)
	if err != nil {
		return "", errors.New("error interno")
	}
	if err := botSpecialLockRefs(ctx, tx, rid, row.Date); err != nil {
		return "", errors.New("error interno")
	}
	if botIsResched(p.Op) {
		if rerr := botSpecialRescheduleAllowedRow(row); rerr != nil {
			return "", rerr
		}
	}
	if botIsResched(p.Op) && args.NewDate != "" && args.NewDate != row.Date {
		if err := botSpecialLockRefs(ctx, tx, rid, args.NewDate); err != nil {
			return "", errors.New("error interno")
		}
	}
	var snap specialBookingSnapshot
	if p.Op != "reschedule_normal" && json.Unmarshal([]byte(row.Raw), &snap) != nil {
		return "", errors.New("bloque especial ilegible")
	}
	req, err := botSpecialApplyArgs(p.Op, snap, args)
	if err != nil {
		return "", err
	}
	next, curMapX, err := s.botSpecialBuildNextOv(ctx, rid, p.BookingID, req, botOvFromArgs(p.Op, args))
	if err != nil {
		return "", errors.New("la validación rechaza el cambio: " + err.Error())
	}
	if p.Op == "reschedule_normal" {
		if nerr := s.botNormalRescheduleGuard(ctx, rid, curMapX, next); nerr != nil {
			return stale(nerr.Error())
		}
	}
	if botSpecialAfterKey(p.Op, next) != p.After {
		return stale("el resultado ya no coincide con la vista previa (menús o precios cambiados); pide una propuesta nueva")
	}
	// Special-only write: the three special columns and nothing else.
	spJSON, _ := next.SpecialJSON.(string)
	if botIsResched(p.Op) {
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
	if again, againCur, err := s.botSpecialBuildNextOv(ctx, rid, p.BookingID, req, botOvFromArgs(p.Op, args)); err != nil || botSpecialAfterKey(p.Op, again) != p.After || (p.Op == "reschedule_normal" && s.botNormalRescheduleGuard(ctx, rid, againCur, again) != nil) {
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
	if p.Op == "reschedule_normal" {
		if _, err := tx.ExecContext(ctx, `UPDATE bookings SET reservation_date = ?, reservation_time = ?, party_size = ?, children = ?, has_mobility_issues = ?, mobility_people = ? WHERE restaurant_id = ? AND id = ?`,
			next.ReservationDate, next.ReservationTime, next.PartySize, next.Children, boolToTinyint(next.HasMobilityIssues), next.MobilityPeople, rid, p.BookingID); err != nil {
			return errors.New("error al guardar")
		}
	} else if _, err := tx.ExecContext(ctx, `UPDATE bookings SET reservation_date = ?, reservation_time = ?, party_size = ?, children = ?, has_mobility_issues = ?, mobility_people = ?, is_special_booking = ?, is_prereserva = ?, special_json = ? WHERE restaurant_id = ? AND id = ?`,
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

// botSpecialRowAllowedFor rejects an operation on a cancelled/closed booking
// or one in the past. verbPlural/verbSingular name the operation in the
// messages ("mueven"/"mueve", "editan"/"edita", "cancelan"/"cancela").
func botSpecialRowAllowedFor(row *botSpecialRow, verbPlural, verbSingular string) error {
	switch strings.ToLower(strings.TrimSpace(row.Status)) {
	case "pending", "confirmed":
	default:
		return errors.New("la reserva está en estado '" + row.Status + "': solo se " + verbPlural + " reservas pendientes o confirmadas")
	}
	loc, _ := time.LoadLocation("Europe/Madrid")
	if loc == nil {
		loc = time.UTC
	}
	n := time.Now().In(loc)
	today := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
	if d, err := time.Parse("2006-01-02", row.Date); err != nil || d.Before(today) {
		return errors.New("la reserva original es de una fecha pasada: no se " + verbSingular + " desde aquí")
	}
	return nil
}

// botSpecialRescheduleAllowedRow rejects a reschedule of a cancelled/closed
// booking (no hidden reactivation) or of one whose original date already passed.
func botSpecialRescheduleAllowedRow(row *botSpecialRow) error {
	return botSpecialRowAllowedFor(row, "mueven", "mueve")
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

// botNormalRescheduleGuard limits the normal-booking reschedule to bookings
// whose other columns do not depend on the date or party size: no group menu,
// no rice, no special block, and a destination that is not a special date.
func (s *Server) botNormalRescheduleGuard(ctx context.Context, rid int, cur map[string]any, next boNormalizedBooking) error {
	if v, _ := cur["special_menu"].(bool); v || next.SpecialMenu {
		return errors.New("la reserva tiene menú de grupo: cámbiala desde el panel")
	}
	if v, ok := cur["menu_de_grupo_id"].(int64); ok && v > 0 {
		return errors.New("la reserva tiene menú de grupo: cámbiala desde el panel")
	}
	if strings.TrimSpace(anyToString(cur["arroz_type"])) != "" || strings.TrimSpace(anyToString(cur["arroz_servings"])) != "" {
		return errors.New("la reserva tiene arroces que dependen de los comensales: cámbiala desde el panel")
	}
	if strings.TrimSpace(anyToString(cur["principales_json"])) != "" {
		return errors.New("la reserva tiene principales por comensal: cámbiala desde el panel")
	}
	if st, _, err := s.loadSpecialDateSettings(ctx, rid, next.ReservationDate); err != nil || st != nil {
		return errors.New("la fecha destino es una fecha especial (o no se pudo comprobar): no se mueve una reserva normal ahí")
	}
	return nil
}

func (s *Server) botCancelPropose(ctx context.Context, rid int, msg botWebhookMessage, actor, origin string, bookingID int64) (string, error) {
	row, err := s.botLoadSpecialRow(ctx, rid, bookingID)
	if err != nil {
		return botJSON(map[string]any{"error": "no hay una reserva con ese id"}), nil
	}
	if rerr := botSpecialRowAllowedFor(row, "cancelan", "cancela"); rerr != nil {
		return botJSON(map[string]any{"error": rerr.Error()}), nil
	}
	if berr := botCancelBlockers(ctx, s.db, rid, bookingID, false); berr != nil {
		return botJSON(map[string]any{"error": berr.Error()}), nil
	}
	beforeHash, herr := botFullRowHash(ctx, s.db, rid, bookingID)
	if herr != nil {
		return botJSON(map[string]any{"error": "no se pudo leer la reserva"}), nil
	}
	code, cerr := botSpecialNewCode()
	if cerr != nil {
		return botJSON(map[string]any{"error": "no se pudo generar la propuesta"}), nil
	}
	extra := ""
	if row.Special {
		var snap specialBookingSnapshot
		if json.Unmarshal([]byte(row.Raw), &snap) == nil {
			extra = fmt.Sprintf(" Es de fecha especial; abonado registrado: %s (los abonos y el menú especial NO se recuperan automáticamente si se reactiva desde el panel).", botEUR(botSnapTotals(snap)["abonado_eur"].(float64)))
		}
	}
	preview := fmt.Sprintf("Propuesta: CANCELAR la reserva #%d (%s %s, %d comensales, estado %s).%s Se archiva en Canceladas y se libera la ocupación. Esta operación no mueve dinero: no ejecuta ningún reembolso ni cobro y no aplica ninguna política de depósito; los pagos ya registrados ni se devuelven ni se modifican (el abono queda solo en la copia de la reserva). No emite el aviso n8n booking.cancelled del panel. No se avisa al cliente ni se envían mensajes. Se conserva una copia completa para poder restaurarla tal cual (booking_propose_restore).",
		bookingID, row.Date, row.Time, row.Party, row.Status, extra)
	afterHash := botSha("cancel|" + beforeHash)
	return s.botProposalPersist(ctx, rid, msg, "cancel", bookingID, botSpecialProposalArgs{}, "", actor, origin, beforeHash, afterHash, preview, code)
}

// botCancelExecute archives and deletes the booking inside the approval
// transaction (same data moves as the panel cancel: cancelled_bookings row,
// occupancy release, DELETE) and keeps a full copy of the row in the
// proposal result for restoration. It does NOT purge booking documents and
// does NOT emit the n8n webhook or notify the customer.
func (s *Server) botCancelExecute(ctx context.Context, tx *sql.Tx, rid int, msg botWebhookMessage, p botSpecialProposalRow, row *botSpecialRow) (string, error) {
	if rerr := botSpecialRescheduleAllowedRow(row); rerr != nil {
		return "", rerr
	}
	if berr := botCancelBlockers(ctx, tx, rid, p.BookingID, true); berr != nil {
		return "", berr
	}
	// Full copy of the row (every column) for restoration.
	rows, err := tx.QueryContext(ctx, "SELECT * FROM bookings WHERE restaurant_id = ? AND id = ?", rid, p.BookingID)
	if err != nil {
		return "", errors.New("error interno")
	}
	cols, _ := rows.Columns()
	backup := map[string]any{}
	if rows.Next() {
		vals := make([]sql.RawBytes, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if rows.Scan(ptrs...) != nil {
			rows.Close()
			return "", errors.New("error interno")
		}
		for i, c := range cols {
			if vals[i] == nil {
				backup[c] = nil
			} else {
				backup[c] = string(vals[i])
			}
		}
	}
	rows.Close()
	if len(backup) == 0 {
		return "", errors.New("la reserva ya no existe")
	}
	mhRows, merr := tx.QueryContext(ctx, "SELECT * FROM modification_history WHERE booking_id = ? ORDER BY id", p.BookingID)
	if merr != nil {
		return "", errors.New("error interno")
	}
	modHist, merr := botScanAllRows(mhRows)
	if merr != nil {
		return "", errors.New("error interno")
	}
	var floor, salon sql.NullInt64
	var custName, phone, email, commentary, arroz, arrozServ, principales sql.NullString
	var strollers, chairs, specialMenu, groupMenu sql.NullInt64
	var date, tm string
	var party int
	if err := tx.QueryRowContext(ctx, `SELECT DATE_FORMAT(reservation_date,'%Y-%m-%d'), TIME_FORMAT(reservation_time,'%H:%i:%s'), party_size, customer_name, contact_phone, contact_email, commentary, arroz_type, arroz_servings, babyStrollers, highChairs, special_menu, menu_de_grupo_id, principales_json, preferred_floor_number, preferred_salon_id FROM bookings WHERE restaurant_id = ? AND id = ?`, rid, p.BookingID).
		Scan(&date, &tm, &party, &custName, &phone, &email, &commentary, &arroz, &arrozServ, &strollers, &chairs, &specialMenu, &groupMenu, &principales, &floor, &salon); err != nil {
		return "", errors.New("error interno")
	}
	cres, err := tx.ExecContext(ctx, `INSERT INTO cancelled_bookings (restaurant_id, booking_id, reservation_date, party_size, reservation_time, customer_name, contact_phone, contact_email, commentary, arroz_type, arroz_servings, babyStrollers, highChairs, cancellation_date, cancelled_by, cancelled_by_user_id, cancelled_by_name, special_menu, menu_de_grupo_id, principales_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NOW(), 'whatsapp', 0, 'Bot WhatsApp (dirección)', ?, ?, ?)`,
		rid, p.BookingID, date, party, tm, custName.String, phone.String, email.String, commentary.String, nullStringOrNil(arroz), nullStringOrNil(arrozServ), nullIntToInt(strollers), nullIntToInt(chairs), int64OrZero(specialMenu), nullInt64OrNil(groupMenu), nullStringOrNil(principales))
	if err != nil {
		return "", errors.New("no se pudo archivar la reserva; no se aplicó nada")
	}
	cancelledID, cerr := cres.LastInsertId()
	if cerr != nil || cancelledID <= 0 {
		return "", errors.New("no se pudo archivar la reserva; no se aplicó nada")
	}
	if err := s.applyBookingLocationOccupancy(ctx, tx, rid, date, floor, salon, party, -1); err != nil {
		return "", errors.New("no se pudo liberar la ocupación; no se aplicó nada")
	}
	res, err := tx.ExecContext(ctx, "DELETE FROM bookings WHERE id = ? AND restaurant_id = ?", p.BookingID, rid)
	if err != nil {
		return "", errors.New("error al cancelar; no se aplicó nada")
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return "", errors.New("la reserva ya no existe")
	}
	notice := fmt.Sprintf("Reserva #%d cancelada (%s %s, %d comensales). Archivada en Canceladas. No se ha avisado al cliente ni se ha hecho ningún reembolso o cobro.", p.BookingID, date, strings.TrimSuffix(tm, ":00"), party)
	rj, _ := json.Marshal(map[string]any{"booking_id": p.BookingID, "op": p.Op, "notice": notice, "row_backup": backup, "modification_history_backup": modHist, "cancelled_id": cancelledID})
	if _, err := tx.ExecContext(ctx, `UPDATE wa_bot_op_proposals SET result_json=?, notify_state='pending' WHERE id=?`, string(rj), p.ID); err != nil {
		return "", errors.New("error interno")
	}
	s.botSpecialAdminEvent(ctx, tx, rid, p.ID, "executed", p.Actor, strings.TrimSpace(msg.ChatJID), map[string]any{"op": p.Op, "booking_id": p.BookingID, "before_hash": p.Before, "after_hash": p.After, "approval_message_id": msg.MessageID})
	if err := tx.Commit(); err != nil {
		log.Printf("[bot] wa_bot_special_admin_v2 commit_unknown rid=%d proposal=%d err=%v", rid, p.ID, err)
		return "", errors.New("no pude confirmar si se guardó; consulta la reserva antes de repetir")
	}
	s.broadcastBookingChanged(rid, p.BookingID, "booking_cancelled")
	return notice, nil
}

// botRestoreSource loads the full-row backup written by a bot cancel plus the
// matching cancelled_bookings archive row.
func (s *Server) botRestoreSource(ctx context.Context, q interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}, rid int, bookingID int64, lock bool) (backup map[string]any, cancelledID, proposalID int64, err error) {
	backup, _, cancelledID, proposalID, err = s.botRestoreSourceFull(ctx, q, rid, bookingID, lock)
	return
}

// RETENTION INVARIANT: the result_json of a cancel proposal in state 'done'
// (row_backup, modification_history_backup, cancelled_id) is the ONLY complete
// copy of the cancelled booking that booking_propose_restore can use. Any future
// cleanup or retention job on wa_bot_op_proposals must exclude op='cancel' rows
// in state 'done' whose booking is still absent from bookings.
func (s *Server) botRestoreSourceFull(ctx context.Context, q interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}, rid int, bookingID int64, lock bool) (backup map[string]any, modHist []map[string]any, cancelledID, proposalID int64, err error) {
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	var rj sql.NullString
	if err = q.QueryRowContext(ctx, `SELECT id, result_json FROM wa_bot_op_proposals WHERE restaurant_id=? AND booking_id=? AND op='cancel' AND state='done' ORDER BY id DESC LIMIT 1`+suffix, rid, bookingID).Scan(&proposalID, &rj); err != nil {
		return nil, nil, 0, 0, errors.New("no hay una cancelación hecha por el bot con copia completa de esa reserva (las canceladas desde el panel se reactivan allí y pierden datos)")
	}
	var r struct {
		Backup  map[string]any   `json:"row_backup"`
		ModHist []map[string]any `json:"modification_history_backup"`
		CID     int64            `json:"cancelled_id"`
	}
	if json.Unmarshal([]byte(rj.String), &r) != nil || len(r.Backup) == 0 {
		return nil, nil, 0, 0, errors.New("la copia de la reserva no está disponible")
	}
	if r.CID <= 0 {
		return nil, nil, 0, 0, errors.New("la copia de la cancelación no identifica su archivo en Canceladas")
	}
	// The archive row must be the exact one this cancel created (same id), not
	// merely the latest for the booking.
	if err = q.QueryRowContext(ctx, `SELECT id FROM cancelled_bookings WHERE id=? AND restaurant_id=? AND booking_id=?`+suffix, r.CID, rid, bookingID).Scan(&cancelledID); err != nil {
		return nil, nil, 0, 0, errors.New("la reserva ya no figura en Canceladas")
	}
	var n int
	if err = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookings WHERE restaurant_id=? AND id=?`, rid, bookingID).Scan(&n); err != nil || n != 0 {
		return nil, nil, 0, 0, errors.New("ya existe una reserva con ese id")
	}
	return r.Backup, r.ModHist, cancelledID, proposalID, nil
}

func botBackupStr(b map[string]any, k string) string {
	if v, ok := b[k].(string); ok {
		return v
	}
	return ""
}

func botBackupKey(backup map[string]any, modHist []map[string]any, cancelledID, proposalID int64) (before, after string) {
	h := botSha(botCanon(backup) + "|" + botCanon(modHist))
	return botSha(fmt.Sprintf("restore|%d|%d|%s", proposalID, cancelledID, h)), botSha("restored|" + h)
}

func (s *Server) botRestoreDateOK(ctx context.Context, q interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}, rid int, backup map[string]any) error {
	date := botBackupStr(backup, "reservation_date")
	if len(date) > 10 {
		date = date[:10]
	}
	loc, _ := time.LoadLocation("Europe/Madrid")
	if loc == nil {
		loc = time.UTC
	}
	n := time.Now().In(loc)
	today := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
	if d, err := time.Parse("2006-01-02", date); err != nil || d.Before(today) {
		return errors.New("la fecha de la reserva ya pasó: no se restaura desde aquí")
	}
	var closed int
	if err := q.QueryRowContext(ctx, "SELECT COUNT(*) FROM closed_days WHERE restaurant_id = ? AND date = ?", rid, date).Scan(&closed); err != nil || closed > 0 {
		return errors.New("el día " + date + " está cerrado (o no se pudo comprobar): no se restaura")
	}
	return nil
}

func (s *Server) botRestorePropose(ctx context.Context, rid int, msg botWebhookMessage, actor, origin string, bookingID int64) (string, error) {
	backup, modHist, cancelledID, proposalID, err := s.botRestoreSourceFull(ctx, s.db, rid, bookingID, false)
	if err != nil {
		return botJSON(map[string]any{"error": err.Error()}), nil
	}
	if derr := s.botRestoreDateOK(ctx, s.db, rid, backup); derr != nil {
		return botJSON(map[string]any{"error": derr.Error()}), nil
	}
	code, cerr := botSpecialNewCode()
	if cerr != nil {
		return botJSON(map[string]any{"error": "no se pudo generar la propuesta"}), nil
	}
	extra := ""
	if sj := botBackupStr(backup, "special_json"); strings.TrimSpace(sj) != "" {
		var snap specialBookingSnapshot
		if json.Unmarshal([]byte(sj), &snap) == nil {
			extra = fmt.Sprintf(" Es de fecha especial; se recupera el menú especial y el abonado registrado (%s).", botEUR(botSnapTotals(snap)["abonado_eur"].(float64)))
		}
	}
	before, after := botBackupKey(backup, modHist, cancelledID, proposalID)
	preview := fmt.Sprintf("Propuesta: RESTAURAR la reserva #%d tal como estaba antes de cancelarla (%s %s, %s comensales, estado %s), con el mismo id y todos sus datos. Restaura la copia histórica guardada al cancelar, sin recalcular menús, precios, abonos ni condiciones actuales.%s Se quita de Canceladas y se vuelve a ocupar el aforo (sin comprobación de aforo). No se ejecuta ningún cobro ni reembolso. No se avisa al cliente.",
		bookingID, strings.TrimSpace(botBackupStr(backup, "reservation_date")), strings.TrimSuffix(botBackupStr(backup, "reservation_time"), ":00"), botBackupStr(backup, "party_size"), botBackupStr(backup, "status"), extra)
	return s.botProposalPersist(ctx, rid, msg, "restore", bookingID, botSpecialProposalArgs{}, "", actor, origin, before, after, preview, code)
}

func (s *Server) botRestoreExecute(ctx context.Context, tx *sql.Tx, rid int, msg botWebhookMessage, p botSpecialProposalRow) (string, error) {
	stale := func(why string) (string, error) {
		_ = tx.Rollback()
		s.markStale(ctx, rid, p, msg)
		return "", errors.New(why)
	}
	backup, modHist, cancelledID, proposalID, err := s.botRestoreSourceFull(ctx, tx, rid, p.BookingID, true)
	if err != nil {
		return stale(err.Error())
	}
	if before, _ := botBackupKey(backup, modHist, cancelledID, proposalID); before != p.Before {
		return stale("la copia o el archivo de la reserva cambiaron; pide una propuesta nueva")
	}
	if derr := s.botRestoreDateOK(ctx, tx, rid, backup); derr != nil {
		return stale(derr.Error())
	}
	cols := make([]string, 0, len(backup))
	for c := range backup {
		if !regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`).MatchString(c) {
			return stale("copia con columnas inesperadas")
		}
		cols = append(cols, c)
	}
	sort.Strings(cols)
	q := make([]string, len(cols))
	args := make([]any, len(cols))
	names := make([]string, len(cols))
	for i, c := range cols {
		q[i] = "?"
		names[i] = "`" + c + "`"
		args[i] = backup[c]
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO bookings ("+strings.Join(names, ",")+") VALUES ("+strings.Join(q, ",")+")", args...); err != nil {
		return "", errors.New("no se pudo restaurar la reserva; no se aplicó nada")
	}
	for _, mh := range modHist {
		mcols := make([]string, 0, len(mh))
		for c := range mh {
			if !regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`).MatchString(c) {
				return "", errors.New("historial con columnas inesperadas; no se aplicó nada")
			}
			mcols = append(mcols, c)
		}
		sort.Strings(mcols)
		mq := make([]string, len(mcols))
		mn := make([]string, len(mcols))
		ma := make([]any, len(mcols))
		for i, c := range mcols {
			mq[i], mn[i], ma[i] = "?", "`"+c+"`", mh[c]
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO modification_history ("+strings.Join(mn, ",")+") VALUES ("+strings.Join(mq, ",")+")", ma...); err != nil {
			return "", errors.New("no se pudo restaurar el historial; no se aplicó nada")
		}
	}
	party, _ := strconv.Atoi(botBackupStr(backup, "party_size"))
	var floor, salon any
	if v, e := strconv.ParseInt(botBackupStr(backup, "preferred_floor_number"), 10, 64); e == nil {
		floor = v
	}
	if v, e := strconv.ParseInt(botBackupStr(backup, "preferred_salon_id"), 10, 64); e == nil {
		salon = v
	}
	date := botBackupStr(backup, "reservation_date")
	if len(date) > 10 {
		date = date[:10]
	}
	if err := s.applyBookingLocationOccupancy(ctx, tx, rid, date, floor, salon, party, 1); err != nil {
		return "", errors.New("no se pudo recuperar la ocupación; no se aplicó nada")
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM cancelled_bookings WHERE id = ? AND restaurant_id = ?", cancelledID, rid); err != nil {
		return "", errors.New("no se pudo quitar de Canceladas; no se aplicó nada")
	}
	hashAfter, herr := botFullRowHash(ctx, tx, rid, p.BookingID)
	if herr != nil || hashAfter == "" {
		return "", errors.New("verificación fallida; no se aplicó nada")
	}
	notice := fmt.Sprintf("Reserva #%d restaurada tal como estaba. Quitada de Canceladas. No se ha avisado al cliente ni se ha hecho ningún cobro o reembolso.", p.BookingID)
	rj, _ := json.Marshal(map[string]any{"booking_id": p.BookingID, "op": p.Op, "notice": notice})
	if _, err := tx.ExecContext(ctx, `UPDATE wa_bot_op_proposals SET result_json=?, notify_state='pending' WHERE id=?`, string(rj), p.ID); err != nil {
		return "", errors.New("error interno")
	}
	s.botSpecialAdminEvent(ctx, tx, rid, p.ID, "executed", p.Actor, strings.TrimSpace(msg.ChatJID), map[string]any{"op": p.Op, "booking_id": p.BookingID, "before_hash": p.Before, "after_hash": p.After, "approval_message_id": msg.MessageID})
	if err := tx.Commit(); err != nil {
		log.Printf("[bot] wa_bot_special_admin_v2 commit_unknown rid=%d proposal=%d err=%v", rid, p.ID, err)
		return "", errors.New("no pude confirmar si se guardó; consulta la reserva antes de repetir")
	}
	s.broadcastBookingChanged(rid, p.BookingID, "booking_created")
	return notice, nil
}

// botCreateArgs are the validated inputs of a booking_propose_create proposal.
// DedupKey is the natural identity of the booking (date, time, canonical phone
// and party size; the customer name is NOT identity) and correlates proposals.
type botCreateArgs struct {
	DedupKey         string `json:"dedup_key"`
	Date             string `json:"date"`
	Time             string `json:"time"`
	PartySize        int    `json:"party_size"`
	CustomerName     string `json:"customer_name"`
	ContactPhone     string `json:"contact_phone"`
	ContactPhoneCC   string `json:"contact_phone_country_code"`
	ContactEmail     string `json:"contact_email,omitempty"`
	Commentary       string `json:"commentary,omitempty"`
	Children         *int   `json:"children,omitempty"`
	BabyStrollers    *int   `json:"baby_strollers,omitempty"`
	HighChairs       *int   `json:"high_chairs,omitempty"`
	PreferredFloor   *int   `json:"preferred_floor_number,omitempty"`
	AllowDuplicateOf int64  `json:"allow_duplicate_of,omitempty"`
}

func botCreateDedupKey(rid int, b boNormalizedBooking) string {
	return botSha(fmt.Sprintf("create|%d|%s|%s|%s|%s|%d", rid, b.ReservationDate, b.ReservationTime, b.ContactPhoneCountryCode, b.ContactPhone, b.PartySize))
}

// botCreateAfterKey binds the preview to the FULL resolved set the insert will
// write (validator output: fallback email, resolved floor, capped children...),
// so any drift between propose and execute is a stale, never a silent change.
func botCreateAfterKey(rid int, b boNormalizedBooking, allowDup int64) string {
	return botSha("createv1|" + botCanon(map[string]any{
		"rid": rid, "date": b.ReservationDate, "time": b.ReservationTime,
		"party": b.PartySize, "children": b.Children,
		"name": b.CustomerName, "phone": b.ContactPhone, "cc": b.ContactPhoneCountryCode,
		"email": b.ContactEmail, "commentary": b.Commentary.String, "commentary_set": b.Commentary.Valid,
		"strollers": b.BabyStrollers, "chairs": b.HighChairs,
		"floor": nullableInt64OrNil(b.PreferredFloorNumber),
		"special_menu": false, "arroz": nil, "extras": nil, "group_menu": nil, "special_json": nil,
		"allow_duplicate_of": allowDup,
	}))
}

// botCreateValidate mirrors the panel create contract: the bot guards (past
// date, closed day, special date) run first and fail closed, then the exact
// panel validator normalizes and validates the rest. Reference reads (floors,
// fallback email, special settings) use the server handle like the reschedule
// path; the execute re-validates before commit and compares the after key.
func (s *Server) botCreateValidate(ctx context.Context, q interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}, rid int, in botCreateArgs) (boNormalizedBooking, error) {
	out := boNormalizedBooking{}
	date := strings.TrimSpace(in.Date)
	d, derr := time.Parse("2006-01-02", date)
	if derr != nil {
		return out, errors.New("fecha inválida (YYYY-MM-DD)")
	}
	loc, _ := time.LoadLocation("Europe/Madrid")
	if loc == nil {
		loc = time.UTC
	}
	n := time.Now().In(loc)
	today := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
	if d.Before(today) {
		return out, errors.New("la fecha es pasada: no se crea desde aquí")
	}
	if in.PartySize <= 0 || in.PartySize > 10000 {
		return out, errors.New("número de comensales inválido")
	}
	if strings.TrimSpace(in.CustomerName) == "" {
		return out, errors.New("nombre inválido")
	}
	for _, v := range []*int{in.Children, in.BabyStrollers, in.HighChairs} {
		if v != nil && *v < 0 {
			return out, errors.New("niños, carritos y tronas no pueden ser negativos")
		}
	}
	if in.AllowDuplicateOf < 0 {
		return out, errors.New("allow_duplicate_of inválido")
	}
	var closed int
	if err := q.QueryRowContext(ctx, "SELECT COUNT(*) FROM closed_days WHERE restaurant_id = ? AND date = ?", rid, date).Scan(&closed); err != nil || closed > 0 {
		return out, errors.New("el día " + date + " está cerrado (o no se pudo comprobar): no se crea desde aquí")
	}
	if st, _, err := s.loadSpecialDateSettings(ctx, rid, date); err != nil || st != nil {
		return out, errors.New("la fecha es una fecha especial (o no se pudo comprobar): las reservas de fecha especial se crean desde el panel")
	}
	var email *string
	if v := strings.TrimSpace(in.ContactEmail); v != "" {
		email = &v
	}
	var commentary *string
	if v := strings.TrimSpace(in.Commentary); v != "" {
		commentary = &v
	}
	return s.boNormalizeAndValidateBookingInput(ctx, rid, boNormalizeInput{
		ReservationDate:         date,
		ReservationTime:         strings.TrimSpace(in.Time),
		PartySize:               in.PartySize,
		CustomerName:            in.CustomerName,
		ContactPhone:            strings.TrimSpace(in.ContactPhone),
		ContactPhoneCountryCode: strings.TrimSpace(in.ContactPhoneCC),
		ContactEmail:            email,
		PreferredFloorNumber:    in.PreferredFloor,
		Commentary:              commentary,
		Children:                in.Children,
		BabyStrollers:           in.BabyStrollers,
		HighChairs:              in.HighChairs,
	})
}

// botCreateFindDuplicates returns live bookings with the same natural key
// (date, time, canonical phone, party). With lock=true the equality on
// (restaurant_id, reservation_date, reservation_time) rides
// idx_bookings_restaurant_date_time_id, so the FOR UPDATE read next-key locks
// that exact range and a twin INSERT (bot or panel) waits for this tx.
func botCreateFindDuplicates(ctx context.Context, q interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}, rid int, b boNormalizedBooking, lock bool) ([]int64, error) {
	sfx := ""
	if lock {
		sfx = " FOR UPDATE"
	}
	rows, err := q.QueryContext(ctx, `SELECT id, contact_phone_country_code, contact_phone, party_size FROM bookings WHERE restaurant_id = ? AND reservation_date = ? AND reservation_time = ?`+sfx, rid, b.ReservationDate, b.ReservationTime)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var found []int64
	for rows.Next() {
		var id, party int64
		var cc, phone sql.NullString
		if rows.Scan(&id, &cc, &phone, &party) != nil {
			return nil, errors.New("error interno")
		}
		if cc.String == b.ContactPhoneCountryCode && phone.String == b.ContactPhone && party == int64(b.PartySize) {
			found = append(found, id)
		}
	}
	return found, rows.Err()
}

func (s *Server) botToolCreatePropose(ctx context.Context, rid int, msg botWebhookMessage, input json.RawMessage) (string, error) {
	actor, origin, ok := s.botSpecialAdminAuthorize(ctx, rid, msg)
	if !ok {
		return botJSON(map[string]any{"error": "no autorizado: solo para miembros verificados del grupo de gestión"}), nil
	}
	s.botSpecialAdminFlushNotify(ctx, rid)
	var in botCreateArgs
	// Unknown fields (arroz, group menu, extras, special...) are REJECTED, never
	// silently ignored: v1 creates plain normal bookings only.
	dec := json.NewDecoder(bytes.NewReader(input))
	dec.DisallowUnknownFields()
	if dec.Decode(&in) != nil {
		return botJSON(map[string]any{"error": "argumentos inválidos: esta herramienta solo admite date, time, party_size, customer_name, contact_phone, contact_phone_country_code, contact_email, commentary, children, baby_strollers, high_chairs, preferred_floor_number y allow_duplicate_of (menús de grupo, arroces, extras y fechas especiales van por el panel)"}), nil
	}
	if strings.TrimSpace(in.Date) == "" || strings.TrimSpace(in.Time) == "" || in.PartySize == 0 || strings.TrimSpace(in.CustomerName) == "" || strings.TrimSpace(in.ContactPhone) == "" {
		return botJSON(map[string]any{"error": "faltan datos: fecha, hora, comensales, nombre y teléfono son obligatorios"}), nil
	}
	return s.botCreatePropose(ctx, rid, msg, actor, origin, in)
}

func (s *Server) botCreatePropose(ctx context.Context, rid int, msg botWebhookMessage, actor, origin string, in botCreateArgs) (string, error) {
	b, err := s.botCreateValidate(ctx, s.db, rid, in)
	if err != nil {
		return botJSON(map[string]any{"error": err.Error()}), nil
	}
	dups, derr := botCreateFindDuplicates(ctx, s.db, rid, b, false)
	if derr != nil {
		return botJSON(map[string]any{"error": "no se pudo comprobar si ya existe la reserva"}), nil
	}
	if len(dups) > 1 {
		return botJSON(map[string]any{"error": "ya hay varias reservas con esos mismos datos (fecha, hora, teléfono y comensales): revísalo desde el panel"}), nil
	}
	if len(dups) == 1 && in.AllowDuplicateOf != dups[0] {
		return botJSON(map[string]any{"error": fmt.Sprintf("ya existe la reserva #%d con esos mismos datos (fecha, hora, teléfono y comensales). Si es una reserva distinta legítima, confírmalo expresamente repitiendo la petición con allow_duplicate_of=%d", dups[0], dups[0])}), nil
	}
	in.DedupKey = botCreateDedupKey(rid, b)
	code, cerr := botSpecialNewCode()
	if cerr != nil {
		return botJSON(map[string]any{"error": "no se pudo generar la propuesta"}), nil
	}
	det := fmt.Sprintf("%s a las %s, %d comensales", b.ReservationDate, strings.TrimSuffix(b.ReservationTime, ":00"), b.PartySize)
	if b.Children > 0 {
		det += fmt.Sprintf(" (%d niños)", b.Children)
	}
	det += ", a nombre de " + b.CustomerName + ", teléfono +" + b.ContactPhoneCountryCode + " " + b.ContactPhone + ", email " + b.ContactEmail
	if b.PreferredFloorNumber.Valid {
		det += fmt.Sprintf(", salón %d", b.PreferredFloorNumber.Int64)
	}
	if b.BabyStrollers > 0 {
		det += fmt.Sprintf(", %d carritos", b.BabyStrollers)
	}
	if b.HighChairs > 0 {
		det += fmt.Sprintf(", %d tronas", b.HighChairs)
	}
	if b.Commentary.Valid {
		det += ", comentario: «" + b.Commentary.String + "»"
	}
	extra := ""
	if len(dups) == 1 {
		extra = fmt.Sprintf(" Se crea AUNQUE YA EXISTE la reserva #%d con los mismos datos (duplicado legítimo confirmado expresamente).", dups[0])
	}
	preview := fmt.Sprintf("Propuesta: CREAR una reserva NUEVA (normal, sin menú de grupo, arroces ni fecha especial): %s.%s Se registra con el estado por defecto del sistema, igual que las creadas desde el panel, y ocupa el aforo sin comprobación de capacidad. NO se avisa al cliente (ni WhatsApp ni email): si hay que avisarle, se hace desde el panel. No emite el aviso n8n booking.created.", det, extra)
	after := botCreateAfterKey(rid, b, in.AllowDuplicateOf)
	before := botSha("create|sin-reserva")
	return s.botProposalPersist(ctx, rid, msg, "create", 0, in, in.DedupKey, actor, origin, before, after, preview, code)
}

// botCreateExecute runs INSIDE the claim transaction: claim + dedupe + INSERT +
// occupancy + result_json + event commit or roll back together.
func (s *Server) botCreateExecute(ctx context.Context, tx *sql.Tx, rid int, msg botWebhookMessage, p botSpecialProposalRow) (string, error) {
	stale := func(why string) (string, error) {
		_ = tx.Rollback()
		s.markStale(ctx, rid, p, msg)
		return "", errors.New(why)
	}
	var in botCreateArgs
	if json.Unmarshal([]byte(p.Args), &in) != nil {
		return "", errors.New("propuesta ilegible")
	}
	b, err := s.botCreateValidate(ctx, tx, rid, in)
	if err != nil {
		return stale("la validación ya no pasa: " + err.Error())
	}
	if botCreateDedupKey(rid, b) != in.DedupKey {
		return stale("la propuesta no coincide con los datos validados; pide una propuesta nueva")
	}
	dups, derr := botCreateFindDuplicates(ctx, tx, rid, b, true)
	if derr != nil {
		return "", errors.New("no se pudo comprobar duplicados; no se aplicó nada")
	}
	if len(dups) > 1 {
		return stale("ya hay varias reservas con esos mismos datos; revísalo desde el panel")
	}
	if len(dups) == 1 && in.AllowDuplicateOf != dups[0] {
		return stale(fmt.Sprintf("ya existe la reserva #%d con esos mismos datos; si es otra legítima, pide una propuesta nueva confirmándolo expresamente", dups[0]))
	}
	if after := botCreateAfterKey(rid, b, in.AllowDuplicateOf); after != p.After {
		return stale("los datos resueltos cambiaron desde la vista previa; pide una propuesta nueva")
	}
	// Same column list and values the panel create writes (boInsertBooking),
	// but inside the claim transaction.
	res, err := tx.ExecContext(ctx, `
		INSERT INTO bookings (
			restaurant_id,
			reservation_date,
			party_size,
			children,
			reservation_time,
			customer_name,
			contact_phone,
			contact_phone_country_code,
			commentary,
			arroz_type,
			arroz_servings,
			babyStrollers,
			highChairs,
			contact_email,
			special_menu,
			menu_de_grupo_id,
			menu_de_grupo_assigned,
			principales_json,
			extras_json,
			table_number,
			preferred_floor_number,
			is_special_booking,
			is_prereserva,
			special_json,
			has_mobility_issues,
			mobility_people
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		rid,
		b.ReservationDate,
		b.PartySize,
		b.Children,
		b.ReservationTime,
		b.CustomerName,
		b.ContactPhone,
		b.ContactPhoneCountryCode,
		nullableStringOrNil(b.Commentary),
		b.ArrozTypeJSON,
		b.ArrozServingsJSON,
		b.BabyStrollers,
		b.HighChairs,
		b.ContactEmail,
		boolToTinyint(b.SpecialMenu),
		b.MenuDeGrupoID,
		boolToTinyint(b.MenuDeGrupoAssigned),
		b.PrincipalesJSON,
		b.ExtrasJSON,
		nullableStringOrNil(b.TableNumber),
		nullableInt64OrNil(b.PreferredFloorNumber),
		boolToTinyint(b.IsSpecialBooking),
		boolToTinyint(b.IsPrereserva),
		nullableStringOrNilFromAny(b.SpecialJSON),
		boolToTinyint(b.HasMobilityIssues),
		b.MobilityPeople,
	)
	if err != nil {
		return "", errors.New("no se pudo crear la reserva; no se aplicó nada")
	}
	id64, err := res.LastInsertId()
	if err != nil || id64 <= 0 {
		return "", errors.New("no se pudo crear la reserva; no se aplicó nada")
	}
	if err := s.applyBookingLocationOccupancy(ctx, tx, rid, b.ReservationDate, b.PreferredFloorNumber, nil, b.PartySize, +1); err != nil {
		return "", errors.New("no se pudo registrar la ocupación; no se aplicó nada")
	}
	// Second validation just before commit: reference data read again.
	again, aerr := s.botCreateValidate(ctx, tx, rid, in)
	if aerr != nil || botCreateAfterKey(rid, again, in.AllowDuplicateOf) != p.After {
		return stale("los datos de referencia cambiaron durante la creación; no se aplicó nada")
	}
	notice := fmt.Sprintf("Reserva #%d creada: %s %s, %d comensales, a nombre de %s. No se ha avisado al cliente ni se ha emitido ningún aviso externo.", id64, b.ReservationDate, strings.TrimSuffix(b.ReservationTime, ":00"), b.PartySize, b.CustomerName)
	rj, _ := json.Marshal(map[string]any{"booking_id": id64, "op": p.Op, "notice": notice})
	if _, err := tx.ExecContext(ctx, `UPDATE wa_bot_op_proposals SET result_json=?, notify_state='pending' WHERE id=?`, string(rj), p.ID); err != nil {
		return "", errors.New("error interno")
	}
	s.botSpecialAdminEvent(ctx, tx, rid, p.ID, "executed", p.Actor, strings.TrimSpace(msg.ChatJID), map[string]any{"op": p.Op, "booking_id": id64, "before_hash": p.Before, "after_hash": p.After, "approval_message_id": msg.MessageID})
	if err := tx.Commit(); err != nil {
		// Outcome unknown to us: never re-create by matching name/phone/time;
		// the durable proposal result is the source of truth.
		log.Printf("[bot] wa_bot_special_admin_v2 commit_unknown rid=%d proposal=%d err=%v", rid, p.ID, err)
		return "", errors.New("no pude confirmar si se creó: consulta el resultado de la propuesta o el panel antes de repetir")
	}
	s.broadcastBookingChanged(rid, id64, "booking_created")
	return notice, nil
}

// botEditArgs are the requested field changes of a booking_propose_edit
// proposal. Only non-nil fields are touched; everything else stays untouched.
type botEditArgs struct {
	CustomerName   *string `json:"customer_name,omitempty"`
	ContactPhone   *string `json:"contact_phone,omitempty"`
	ContactPhoneCC *string `json:"contact_phone_country_code,omitempty"`
	ContactEmail   *string `json:"contact_email,omitempty"`
	TableNumber    *string `json:"table_number,omitempty"`
	Commentary     *string `json:"commentary,omitempty"`
	Children       *int    `json:"children,omitempty"`
	BabyStrollers  *int    `json:"baby_strollers,omitempty"`
	HighChairs     *int    `json:"high_chairs,omitempty"`
}

// botEditColumns is the closed set of columns an edit may write; the UPDATE
// clause is built only from these names.
var botEditColumns = map[string]bool{
	"customer_name": true, "contact_phone": true, "contact_phone_country_code": true,
	"contact_email": true, "table_number": true, "commentary": true,
	"children": true, "babyStrollers": true, "highChairs": true,
}

type botEditCurrent struct {
	Name, Phone, CC, Email, Table, Commentary string
	TableSet, CommentarySet, GroupMenu        bool
	Children, Strollers, HighChairs, Party    int
}

// botEditMasked replaces stored PII in previews and notices: the chat sees the
// new actor-provided value, never the old value read from the database.
const botEditMasked = "(actual oculto)"

type botEditChange struct {
	Col, Label, Old, New string
	Val                  any
}

func botEditScanCurrent(row *sql.Row) (*botEditCurrent, error) {
	var c botEditCurrent
	var phone, cc, email, table, comm sql.NullString
	var children, strollers, chairs, gm sql.NullInt64
	if err := row.Scan(&c.Name, &phone, &cc, &email, &table, &comm, &children, &strollers, &chairs, &gm, &c.Party); err != nil {
		return nil, err
	}
	c.Phone, c.CC, c.Email = phone.String, cc.String, email.String
	c.Table, c.TableSet = table.String, table.Valid
	c.Commentary, c.CommentarySet = comm.String, comm.Valid
	c.Children, c.Strollers, c.HighChairs = int(children.Int64), int(strollers.Int64), int(chairs.Int64)
	c.GroupMenu = gm.Int64 != 0
	return &c, nil
}

const botEditCurrentSelect = `SELECT customer_name, contact_phone, contact_phone_country_code, contact_email, table_number, commentary, children, babyStrollers, highChairs, special_menu, party_size FROM bookings WHERE restaurant_id = ? AND id = ?`

// botEditResolve computes the exact resolved writes for the requested fields,
// mirroring the panel validation rules (phone normalization, email fallback,
// children capped at party). Only fields that actually change are returned.
func (s *Server) botEditResolve(ctx context.Context, rid int, cur *botEditCurrent, in botEditArgs) ([]botEditChange, error) {
	var out []botEditChange
	if in.CustomerName != nil {
		v := strings.TrimSpace(*in.CustomerName)
		if v == "" {
			return nil, errors.New("nombre inválido")
		}
		if v != cur.Name {
			out = append(out, botEditChange{Col: "customer_name", Label: "nombre", Old: botEditMasked, New: v, Val: v})
		}
	}
	if in.ContactPhone != nil || in.ContactPhoneCC != nil {
		cc := cur.CC
		if in.ContactPhoneCC != nil {
			cc = strings.TrimSpace(*in.ContactPhoneCC)
		}
		ph := cur.Phone
		if in.ContactPhone != nil {
			ph = strings.TrimSpace(*in.ContactPhone)
		}
		ncc, national, _, ok := normalizePhoneParts(cc, ph)
		if !ok {
			return nil, errors.New("teléfono inválido")
		}
		if ncc != cur.CC || national != cur.Phone {
			// Display only the parts the actor provided; parts read from the
			// database stay masked even on the "new" side.
			ccPart := "+•••"
			if in.ContactPhoneCC != nil {
				ccPart = "+" + ncc
			}
			numPart := "•••"
			if in.ContactPhone != nil {
				numPart = national
			}
			nw := ccPart + " " + numPart
			if national != cur.Phone {
				out = append(out, botEditChange{Col: "contact_phone", Label: "teléfono", Old: botEditMasked, New: nw, Val: national})
			}
			if ncc != cur.CC {
				out = append(out, botEditChange{Col: "contact_phone_country_code", Label: "teléfono", Old: botEditMasked, New: nw, Val: ncc})
			}
		}
	}
	if in.ContactEmail != nil {
		v := strings.TrimSpace(*in.ContactEmail)
		if v == "" {
			// Panel semantics: an empty email stores the restaurant fallback.
			// The fallback is read from the database, not provided by the
			// actor, so the display names it without showing the address.
			v = s.restaurantFallbackEmail(ctx, rid)
			if v != cur.Email {
				out = append(out, botEditChange{Col: "contact_email", Label: "email", Old: botEditMasked, New: "email predeterminado del restaurante", Val: v})
			}
		} else if v != cur.Email {
			out = append(out, botEditChange{Col: "contact_email", Label: "email", Old: botEditMasked, New: v, Val: v})
		}
	}
	if in.TableNumber != nil {
		v := strings.TrimSpace(*in.TableNumber)
		if v != cur.Table || (v != "") != cur.TableSet {
			old := botEditMasked
			if !cur.TableSet {
				old = "(sin mesa)"
			}
			nw := v
			var val any
			if v == "" {
				nw = "(sin mesa)"
				val = nil
			} else {
				val = v
			}
			out = append(out, botEditChange{Col: "table_number", Label: "mesa", Old: old, New: nw, Val: val})
		}
	}
	if in.Commentary != nil {
		if cur.GroupMenu {
			return nil, errors.New("la reserva tiene menú de grupo y su comentario se genera solo: cámbialo desde el panel")
		}
		v := strings.TrimSpace(*in.Commentary)
		if v != cur.Commentary || (v != "") != cur.CommentarySet {
			old := botEditMasked
			if !cur.CommentarySet {
				old = "(sin comentario)"
			}
			nw := v
			var val any
			if v == "" {
				nw = "(sin comentario)"
				val = nil
			} else {
				val = v
			}
			out = append(out, botEditChange{Col: "commentary", Label: "comentario", Old: old, New: nw, Val: val})
		}
	}
	if in.Children != nil {
		if *in.Children < 0 {
			return nil, errors.New("niños no puede ser negativo")
		}
		v := *in.Children
		if v > cur.Party {
			v = cur.Party
		}
		if v != cur.Children {
			out = append(out, botEditChange{Col: "children", Label: "niños", Old: fmt.Sprint(cur.Children), New: fmt.Sprint(v), Val: v})
		}
	}
	if in.BabyStrollers != nil {
		if *in.BabyStrollers < 0 {
			return nil, errors.New("carritos no puede ser negativo")
		}
		if *in.BabyStrollers != cur.Strollers {
			out = append(out, botEditChange{Col: "babyStrollers", Label: "carritos", Old: fmt.Sprint(cur.Strollers), New: fmt.Sprint(*in.BabyStrollers), Val: *in.BabyStrollers})
		}
	}
	if in.HighChairs != nil {
		if *in.HighChairs < 0 {
			return nil, errors.New("tronas no puede ser negativo")
		}
		if *in.HighChairs != cur.HighChairs {
			out = append(out, botEditChange{Col: "highChairs", Label: "tronas", Old: fmt.Sprint(cur.HighChairs), New: fmt.Sprint(*in.HighChairs), Val: *in.HighChairs})
		}
	}
	return out, nil
}

func botEditAfterKey(bookingID int64, changes []botEditChange) string {
	m := map[string]any{"booking_id": bookingID}
	for _, c := range changes {
		m[c.Col] = c.Val
	}
	return botSha("editv1|" + botCanon(m))
}

func (s *Server) botToolEditPropose(ctx context.Context, rid int, msg botWebhookMessage, input json.RawMessage) (string, error) {
	actor, origin, ok := s.botSpecialAdminAuthorize(ctx, rid, msg)
	if !ok {
		return botJSON(map[string]any{"error": "no autorizado: solo para miembros verificados del grupo de gestión"}), nil
	}
	s.botSpecialAdminFlushNotify(ctx, rid)
	var in struct {
		BookingID int64 `json:"booking_id"`
		botEditArgs
	}
	dec := json.NewDecoder(bytes.NewReader(input))
	dec.DisallowUnknownFields()
	if dec.Decode(&in) != nil || in.BookingID <= 0 {
		return botJSON(map[string]any{"error": "argumentos inválidos: booking_id y al menos un campo a editar"}), nil
	}
	a := in.botEditArgs
	if a.CustomerName == nil && a.ContactPhone == nil && a.ContactPhoneCC == nil && a.ContactEmail == nil && a.TableNumber == nil && a.Commentary == nil && a.Children == nil && a.BabyStrollers == nil && a.HighChairs == nil {
		return botJSON(map[string]any{"error": "indica al menos un campo a editar"}), nil
	}
	return s.botEditPropose(ctx, rid, msg, actor, origin, in.BookingID, a)
}

func (s *Server) botEditPropose(ctx context.Context, rid int, msg botWebhookMessage, actor, origin string, bookingID int64, in botEditArgs) (string, error) {
	row, err := s.botLoadSpecialRow(ctx, rid, bookingID)
	if err != nil {
		return botJSON(map[string]any{"error": "no hay una reserva con ese id"}), nil
	}
	if rerr := botSpecialRowAllowedFor(row, "editan", "edita"); rerr != nil {
		return botJSON(map[string]any{"error": rerr.Error()}), nil
	}
	cur, cerr := botEditScanCurrent(s.db.QueryRowContext(ctx, botEditCurrentSelect, rid, bookingID))
	if cerr != nil {
		return botJSON(map[string]any{"error": "no se pudo leer la reserva"}), nil
	}
	changes, rerr := s.botEditResolve(ctx, rid, cur, in)
	if rerr != nil {
		return botJSON(map[string]any{"error": rerr.Error()}), nil
	}
	if len(changes) == 0 {
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
	parts := make([]string, 0, len(changes))
	seen := map[string]bool{}
	for _, c := range changes {
		if seen[c.Label] {
			continue
		}
		seen[c.Label] = true
		parts = append(parts, fmt.Sprintf("%s: «%s» → «%s»", c.Label, c.Old, c.New))
	}
	preview := fmt.Sprintf("Propuesta: EDITAR la reserva #%d (%s %s, %d comensales): %s. No cambian fecha, hora, comensales, salón, menús ni ocupación. No se avisa al cliente.",
		bookingID, row.Date, row.Time, row.Party, strings.Join(parts, "; "))
	after := botEditAfterKey(bookingID, changes)
	return s.botProposalPersist(ctx, rid, msg, "edit", bookingID, in, "", actor, origin, beforeHash, after, preview, code)
}

// botEditExecute runs INSIDE the claim transaction: claim + narrow column write
// + others-hash verification + history + result_json + event commit together.
func (s *Server) botEditExecute(ctx context.Context, tx *sql.Tx, rid int, msg botWebhookMessage, p botSpecialProposalRow) (string, error) {
	stale := func(why string) (string, error) {
		_ = tx.Rollback()
		s.markStale(ctx, rid, p, msg)
		return "", errors.New(why)
	}
	var in botEditArgs
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
	cur, cerr := botEditScanCurrent(tx.QueryRowContext(ctx, botEditCurrentSelect, rid, p.BookingID))
	if cerr != nil {
		return "", errors.New("error interno")
	}
	changes, rerr := s.botEditResolve(ctx, rid, cur, in)
	if rerr != nil {
		return stale("la validación ya no pasa: " + rerr.Error())
	}
	if len(changes) == 0 {
		return stale("los campos ya tienen los valores pedidos; pide una propuesta nueva")
	}
	if botEditAfterKey(p.BookingID, changes) != p.After {
		return stale("el resultado ya no coincide con la vista previa; pide una propuesta nueva")
	}
	cols := make([]string, 0, len(changes))
	for _, c := range changes {
		if !botEditColumns[c.Col] {
			return "", errors.New("columna no permitida; no se aplicó nada")
		}
		cols = append(cols, c.Col)
	}
	preOthers, err := botFullRowHash(ctx, tx, rid, p.BookingID, cols...)
	if err != nil {
		return "", errors.New("error interno")
	}
	sets := make([]string, len(cols))
	vals := make([]any, len(cols))
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
	// History: the panel tracks strollers and high chairs changes; mirror it.
	for _, c := range changes {
		if c.Col != "babyStrollers" && c.Col != "highChairs" {
			continue
		}
		field := "strollers"
		if c.Col == "highChairs" {
			field = "high_chairs"
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO booking_modifications (restaurant_id, booking_id, original_reservation_date, field_modified, old_value, new_value, modified_by, modified_by_name, customer_name, contact_phone, modification_date) VALUES (?, ?, ?, ?, ?, ?, 'whatsapp', 'Bot WhatsApp (dirección)', ?, ?, NOW())`,
			rid, p.BookingID, row.Date, field, c.Old, c.New, cur.Name, cur.Phone); err != nil {
			return "", errors.New("no se pudo registrar el historial; no se aplicó nada")
		}
	}
	parts := make([]string, 0, len(changes))
	seen := map[string]bool{}
	for _, c := range changes {
		if seen[c.Label] {
			continue
		}
		seen[c.Label] = true
		parts = append(parts, fmt.Sprintf("%s: «%s» → «%s»", c.Label, c.Old, c.New))
	}
	notice := fmt.Sprintf("Reserva #%d editada: %s. No se ha avisado al cliente.", p.BookingID, strings.Join(parts, "; "))
	rj, _ := json.Marshal(map[string]any{"booking_id": p.BookingID, "op": p.Op, "notice": notice})
	if _, err := tx.ExecContext(ctx, `UPDATE wa_bot_op_proposals SET result_json=?, notify_state='pending' WHERE id=?`, string(rj), p.ID); err != nil {
		return "", errors.New("error interno")
	}
	s.botSpecialAdminEvent(ctx, tx, rid, p.ID, "executed", p.Actor, strings.TrimSpace(msg.ChatJID), map[string]any{"op": p.Op, "booking_id": p.BookingID, "before_hash": p.Before, "after_hash": p.After, "changed_columns": cols, "approval_message_id": msg.MessageID})
	if err := tx.Commit(); err != nil {
		log.Printf("[bot] wa_bot_special_admin_v2 commit_unknown rid=%d proposal=%d err=%v", rid, p.ID, err)
		return "", errors.New("no pude confirmar si se guardó; consulta la reserva antes de repetir")
	}
	s.broadcastBookingChanged(rid, p.BookingID, "booking_updated")
	return notice, nil
}

// botCancelBlockers refuses a bot cancel when the delete would not be a clean,
// reversible move: live attached documents (the orphan-document sweep deletes
// their stored files for good) or linked invoices (FK, would abort the delete).
func botCancelBlockers(ctx context.Context, q interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}, rid int, bookingID int64, lock bool) error {
	var docs, inv int
	// In the execute transaction the count is a locking read (next-key locks on
	// the booking_documents index range); document inserts also lock the
	// booking row, so a document cannot slip in between this check and the DELETE.
	sfx := ""
	if lock {
		sfx = " FOR UPDATE"
	}
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM booking_documents WHERE restaurant_id = ? AND booking_id = ? AND deleted_at IS NULL`+sfx, rid, bookingID).Scan(&docs); err != nil {
		return errors.New("no se pudo comprobar los documentos de la reserva; no se cancela")
	}
	if docs > 0 {
		return errors.New("la reserva tiene documentos adjuntos: la limpieza automática los borraría para siempre; cancélala desde el panel")
	}
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM invoices WHERE reservation_id = ?`+sfx, bookingID).Scan(&inv); err != nil {
		return errors.New("no se pudo comprobar las facturas de la reserva; no se cancela")
	}
	if inv > 0 {
		return errors.New("la reserva tiene facturas asociadas: no se cancela desde aquí")
	}
	return nil
}

func botScanAllRows(rows *sql.Rows) ([]map[string]any, error) {
	defer rows.Close()
	cols, _ := rows.Columns()
	var out []map[string]any
	for rows.Next() {
		vals := make([]sql.RawBytes, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		m := map[string]any{}
		for i, c := range cols {
			if vals[i] == nil {
				m[c] = nil
			} else {
				m[c] = string(vals[i])
			}
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
