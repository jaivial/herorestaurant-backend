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

// botGroupFetchOwnJIDs asks the provider which JIDs belong to the bot so a
// mention of the linked number or LID is recognised. Failures are not fatal:
// the raw "@<digits>" body fallback still works.
func (s *Server) botGroupFetchOwnJIDs(ctx context.Context, restaurantID int) []string {
	own := s.botGroupOwnJIDs(ctx, restaurantID)
	if len(own) > 0 {
		return own
	}
	gw, ok := s.botGatewayFor(ctx, restaurantID)
	if !ok {
		return nil
	}
	evo, ok := gw.(*evolutionGateway)
	if !ok {
		return nil
	}
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, code, raw, err := s.uazapiJSONRequest(reqCtx, strings.TrimRight(evo.baseURL, "/")+"/group/fetchAllGroups/"+evo.instanceName+"?getParticipants=true", http.MethodGet, map[string]string{"apikey": evo.apiKey}, nil)
	if err != nil || code < 200 || code >= 300 {
		return nil
	}
	return botGroupOwnerJIDsFromGroups(raw)
}

// botGroupOwnerJIDsFromGroups extracts the "owner"/"ownerJid" of every group,
// which is the bot's own number on its own instances.
func botGroupOwnerJIDsFromGroups(raw string) []string {
	var groups []struct {
		Owner    string `json:"owner"`
		OwnerJID string `json:"ownerJid"`
	}
	if err := json.Unmarshal([]byte(raw), &groups); err != nil {
		return nil
	}
	var out []string
	for _, g := range groups {
		for _, v := range []string{g.Owner, g.OwnerJID} {
			if d := digitsOnly(v); d != "" {
				out = append(out, d)
			}
		}
	}
	return out
}

// evoGroupInput is the flattened group message the parser works with. It is
// built by evolutionGateway.ParseInboundMessage so the mention rules below
// stay free of provider JSON details (SOLID: the provider adapter only
// translates, this file decides).
type evoGroupInput struct {
	Instance     string
	GroupJID     string
	MessageID    string
	FromMe       bool
	Participant  string
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
	participant := botGroupParticipantPhone(in.Participant)
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
		ParticipantJID: botGroupParticipantJID(in.Participant),
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

// botGroupParticipantJID normalizes the sender JID inside a group. Recent
// Baileys versions address members by opaque LID and keep the phone JID in
// participantAlt; the phone is what the booking tools key on.
func botGroupParticipantJID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.HasSuffix(raw, "@s.whatsapp.net") {
		return raw
	}
	return raw
}

// botGroupParticipantPhone returns the member's national/international phone
// used as the customer identity for bookings, or "" when the JID is an opaque
// LID we cannot resolve.
func botGroupParticipantPhone(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if d := digitsOnly(strings.TrimSuffix(raw, "@s.whatsapp.net")); d != "" {
		return d
	}
	return ""
}
