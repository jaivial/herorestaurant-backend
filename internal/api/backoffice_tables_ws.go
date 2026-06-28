package api

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"preactvillacarmen/internal/httpx"
)

type boTablesHub struct {
	mu    sync.RWMutex
	rooms map[int]map[*boTablesClient]struct{}
}

type boTablesClient struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func newBOTablesHub() *boTablesHub {
	return &boTablesHub{rooms: map[int]map[*boTablesClient]struct{}{}}
}

func (h *boTablesHub) add(restaurantID int, c *boTablesClient) {
	if restaurantID <= 0 || c == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	room := h.rooms[restaurantID]
	if room == nil {
		room = map[*boTablesClient]struct{}{}
		h.rooms[restaurantID] = room
	}
	room[c] = struct{}{}
}

func (h *boTablesHub) remove(restaurantID int, c *boTablesClient) {
	if restaurantID <= 0 || c == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	room := h.rooms[restaurantID]
	if room == nil {
		return
	}
	delete(room, c)
	if len(room) == 0 {
		delete(h.rooms, restaurantID)
	}
}

func (h *boTablesHub) list(restaurantID int) []*boTablesClient {
	h.mu.RLock()
	defer h.mu.RUnlock()
	room := h.rooms[restaurantID]
	if len(room) == 0 {
		return nil
	}
	out := make([]*boTablesClient, 0, len(room))
	for c := range room {
		out = append(out, c)
	}
	return out
}

func (h *boTablesHub) broadcast(restaurantID int, payload any) {
	if restaurantID <= 0 {
		return
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	clients := h.list(restaurantID)
	for _, c := range clients {
		if err := c.writeText(raw); err != nil {
			h.remove(restaurantID, c)
			_ = c.close()
		}
	}
}

func (c *boTablesClient) writeText(raw []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(7 * time.Second))
	return c.conn.WriteMessage(websocket.TextMessage, raw)
}

func (c *boTablesClient) writeJSON(v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.writeText(raw)
}

func (c *boTablesClient) ping() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(7 * time.Second))
	return c.conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(7*time.Second))
}

func (c *boTablesClient) close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.Close()
}

var boTablesWSUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func (s *Server) handleBOTablesWS(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "No autorizado")
		return
	}

	conn, err := boTablesWSUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	client := &boTablesClient{conn: conn}
	s.tablesHub.add(a.ActiveRestaurantID, client)

	go func() {
		defer func() {
			s.tablesHub.remove(a.ActiveRestaurantID, client)
			_ = client.close()
		}()

		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		go func() {
			for range ticker.C {
				if err := client.ping(); err != nil {
					_ = client.close()
					return
				}
			}
		}()

		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				}
				break
			}
		}
	}()
}
