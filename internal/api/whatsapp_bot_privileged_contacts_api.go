package api

import (
	"net/http"

	"preactvillacarmen/internal/httpx"
)

// Coordination id: wa_bot_privileged_contacts_v1
//
// The backoffice shows the conversations of the connected WhatsApp and lets
// the operator pick the privileged ones. Two endpoints:
//
//	GET  /api/admin/bot/ai/{restaurantId}/privileged-contacts
//	PUT  /api/admin/bot/ai/{restaurantId}/privileged-contacts
//
// The GET returns the conversations of the provider plus the ones already
// selected (which may not be in the provider's answer any more, e.g. a chat
// that was deleted).

// handleBOBotPrivilegedContactsGet lists the conversations of the connected
// WhatsApp account so the operator can pick the privileged ones.
func (s *Server) handleBOBotPrivilegedContactsGet(w http.ResponseWriter, r *http.Request) {
	rid, ok := botSettingsRestaurantID(r)
	if !ok {
		httpx.WriteError(w, http.StatusBadRequest, "restaurantId inválido")
		return
	}
	ctx := r.Context()
	tenant := s.loadBotTenantConfig(ctx, rid)

	chats, err := s.botProviderChats(ctx, rid)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":  false,
			"message":  "No se pudo leer el WhatsApp conectado",
			"chats":    []waChat{},
			"selected": botPrivilegedContactsFromConfig(tenant),
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

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"chats":    chats,
		"selected": botPrivilegedContactsFromConfig(tenant),
	})
}

// handleBOBotPrivilegedContactsPut stores the privileged conversations.
func (s *Server) handleBOBotPrivilegedContactsPut(w http.ResponseWriter, r *http.Request) {
	rid, ok := botSettingsRestaurantID(r)
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
