package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Coordination id: wa_bot_group_mention_v1
//
// The bot answers inside the restaurant's management WhatsApp group ("Bot
// Alqueria") when a member mentions it. WhatsApp groups are noisy: every
// message from every member arrives on the same webhook, so the bot MUST stay
// silent unless it is explicitly addressed.
//
// Three rules make that safe and deterministic:
//
//  1. Only the configured management group is served. The JID comes from the
//     tenant config (management_group_jid) or from a name lookup against the
//     instance (botManagementGroupJID, which defaults to "Bot Alqueria").
//     Any other group is ignored, so a restaurant can never have its customer
//     data answered in an unrelated group.
//  2. The bot only answers messages that mention it. A group message carries
//     mentionText[] and the sender's participant JID; the bot's own JIDs are
//     resolved once per restaurant from the instance owner/participants. The
//     own phone number is also accepted (staff often type "@34600111222" or
//     "bot" instead of tapping the mention), and a quote of a previous bot
//     message counts as addressed.
//  3. Replies are addressed to the GROUP JID (@g.us), never to the member's
//     phone, and the transcript is keyed by the group so several members share
//     one coherent thread instead of polluting each other's 1:1 history.
//
// Group conversations are a management surface: the same tools are available,
// but the special-date policy still applies (wa_bot_special_date_policy_v1) and
// CRUD is restricted to the special menus offered for the date.

// botGroupMentionPrefix marks a synthetic conversation key for group threads.
// The SQLite transcript store keys rows by digitsOnly(userPhone), which would
// collapse a group JID into its digits and mix it with a phone number, so
// group threads get a namespaced key instead.
const botGroupMentionPrefix = "group:"

// botGroupIsGroupJID reports whether a WhatsApp id addresses a group chat.
func botGroupIsGroupJID(jid string) bool {
	return strings.HasSuffix(strings.TrimSpace(jid), "@g.us")
}

// botGroupConversationKey returns the transcript/session key for a group
// thread. Group ids are stable, so the same key survives across turns.
func botGroupConversationKey(groupJID string) string {
	jid := strings.TrimSpace(groupJID)
	if jid == "" {
		return ""
	}
	return botGroupMentionPrefix + jid
}

// botGroupOwnJIDs returns the phone digits that address the bot, taken from
// the linked number of the restaurant's instance. It is the cheap, always
// available source; botGroupFetchOwnJIDs falls back to the provider's group
// owners when the instance has not reported a number yet.
func (s *Server) botGroupOwnJIDs(ctx context.Context, restaurantID int) []string {
	rec, found, err := s.loadRestaurantUAZAPIInstance(ctx, restaurantID)
	if err != nil || !found {
		return nil
	}
	if v := digitsOnly(rec.ConnectedPhone); v != "" {
		return []string{v}
	}
	return nil
}

// botGroupMentionsBot reports whether the bot is addressed in this group
// message. ownedJIDs are the bot's own phone digits; participant is the
// member who wrote (empty for the bot's own messages).
func botGroupMentionsBot(mentions []string, quotedFrom []string, text string, ownedJIDs []string) bool {
	owned := make(map[string]struct{}, len(ownedJIDs))
	for _, j := range ownedJIDs {
		if d := digitsOnly(j); d != "" {
			owned[d] = struct{}{}
		}
	}
	match := func(candidate string) bool {
		d := digitsOnly(candidate)
		if d == "" {
			return false
		}
		_, ok := owned[d]
		return ok
	}
	for _, m := range mentions {
		if match(m) {
			return true
		}
	}
	// Quoting a previous bot message is the desktop/web way of addressing it
	// when the mention chip is not rendered.
	for _, q := range quotedFrom {
		if match(q) {
			return true
		}
	}
	// A raw "@<botdigits>" typed in the body (staff habit).
	return botGroupTextMentionsOwnJID(text, owned)
}

// botGroupMentionRe matches an explicit "@<digits>" inside a message body.
var botGroupMentionRe = regexp.MustCompile(`@(\d{6,20})`)

func botGroupTextMentionsOwnJID(text string, owned map[string]struct{}) bool {
	if len(owned) == 0 {
		return false
	}
	for _, m := range botGroupMentionRe.FindAllStringSubmatch(text, -1) {
		if len(m) > 1 {
			if _, ok := owned[m[1]]; ok {
				return true
			}
		}
	}
	return false
}

