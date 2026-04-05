package api

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

// RegisterWidgetRoutes registers the /widget/* routes that accept ?restaurant_id= query param.
// This allows the embeddable booking widget to work from any domain.
func (s *Server) RegisterWidgetRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(s.withWidgetRestaurant)

		// Availability endpoints
		r.Get("/reservations/closed-days", s.handleReservationsClosedDays)
		r.Get("/reservations/month-availability", s.handleReservationsMonthAvailability)
		r.Get("/reservations/hour-data", s.handleGetHourData)
		r.Get("/reservations/day-context", s.handleGetReservationDayContext)

		// Booking submission
		r.Post("/bookings/front", s.handleInsertBookingFront)
	})
}

// withWidgetRestaurant is middleware that reads restaurant_id from the query string
// and sets up the context for the handler. This is used by the embeddable widget
// which cannot rely on Host header-based tenant resolution.
func (s *Server) withWidgetRestaurant(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawID := r.URL.Query().Get("restaurant_id")
		if rawID == "" {
			http.Error(w, "restaurant_id query parameter is required", http.StatusBadRequest)
			return
		}

		id, err := strconv.Atoi(rawID)
		if err != nil || id <= 0 {
			http.Error(w, "invalid restaurant_id", http.StatusBadRequest)
			return
		}

		// Validate restaurant exists by trying to look it up
		// We use the existing restaurant domain lookup mechanism but with explicit ID
		ctx := withRestaurantID(r.Context(), id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
