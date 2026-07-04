package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"

	"github.com/go-chi/chi/v5"
	"preactvillacarmen/internal/httpx"
)

// hexColorRegex validates a 7-char hex color like #7c3aed.
var hexColorRegex = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// RegisterWidgetRoutes registers the /widget/* routes that accept ?restaurant_id= query param.
// This allows the embeddable booking widget to work from any domain.
func (s *Server) RegisterWidgetRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(s.withWidgetRestaurant)

		// Settings
		r.Get("/settings", s.handleGetWidgetSettings)

		// Availability endpoints
		r.Get("/reservations/closed-days", s.handleReservationsClosedDays)
		r.Get("/reservations/month-availability", s.handleReservationsMonthAvailability)
		r.Get("/reservations/hour-data", s.handleGetHourData)
		r.Get("/reservations/day-context", s.handleGetReservationDayContext)

		// Booking submission
		r.Post("/bookings/front", s.handleInsertBookingFront)
	})

	// Admin-only: update widget settings (requires backoffice session or admin token).
	r.Group(func(r chi.Router) {
		r.Use(s.withWidgetRestaurant)
		r.Use(s.requireAdmin)
		r.Put("/settings", s.handlePutWidgetSettings)
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

		ctx := withRestaurantID(r.Context(), id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// handleGetWidgetSettings returns the widget color settings for a restaurant.
// GET /widget/settings?restaurant_id=X
func (s *Server) handleGetWidgetSettings(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusBadRequest, "restaurant_id required")
		return
	}

	settings, err := s.getWidgetSettings(r, restaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Failed to load widget settings")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"settings": settings,
	})
}

// handlePutWidgetSettings updates widget color settings for a restaurant.
// PUT /widget/settings?restaurant_id=X  (admin-protected)
func (s *Server) handlePutWidgetSettings(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusBadRequest, "restaurant_id required")
		return
	}

	var body struct {
		PrimaryColor *string `json:"primary_color"`
		SuccessColor *string `json:"success_color"`
		BorderColor  *string `json:"border_color"`
		SurfaceColor *string `json:"surface_color"`
		TextColor    *string `json:"text_color"`
		MutedColor   *string `json:"muted_color"`
		FontStack    *string `json:"font_stack"`
	}

	if err := decodeWidgetJSON(r, &body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}

	// Validate hex colors.
	for label, val := range map[string]*string{
		"primary_color": body.PrimaryColor,
		"success_color": body.SuccessColor,
		"border_color":  body.BorderColor,
		"surface_color": body.SurfaceColor,
		"text_color":    body.TextColor,
		"muted_color":   body.MutedColor,
	} {
		if val != nil && !hexColorRegex.MatchString(*val) {
			httpx.WriteError(w, http.StatusBadRequest, label+" must be a valid hex color (#RRGGBB)")
			return
		}
	}

	err := s.upsertWidgetSettings(r, restaurantID, &widgetSettingsUpdate{
		PrimaryColor: body.PrimaryColor,
		SuccessColor: body.SuccessColor,
		BorderColor: body.BorderColor,
		SurfaceColor: body.SurfaceColor,
		TextColor: body.TextColor,
		MutedColor: body.MutedColor,
		FontStack: body.FontStack,
	})
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Failed to save widget settings")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "Widget settings updated",
	})
}

type widgetSettings struct {
	PrimaryColor string `json:"primary_color"`
	SuccessColor string `json:"success_color"`
	BorderColor  string `json:"border_color"`
	SurfaceColor string `json:"surface_color"`
	TextColor    string `json:"text_color"`
	MutedColor   string `json:"muted_color"`
	FontStack    string `json:"font_stack"`
}

func (s *Server) getWidgetSettings(r *http.Request, restaurantID int) (widgetSettings, error) {
	var ws widgetSettings
	err := s.db.QueryRowContext(r.Context(),
		`SELECT primary_color, success_color, border_color, surface_color, text_color, muted_color, font_stack
		 FROM widget_settings WHERE restaurant_id = ?`, restaurantID,
	).Scan(&ws.PrimaryColor, &ws.SuccessColor, &ws.BorderColor, &ws.SurfaceColor, &ws.TextColor, &ws.MutedColor, &ws.FontStack)

	if err != nil {
		if err == sql.ErrNoRows {
			// Return defaults.
			return widgetSettings{
				PrimaryColor: "#7c3aed",
				SuccessColor: "#16a34a",
				BorderColor:  "#e5e7eb",
				SurfaceColor: "#ffffff",
				TextColor:    "#1f2937",
				MutedColor:   "#6b7280",
				FontStack:    "system-ui, -apple-system, sans-serif",
			}, nil
		}
		return ws, err
	}
	return ws, nil
}

