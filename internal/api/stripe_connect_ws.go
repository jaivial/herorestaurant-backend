package api

import (
	"context"
	"log"
	"net/http"
	"time"

	"preactvillacarmen/internal/httpx"
)

// =============================================================================
// Stripe Connect realtime: Config > Cobros online keeps one WebSocket open
// while visible; the backend pushes {"type":"stripe_connect_status","connect":…}
// whenever the tenant's account changes (Connect webhook account.updated,
// onboarding, delete). Replaces the 8 s polling.
// Reuses the restaurant-room hub type of fichaje (boFichajeHub) as a separate
// instance so only Cobros online clients receive these events.
// Coordination id: stripe_connect_multitenant_v1.ws
// =============================================================================

// broadcastStripeConnectStatus sends the fresh status DTO of a restaurant to
// every open Cobros online socket of that restaurant. Best effort.
func (s *Server) broadcastStripeConnectStatus(ctx context.Context, restaurantID int, row *connectAccountRow) {
	if s == nil || s.stripeConnectHub == nil || restaurantID <= 0 {
		return
	}
	s.stripeConnectHub.broadcast(restaurantID, map[string]any{
		"type":         "stripe_connect_status",
		"restaurantId": restaurantID,
		"at":           time.Now().UTC().Format(time.RFC3339),
		"connect":      s.connectDTO(ctx, restaurantID, row, nil),
	})
	log.Printf("[stripe_connect_multitenant_v1.ws] restaurant=%d status=%v pushed", restaurantID, statusOf(row))
}

func statusOf(row *connectAccountRow) string {
	if row == nil {
		return "not_connected"
	}
	return row.Status
}

func (s *Server) handleBOStripeConnectWS(w http.ResponseWriter, r *http.Request) {
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
	s.stripeConnectHub.add(rid, client)
	defer func() {
		s.stripeConnectHub.remove(rid, client)
		_ = client.close()
	}()

	conn.SetReadLimit(4 << 10)
	_ = conn.SetReadDeadline(time.Now().Add(70 * time.Second))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(70 * time.Second)) })

	// hello carries the current status so the client never needs a first GET.
	if row, err := s.loadConnectAccount(r.Context(), rid); err == nil {
		_ = client.writeJSON(map[string]any{"type": "hello", "restaurantId": rid, "connect": s.connectDTO(r.Context(), rid, row, nil)})
	}

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return // client messages are ignored; reads only detect close
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