// botGroupStripMentions removes the mention tokens from the body so the LLM
// never sees "@34600111222 hola" as the customer's own words, and drops the
// leading separators left behind.
func botGroupStripMentions(text string, mentions []string) string {
	out := text
	if len(mentions) > 0 {
		for _, m := range mentions {
			if strings.TrimSpace(m) == "" {
				continue
			}
			out = strings.ReplaceAll(out, m, " ")
		}
	}
	// Any remaining raw mention of the bot's own number is noise too.
	out = botGroupMentionRe.ReplaceAllString(out, " ")
	out = strings.TrimSpace(out)
	// Tidy the separators a mention leaves at the start of the text.
	out = strings.TrimLeft(out, " \t:,-–—")
	return strings.TrimSpace(out)
}

// botGroupDecisionTTL bounds how long a group verdict is reused. A group
// message that is not ours must not trigger a fresh Evolution round trip on
// every single message, so both outcomes are cached briefly (the positive case
// is already cached for 30 min by botManagementGroupJID).
const botGroupDecisionTTL = 5 * time.Minute

var botGroupDecisionCache = struct {
	sync.Mutex
	byKey map[string]botGroupDecisionEntry
}{byKey: map[string]botGroupDecisionEntry{}}

type botGroupDecisionEntry struct {
	allowed bool
	at      time.Time
}

// botGroupIsManagementGroup reports whether jid is the group the bot answers
// in. Used by the webhook to gate group turns.
//
// The verdict (yes and no) is cached for a few minutes: a busy unrelated group
// must not make every message perform a provider round trip, and a restaurant
// must not pay that cost to be ignored.
func (s *Server) botGroupIsManagementGroup(ctx context.Context, restaurantID int, jid string, tenant botTenantConfig) bool {
	jid = strings.TrimSpace(jid)
	if !botGroupIsGroupJID(jid) {
		return false
	}
	// A pinned JID in the tenant config is authoritative and needs no lookup.
	if pinned := strings.TrimSpace(tenant.ManagementGroupJID); pinned != "" && pinned == jid {
		return true
	}
	key := fmt.Sprintf("%d:%s", restaurantID, jid)
	botGroupDecisionCache.Lock()
	if e, ok := botGroupDecisionCache.byKey[key]; ok && time.Since(e.at) < botGroupDecisionTTL {
		botGroupDecisionCache.Unlock()
		if !e.allowed {
			log.Printf("[bot] checkpoint wa_bot_group_mention_v1 restaurant_id=%d group_not_ours jid=%s", restaurantID, jid)
		}
		return e.allowed
	}
	botGroupDecisionCache.Unlock()

	want := s.botManagementGroupJID(ctx, restaurantID, tenant)
	allowed := want != "" && want == jid
	botGroupDecisionCache.Lock()
	botGroupDecisionCache.byKey[key] = botGroupDecisionEntry{allowed: allowed, at: time.Now()}
	botGroupDecisionCache.Unlock()
	if !allowed {
		log.Printf("[bot] checkpoint wa_bot_group_mention_v1 restaurant_id=%d group_not_configured jid=%s", restaurantID, jid)
	}
	return allowed
}

// botGroupMessageAudit is the single observability record for every group
// event, so a silent bot in a group is diagnosable from the logs alone.
func botGroupMessageAudit(restaurantID int, groupJID, participant, reason string) {
	log.Printf("[bot] checkpoint wa_bot_group_mention_v1 restaurant_id=%d group=%s participant=%s decision=%s",
		restaurantID, groupJID, participant, reason)
}

// botOwnJIDsTTL bounds how long the bot's own identifiers are reused. The
// mapping phone <-> LID is stable for the life of a linked instance, so it does
// not need refreshing per message; without a cache this would add a provider
// round trip (~0.5-0.7s, 10s timeout) to EVERY inbound webhook, 1:1 included,
// just to answer a group mention.
const botOwnJIDsTTL = 30 * time.Minute

var botOwnJIDsCache = struct {
	sync.Mutex
	byRID map[int]botOwnJIDsEntry
}{byRID: map[int]botOwnJIDsEntry{}}

type botOwnJIDsEntry struct {
	jids []string
	at   time.Time
}

// botOwnJIDsCached returns the previously learned identities for the restaurant
// when they are still fresh.
func botOwnJIDsCached(restaurantID int) []string {
	botOwnJIDsCache.Lock()
	defer botOwnJIDsCache.Unlock()
	e, ok := botOwnJIDsCache.byRID[restaurantID]
	if !ok || time.Since(e.at) >= botOwnJIDsTTL {
		return nil
	}
	return e.jids
}

// botOwnJIDsRemember stores the learned identities for the restaurant.
func botOwnJIDsRemember(restaurantID int, jids []string) {
	if len(jids) == 0 {
		return
	}
	botOwnJIDsCache.Lock()
	botOwnJIDsCache.byRID[restaurantID] = botOwnJIDsEntry{jids: jids, at: time.Now()}
	botOwnJIDsCache.Unlock()
}