type widgetSettingsUpdate struct {
	PrimaryColor *string `json:"primary_color"`
	SuccessColor *string `json:"success_color"`
	BorderColor  *string `json:"border_color"`
	SurfaceColor *string `json:"surface_color"`
	TextColor    *string `json:"text_color"`
	MutedColor   *string `json:"muted_color"`
	FontStack    *string `json:"font_stack"`
}

func (s *Server) upsertWidgetSettings(r *http.Request, restaurantID int, body *widgetSettingsUpdate) error {
	// Build dynamic SET clause from non-nil fields.
	sets := map[string]string{}
	if body.PrimaryColor != nil {
		sets["primary_color"] = *body.PrimaryColor
	}
	if body.SuccessColor != nil {
		sets["success_color"] = *body.SuccessColor
	}
	if body.BorderColor != nil {
		sets["border_color"] = *body.BorderColor
	}
	if body.SurfaceColor != nil {
		sets["surface_color"] = *body.SurfaceColor
	}
	if body.TextColor != nil {
		sets["text_color"] = *body.TextColor
	}
	if body.MutedColor != nil {
		sets["muted_color"] = *body.MutedColor
	}
	if body.FontStack != nil {
		sets["font_stack"] = *body.FontStack
	}

	if len(sets) == 0 {
		return nil // Nothing to update.
	}

	// Use INSERT ... ON DUPLICATE KEY UPDATE for upsert.
	args := []any{restaurantID}
	updateCols := ""
	colNames := "restaurant_id"
	placeHolders := "?"

	// Get current values for columns not provided.
	current, err := s.getWidgetSettings(r, restaurantID)
	if err != nil {
		return err
	}

	allCols := map[string]string{
		"primary_color": current.PrimaryColor,
		"success_color": current.SuccessColor,
		"border_color":  current.BorderColor,
		"surface_color": current.SurfaceColor,
		"text_color":    current.TextColor,
		"muted_color":   current.MutedColor,
		"font_stack":    current.FontStack,
	}

	// Merge updates into current values.
	for k, v := range sets {
		allCols[k] = v
	}

	for _, col := range []string{"primary_color", "success_color", "border_color", "surface_color", "text_color", "muted_color", "font_stack"} {
		colNames += ", " + col
		placeHolders += ", ?"
		args = append(args, allCols[col])
		if _, ok := sets[col]; ok {
			if updateCols != "" {
				updateCols += ", "
			}
			updateCols += col + " = VALUES(" + col + ")"
		}
	}

	query := "INSERT INTO widget_settings (" + colNames + ") VALUES (" + placeHolders + ")"
	if updateCols != "" {
		query += " ON DUPLICATE KEY UPDATE " + updateCols
	}

	_, err = s.db.ExecContext(r.Context(), query, args...)
	return err
}

func decodeWidgetJSON(r *http.Request, dst any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(dst)
}

// handleBOWidgetSettingsGet returns widget settings for the backoffice admin.
// GET /api/admin/widget/settings
func (s *Server) handleBOWidgetSettingsGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}


	settings, err := s.getWidgetSettings(r, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Failed to load widget settings")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"settings": settings,
	})
}

func (s *Server) handleBOWidgetSettingsPut(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}


	var body widgetSettingsUpdate
	if err := decodeWidgetJSON(r, &body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}

	for label, val := range map[string]*string{
		"primary_color": body.PrimaryColor,
		"success_color": body.SuccessColor,
		"border_color":  body.BorderColor,
		"surface_color": body.SurfaceColor,
		"text_color":    body.TextColor,
		"muted_color":   body.MutedColor,
	} {
		if val != nil && !hexColorRegex.MatchString(*val) {
			httpx.WriteError(w, http.StatusBadRequest, label+" must be a valid hex color (#RRGGBB)")
			return
		}
	}

	if err := s.upsertWidgetSettings(r, a.ActiveRestaurantID, &body); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Failed to save widget settings")
		return
	}

	// Return updated settings.
	settings, err := s.getWidgetSettings(r, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Widget settings updated"})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"message":  "Widget settings updated",
		"settings": settings,
	})
}
