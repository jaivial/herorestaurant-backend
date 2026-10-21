package api

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Coordination id: wa_bot_group_membership_v1
//
// The privileged group hands the bot the full forky catalog (every backoffice
// operation, with a synthetic root auth), so "who may ask" is a security
// boundary, not a UX detail. Two conditions must hold before a turn is
// privileged:
//
//  1. the chat is a configured privileged group (botGroupIsPrivileged), and
//  2. the sender is a MEMBER of that group.
//
// Condition 2 was missing. WhatsApp only delivers a group message to a member,
// so in practice a non-member cannot write there; but the webhook is a public
// HTTP endpoint and the group id is a static string in the environment. A
// forged payload carrying the privileged group id and an arbitrary
// participant would otherwise be served the root catalog. Membership is
// therefore verified against the provider's own participant list for that
// group before the turn is treated as privileged.
//
// The check is fail-closed: if the provider cannot be reached, the turn is
// served as an ordinary group turn (no forky catalog), never as a privileged
// one. A provider outage must not widen the blast radius.

// botGroupMembersCacheTTL bounds how long a group's participant list is reused.
// The list changes when someone joins or leaves, and the window is short
// enough that a removed member loses the catalog quickly.
const botGroupMembersCacheTTL = 60 * time.Second

// botGroupMembersCacheFailTTL bounds how long a FAILED lookup is reused.
// A group that was renamed or deleted makes every message wait for the
// provider timeout before being refused, so the failure is remembered
// for a short while. The refusal itself is unchanged: fail closed.
const botGroupMembersCacheFailTTL = 15 * time.Second

// botGroupMembers is the participant list of a group, split by the kind of
// identity each member is addressed with.
//
// The split matters: Baileys addresses a member by an opaque LID
// ("262096671481918:0@lid") and keeps the real phone in
// participants[].phoneNumber. The digits of a LID are NOT a phone number, so
// comparing bare digits across the two kinds could accept a sender whose LID
// happens to end in the same digits as somebody else's phone. Each identity
// is therefore only ever compared with the same kind.
type botGroupMembers struct {
	// phones are the members addressed by their phone number
	// ("34692747052" from "34692747052@s.whatsapp.net").
	phones map[string]struct{}
	// lids are the members addressed by their opaque LID
	// ("262096671481918" from "262096671481918:0@lid").
	lids map[string]struct{}
	// phoneByLID maps a member's opaque LID to the phone the provider gave
	// for it, so the identity of a group message is a PHONE and never a LID
	// (wa_bot_privileged_boundary_v1: the actor is always a verified phone).
	phoneByLID map[string]string
}

func newBotGroupMembers() botGroupMembers {
	return botGroupMembers{phones: map[string]struct{}{}, lids: map[string]struct{}{}, phoneByLID: map[string]string{}}
}

// empty reports whether nothing could be proven.
func (m botGroupMembers) empty() bool {
	return len(m.phones) == 0 && len(m.lids) == 0
}

type botGroupMembersEntry struct {
	members botGroupMembers
	at      time.Time
}

// botGroupMembersCache is keyed by (instance, group), not by group alone.
//
// The participant list is fetched from ONE tenant's provider instance,
// so a key of just the group id would let the first tenant to fill it
// poison the entry for the second: two restaurants can share a WhatsApp
// group (the same group holding the bots of two tenants), and then the
// second would compare its senders against the FIRST tenant's member
// list. The instance name is what tells the two apart.
var botGroupMembersCache = struct {
	sync.Mutex
	byInstanceGroup map[string]botGroupMembersEntry
}{byInstanceGroup: map[string]botGroupMembersEntry{}}

// botGroupMembersCacheKey is the cache key for one group of one instance.
//
// The length of the instance name prefixes the key, so no combination of
// values can be read as another combination: ("a","b") and ("ab","")
// would otherwise be the same string with a plain separator.
func botGroupMembersCacheKey(instanceName, groupJID string) string {
	instanceName = strings.TrimSpace(instanceName)
	groupJID = strings.TrimSpace(groupJID)
	return strconv.Itoa(len(instanceName)) + ":" + instanceName + groupJID
}

