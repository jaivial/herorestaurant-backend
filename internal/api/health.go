package api

import (
	"context"
	"net/http"
	"time"

	"preactvillacarmen/internal/httpx"
)

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	if err := s.db.PingContext(ctx); err != nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"success": false, "message": "Database unavailable"})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}
