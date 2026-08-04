package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"preactvillacarmen/internal/httpx"
)

// One socket carries both technical-sheet concerns: live search results while
// the user types, and image-job progress. Messages are type-discriminated so a
// second connection is not needed - two sockets would double the reconnect
// logic for no benefit.
//
// REST remains the source of truth: everything pushed here is also available
// from a normal request, so a dropped socket degrades responsiveness, never
// correctness.

type sheetWSClient struct {
	conn         *websocket.Conn
	restaurantID int
	mu           sync.Mutex
}

func (c *sheetWSClient) send(payload any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return c.conn.WriteJSON(payload)
}

// sheetWSHub tracks live connections per tenant so a job update is only pushed
// to the restaurant that owns it.
type sheetWSHub struct {
	mu      sync.RWMutex
	clients map[int]map[*sheetWSClient]struct{}
}

func newSheetWSHub() *sheetWSHub {
	return &sheetWSHub{clients: map[int]map[*sheetWSClient]struct{}{}}
}

func (h *sheetWSHub) add(client *sheetWSClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.clients[client.restaurantID] == nil {
		h.clients[client.restaurantID] = map[*sheetWSClient]struct{}{}
	}
	h.clients[client.restaurantID][client] = struct{}{}
}

func (h *sheetWSHub) remove(client *sheetWSClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if set, ok := h.clients[client.restaurantID]; ok {
		delete(set, client)
		if len(set) == 0 {
			delete(h.clients, client.restaurantID)
		}
	}
}

// broadcastImageJob notifies one tenant that a job changed state. Delivery is
// best effort by design: the editor also polls REST, so a missed frame costs a
// moment of staleness, not correctness.
func (h *sheetWSHub) broadcastImageJob(restaurantID int, payload map[string]any) {
	h.mu.RLock()
	clients := make([]*sheetWSClient, 0, len(h.clients[restaurantID]))
	for client := range h.clients[restaurantID] {
		clients = append(clients, client)
	}
	h.mu.RUnlock()

	message := map[string]any{"type": "imageJob"}
	for key, value := range payload {
		message[key] = value
	}
	for _, client := range clients {
		_ = client.send(message)
	}
}

func (s *Server) handleBOTechnicalSheetsWS(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "No autorizado")
		return
	}
	conn, err := boTablesWSUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	client := &sheetWSClient{conn: conn, restaurantID: a.ActiveRestaurantID}
	s.sheetHub.add(client)

	go func() {
		defer func() {
			s.sheetHub.remove(client)
			_ = conn.Close()
		}()

		// A dead peer otherwise holds the connection open indefinitely.
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		go func() {
			for range ticker.C {
				client.mu.Lock()
				err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
				client.mu.Unlock()
				if err != nil {
					_ = conn.Close()
					return
				}
			}
		}()

		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var incoming struct {
				Type  string `json:"type"`
				Query string `json:"query"`
			}
			if json.Unmarshal(raw, &incoming) != nil {
				continue
			}
			if incoming.Type == "search" {
				// The search runs under the connection's own tenant, never a
				// tenant supplied in the message.
				s.replySheetSearch(r.Context(), client, incoming.Query)
			}
		}
	}()
}

func (s *Server) replySheetSearch(ctx context.Context, client *sheetWSClient, query string) {
	sheets, err := s.searchSheets(ctx, client.restaurantID, strings.TrimSpace(query))
	if err != nil {
		_ = client.send(map[string]any{"type": "searchError", "message": "No se pudo buscar"})
		return
	}
	_ = client.send(map[string]any{"type": "searchResults", "query": query, "sheets": sheets})
}

func (s *Server) searchSheets(ctx context.Context, restaurantID int, query string) ([]map[string]any, error) {
	sql := `SELECT r.id, r.name, r.status, COALESCE(r.portions,1),
	               (SELECT COUNT(*) FROM comida_items p
	                 WHERE p.restaurant_id=r.restaurant_id AND p.stock_recipe_id=r.id)
	          FROM stock_recipes r
	         WHERE r.restaurant_id=? AND r.is_active=1`
	args := []any{restaurantID}
	if query != "" {
		sql += ` AND r.name LIKE ?`
		args = append(args, "%"+query+"%")
	}
	sql += ` ORDER BY r.name LIMIT 25`

	rows, err := s.db.QueryContext(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var name, status string
		var portions, usageCount int
		if err := rows.Scan(&id, &name, &status, &portions, &usageCount); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"id": id, "name": name, "status": status,
			"portions": portions, "usageCount": usageCount,
		})
	}
	return out, rows.Err()
}
