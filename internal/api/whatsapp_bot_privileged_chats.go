package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Coordination id: wa_bot_privileged_contacts_v1
//
// The backoffice has to show which conversations of the connected WhatsApp
// may use the privileged catalogue, so the operator picks them from the real
// list instead of typing jids by hand. The list comes from the provider:
// the groups of the instance and the one-to-one chats it has.
//
// The provider is asked for both, because a privileged contact can be a group
// (the management group) or a single person (a head chef with their own
// number).

// waChat is one conversation of the connected WhatsApp account, as the
// backoffice shows it.
type waChat struct {
	// JID addresses the conversation: a group id (@g.us) or a contact
	// (@s.whatsapp.net / @lid).
	JID string `json:"jid"`
	// Name is the group subject or the contact's display name.
	Name string `json:"name"`
	// Kind is "group" or "contact".
	Kind string `json:"kind"`
	// IsGroup tells whether the jid addresses a group.
	IsGroup bool `json:"isGroup"`
}

// botProviderChats returns the conversations of the connected instance:
// the groups first, then the one-to-one chats.
//
// It talks to the provider directly (like the participant lookup does)
// because the gateway interface does not expose a chat listing and adding
// one to the interface would touch every provider adapter.
func (s *Server) botProviderChats(ctx context.Context, restaurantID int) ([]waChat, error) {
	evo, ok := s.botGatewayFor(ctx, restaurantID)
	if !ok {
		return nil, errBotProviderUnavailable
	}
	egw, ok := evo.(*evolutionGateway)
	if !ok {
		return nil, errBotProviderUnavailable
	}

	out := make([]waChat, 0, 32)

	// Groups: /group/fetchAllGroups gives the subject of each one.
	groupsRaw, groupsErr := botProviderFetchAllGroupsWithError(ctx, egw.baseURL, egw.apiKey, egw.instanceName)
	if groupsErr == nil && len(groupsRaw) > 0 {
		var groups []struct {
			ID      string `json:"id"`
			Subject string `json:"subject"`
			Name    string `json:"name"`
		}
		if json.Unmarshal(groupsRaw, &groups) == nil {
			for _, g := range groups {
				id := strings.TrimSpace(g.ID)
				if id == "" {
					continue
				}
				name := strings.TrimSpace(g.Subject)
				if name == "" {
					name = strings.TrimSpace(g.Name)
				}
				out = append(out, waChat{JID: id, Name: name, Kind: "group", IsGroup: true})
			}
		}
	}

	// One-to-one chats: /chat/findChats returns every conversation.
	chatsRaw, chatsErr := botProviderFindChats(ctx, egw.baseURL, egw.apiKey, egw.instanceName)
	if chatsErr == nil && len(chatsRaw) > 0 {
		var chats []struct {
			RemoteJID string `json:"remoteJid"`
			Name      string `json:"name"`
			PushName  string `json:"pushName"`
			LastMsg   struct {
				PushName string `json:"pushName"`
			} `json:"lastMessage"`
		}
		if json.Unmarshal(chatsRaw, &chats) == nil {
			for _, c := range chats {
				jid := strings.TrimSpace(c.RemoteJID)
				if jid == "" || strings.HasSuffix(jid, "@g.us") {
					// The groups came from the other endpoint.
					continue
				}
				name := strings.TrimSpace(c.Name)
				if name == "" {
					name = strings.TrimSpace(c.PushName)
				}
				if name == "" {
					name = strings.TrimSpace(c.LastMsg.PushName)
				}
				out = append(out, waChat{JID: jid, Name: name, Kind: "contact"})
			}
		}
	}

	// A provider that is down, unauthorized or answering nonsense must not
	// look like "this account has no conversations": the backoffice would
	// show an empty list and the operator would think the selection is
	// broken. When BOTH calls failed there is nothing to show, so the error
	// is reported and the UI says it could not read the WhatsApp.
	//
	// When only one of them failed the other one still gave a usable list,
	// so it is returned as it is.
	if groupsErr != nil && chatsErr != nil {
		return nil, fmt.Errorf("no se pudo leer el WhatsApp conectado: %w", chatsErr)
	}

	return out, nil
}
