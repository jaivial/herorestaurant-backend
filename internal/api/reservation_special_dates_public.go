package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"preactvillacarmen/internal/httpx"
)

// Public read-only endpoints for "special dates" (reservas especiales).
//
// Coordination id: special_dates_v1
// Tenant resolution: withRestaurant middleware (Host -> restaurant_domains).
//
// Endpoints:
//   GET /reservations/special-dates?from=&to=  -> light list for calendar marking.
//   GET /reservations/special-date?date=         -> full public settings for one date.
//
// When a date has no row in `special_dates`, or is_active is false, the single
// endpoint returns 404; the list endpoint simply omits it (only active rows
// are returned, because the calendar only marks dates the public can act on).

// publicSpecialDatesMaxRangeDays is the longest span the list endpoint accepts.
// Longer spans than this would let a misbehaving client ask for half a year of
// data; keeping it well above the closed-days cap (~40 days) makes the endpoint
// usable for "show me everything special this season" while still bounded.
const publicSpecialDatesMaxRangeDays = 183 // ~6 months

func (s *Server) handlePublicSpecialDatesList(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	from := strings.TrimSpace(r.URL.Query().Get("from"))
	to := strings.TrimSpace(r.URL.Query().Get("to"))
	if from == "" || !isValidISODate(from) || to == "" || !isValidISODate(to) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Formato de fecha inválido",
		})
		return
	}

	start, err := time.Parse("2006-01-02", from)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Formato de fecha inválido",
		})
		return
	}
	end, err := time.Parse("2006-01-02", to)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Formato de fecha inválido",
		})
		return
	}
	if end.Before(start) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Rango de fechas inválido",
		})
		return
	}

	days := int(end.Sub(start).Hours()/24) + 1
	if days > publicSpecialDatesMaxRangeDays {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "El rango máximo permitido es de 6 meses",
		})
		return
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT DATE_FORMAT(date, '%Y-%m-%d'), is_active, prereserva_enabled, title,
		       max_per_table_enabled, max_per_table, mobility_enabled
		FROM special_dates
		WHERE restaurant_id = ? AND is_active = 1 AND date BETWEEN ? AND ?
		ORDER BY date ASC
	`, restaurantID, from, to)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando special_dates")
		return
	}
	defer rows.Close()

	out := make([]map[string]any, 0, 8)
	for rows.Next() {
		var date string
		var isActive, prereserva int
		var title string
		var maxPerTableEnabled int
		var maxPerTable sql.NullInt64
		var mobilityEnabled int
		if err := rows.Scan(&date, &isActive, &prereserva, &title, &maxPerTableEnabled, &maxPerTable, &mobilityEnabled); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, "Error leyendo special_dates")
			return
		}
		row := map[string]any{
			"date":                  date,
			"is_active":             isActive != 0,
			"prereserva_enabled":    prereserva != 0,
			"title":                 title,
			"max_per_table_enabled": maxPerTableEnabled != 0,
			// Coordination id: mobility_issues_v1
			"mobility_enabled": mobilityEnabled != 0,
		}
		if maxPerTable.Valid {
			row["max_per_table"] = maxPerTable.Int64
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, "Error leyendo special_dates")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":       true,
		"from":          from,
		"to":            to,
		"special_dates": out,
	})
}

func (s *Server) handlePublicSpecialDateGet(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	date := strings.TrimSpace(r.URL.Query().Get("date"))
	if date == "" || !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Formato de fecha inválido",
		})
		return
	}

	var id int64
	var isActive, prereservaEnabled, maxPerTableEnabled, requiresAdelanto, adelantoUnified int
	var title string
	var description sql.NullString
	var maxPerTable sql.NullInt64
	var adelantoMethodsRaw sql.NullString
	var adelantoUnifiedAmount sql.NullFloat64
	var mobilityEnabledDetail int

	err := s.db.QueryRowContext(r.Context(), `
		SELECT id, is_active, title, description, prereserva_enabled,
		       max_per_table_enabled, max_per_table, mobility_enabled, requires_adelanto,
		       adelanto_payment_methods, adelanto_unified, adelanto_unified_amount
		FROM special_dates
		WHERE restaurant_id = ? AND date = ?
		LIMIT 1
	`, restaurantID, date).Scan(
		&id, &isActive, &title, &description, &prereservaEnabled,
		&maxPerTableEnabled, &maxPerTable, &mobilityEnabledDetail, &requiresAdelanto,
		&adelantoMethodsRaw, &adelantoUnified, &adelantoUnifiedAmount,
	)

	if err == sql.ErrNoRows || isActive == 0 {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Fecha no disponible",
		})
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando special_dates")
		return
	}

	var adelantoMethods []string
	if adelantoMethodsRaw.Valid && strings.TrimSpace(adelantoMethodsRaw.String) != "" {
		_ = json.Unmarshal([]byte(adelantoMethodsRaw.String), &adelantoMethods)
	}
	if adelantoMethods == nil {
		adelantoMethods = []string{}
	}

	menus, err := s.loadPublicSpecialDateMenus(r.Context(), restaurantID, id)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando special_date_menus")
		return
	}

	resp := map[string]any{
		"date":                  date,
		"is_active":             true,
		"title":                 title,
		"description":           description.String,
		"prereserva_enabled":    prereservaEnabled != 0,
		"max_per_table_enabled": maxPerTableEnabled != 0,
		// Coordination id: mobility_issues_v1
		"mobility_enabled":         mobilityEnabledDetail != 0,
		"requires_adelanto":        requiresAdelanto != 0,
		"adelanto_payment_methods": adelantoMethods,
		"adelanto_unified":         adelantoUnified != 0,
		"menus":                    menus,
	}
	if maxPerTable.Valid {
		resp["max_per_table"] = maxPerTable.Int64
	}
	if adelantoUnifiedAmount.Valid {
		resp["adelanto_unified_amount"] = adelantoUnifiedAmount.Float64
	}

	httpx.WriteJSON(w, http.StatusOK, resp)
}

// loadPublicSpecialDateMenus reads the child rows for one special date and
// enriches each one with the menu's title/price when it references a real menu.
func (s *Server) loadPublicSpecialDateMenus(ctx context.Context, restaurantID int, specialDateID int64) ([]map[string]any, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT sdm.id, sdm.menu_id, sdm.custom_title, sdm.custom_image_url, sdm.adelanto_amount, sdm.price,
		       COALESCE(m.menu_title, '') AS menu_title,
		       COALESCE(m.price, 0) AS menu_price
		FROM special_date_menus sdm
		LEFT JOIN menus m
		  ON m.id = sdm.menu_id AND m.restaurant_id = sdm.restaurant_id
		  AND m.active = 1 AND m.is_draft = 0
		WHERE sdm.restaurant_id = ? AND sdm.special_date_id = ?
		ORDER BY sdm.position ASC, sdm.id ASC
	`, restaurantID, specialDateID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]map[string]any, 0, 4)
	for rows.Next() {
		var id int64
		var menuID sql.NullInt64
		var customTitle, customImageURL sql.NullString
		var adelantoAmount, customPrice, menuPrice sql.NullFloat64
		var menuTitle string
		if err := rows.Scan(&id, &menuID, &customTitle, &customImageURL, &adelantoAmount, &customPrice, &menuTitle, &menuPrice); err != nil {
			return nil, err
		}
		isCustom := !menuID.Valid
		row := map[string]any{
			"id":        id,
			"is_custom": isCustom,
		}
		if !isCustom {
			row["menu_id"] = menuID.Int64
			row["label"] = strings.TrimSpace(menuTitle)
			// Prefer the operator-defined price when present; fall back to the
			// catalogue price so old rows without a custom price keep working.
			row["price"] = menuPrice
			if customPrice.Valid {
				row["price"] = customPrice.Float64
			}
		}
		if customTitle.Valid {
			row["custom_title"] = customTitle.String
			if isCustom {
				row["label"] = customTitle.String
				// Custom uploaded menus: use the operator-defined price (0 by default).
				if customPrice.Valid {
					row["price"] = customPrice.Float64
				} else {
					row["price"] = 0.0
				}
			}
		}
		if customImageURL.Valid {
			row["custom_image_url"] = customImageURL.String
		}
		if adelantoAmount.Valid {
			row["adelanto_amount"] = adelantoAmount.Float64
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
