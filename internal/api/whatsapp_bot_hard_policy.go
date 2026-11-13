package api

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Coordination ids: wa_bot_booking_horizon_v1 / wa_bot_event_menu_sticky_v1
//
// Hard, deterministic customer policies enforced in Go (never by prompt or
// retrieved knowledge, so a later GraphRAG layer cannot weaken them):
//   - booking horizon: a date more than botBookingHorizonDays ahead is never
//     bookable nor checked for availability; the customer is referred to the
//     restaurant management (forward + contact card).
//   - special events (communion, wedding, baptism, birthday...) + menu or
//     budget/price: management contact only, sticky for the conversation; no
//     event menu or price is ever sent. Only a genuine shift to an ordinary
//     booking under botEventExitMaxParty people leaves the loop.
// Privileged (staff) chats are exempt, like every other customer guard.

const (
	botBookingHorizonDays = 35
	botEventExitMaxParty  = 10
	botReasonHorizon      = "beyond_horizon"
	botReasonEvent        = "special_event"
	// botReasonEventCleared marks a genuine exit: until the TTL, older event
	// turns in the history no longer re-lock the conversation.
	botReasonEventCleared = "special_event_cleared"
)

// botHorizonDateTools take a date the customer could book or probe.
var botHorizonDateTools = map[string]bool{
	"get_day_schedule": true, "check_day_capacity": true, "get_date_overview": true, "check_availability_for_party": true,
	"create_booking": true, "modify_booking": true, "get_special_date_info": true, "get_special_date_menu": true,
	"create_special_booking": true, "modify_special_booking": true,
}

// botEventMenuTools expose menus/prices; refused while the event loop is open.
var botEventMenuTools = map[string]bool{
	"list_menus": true, "get_menu_details": true, "get_booking_menu": true, "send_menu_buttons": true,
	"get_special_date_menu": true, "get_rice_menu": true, "list_booking_extras": true,
}

// botHorizonLimit is the last bookable day (Madrid time).
func botHorizonLimit(now time.Time) string {
	return now.In(boMadridTZ).AddDate(0, 0, botBookingHorizonDays).Format("2006-01-02")
}

// botBeyondHorizon: dateISO (YYYY-MM-DD) is later than the last bookable day.
func botBeyondHorizon(dateISO string, now time.Time) bool {
	return dateISO != "" && dateISO > botHorizonLimit(now)
}

// botToolInputDates extracts every parsable date argument of a tool call.
func botToolInputDates(input json.RawMessage) []string {
	var in map[string]any
	if json.Unmarshal(input, &in) != nil {
		return nil
	}
	var out []string
	for _, k := range []string{"date", "date_from", "date_to", "from_date", "to_date", "new_date"} {
		if v, ok := in[k].(string); ok {
			if iso, err := parseBotDate(v); err == nil {
				out = append(out, iso)
			}
		}
	}
	return out
}

var botMonthNumbers = map[string]int{"enero": 1, "febrero": 2, "marzo": 3, "abril": 4, "mayo": 5, "junio": 6, "julio": 7, "agosto": 8,
	"septiembre": 9, "setiembre": 9, "octubre": 10, "noviembre": 11, "diciembre": 12,
	"january": 1, "february": 2, "march": 3, "april": 4, "may": 5, "june": 6, "july": 7, "august": 8, "september": 9, "october": 10, "november": 11, "december": 12}

var (
	botTextDayMonthRe = regexp.MustCompile(`\b(\d{1,2})\s+(?:de\s+)?(enero|febrero|marzo|abril|mayo|junio|julio|agosto|septiembre|setiembre|octubre|noviembre|diciembre|january|february|march|april|may|june|july|august|september|october|november|december)(?:\s+(?:de|del)?\s*(\d{4}))?`)
	botTextISORe      = regexp.MustCompile(`\b(\d{4})-(\d{1,2})-(\d{1,2})\b`)
	botTextNumericRe  = regexp.MustCompile(`\b(\d{1,2})/(\d{1,2})(?:/(\d{2,4}))?\b|\b(\d{1,2})-(\d{1,2})-(\d{4})\b`) // "14.10" (time) and "2-3 personas" are not dates
	botTextMonthRe    = regexp.MustCompile(`\b(?:para|en|in|for)\s+(?:el\s+mes\s+de\s+)?(enero|febrero|marzo|abril|mayo|junio|julio|agosto|septiembre|setiembre|octubre|noviembre|diciembre|january|february|march|april|june|july|august|september|october|november|december)\b`)
)