// botGroupMemberPhonesFromPayload extracts the members of the wanted group
// from a /group/fetchAllGroups answer.
//
// Only the group whose id matches is used: a provider listing a different
// group proves nothing about this one.
func botGroupMemberPhonesFromPayload(raw []byte, groupJID string) botGroupMembers {
	groupJID = strings.TrimSpace(groupJID)
	out := newBotGroupMembers()
	if groupJID == "" {
		return out
	}
	var groups []struct {
		ID           string `json:"id"`
		Subject      string `json:"subject"`
		Participants []struct {
			ID          string `json:"id"`
			PhoneNumber string `json:"phoneNumber"`
			Jid         string `json:"jid"`
		} `json:"participants"`
	}
	if err := json.Unmarshal(raw, &groups); err != nil {
		return out
	}
	for _, g := range groups {
		if strings.TrimSpace(g.ID) != groupJID {
			continue
		}
		for _, p := range g.Participants {
			// The phone form: the number, however the provider spells it.
			phone := ""
			if d := botGroupPhoneDigits(p.PhoneNumber); d != "" {
				out.phones[d] = struct{}{}
				phone = d
			}
			if d := botGroupPhoneDigits(p.Jid); d != "" {
				out.phones[d] = struct{}{}
				phone = d
			}
			// The LID form: the opaque id, without the device suffix.
			if d := botGroupLidDigits(p.ID); d != "" {
				out.lids[d] = struct{}{}
				// The provider reports both forms for the same person, so the
				// LID can be resolved to the phone it belongs to.
				if phone != "" {
					out.phoneByLID[d] = phone
				}
			}
		}
		return out
	}
	return out
}

// botGroupPhoneDigits returns the digits of a phone-number identity
// ("34692747052@s.whatsapp.net" -> "34692747052"). It refuses anything that is
// not a phone jid: the digits of an opaque LID are not a phone number.
//
// The device suffix Baileys appends is ignored, like in the LID form: the
// provider writes its own number as "34960255536:28@s.whatsapp.net" and the
// same member as "34960255536@s.whatsapp.net" must be ONE key.
func botGroupPhoneDigits(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || !strings.HasSuffix(v, "@s.whatsapp.net") {
		return ""
	}
	return botWhatsappIDDigits(v)
}

// botGroupLidDigits returns the digits of an opaque LID identity
// ("262096671481918:0@lid" -> "262096671481918").
//
// The device suffix Baileys appends is ignored: digitsOnly of the suffixed
// form is "2620966714819180" and of the plain form "262096671481918", so the
// same member would be registered under two keys and a match would depend on
// which form the provider used that day.
func botGroupLidDigits(v string) string {
	v = botJIDStripDeviceSuffix(v)
	if !strings.HasSuffix(v, "@lid") {
		return ""
	}
	return digitsOnly(strings.TrimSuffix(v, "@lid"))
}

// botGroupMembersFromProvider returns the participant list of a group as
// reported by the provider, or an empty list when it cannot be obtained.
func (s *Server) botGroupMembersFromProvider(ctx context.Context, restaurantID int, instanceName, groupJID string) botGroupMembers {
	evo, ok := s.botGatewayFor(ctx, restaurantID)
	if !ok {
		return newBotGroupMembers()
	}
	egw, ok := evo.(*evolutionGateway)
	if !ok {
		// A gateway without a participant lookup cannot prove membership.
		return newBotGroupMembers()
	}
	if instanceName == "" {
		instanceName = egw.instanceName
	}
	// The provider needs the apikey header: without it the answer is a 401
	// and the membership cannot be proven (which, correctly, means the turn
	// is not privileged). botProviderFetchAllGroups sends it.
	raw := botProviderFetchAllGroups(ctx, egw.baseURL, egw.apiKey, egw.instanceName)
	if len(raw) == 0 {
		return newBotGroupMembers()
	}
	return botGroupMemberPhonesFromPayload(raw, groupJID)
}

// botProviderInstanceName returns the provider instance a restaurant uses,
// which is the other half of the membership cache key: two restaurants can
// share a WhatsApp group, and their member lists are not the same.
func (s *Server) botProviderInstanceName(ctx context.Context, restaurantID int) string {
	evo, ok := s.botGatewayFor(ctx, restaurantID)
	if !ok {
		return ""
	}
	if egw, ok := evo.(*evolutionGateway); ok {
		return strings.TrimSpace(egw.instanceName)
	}
	return ""
}