// botGroupFetchOwnJIDs returns every identifier the bot answers to, so a
// mention of the linked number OR of the bot's Baileys LID is recognised.
//
// Both sources are needed and neither alone is sufficient:
//   - the database holds the phone number (restaurant_uazapi_instances
//     .connected_phone), but not the LID;
//   - the provider holds the LID (the group owner / participants[].id), and the
//     phone form of the same member (participants[].phoneNumber).
//
// An earlier version returned the database value and stopped, so on a
// LID-addressed instance the mention (a LID) never matched the stored phone and
// the bot stayed silent in its own group. Failures are not fatal: the raw
// "@<digits>" body fallback still works.
func (s *Server) botGroupFetchOwnJIDs(ctx context.Context, restaurantID int) []string {
	if cached := botOwnJIDsCached(restaurantID); len(cached) > 0 {
		return cached
	}
	own := s.botGroupOwnJIDs(ctx, restaurantID)
	gw, ok := s.botGatewayFor(ctx, restaurantID)
	if !ok {
		// Cache the database-only answer too, so a missing gateway does not
		// re-attempt the lookup on every inbound message.
		botOwnJIDsRemember(restaurantID, own)
		return own
	}
	evo, ok := gw.(*evolutionGateway)
	if !ok {
		botOwnJIDsRemember(restaurantID, own)
		return own
	}
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, code, raw, err := s.uazapiJSONRequest(reqCtx, strings.TrimRight(evo.baseURL, "/")+"/group/fetchAllGroups/"+evo.instanceName+"?getParticipants=true", http.MethodGet, map[string]string{"apikey": evo.apiKey}, nil)
	if err != nil || code < 200 || code >= 300 {
		// Provider down: fall back to the phone and cache it, so a transient
		// outage costs one attempt per TTL instead of one per message.
		if len(own) > 0 {
			botOwnJIDsRemember(restaurantID, own)
		}
		return own
	}
	// The bot's phone is the key used to find its LID among the participants;
	// the database value is authoritative, so prefer it and fall back to the
	// first stored number when the row has none.
	phone := ""
	for _, v := range own {
		if d := digitsOnly(v); d != "" {
			phone = v
			break
		}
	}
	// The LID is the form a mention actually uses, so it goes first, then the
	// phone forms as a fallback.
	merged := append(botGroupOwnJIDsFromGroups(raw, phone), own...)
	botOwnJIDsRemember(restaurantID, merged)
	return merged
}

// botGroupOwnJIDsFromGroups maps the bot's phone number to the LID the group
// uses for it, so a mention is recognised in either form.
//
// Baileys addresses members by opaque LID (262096671481918@lid) and keeps the
// real phone in participants[].phoneNumber (34960255536@s.whatsapp.net). The
// digits of a LID are NOT the phone number, so matching a mention only against
// the stored phone fails on every LID-addressed instance: the mention arrives as
// the LID and never equals the phone.
//
// phone is the authoritative bot number (the instance ownerJid, which equals
// restaurant_uazapi_instances.connected_phone). Only that participant is
// expanded, so a mention of any other member cannot trigger the bot. The
// group's owner/subjectOwner are deliberately NOT used: they identify the human
// who created the group, not the bot.
func botGroupOwnJIDsFromGroups(raw, phone string) []string {
	var groups []struct {
		Participants []struct {
			ID          string `json:"id"`
			PhoneNumber string `json:"phoneNumber"`
		} `json:"participants"`
	}
	if err := json.Unmarshal([]byte(raw), &groups); err != nil {
		return nil
	}
	var out []string
	seen := map[string]struct{}{}
	add := func(v string) {
		if d := digitsOnly(v); d != "" {
			if _, dup := seen[d]; dup {
				return
			}
			seen[d] = struct{}{}
			out = append(out, d)
		}
	}
	botPhone := digitsOnly(phone)
	for _, g := range groups {
		if botPhone == "" {
			continue
		}
		for _, p := range g.Participants {
			if digitsOnly(p.PhoneNumber) == botPhone {
				// Same member: register both identifiers so a mention in
				// either form matches.
				add(p.PhoneNumber)
				add(p.ID)
			}
		}
		// No other participant is registered: the group owner is the human
		// who CREATED the group, not the bot, and registering every member
		// would let a mention of any member trigger the bot.
	}
	return out
}