// botResolveDay builds the next occurrence (today or later) of day/month
// when the year is missing. ok=false for impossible dates.
func botResolveDay(day, month, year int, now time.Time) (string, bool) {
	today := now.In(boMadridTZ)
	y := year
	if y == 0 {
		y = today.Year()
	} else if y < 100 {
		y += 2000
	}
	t := time.Date(y, time.Month(month), day, 0, 0, 0, 0, boMadridTZ)
	if t.Day() != day || int(t.Month()) != month {
		return "", false
	}
	if year == 0 && t.Format("2006-01-02") < today.Format("2006-01-02") {
		t = t.AddDate(1, 0, 0)
	}
	return t.Format("2006-01-02"), true
}

// botTextFarDate returns the first date written in the text that is beyond
// the horizon ("" when none). A bare month ("para mayo") counts when even its
// first day is beyond the horizon.
func botTextFarDate(text string, now time.Time) string {
	t := strings.ToLower(text)
	for _, m := range botTextDayMonthRe.FindAllStringSubmatch(t, -1) {
		d, _ := strconv.Atoi(m[1])
		y, _ := strconv.Atoi(m[3])
		if iso, ok := botResolveDay(d, botMonthNumbers[m[2]], y, now); ok && botBeyondHorizon(iso, now) {
			return iso
		}
	}
	for _, m := range botTextISORe.FindAllStringSubmatch(t, -1) {
		y, _ := strconv.Atoi(m[1])
		mo, _ := strconv.Atoi(m[2])
		d, _ := strconv.Atoi(m[3])
		if iso, ok := botResolveDay(d, mo, y, now); ok && botBeyondHorizon(iso, now) {
			return iso
		}
	}
	t = botTextISORe.ReplaceAllString(t, " ") // "2027-05-02" must not also read as d-m
	for _, m := range botTextNumericRe.FindAllStringSubmatch(t, -1) {
		if m[1] == "" { // dashed form d-m-yyyy
			m = []string{m[0], m[4], m[5], m[6]}
		}
		d, _ := strconv.Atoi(m[1])
		mo, _ := strconv.Atoi(m[2])
		y, _ := strconv.Atoi(m[3])
		if mo < 1 || mo > 12 {
			continue
		}
		if iso, ok := botResolveDay(d, mo, y, now); ok && botBeyondHorizon(iso, now) {
			return iso
		}
	}
	for _, m := range botTextMonthRe.FindAllStringSubmatch(t, -1) {
		// The current month is never "far": its next 1st would be next year.
		if botMonthNumbers[m[1]] == int(now.In(boMadridTZ).Month()) {
			continue
		}
		if iso, ok := botResolveDay(1, botMonthNumbers[m[1]], 0, now); ok && botBeyondHorizon(iso, now) {
			return iso
		}
	}
	return ""
}

// botWordsRe matches whole words; Go's \b is ASCII-only and would miss
// "comunión" or "menú", so the boundaries are explicit letter/digit classes.
func botWordsRe(alts string) *regexp.Regexp {
	return regexp.MustCompile(`(?:^|[^\p{L}\p{N}])(?:` + alts + `)(?:$|[^\p{L}\p{N}])`)
}

var (
	botEventWordRe  = botWordsRe(`comuni[oó]n(?:es)?|primera comuni[oó]n|bodas?|bautiz[oa]s?|bautismo|cumplea[ñn]os|cumple|aniversario|despedidas?|celebraci[oó]n(?:es)?|eventos?|graduaci[oó]n|jubilaci[oó]n|comida de empresa|cena de empresa|wedding|baptism|christening|birthday|communion|anniversary|party`)
	botMenuBudgetRe = botWordsRe(`men[uú]s?|carta|precios?|presupuestos?|cu[aá]nto (?:cuesta|vale|sale|ser[ií]a)|coste|costo|tarifas?|por persona|budget|prices?|quote|cost|menu`)
	botPartyUnitRe  = regexp.MustCompile(`(?:^|[^\p{L}\p{N}])(\d{1,3})\s*(?:personas|pax|comensales|adultos|adultas|ni[ñn]os|ni[ñn]as|peques|beb[eé]s|menores|people|persons|guests|adults|kids|children)(?:$|[^\p{L}\p{N}])`)
	botPartySomosRe = regexp.MustCompile(`(?:^|[^\p{L}\p{N}])(?:somos|seremos|seriamos|seríamos)\s+(\d{1,3})(?:$|[^\p{L}\p{N}])`)
	// botEventSameTopicRe: wording that keeps talking about the event/booking
	// already discussed ("ahora somos 8 para el evento", "para ese día").
	botEventSameTopicRe = botWordsRe(`ahora somos|al final somos|al final seremos|para eso|para ello|para ese d[ií]a|esa fecha|misma fecha|el mismo d[ií]a|lo mismo|lo de antes|lo que te dije`)
)

