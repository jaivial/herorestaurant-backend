package api

import (
	"net/http"
	"strings"

	"preactvillacarmen/internal/httpx"
)

// Coordination id: wa_bot_privileged_contacts_v1
//
// The backoffice shows the conversations of the connected WhatsApp and lets
// the operator pick the privileged ones. Three endpoints:
//
//	GET  /api/admin/bot/ai/{restaurantId}/privileged-contacts
//	PUT  /api/admin/bot/ai/{restaurantId}/privileged-contacts
//	POST /api/admin/bot/ai/{restaurantId}/privileged-contacts
//
// The GET returns the conversations of the provider plus the ones already
// selected (which may not be in the provider's answer any more, e.g. a chat
// that was deleted).
//
// Observability point wa_bot_privileged_contacts_v2 (additive): the GET also
// accepts ?q= (case-insensitive substring over name and jid) and ?page= with
// ?count= (1..100). Without count the full list is returned, the exact shape
// the v1 panel consumed. The POST adds one phone number by hand.

// botSettingsRestaurantIDForSession returns the restaurant of the request
// path, but only when the session may act on it.
//
// The privileged routes address a restaurant by its id in the path, and the
// id in the path is not the session's restaurant: a root who administers
// several restaurants could otherwise write the privileged selection of a
// restaurant that is not the one they are working on. The selection decides
// who gets the root catalog, so it is bound to the session's restaurant.
//
// A request whose path names another restaurant is refused; the session's own
// restaurant is always allowed, and a path without the parameter (the bot
// config route) uses the session's restaurant as well.
func (s *Server) botSettingsRestaurantIDForSession(r *http.Request) (int, bool) {
	rid, ok := botSettingsRestaurantID(r)
	if !ok {
		return 0, false
	}
	a, hasAuth := boAuthFromContext(r.Context())
	if !hasAuth {
		return 0, false
	}
	if a.ActiveRestaurantID > 0 && rid != a.ActiveRestaurantID {
		return 0, false
	}
	return rid, true
}

// botChatsFromContacts labels the stored selection as conversations, the same
// shape the provider list uses.
//
// Coordination id: wa_bot_privileged_contacts_v2
func botChatsFromContacts(contacts []botPrivilegedContact) []waChat {
	out := make([]waChat, 0, len(contacts))
	for _, c := range contacts {
		out = append(out, waChat{JID: c.JID, Name: c.Name, Kind: chatKindOf(c), IsGroup: c.IsGroup})
	}
	return out
}

// botChatsPage applies the wa_bot_privileged_contacts_v2 search and pagination
// to a conversation list. Without count the whole list comes back (legacy v1
// shape); the returns are (page slice, total, page, count, has_more).
func botChatsPage(chats []waChat, r *http.Request) ([]waChat, int, int, int, bool) {
	chats = botChatsSearch(chats, r.URL.Query().Get("q"))
	total := len(chats)
	page := stockQueryInt(r, "page", 1, 1, 1_000_000)
	count := stockQueryInt(r, "count", 0, 1, 100)
	if count > 0 {
		start := (page - 1) * count
		if start > total {
			start = total
		}
		end := start + count
		if end > total {
			end = total
		}
		chats = chats[start:end]
	} else {
		page, count = 1, total
	}
	return chats, total, page, count, page*count < total
}

