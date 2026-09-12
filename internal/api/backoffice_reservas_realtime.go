package api

import (
	"net/http"
	"time"

	"preactvillacarmen/internal/httpx"
)

// Realtime sync of the reservations table column visibility. The preference is
// personal, so the restaurant-scoped bus carries the owner user id and every
// client applies only its own change.
// Coordination id: reservas_columns_realtime_v1

type boReservasColumnsEvent struct {
	Type         string   `json:"type"`
	RestaurantID int      `json:"restaurant_id"`
	UserID       int      `json:"user_id"`
	Columns      []string `json:"columns"`
}

// broadcastReservasColumns notifies the restaurant's connected backoffice
// clients that a user changed the visible columns. Reuses the shared
// restaurant presence hub so there is one broadcast primitive, not a second one.
func (s *Server) broadcastReservasColumns(restaurantID, userID int, columns []string) {
	if s == nil || s.tablesHub == nil || restaurantID <= 0 {
		return
	}
	s.tablesHub.broadcast(restaurantID, boReservasColumnsEvent{
		Type:         "reservas_columns",
		RestaurantID: restaurantID,
		UserID:       userID,
		Columns:      columns,
	})
}

// handleBOReservasColumnsWS keeps one lightweight socket per backoffice tab so
// column changes made anywhere reach every open client in real time.
// GET /admin/reservas/ws
func (s *Server) handleBOReservasColumnsWS(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
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
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()
}