// botGroupMembers returns the participant list of the group, using the
// provider and the short-lived cache. An empty list means the membership
// could not be proven, so the caller can fail closed.
func (s *Server) botGroupMembers(ctx context.Context, restaurantID int, groupJID string) botGroupMembers {
	members, _ := s.botGroupMembersCached(ctx, restaurantID, groupJID)
	return members
}

// botGroupMembersCached returns the participant list of the group AND whether
// the cache had an entry for it.
//
// The difference matters: an entry that is EMPTY means the provider failed
// (the answer is "cannot prove"), while no entry at all means the list was
// never fetched (the caller may fetch it once). Without the difference a
// degraded provider would cost a full provider round trip on EVERY message.
func (s *Server) botGroupMembersCached(ctx context.Context, restaurantID int, groupJID string) (botGroupMembers, bool) {
	groupJID = strings.TrimSpace(groupJID)
	empty := newBotGroupMembers()
	if groupJID == "" {
		return empty, false
	}
	// The cache is keyed by (instance, group): the participant list comes
	// from ONE tenant's provider, so the instance is part of the key.
	instanceName := s.botProviderInstanceName(ctx, restaurantID)
	if instanceName == "" {
		// Without an instance there is nothing to key on and nothing to
		// fetch from: fail closed.
		return empty, false
	}

	// Short-lived cache: the privileged gate runs on every group message and
	// the provider round trip is the expensive part.
	key := botGroupMembersCacheKey(instanceName, groupJID)
	botGroupMembersCache.Lock()
	if e, ok := botGroupMembersCache.byInstanceGroup[key]; ok {
		if time.Since(e.at) < botGroupMembersCacheTTL {
			botGroupMembersCache.Unlock()
			return e.members, true
		}
		// A negative entry (nothing could be proven) is held for a shorter
		// time: a renamed or deleted group would otherwise make EVERY
		// message wait for the provider timeout before being refused.
		if e.members.empty() && time.Since(e.at) < botGroupMembersCacheFailTTL {
			botGroupMembersCache.Unlock()
			return e.members, true
		}
	}
	botGroupMembersCache.Unlock()

	members := s.botGroupMembersFromProvider(ctx, restaurantID, instanceName, groupJID)
	botGroupMembersCache.Lock()
	botGroupMembersCache.byInstanceGroup[key] = botGroupMembersEntry{members: members, at: time.Now()}
	botGroupMembersCache.Unlock()
	return members, true
}

// botGroupSenderIsMember reports whether the sender of a group message is a
// member of that group. It is fail-closed: an unknown membership is false.
//
// The sender is matched against the set of its own kind: a phone jid against
// the phones, a lid against the lids. A lid is never compared with a phone
// number, so an opaque id cannot be mistaken for somebody's number.
func (s *Server) botGroupSenderIsMember(ctx context.Context, restaurantID int, groupJID, senderJID string) bool {
	return botGroupSenderInMembers(s.botGroupMembers(ctx, restaurantID, groupJID), senderJID)
}

// botGroupSenderInMembers is the membership decision, free of any I/O.
func botGroupSenderInMembers(members botGroupMembers, senderJID string) bool {
	senderJID = strings.TrimSpace(senderJID)
	if senderJID == "" {
		return false
	}
	if strings.HasSuffix(senderJID, "@lid") {
		d := botGroupLidDigits(senderJID)
		if d == "" {
			return false
		}
		_, ok := members.lids[d]
		return ok
	}
	d := botGroupPhoneDigits(senderJID)
	if d == "" {
		return false
	}
	_, ok := members.phones[d]
	return ok
}

// botGroupMemberPhone returns the PHONE of a member, or "" when the provider
// does not know one (a member addressed only by its opaque LID whose entry
// carries no phoneNumber). The phone is what identifies the actor, so a member
// without a known phone cannot act as a privileged actor.
func botGroupMemberPhone(members botGroupMembers, senderJID string) string {
	senderJID = strings.TrimSpace(senderJID)
	if senderJID == "" {
		return ""
	}
	if strings.HasSuffix(senderJID, "@lid") {
		d := botGroupLidDigits(senderJID)
		if d == "" {
			return ""
		}
		return members.phoneByLID[d]
	}
	d := botGroupPhoneDigits(senderJID)
	if d == "" {
		return ""
	}
	return d + "@s.whatsapp.net"
}
