package api

import (
	"context"
	"strings"
)

// Coordination id: wa_bot_privileged_boundary_v1
//
// THE privilege boundary of the WhatsApp bot. A turn may use the privileged
// catalog (the full backoffice tool registry) ONLY by one of these two paths:
//
//	(a) the message is written in the AUTHORIZED GROUP: the group JID that
//	    comes from the environment (BOT_SPECIAL_ADMIN_SCOPES for the tenant
//	    and/or BOT_PRIVILEGED_GROUP_JIDS globally); or
//	(b) the message is a 1:1 chat whose PHONE (the provider-supplied phone
//	    alias) belongs to a CURRENT member of that authorized group.
//
// Both paths are 1:1 with the actor: the actor is always a verified PHONE
// number (never a LID) and its membership is proven against a FRESH provider
// fetch of the participant list.
//
// What is NOT a credential (deviation B1 of PR588, corrected here): the
// conversations stored in the tenant config (PrivilegedContacts /
// PrivilegedGroupJIDs). A backoffice selection is a preference, not a proof of
// identity, so it never grants the privileged catalog on its own.
//
// Fail closed everywhere: an unreachable provider, a member list that cannot
// be read, a message addressed only by LID (no verified phone) and a private
// chat from a number that is not a current member are all "not privileged".

// botPrivilegedScopeGroups returns the authorized groups of a tenant, taken
// from the ENVIRONMENT only.
//
// The tenant's own lists (whatsapp_bot_config.config_json) are deliberately
// not consulted: they are a selection made in the backoffice, not a proof.
func (s *Server) botPrivilegedScopeGroups(restaurantID int) []string {
	out := make([]string, 0, 2)
	add := func(jid string) {
		jid = strings.TrimSpace(jid)
		if jid == "" || !strings.HasSuffix(jid, "@g.us") {
			return
		}
		for _, v := range out {
			if v == jid {
				return
			}
		}
		out = append(out, jid)
	}
	// The per-tenant admin scope of the special-admin flow (env).
	add(botSpecialAdminScopeGroup(restaurantID))
	// The global env list of privileged groups.
	for _, g := range s.cfg.BotPrivilegedGroupJIDs {
		add(g)
	}
	return out
}

// botPrivilegedScopeGroupSet is the lookup form of botPrivilegedScopeGroups.
func (s *Server) botPrivilegedScopeGroupSet(restaurantID int) map[string]struct{} {
	out := map[string]struct{}{}
	for _, g := range s.botPrivilegedScopeGroups(restaurantID) {
		out[g] = struct{}{}
	}
	return out
}

// botPrivilegedChatIsScopeGroup reports whether the chat is one of the
// authorized groups.
func (s *Server) botPrivilegedChatIsScopeGroup(restaurantID int, chatJID string) bool {
	chatJID = strings.TrimSpace(chatJID)
	if chatJID == "" {
		return false
	}
	_, ok := s.botPrivilegedScopeGroupSet(restaurantID)[chatJID]
	return ok
}

// botPrivilegedTurnAllowed decides whether the turn is privileged and returns
// the ACTOR: the verified phone number of the person who wrote.
//
// The proof is always fresh (a provider fetch of the participant list, never
// the short-lived cache): a member who left the group loses the privilege in
// the same turn, not a minute later. The cached list is only the cheap entry
// filter that avoids a provider round trip for every customer message.
func (s *Server) botPrivilegedTurnAllowed(ctx context.Context, restaurantID int, msg botWebhookMessage) (string, bool) {
	if msg.FromMe {
		return "", false
	}
	if msg.IsGroup {
		// (a) the authorized group itself.
		if !s.botPrivilegedChatIsScopeGroup(restaurantID, msg.ChatJID) {
			return "", false
		}
		phone, ok := s.botPrivilegedMemberPhone(ctx, restaurantID, msg.ChatJID, msg.ParticipantJID)
		if !ok {
			return "", false
		}
		return phone, true
	}
	// (b) a private chat from the phone of a CURRENT member of the group.
	phone := strings.TrimSpace(msg.ActorPhoneJID)
	if phone == "" || botGroupPhoneDigits(phone) == "" {
		return "", false
	}
	for _, g := range s.botPrivilegedScopeGroups(restaurantID) {
		if p, ok := s.botPrivilegedMemberPhone(ctx, restaurantID, g, phone); ok {
			return p, true
		}
	}
	return "", false
}

