package api

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"time"

	"preactvillacarmen/internal/httpx"
)

func publicAdVisibleOnDate(ad boAd, isoDate string) bool {
	if !ad.Active {
		return false
	}
	if ad.StartsAt == nil && ad.EndsAt == nil {
		return true
	}
	if ad.StartsAt == nil || ad.EndsAt == nil || isoDate == "" {
		return false
	}
	return isoDate >= *ad.StartsAt && isoDate <= *ad.EndsAt
}

// GET /api/public/ads?restaurant_id=1&date=YYYY-MM-DD
// restaurant_id is optional when the request host already resolves a tenant.
// date is optional; when present the response is filtered to ads visible that day.
func (s *Server) handlePublicAdsList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	restaurantID := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("restaurant_id")); raw != "" {
		id, err := strconv.Atoi(raw)
		if err != nil || id <= 0 {
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid restaurant_id"})
			return
		}
		restaurantID = id
	} else if id, ok := s.resolveRestaurantID(r); ok {
		restaurantID = id
	}
	if restaurantID <= 0 {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Unknown restaurant"})
		return
	}

	date := strings.TrimSpace(r.URL.Query().Get("date"))
	if date != "" {
		parsed, err := time.Parse("2006-01-02", date)
		if err != nil || parsed.Format("2006-01-02") != date {
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid date; expected YYYY-MM-DD"})
			return
		}
	}

	rows, err := s.db.QueryContext(r.Context(), `SELECT id FROM restaurant_ads WHERE restaurant_id = ? AND active = 1 ORDER BY updated_at DESC, id DESC`, restaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading ads")
		return
	}
	defer rows.Close()

	ads := make([]boAd, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error reading ads")
			return
		}
		ad, err := s.readBOAd(r.Context(), restaurantID, id)
		if err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			httpx.WriteError(w, http.StatusInternalServerError, "Error reading ads")
			return
		}
		if date == "" || publicAdVisibleOnDate(ad, date) {
			ad.BlockedRanges = nil
			ads = append(ads, ad)
		}
	}
	if err := rows.Err(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error reading ads")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "restaurant_id": restaurantID, "ads": ads})
}
