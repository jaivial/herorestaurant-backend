package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// Coordination id: wa_bot_privileged_contacts_v1
//
// Which conversations of the connected WhatsApp may run the full backoffice
// catalogue (and the panel service session that goes with it).
//
// The selection lives in the bot tenant config (whatsapp_bot_config) next to
// the rest of the bot settings, and it holds the CONVERSATIONS, not the
// members: a privileged group is privileged for whoever writes in it (the
// members of that group), and a privileged contact is privileged for that
// phone.
//
// The gate is the same one the group has always used, with one addition: a
// conversation can now be privileged by being a CONTACT, not only by being a
// group. The membership rule still applies to groups (a member of the group
// is a member of the group); a privileged contact is the conversation itself,
// so there is nobody else to check.

// botPrivilegedContact is one conversation the operator selected.
type botPrivilegedContact struct {
	// JID addresses the conversation.
	JID string `json:"jid"`
	// Name is the label shown in the backoffice (group subject or contact
	// name). It is informative: the gate only uses the JID.
	Name string `json:"name"`
	// IsGroup tells a group from a one-to-one chat.
	IsGroup bool `json:"isGroup"`
}

// botPrivilegedContactsFromConfig reads the privileged conversations of a
// tenant from its bot config.
func botPrivilegedContactsFromConfig(tenant botTenantConfig) []botPrivilegedContact {
	out := make([]botPrivilegedContact, 0, len(tenant.PrivilegedContacts))
	for _, c := range tenant.PrivilegedContacts {
		jid := strings.TrimSpace(c.JID)
		if jid == "" {
			continue
		}
		out = append(out, botPrivilegedContact{JID: jid, Name: strings.TrimSpace(c.Name), IsGroup: c.IsGroup})
	}
	return out
}

// botPrivilegedContactJIDs returns just the jids, for a quick lookup.
func botPrivilegedContactJIDs(tenant botTenantConfig) map[string]struct{} {
	out := map[string]struct{}{}
	for _, c := range botPrivilegedContactsFromConfig(tenant) {
		out[c.JID] = struct{}{}
	}
	return out
}

// botPrivilegedContactIsGroup reports whether the privileged conversation is a
// group, which is what decides if the membership check applies.
func botPrivilegedContactIsGroup(tenant botTenantConfig, jid string) (bool, bool) {
	jid = strings.TrimSpace(jid)
	for _, c := range botPrivilegedContactsFromConfig(tenant) {
		if c.JID == jid {
			return c.IsGroup, true
		}
	}
	return false, false
}

// botSavePrivilegedContacts stores the privileged conversations of a tenant,
// keeping the rest of the bot config untouched.
//
// It merges instead of replacing the whole config_json: the config holds the
// tone, the language, the knowledge and the model, and rewriting it from a
// partial payload would drop them.
func (s *Server) botSavePrivilegedContacts(ctx context.Context, restaurantID int, contacts []botPrivilegedContact) error {
	if s.db == nil {
		return errors.New("bot privileged contacts: base de datos no disponible")
	}
	clean := make([]botPrivilegedContact, 0, len(contacts))
	seen := map[string]struct{}{}
	for _, c := range contacts {
		jid := strings.TrimSpace(c.JID)
		if jid == "" {
			continue
		}
		if _, dup := seen[jid]; dup {
			continue
		}
		seen[jid] = struct{}{}
		clean = append(clean, botPrivilegedContact{JID: jid, Name: strings.TrimSpace(c.Name), IsGroup: c.IsGroup})
	}
	// Read the stored config, change only the privileged contacts, write it
	// back.
	var current sql.NullString
	if err := s.db.QueryRowContext(ctx, `
		SELECT config_json FROM whatsapp_bot_config WHERE restaurant_id = ? LIMIT 1
	`, restaurantID).Scan(&current); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	tenant := parseBotTenantConfig(current.String)
	tenant.PrivilegedContacts = clean
	merged, err := json.Marshal(tenant)
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO whatsapp_bot_config (restaurant_id, config_json) VALUES (?, ?)
		ON DUPLICATE KEY UPDATE config_json = VALUES(config_json)
	`, restaurantID, string(merged)); err != nil {
		return err
	}
	// The service session is bound to the restaurant, not to the selection,
	// but a new selection means the old session is no longer needed.
	botServiceSessionReset(restaurantID)
	return nil
}

// botPrivilegedConversationIsGroup reports whether a privileged conversation is
// a group.
//
// The distinction decides which gate applies: a privileged GROUP is
// privileged for its members, so the sender has to be one of them; a
// privileged CONTACT is the conversation itself, so there is nobody else to
// check.
//
// A conversation that is not in the selection is not privileged, so the answer
// is false and the caller keeps the regular limits.
func botPrivilegedConversationIsGroup(tenant botTenantConfig, jid string) bool {
	isGroup, ok := botPrivilegedContactIsGroup(tenant, jid)
	if ok {
		return isGroup
	}
	// A group listed in the env or in the per-restaurant list is a group too.
	j := strings.TrimSpace(jid)
	for _, v := range tenant.PrivilegedGroupJIDs {
		if strings.TrimSpace(v) == j {
			return true
		}
	}
	return strings.HasSuffix(j, "@g.us")
}

// botConversationIsPrivileged reports whether the conversation is privileged,
// given BOTH of its identities.
//
// WhatsApp addresses the same 1:1 chat by two forms: the opaque @lid and the
// phone-number JID. The provider lists the chat by one of them and delivers
// the inbound message addressed to the other, so the gate has to try both: a
// selection made on the listed form has to match the delivered one, or the
// privilege would silently never apply.
//
// The alternate identity is empty for groups (a group has a single id) and for
// a conversation the provider only knows by one form.
func botConversationIsPrivileged(s *Server, tenant botTenantConfig, jid, altJID string) bool {
	if s.botGroupIsPrivileged(jid, tenant) {
		return true
	}
	if alt := strings.TrimSpace(altJID); alt != "" {
		return s.botGroupIsPrivileged(alt, tenant)
	}
	return false
}

// botStoredPrivilegedContacts returns the privileged conversations a tenant
// already has stored.
//
// It is the counterpart of botStoredPrivilegedGroupJIDs: a write that may
// not change the privileged list (a non-root write) keeps the stored value
// instead of dropping it, so editing the tone never silently revokes the
// selection.
func (s *Server) botStoredPrivilegedContacts(ctx context.Context, restaurantID int) []botPrivilegedContact {
	if s.db == nil {
		return nil
	}
	var current sql.NullString
	if err := s.db.QueryRowContext(ctx, `
		SELECT config_json FROM whatsapp_bot_config WHERE restaurant_id = ? LIMIT 1
	`, restaurantID).Scan(&current); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return botPrivilegedContactsFromConfig(parseBotTenantConfig(current.String))
}