// botPrivilegedMemberPhone proves that the sender is a CURRENT member of the
// group and returns its phone.
//
// Two steps: the cached participant list is the cheap entry filter (a sender
// the cache does not know is not a candidate, and no provider round trip is
// spent on it), and the FRESH list is the one that decides. The phone is what
// identifies the actor: a member the provider reports only by its opaque LID,
// with no phone, cannot act.
func (s *Server) botPrivilegedMemberPhone(ctx context.Context, restaurantID int, groupJID, senderJID string) (string, bool) {
	// The cached list is the cheap entry filter: a sender the cache does not
	// know is not a candidate, and no provider round trip is spent on it.
	//
	// An entry that EXISTS but is empty means the provider failed: the answer
	// is "cannot prove", so the turn is refused WITHOUT paying a round trip
	// (a degraded provider would otherwise cost every customer message its
	// whole timeout).
	cached, ok := s.botGroupMembersCached(ctx, restaurantID, groupJID)
	if !ok {
		return "", false
	}
	if !botGroupSenderInMembers(cached, senderJID) {
		return "", false
	}
	// The sender LOOKS like a member: the FRESH list is the one that decides.
	members := s.botGroupMembersFromProvider(ctx, restaurantID, "", groupJID)
	if members.empty() {
		return "", false
	}
	if !botGroupSenderInMembers(members, senderJID) {
		return "", false
	}
	phone := botGroupMemberPhone(members, senderJID)
	if phone == "" {
		return "", false
	}
	return phone, true
}

// botPrivilegedActor is the gate of the tools of the privileged surface: it
// returns the ACTOR (the verified phone) and the ORIGIN chat, after proving
// that the actor is a CURRENT member of an authorized group with a FRESH
// provider fetch.
//
// It is the same proof for the proposal, for the approval and for the tool
// call, so the three can never disagree.
func (s *Server) botPrivilegedActor(ctx context.Context, restaurantID int, msg botWebhookMessage) (actor, origin string, ok bool) {
	if msg.FromMe {
		return "", "", false
	}
	actor = strings.TrimSpace(msg.ActorPhoneJID)
	origin = strings.TrimSpace(msg.ChatJID)
	if actor == "" || origin == "" {
		return "", "", false
	}
	if !s.botPrivilegedActorVerified(ctx, restaurantID, actor) {
		return "", "", false
	}
	return actor, origin, true
}

// botPrivilegedActorVerified proves that a phone belongs to a CURRENT member
// of an authorized group, with a fresh provider fetch.
//
// The authorized groups are the ones of the environment; the special-admin
// scope of the tenant is one of them (it is the same group, configured for
// the booking tools).
func (s *Server) botPrivilegedActorVerified(ctx context.Context, restaurantID int, actorPhone string) bool {
	digits := botGroupPhoneDigits(strings.TrimSpace(actorPhone))
	if digits == "" {
		return false
	}
	for _, g := range s.botPrivilegedScopeGroups(restaurantID) {
		cached := s.botGroupMembers(ctx, restaurantID, g)
		if !cached.empty() {
			if _, ok := cached.phones[digits]; !ok {
				continue
			}
		}
		members := s.botGroupMembersFromProvider(ctx, restaurantID, "", g)
		if members.empty() {
			continue
		}
		if _, ok := members.phones[digits]; ok {
			return true
		}
	}
	return false
}
