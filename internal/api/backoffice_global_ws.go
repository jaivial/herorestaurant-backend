package api

import (
	"log"
	"net/http"
	"time"

	"preactvillacarmen/internal/httpx"
)

// =============================================================================
// Global backoffice WebSocket: /api/admin/ws. The backoffice shell
// (GlobalSocketProvider) opens exactly one per session and subscribes to
// topics; every message is the envelope {"type": <topic>, "payload": <json>}.
// Today it carries "special_date" (Especial card list); new realtime features
// publish on this hub instead of opening their own socket.
// Reuses the restaurant-room hub type (boFichajeHub) as its own instance.
// The frontend half shipped in backoffice #400; the backend half (5d403a4)
// never reached dev, so every tab retried the socket and logged errors.
// Coordination id: global_socket_v1
// =============================================================================

// broadcastBOGlobal sends {type: topic, payload} to every open global socket
// of the restaurant. Best effort.
func (s *Server) broadcastBOGlobal(restaurantID int, topic string, payload any) {
	if s == nil || s.globalHub == nil || restaurantID <= 0 {
		return
	}
	s.globalHub.broadcast(restaurantID, map[string]any{"type": topic, "payload": payload})
}

// broadcastBOSpecialDateChange tells open Especial pages to patch one card.
// Coordination id: global_socket_special_date_v1
func (s *Server) broadcastBOSpecialDateChange(restaurantID int, action, date string, isActive bool, title string) {
	s.broadcastBOGlobal(restaurantID, "special_date", map[string]any{
		"type":          action, // special_date_changed | special_date_deleted
		"restaurant_id": restaurantID,
		"date":          date,
		"is_active":     isActive,
		"title":         title,
	})
}

func (s *Server) handleBOGlobalWS(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	conn, err := boFichajeWSUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	rid := a.ActiveRestaurantID
	client := &boFichajeClient{conn: conn}
	s.globalHub.add(rid, client)
	log.Printf("[global_socket_v1] restaurant=%d user=%d connected", rid, a.User.ID)
	defer func() {
		s.globalHub.remove(rid, client)
		_ = client.close()
	}()

	conn.SetReadLimit(64 << 10)
	_ = conn.SetReadDeadline(time.Now().Add(70 * time.Second))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(70 * time.Second)) })

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return // client publishes are not handled server-side yet
			}
		}
	}()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-readDone:
			return
		case <-ping.C:
			if err := client.ping(); err != nil {
				return
			}
		}
	}
}