func botTextHasEvent(t string) bool      { return botEventWordRe.MatchString(strings.ToLower(t)) }
func botTextHasMenuBudget(t string) bool { return botMenuBudgetRe.MatchString(strings.ToLower(t)) }

// botTextParty returns the party size written in the text (0 = none): the
// SUM of every "N <unit>" (8 adultos y 4 niños = 12); "somos N" only counts
// when no unit was written.
func botTextParty(t string) int {
	t = strings.ToLower(t)
	total := 0
	for _, m := range botPartyUnitRe.FindAllStringSubmatch(t, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil {
			total += n
		}
	}
	if total > 0 {
		return total
	}
	if m := botPartySomosRe.FindStringSubmatch(t); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}

// botEventExit is the only way out of the event loop: an ordinary booking
// request, no event word, and an explicit party below the limit.
func botEventExit(text string) bool {
	party := botTextParty(text)
	return !botTextHasEvent(text) && !botEventSameTopicRe.MatchString(strings.ToLower(text)) && !botTextHasMenuBudget(text) &&
		botBookingIntentFromText(text) == "create_booking" && party > 0 && party < botEventExitMaxParty
}

// botRecentUserTexts returns the customer's last turns, current one included:
// consecutive customer messages arrive merged into one block, so the block
// holding the current text may also hold the earlier "comunión" message.
func botRecentUserTexts(history []botMessage, max int) []string {
	var out []string
	for i := len(history) - 1; i >= 0 && len(out) < max; i-- {
		if history[i].Role == "user" && len(history[i].Content) > 0 {
			out = append(out, history[i].Content[0].Text)
		}
	}
	return out
}

// botEventMenuTriggered: event + menu/budget across the current message and
// the recent customer turns (the prod case: "comunión" first, "menú y
// precios" next).
func botEventMenuTriggered(text string, history []botMessage) bool {
	recent := strings.Join(botRecentUserTexts(history, 6), "\n")
	curEvent, curMenu := botTextHasEvent(text), botTextHasMenuBudget(text)
	return (curEvent && curMenu) || (curMenu && botTextHasEvent(recent)) || (curEvent && botTextHasMenuBudget(recent))
}

// botActiveHandoffReason is the open sticky reason ("" when none/expired).
func (s *Server) botActiveHandoffReason(ctx context.Context, restaurantID int, sender string) string {
	h, err := s.botConversation.GetHandoff(ctx, restaurantID, sender)
	if err != nil || h == nil || time.Since(time.UnixMilli(h.UpdatedAt)) > botHandoffTTL {
		return ""
	}
	return h.Reason
}

// botHardPolicyGuard runs before the pipeline and the agent. It returns true
// when the turn was fully answered by a policy referral.
func (s *Server) botHardPolicyGuard(ctx context.Context, restaurantID int, msg botWebhookMessage, tenant botTenantConfig, history []botMessage) bool {
	if msg.PrivilegedGroup || s.botConversation == nil {
		return false
	}
	now := time.Now()
	active := s.botActiveHandoffReason(ctx, restaurantID, msg.Sender)
	passthrough := false
	if active == botReasonEvent {
		if botEventUnrelatedInfo(msg.Text) {
			// Practical question with no event/menu/booking content: the agent
			// answers it, the event state stays (menu tools stay blocked).
			passthrough = true
			botCheckpoint("wa_bot_event_menu_sticky_v1", "restaurant_id=%d sender=%s passthrough=unrelated_info", restaurantID, msg.Sender)
		} else if botEventExit(msg.Text) {
			_ = s.botConversation.SetHandoff(ctx, restaurantID, msg.Sender, "", botReasonEventCleared, false)
			active = botReasonEventCleared
			botCheckpoint("wa_bot_event_menu_sticky_v1", "restaurant_id=%d sender=%s exit=ordinary_booking party=%d", restaurantID, msg.Sender, botTextParty(msg.Text))
		} else {
			s.botPolicyReferral(ctx, restaurantID, msg, tenant, botReasonEvent, "", true)
			return true
		}
	}
	if active == botReasonEventCleared {
		history = nil // only the current message can re-open the loop
	}
	if !passthrough && botEventMenuTriggered(msg.Text, history) {
		s.botPolicyReferral(ctx, restaurantID, msg, tenant, botReasonEvent, "", false)
		return true
	}
	if far := botTextFarDate(msg.Text, now); far != "" {
		s.botPolicyReferral(ctx, restaurantID, msg, tenant, botReasonHorizon, far, false)
		return true
	}
	return false
}

