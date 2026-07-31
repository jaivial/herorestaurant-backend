package api

import (
	"context"
	"net/http"
	"time"

	"preactvillacarmen/internal/httpx"
)

func (s *Server) handleBOComidaCounts(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	var vinos, cafes, postres, platos, bebidas int
	if err := s.db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM VINOS WHERE restaurant_id = ?),
			(SELECT COUNT(*) FROM comida_items WHERE restaurant_id = ? AND source_type = 'cafes'),
			(SELECT COUNT(*) FROM POSTRES WHERE restaurant_id = ?),
			(SELECT COUNT(*) FROM comida_items WHERE restaurant_id = ? AND source_type = 'platos'),
			(SELECT COUNT(*) FROM comida_items WHERE restaurant_id = ? AND source_type = 'bebidas')
	`, a.ActiveRestaurantID, a.ActiveRestaurantID, a.ActiveRestaurantID, a.ActiveRestaurantID, a.ActiveRestaurantID).Scan(
		&vinos,
		&cafes,
		&postres,
		&platos,
		&bebidas,
	); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando carta")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"countsByType": map[string]int{
			"vinos": vinos, "cafes": cafes, "postres": postres, "platos": platos, "bebidas": bebidas,
		},
	})
}
