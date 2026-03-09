package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

func (s *Server) handleWebsiteBuilderRenderFragment(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), websiteBuilderDBTimeout)
	defer cancel()

	kind := strings.TrimSpace(strings.ToLower(chi.URLParam(r, "kind")))
	if kind == "" {
		http.Error(w, "missing fragment kind", http.StatusBadRequest)
		return
	}

	restaurantID, err := s.websiteBuilderRestaurantID(ctx, r)
	if err != nil || restaurantID <= 0 {
		http.Error(w, "website not found", http.StatusNotFound)
		return
	}

	var fragment string
	switch kind {
	case "menus":
		fragment, err = s.renderWebsiteBuilderMenus(ctx, restaurantID)
	case "wines":
		fragment, err = s.renderWebsiteBuilderWines(ctx, restaurantID)
	case "hours":
		fragment, err = s.renderWebsiteBuilderHours(ctx, restaurantID)
	default:
		http.Error(w, "unknown fragment kind", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "failed to render fragment", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(fragment))
}

func (s *Server) websiteBuilderRestaurantID(ctx context.Context, r *http.Request) (int, error) {
	if raw := strings.TrimSpace(r.URL.Query().Get("restaurant_id")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err == nil && value > 0 {
			return value, nil
		}
	}
	websiteIDRaw := strings.TrimSpace(r.URL.Query().Get("website_id"))
	websiteID, err := strconv.Atoi(websiteIDRaw)
	if err != nil || websiteID <= 0 {
		return 0, fmt.Errorf("invalid website id")
	}
	var restaurantID int
	err = s.db.QueryRowContext(ctx, `SELECT restaurant_id FROM restaurant_websites WHERE id = ? LIMIT 1`, websiteID).Scan(&restaurantID)
	if err != nil {
		return 0, err
	}
	return restaurantID, nil
}

func (s *Server) renderWebsiteBuilderMenus(ctx context.Context, restaurantID int) (string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT COALESCE(menu_title, ''), COALESCE(price, ''), COALESCE(menu_type, '')
		FROM menus
		WHERE restaurant_id = ? AND active = 1 AND is_draft = 0
		ORDER BY modified_at DESC, id DESC
		LIMIT 12
	`, restaurantID)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var out strings.Builder
	out.WriteString(`<section data-ui="website-fragment-menus"><div data-ui="website-fragment-grid">`)
	count := 0
	for rows.Next() {
		var title string
		var price string
		var menuType string
		if err := rows.Scan(&title, &price, &menuType); err != nil {
			return "", err
		}
		count++
		out.WriteString(`<article data-ui="website-menu-card">`)
		out.WriteString(`<h3 data-ui="website-menu-title">` + html.EscapeString(strings.TrimSpace(title)) + `</h3>`)
		if strings.TrimSpace(menuType) != "" {
			out.WriteString(`<p data-ui="website-menu-type">` + html.EscapeString(strings.TrimSpace(menuType)) + `</p>`)
		}
		if strings.TrimSpace(price) != "" {
			out.WriteString(`<p data-ui="website-menu-price">` + html.EscapeString(strings.TrimSpace(price)) + `</p>`)
		}
		out.WriteString(`</article>`)
	}
	if count == 0 {
		out.WriteString(`<p data-ui="website-fragment-empty">No hay menús publicados todavía.</p>`)
	}
	out.WriteString(`</div></section>`)
	return out.String(), nil
}

func (s *Server) renderWebsiteBuilderWines(ctx context.Context, restaurantID int) (string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT COALESCE(nombre, ''), COALESCE(precio, 0), COALESCE(tipo, ''), COALESCE(bodega, '')
		FROM VINOS
		WHERE restaurant_id = ? AND active = 1
		ORDER BY tipo ASC, num ASC
		LIMIT 40
	`, restaurantID)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var out strings.Builder
	out.WriteString(`<section data-ui="website-fragment-wines"><div data-ui="website-fragment-list">`)
	count := 0
	for rows.Next() {
		var name string
		var price float64
		var wineType string
		var winery string
		if err := rows.Scan(&name, &price, &wineType, &winery); err != nil {
			return "", err
		}
		count++
		out.WriteString(`<article data-ui="website-wine-card">`)
		out.WriteString(`<div data-ui="website-wine-main"><h3 data-ui="website-wine-name">` + html.EscapeString(strings.TrimSpace(name)) + `</h3>`)
		if strings.TrimSpace(winery) != "" {
			out.WriteString(`<p data-ui="website-wine-winery">` + html.EscapeString(strings.TrimSpace(winery)) + `</p>`)
		}
		if strings.TrimSpace(wineType) != "" {
			out.WriteString(`<p data-ui="website-wine-type">` + html.EscapeString(strings.TrimSpace(wineType)) + `</p>`)
		}
		out.WriteString(`</div><strong data-ui="website-wine-price">` + html.EscapeString(fmt.Sprintf("%.2f €", price)) + `</strong></article>`)
	}
	if count == 0 {
		out.WriteString(`<p data-ui="website-fragment-empty">No hay vinos activos todavía.</p>`)
	}
	out.WriteString(`</div></section>`)
	return out.String(), nil
}

func (s *Server) renderWebsiteBuilderHours(ctx context.Context, restaurantID int) (string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT dateselected, hoursarray
		FROM openinghours
		WHERE restaurant_id = ? AND dateselected >= ?
		ORDER BY dateselected ASC
		LIMIT 7
	`, restaurantID, time.Now().Format("2006-01-02"))
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var out strings.Builder
	out.WriteString(`<section data-ui="website-fragment-hours"><div data-ui="website-fragment-list">`)
	count := 0
	for rows.Next() {
		var dateISO string
		var hoursRaw sql.NullString
		if err := rows.Scan(&dateISO, &hoursRaw); err != nil {
			return "", err
		}
		count++
		hours := []string{}
		if strings.TrimSpace(hoursRaw.String) != "" {
			_ = json.Unmarshal([]byte(hoursRaw.String), &hours)
		}
		out.WriteString(`<article data-ui="website-hours-row"><h3 data-ui="website-hours-date">` + html.EscapeString(dateISO) + `</h3>`)
		if len(hours) == 0 {
			out.WriteString(`<p data-ui="website-hours-empty">Sin horas disponibles</p>`)
		} else {
			out.WriteString(`<p data-ui="website-hours-values">` + html.EscapeString(strings.Join(hours, " · ")) + `</p>`)
		}
		out.WriteString(`</article>`)
	}
	if count == 0 {
		out.WriteString(`<p data-ui="website-fragment-empty">No hay horarios cargados todavía.</p>`)
	}
	out.WriteString(`</div></section>`)
	return out.String(), nil
}