// botPolicyToolBoundary is the tool-level twin of botHardPolicyGuard: even
// if the text guard missed it, no date beyond the horizon is ever probed or
// booked and no menu/price leaves while the event loop is open.
func (s *Server) botPolicyToolBoundary(ctx context.Context, restaurantID int, msg botWebhookMessage, tenant botTenantConfig, state *botTurnState, name string, input json.RawMessage) (bool, string) {
	if msg.PrivilegedGroup {
		return false, ""
	}
	reason, detail := "", ""
	if botHorizonDateTools[name] {
		for _, d := range botToolInputDates(input) {
			if botBeyondHorizon(d, time.Now()) {
				reason, detail = botReasonHorizon, d
				break
			}
		}
	}
	if reason == "" && botEventMenuTools[name] && (botTextHasEvent(msg.Text) || s.botActiveHandoffReason(ctx, restaurantID, msg.Sender) == botReasonEvent) {
		reason = botReasonEvent
	}
	if reason == "" {
		return false, ""
	}
	botCheckpoint("wa_bot_policy_tool_boundary_v1", "restaurant_id=%d sender=%s tool=%s reason=%s date=%s", restaurantID, msg.Sender, name, reason, detail)
	if state == nil || !state.policyReferred {
		s.botPolicyReferral(ctx, restaurantID, msg, tenant, reason, detail, false)
		if state != nil {
			state.policyReferred, state.noticeDelivered = true, true
		}
	}
	return true, botJSON(map[string]any{
		"blocked": true, "reason": reason, "operation": name, "terminal": true,
		"instruction": "La operación NO se ha realizado y NO debes dar fechas, disponibilidad, menús ni precios sobre esto. El cliente ya ha recibido el contacto de la gestión del restaurante. No envíes ningún mensaje adicional.",
	})
}

// botPolicyReferral forwards to management and sends the management contact
// card. Event referrals open/refresh the sticky loop.
func (s *Server) botPolicyReferral(ctx context.Context, restaurantID int, msg botWebhookMessage, tenant botTenantConfig, reason, date string, repeat bool) {
	detail := ""
	if date != "" {
		detail = "fecha " + date
	}
	sent := s.botForwardToManagement(ctx, restaurantID, msg, tenant, reason, detail, botManagementPrefixFor(reason, msg.Language), msg.Language, repeat)
	if reason == botReasonEvent {
		_ = s.botConversation.SetHandoff(ctx, restaurantID, msg.Sender, truncate(msg.Text, 160), botReasonEvent, repeat)
	}
	botCheckpoint("wa_bot_hard_policy_v1", "restaurant_id=%d sender=%s message_id=%s reason=%s date=%s repeat=%t forwarded=%t horizon_limit=%s",
		restaurantID, msg.Sender, msg.MessageID, reason, date, repeat, sent, botHorizonLimit(time.Now()))
}

// botEventInfoRe: practical questions that do not negotiate the event.
var botEventInfoRe = botWordsRe(`horarios?|a qu[eé] hora|abr[ií]s|abren|abierto|cerr[aá]is|cierran|cerrado|d[oó]nde est[aá]is|direcci[oó]n|ubicaci[oó]n|c[oó]mo (?:se )?llega|parking|aparcar|aparcamiento|wifi|tel[eé]fono|opening hours|address|where are you`)

// botEventUnrelatedInfo: an info question with nothing of the event topic
// (no event, menu/budget, same-topic wording, party size or booking intent).
func botEventUnrelatedInfo(text string) bool {
	t := strings.ToLower(text)
	return botEventInfoRe.MatchString(t) && !botTextHasEvent(t) && !botTextHasMenuBudget(t) && !botEventSameTopicRe.MatchString(t) &&
		botTextParty(t) == 0 && botBookingIntentFromText(t) == ""
}

// botOpenHandoff opens/refreshes a sticky handoff without ever replacing an
// open event loop with another reason (wa_bot_event_menu_sticky_v1).
func (s *Server) botOpenHandoff(ctx context.Context, restaurantID int, sender, topic, reason string, repeat bool) {
	if reason != botReasonEvent && s.botActiveHandoffReason(ctx, restaurantID, sender) == botReasonEvent {
		botCheckpoint("wa_bot_event_menu_sticky_v1", "restaurant_id=%d sender=%s keep=special_event over=%s", restaurantID, sender, reason)
		return
	}
	_ = s.botConversation.SetHandoff(ctx, restaurantID, sender, topic, reason, repeat)
}
