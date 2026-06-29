package api

import (
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"preactvillacarmen/internal/httpx"
)

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