// handleBOBotPrivilegedContactsGet lists the conversations of the connected
// WhatsApp account so the operator can pick the privileged ones.
func (s *Server) handleBOBotPrivilegedContactsGet(w http.ResponseWriter, r *http.Request) {
	rid, ok := s.botSettingsRestaurantIDForSession(r)
	if !ok {
		httpx.WriteError(w, http.StatusBadRequest, "restaurantId inválido")
		return
	}
	ctx := r.Context()
	tenant := s.loadBotTenantConfig(ctx, rid)

	chats, err := s.botProviderChats(ctx, rid)
	if err != nil {
		// A provider outage must not hide what the operator already selected:
		// the stored selection IS the list here (it includes the manually
		// added phones), so it stays visible and manageable while the
		// WhatsApp connection is down.
		selected := botPrivilegedContactsFromConfig(tenant)
		fallback, total, page, count, hasMore := botChatsPage(botChatsFromContacts(selected), r)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":  false,
			"message":  "No se pudo leer el WhatsApp conectado",
			"chats":    fallback,
			"selected": selected,
			"total":    total,
			"page":     page,
			"count":    count,
			"has_more": hasMore,
		})
		return
	}

	// The selected conversations that the provider no longer lists are kept, so
	// the operator does not lose the selection by accident.
	listed := map[string]struct{}{}
	for _, c := range chats {
		listed[c.JID] = struct{}{}
	}
	for _, c := range botPrivilegedContactsFromConfig(tenant) {
		if _, ok := listed[c.JID]; !ok {
			chats = append(chats, waChat{JID: c.JID, Name: c.Name, Kind: chatKindOf(c), IsGroup: c.IsGroup})
		}
	}

	chats, total, page, count, hasMore := botChatsPage(chats, r)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"chats":    chats,
		"selected": botPrivilegedContactsFromConfig(tenant),
		// Coordination id: wa_bot_privileged_boundary_v1: the selection is
		// ADVISORY. The privilege comes from the authorized group of the
		// environment and from the current membership of it, never from this
		// list, so the operator has to know it (the panel shows it as the
		// privileged conversations).
		"authorized_groups": s.botPrivilegedScopeGroups(rid),
		"note":              "Las conversaciones autorizadas las decide el grupo autorizado del entorno (BOT_PRIVILEGED_GROUP_JIDS / BOT_SPECIAL_ADMIN_SCOPES) y la pertenencia actual a ese grupo. Esta seleccion es informativa: no concede el catalogo completo por si sola.",
		// wa_bot_privileged_contacts_v2 (additive): search + pagination.
		"total":    total,
		"page":     page,
		"count":    count,
		"has_more": hasMore,
	})
}

// botChatsSearch filters the conversations by a case-insensitive substring
// over the label and the jid. An empty query keeps every chat.
//
// Coordination id: wa_bot_privileged_contacts_v2 (GET ?q=)
func botChatsSearch(chats []waChat, raw string) []waChat {
	q := strings.ToLower(strings.TrimSpace(raw))
	if q == "" {
		return chats
	}
	out := make([]waChat, 0, len(chats))
	for _, c := range chats {
		if strings.Contains(strings.ToLower(c.Name), q) || strings.Contains(strings.ToLower(c.JID), q) {
			out = append(out, c)
		}
	}
	return out
}

// handleBOBotPrivilegedContactsPost adds one phone number to the privileged
// list by hand. It covers a number the operator already knows (a client, a
// supplier) before the conversation shows up in the provider's chat list: the
// gate matches the JID, so the privilege applies as soon as that number
// writes. The high is immediate (no "Guardar" round trip) and idempotent.
//
// Coordination id: wa_bot_privileged_contacts_v2 (POST {phone, name})
func (s *Server) handleBOBotPrivilegedContactsPost(w http.ResponseWriter, r *http.Request) {
	rid, ok := s.botSettingsRestaurantIDForSession(r)
	if !ok {
		httpx.WriteError(w, http.StatusBadRequest, "restaurantId inválido")
		return
	}
	var req struct {
		Phone string `json:"phone"`
		Name  string `json:"name"`
	}
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "JSON inválido"})
		return
	}
	jid, err := botAccessNormalizeTarget(req.Phone, false)
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": err.Error()})
		return
	}
	tenant := s.loadBotTenantConfig(r.Context(), rid)
	contacts := botPrivilegedContactsFromConfig(tenant)
	for _, c := range contacts {
		if c.JID == jid {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "selected": contacts})
			return
		}
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = jid
	}
	contacts = append(contacts, botPrivilegedContact{JID: jid, Name: name})
	if err := s.botSavePrivilegedContacts(r.Context(), rid, contacts); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "No se pudo guardar el número"})
		return
	}
	tenant = s.loadBotTenantConfig(r.Context(), rid)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"selected": botPrivilegedContactsFromConfig(tenant),
	})
}

// handleBOBotPrivilegedContactsPut stores the privileged conversations.
func (s *Server) handleBOBotPrivilegedContactsPut(w http.ResponseWriter, r *http.Request) {
	rid, ok := s.botSettingsRestaurantIDForSession(r)
	if !ok {
		httpx.WriteError(w, http.StatusBadRequest, "restaurantId inválido")
		return
	}
	var req struct {
		Contacts []botPrivilegedContact `json:"contacts"`
	}
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "JSON inválido"})
		return
	}
	if err := s.botSavePrivilegedContacts(r.Context(), rid, req.Contacts); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "No se pudo guardar la selección"})
		return
	}
	tenant := s.loadBotTenantConfig(r.Context(), rid)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"selected": botPrivilegedContactsFromConfig(tenant),
	})
}

// chatKindOf labels a conversation for the backoffice list.
func chatKindOf(c botPrivilegedContact) string {
	if c.IsGroup {
		return "group"
	}
	return "contact"
}