// evoGroupInput is the flattened group message the parser works with. It is
// built by evolutionGateway.ParseInboundMessage so the mention rules below
// stay free of provider JSON details (SOLID: the provider adapter only
// translates, this file decides).
type evoGroupInput struct {
	Instance  string
	GroupJID  string
	MessageID string
	FromMe    bool
	// Participant holds every field the provider gave us for the sender. The
	// phone JID may be in any of them (see botGroupParticipantJID), so they are
	// kept as a list rather than collapsed to the first non-empty value.
	Participants []string
	PushName     string
	MessageType  string
	Text         string
	MentionText  []string
	MentionedJID string
	AudioB64     string
	IsAudio      bool
	Ignored      bool
	OwnJIDs      []string
}

// parseEvolutionGroupMessage applies the group mention gate and returns the
// normalized inbound message. ok=false means "not a turn for the bot".
//
// The gate, in order:
//  1. non-conversational payloads (reactions, edits, deletes) never qualify;
//  2. the bot's own messages are echoes and are dropped (its replies are
//     recorded at send time, not replayed as a new turn);
//  3. the sender must be a real member (a participant JID is present and is
//     not the bot itself);
//  4. the bot must be explicitly mentioned.
//
// Every rejection is logged under the wa_bot_group_mention_v1 coordination id
// so a group that stays silent is diagnosable from the backend logs.
func parseEvolutionGroupMessage(in evoGroupInput) (waInbound, bool) {
	groupJID := strings.TrimSpace(in.GroupJID)
	if groupJID == "" {
		return waInbound{}, false
	}
	participantJID := botGroupParticipantJID(in.Participants...)
	participant := botGroupParticipantPhone(participantJID)
	if in.Ignored {
		botGroupMessageAudit(0, groupJID, participant, "ignored_non_conversational")
		return waInbound{}, false
	}
	if in.FromMe {
		// The bot's own message echoed back by the provider.
		botGroupMessageAudit(0, groupJID, participant, "dropped_from_me")
		return waInbound{}, false
	}
	if participant == "" {
		botGroupMessageAudit(0, groupJID, "", "dropped_without_participant")
		return waInbound{}, false
	}

	mentions := make([]string, 0, 2)
	for _, m := range append(append([]string{}, in.MentionText...), in.MentionedJID) {
		if v := strings.TrimSpace(m); v != "" {
			mentions = append(mentions, v)
		}
	}
	if !botGroupMentionsBot(mentions, nil, in.Text, in.OwnJIDs) {
		botGroupMessageAudit(0, groupJID, participant, "skipped_not_mentioned")
		return waInbound{}, false
	}

	// The LLM must read the request, not the mention chip: strip it.
	text := botGroupStripMentions(in.Text, mentions)

	pushName := strings.TrimSpace(in.PushName)
	if pushName == "" {
		pushName = "Cliente"
	}
	return waInbound{
		// Replies and the transcript both address the GROUP, never the member.
		Sender:         groupJID,
		ChatJID:        groupJID,
		ParticipantJID: participantJID,
		Text:           text,
		PushName:       sanitizeBotPushName(pushName),
		MessageID:      in.MessageID,
		FromMe:         in.FromMe,
		SessionRef:     in.Instance,
		IsAudio:        in.IsAudio,
		Ignored:        in.Ignored,
		MediaKind:      botMediaKindLabel(in.MessageType),
		AudioB64:       in.AudioB64,
		Mentioned:      true,
		MentionJIDs:    mentions,
		OwnJIDs:        in.OwnJIDs,
	}, true
}

// botGroupParticipantJID returns the phone-number JID of the member who wrote
// in the group, or "" when only an opaque LID is known.
//
// Recent Baileys versions address group members by WhatsApp's opaque LID
// (2276688822233@lid) and keep the phone JID in a sibling field. The booking
// tools key on the phone, so the caller must try every participant field and
// keep the first one that is a real @s.whatsapp.net id — the same rule the 1:1
// path applies to remoteJid/remoteJidAlt.
func botGroupParticipantJID(participants ...string) string {
	for _, raw := range participants {
		raw = strings.TrimSpace(raw)
		if strings.HasSuffix(raw, "@s.whatsapp.net") {
			return raw
		}
	}
	return ""
}

// botGroupParticipantPhone returns the member's national/international phone
// used as the customer identity for bookings, or "" when the value is not a
// resolvable phone JID.
//
// It deliberately refuses anything that is not a @s.whatsapp.net id. digitsOnly
// on an opaque LID ("2276688822233@lid") yields digits that are NOT a phone
// number, and that value is only locally unique, so feeding it to an ownership
// check against contact_phone would either never match or collide with a real
// customer number. "" makes the caller refuse the turn instead.
func botGroupParticipantPhone(raw string) string {
	raw = strings.TrimSpace(raw)
	if !strings.HasSuffix(raw, "@s.whatsapp.net") {
		return ""
	}
	return digitsOnly(strings.TrimSuffix(raw, "@s.whatsapp.net"))
}
