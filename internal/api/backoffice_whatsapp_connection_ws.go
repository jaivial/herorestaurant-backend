package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"preactvillacarmen/internal/httpx"
)

type boWhatsAppConnectionHub struct {
	mu    sync.RWMutex
	rooms map[int]map[chan []byte]struct{}
}

func newBOWAConnectionHub() *boWhatsAppConnectionHub {
	return &boWhatsAppConnectionHub{rooms: map[int]map[chan []byte]struct{}{}}
}

func (h *boWhatsAppConnectionHub) subscribe(restaurantID int) (<-chan []byte, func()) {
	ch := make(chan []byte, 1)
	h.mu.Lock()
	if h.rooms[restaurantID] == nil {
		h.rooms[restaurantID] = map[chan []byte]struct{}{}
	}
	h.rooms[restaurantID][ch] = struct{}{}
	h.mu.Unlock()

	return ch, func() {
		h.mu.Lock()
		delete(h.rooms[restaurantID], ch)
		if len(h.rooms[restaurantID]) == 0 {
			delete(h.rooms, restaurantID)
		}
		h.mu.Unlock()
	}
}

func (h *boWhatsAppConnectionHub) broadcast(restaurantID int, payload any) {
	if h == nil || restaurantID <= 0 {
		return
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.rooms[restaurantID] {
		select {
		case ch <- raw:
		default:
			// Connection state is a snapshot. Replace stale buffered state.
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- raw:
			default:
			}
		}
	}
}

func (s *Server) whatsappConnectionSnapshot(ctx context.Context, restaurantID int, refresh bool) (map[string]any, error) {
	entitled, err := s.hasActiveRecurringFeature(ctx, restaurantID, boPremiumWhatsAppFeatureKey)
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"success":    true,
		"entitled":   entitled,
		"connected":  false,
		"connection": nil,
	}
	if !entitled {
		out["code"] = "NEEDS_SUBSCRIPTION"
		out["message"] = "Tu plan no incluye el bot de WhatsApp"
		return out, nil
	}

	rec, found, err := s.loadRestaurantUAZAPIInstance(ctx, restaurantID)
	if err != nil {
		return nil, err
	}
	if !found {
		out["message"] = "No hay una instancia de WhatsApp provisionada"
		return out, nil
	}

	connection := s.whatsappConnectionPayload(rec)
	if refresh && rec.IsActive {
		if current, refreshErr := s.refreshRestaurantUAZAPIConnectionStatus(ctx, restaurantID); refreshErr == nil {
			connection = current
		} else {
			connection["warning"] = "No se pudo refrescar estado en este momento"
		}
	}
	out["connection"] = connection
	out["connected"] = anyToBool(connection["connected"])
	out["message"] = whatsappConnectionMessage(connection)
	return out, nil
}

func (s *Server) broadcastWhatsAppConnection(ctx context.Context, restaurantID int) {
	if s == nil || s.whatsappConnectionHub == nil {
		return
	}
	snapshot, err := s.whatsappConnectionSnapshot(ctx, restaurantID, false)
	if err != nil {
		return
	}
	snapshot["type"] = "whatsapp.connection"
	snapshot["restaurantId"] = restaurantID
	snapshot["at"] = time.Now().UTC().Format(time.RFC3339)
	s.whatsappConnectionHub.broadcast(restaurantID, snapshot)
}

func (s *Server) allowBOWebSocketOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	configured := strings.TrimSpace(s.cfg.CORSAllowOrigins)
	// CORS wildcard is unsafe for cookie-authenticated WebSockets. Require an
	// explicit allowed origin or same host forwarded by the backoffice proxy.
	if configured != "" && configured != "*" && s.resolveAllowedOrigin(origin) != "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	wantHost := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if wantHost == "" {
		wantHost = r.Host
	}
	return strings.EqualFold(u.Host, wantHost)
}

func (s *Server) handleBOMembersWhatsAppWS(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	upgrader := websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin:     s.allowBOWebSocketOrigin,
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	events, unsubscribe := s.whatsappConnectionHub.subscribe(a.ActiveRestaurantID)
	defer unsubscribe()

	conn.SetReadLimit(1024)
	_ = conn.SetReadDeadline(time.Now().Add(70 * time.Second))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(70 * time.Second))
	})

	if snapshot, snapshotErr := s.whatsappConnectionSnapshot(r.Context(), a.ActiveRestaurantID, false); snapshotErr == nil {
		snapshot["type"] = "whatsapp.connection"
		snapshot["restaurantId"] = a.ActiveRestaurantID
		snapshot["at"] = time.Now().UTC().Format(time.RFC3339)
		_ = conn.WriteJSON(snapshot)
	}

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	pings := time.NewTicker(25 * time.Second)
	defer pings.Stop()
	for {
		select {
		case raw := <-events:
			_ = conn.SetWriteDeadline(time.Now().Add(7 * time.Second))
			if err := conn.WriteMessage(websocket.TextMessage, raw); err != nil {
				return
			}
		case <-pings.C:
			_ = conn.SetWriteDeadline(time.Now().Add(7 * time.Second))
			if err := conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(7*time.Second)); err != nil {
				return
			}
		case <-readDone:
			return
		case <-r.Context().Done():
			return
		}
	}
}
