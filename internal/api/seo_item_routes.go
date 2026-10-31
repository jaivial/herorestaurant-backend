package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"preactvillacarmen/internal/httpx"
)

// Coordination id: seo_item_routes_v1
//
// Una ruta publica por plato de carta y por vino: /platos/<slug> y
// /vinos/<slug>. El registro (seo_item_routes) guarda el slug ya generado,
// de modo que:
//   - el slug se detecta por ID de entidad: si ese plato ya tiene ruta, se
//     reutiliza la suya aunque se renombre el plato;
//   - dos platos con el mismo nombre comparten la misma pagina en lugar de
//     duplicarla con sufijos;
//   - cuando un plato se desactiva o sale de su menu, su ruta se desactiva y
//     desaparece del sitemap.
const (
	seoItemTypeDish = "dish"
	seoItemTypeWine = "wine"
)

// seoItemSource is the live data of a dish or a wine, before routing.
type seoItemSource struct {
	entityType      string
	entityID        int64
	name            string
	nameEN          string
	description     string
	descriptionEN   string
	price           *float64
	allergens       []string
	photo           string
	section         string
	menuTitle       string
	menuPath        string
	tipo            string
	bodega          string
	denominacion    string
	graduacion      *float64
	anyo            string
	supplementPrice *float64
	updatedAt       string
}

// seoItemRoute is the public shape of one routed item.
type seoItemRoute struct {
	ID                 int64    `json:"id"`
	EntityType         string   `json:"entity_type"`
	EntityID           int64    `json:"entity_id"`
	Slug               string   `json:"slug"`
	Path               string   `json:"path"`
	Name               string   `json:"name"`
	NameEN             string   `json:"name_en"`
	Description        string   `json:"description"`
	DescriptionEN      string   `json:"description_en"`
	Price              *float64 `json:"price"`
	Allergens          []string `json:"allergens"`
	Photo              string   `json:"photo"`
	Section            string   `json:"section"`
	MenuTitle          string   `json:"menu_title"`
	MenuPath           string   `json:"menu_path"`
	Tipo               string   `json:"tipo"`
	Bodega             string   `json:"bodega"`
	DenominacionOrigen string   `json:"denominacion_origen"`
	Graduacion         *float64 `json:"graduacion"`
	Anyo               string   `json:"anyo"`
	SEOTitle           string   `json:"seo_title"`
	SEODescription     string   `json:"seo_description"`
	Active             bool     `json:"active"`
	HasContent         bool     `json:"has_content"`
	UpdatedAt          string   `json:"updated_at"`
	RouteUpdatedAt     string   `json:"route_updated_at"`
}

// seoRouteRow is a row of the seo_item_routes registry.
type seoRouteRow struct {
	id             int64
	entityType     string
	entityID       int64
	slug           string
	seoTitle       string
	seoDescription string
	active         bool
	updatedAt      string
}

// slugifySeoItem turns a dish or wine name into a URL slug: lowercase, no
// accents, no punctuation, no accents in the middle of words.
func slugifySeoItem(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	slug := strings.Trim(b.String(), "-")
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}
	if len(slug) > 90 {
		slug = strings.Trim(slug[:90], "-")
	}
	if slug == "" {
		slug = "plato"
	}
	return slug
}

func seoItemPath(entityType, slug string) string {
	if entityType == seoItemTypeWine {
		return "/vinos/" + slug
	}
	return "/platos/" + slug
}

// seoMenuPath mirrors the frontend menuPath(): the SEO slug wins, otherwise
// the legacy id + slug pair.
func seoMenuPath(menuID int64, seoSlug, slug string) string {
	if seoSlug != "" {
		return "/menu/" + url.PathEscape(seoSlug)
	}
	if slug != "" {
		return "/menu/" + strconv.FormatInt(menuID, 10) + "/" + url.PathEscape(slug)
	}
	return "/menu/" + strconv.FormatInt(menuID, 10)
}

// seoDishSources loads the dishes that are active in a public carta menu.
func (s *Server) seoDishSources(ctx context.Context, restaurantID int) ([]seoItemSource, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT d.id,
		       d.title_snapshot,
		       COALESCE(d.description_snapshot, ''),
		       COALESCE(d.description_enabled, 1),
		       d.allergens_json,
		       d.foto_path,
		       d.price,
		       d.supplement_enabled,
		       d.supplement_price,
		       COALESCE(s.title, ''),
		       COALESCE(s.display_title, ''),
		       m.id,
		       COALESCE(m.menu_title, ''),
		       COALESCE(m.seo_slug, ''),
		       COALESCE(m.slug, ''),
		       COALESCE(d.updated_at, '')
		FROM group_menu_section_dishes_v2 d
		JOIN menus m ON m.id = d.menu_id AND m.restaurant_id = d.restaurant_id
		LEFT JOIN group_menu_sections_v2 s ON s.id = d.section_id AND s.restaurant_id = d.restaurant_id
		WHERE d.restaurant_id = ?
		  AND d.active = 1
		  AND m.active = 1
		  AND m.is_draft = 0
		  AND COALESCE(m.menu_public_active, 1) = 1
		  AND m.menu_type IN (1, 2)
		ORDER BY m.menu_type ASC, m.id ASC, d.position ASC, d.id ASC
	`, restaurantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	sources := make([]seoItemSource, 0, 128)
	ids := make([]int64, 0, 128)
	for rows.Next() {
		var (
			source             seoItemSource
			descriptionEnabled int
			allergensRaw       sql.NullString
			fotoPath           sql.NullString
			priceRaw           sql.NullFloat64
			supplementInt      int
			supplementRaw      sql.NullFloat64
			sectionTitle       string
			displayTitle       string
			menuID             int64
			menuSEO            string
			menuSlug           string
			dishUpdated        string
		)
		if err := rows.Scan(
			&source.entityID,
			&source.name,
			&source.description,
			&descriptionEnabled,
			&allergensRaw,
			&fotoPath,
			&priceRaw,
			&supplementInt,
			&supplementRaw,
			&sectionTitle,
			&displayTitle,
			&menuID,
			&source.menuTitle,
			&menuSEO,
			&menuSlug,
			&dishUpdated,
		); err != nil {
			return nil, err
		}
		if descriptionEnabled == 0 {
			source.description = ""
		}
		source.entityType = seoItemTypeDish
		source.updatedAt = dishUpdated
		source.allergens = anySliceToStringList(decodeJSONOrFallback(allergensRaw.String, []any{}))
		if priceRaw.Valid {
			value := priceRaw.Float64
			source.price = &value
		}
		if supplementInt != 0 && supplementRaw.Valid {
			value := supplementRaw.Float64
			source.supplementPrice = &value
		}
		if fotoPath.Valid && strings.TrimSpace(fotoPath.String) != "" {
			source.photo = s.bunnyPullURL(ctx, restaurantID, fotoPath.String)
		}
		source.section = strings.TrimSpace(displayTitle)
		if source.section == "" {
			source.section = strings.TrimSpace(sectionTitle)
		}
		source.menuPath = seoMenuPath(menuID, menuSEO, menuSlug)
		sources = append(sources, source)
		ids = append(ids, source.entityID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	translations, _ := s.loadTranslations(ctx, restaurantID, entitySectionDishes, ids, translationLang)
	for i := range sources {
		if tr, ok := translations[sources[i].entityID]; ok {
			sources[i].nameEN = translationOr(tr, "title")
			sources[i].descriptionEN = translationOr(tr, "description")
		}
	}
	return sources, nil
}

// seoWineSources loads the wines that are active in the (global) wine list.
func (s *Server) seoWineSources(ctx context.Context, restaurantID int) ([]seoItemSource, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT w.id,
		       w.nombre,
		       COALESCE(w.descripcion, ''),
		       w.precio,
		       w.foto_path,
		       COALESCE(w.tipo, ''),
		       COALESCE(w.bodega, ''),
		       COALESCE(w.denominacion_origen, ''),
		       w.graduacion,
		       COALESCE(w.anyo, ''),
		       COALESCE(l.legacy_num, 0),
		       COALESCE(w.updated_at, '')
		FROM group_menu_wines_v2 w
		LEFT JOIN legacy_wine_map l ON l.restaurant_id = w.restaurant_id AND l.v2_wine_id = w.id
		WHERE w.restaurant_id = ? AND w.active = 1
		ORDER BY w.tipo ASC, w.position ASC, w.id ASC
	`, restaurantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	sources := make([]seoItemSource, 0, 64)
	legacyIDs := make([]int64, 0, 64)
	legacyByEntity := make(map[int64]int64, 64)
	for rows.Next() {
		var (
			source     seoItemSource
			priceRaw   sql.NullFloat64
			fotoPath   sql.NullString
			graduation sql.NullFloat64
			legacyNum  int64
			updatedAt  string
		)
		if err := rows.Scan(
			&source.entityID,
			&source.name,
			&source.description,
			&priceRaw,
			&fotoPath,
			&source.tipo,
			&source.bodega,
			&source.denominacion,
			&graduation,
			&source.anyo,
			&legacyNum,
			&updatedAt,
		); err != nil {
			return nil, err
		}
		source.entityType = seoItemTypeWine
		source.updatedAt = updatedAt
		if priceRaw.Valid {
			value := priceRaw.Float64
			source.price = &value
		}
		if graduation.Valid {
			value := graduation.Float64
			source.graduacion = &value
		}
		if fotoPath.Valid && strings.TrimSpace(fotoPath.String) != "" {
			source.photo = s.bunnyPullURL(ctx, restaurantID, fotoPath.String)
		}
		sources = append(sources, source)
		if legacyNum > 0 {
			legacyIDs = append(legacyIDs, legacyNum)
			legacyByEntity[source.entityID] = legacyNum
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	translations, _ := s.loadTranslations(ctx, restaurantID, entityVinos, legacyIDs, translationLang)
	for i := range sources {
		legacyNum, ok := legacyByEntity[sources[i].entityID]
		if !ok {
			continue
		}
		tr, ok := translations[legacyNum]
		if !ok {
			continue
		}
		if name := translationOr(tr, "nombre"); name != "" {
			sources[i].nameEN = name
		}
		if description := translationOr(tr, "descripcion"); description != "" {
			sources[i].descriptionEN = description
		}
		if bodega := translationOr(tr, "bodega"); bodega != "" {
			sources[i].bodega = bodega
		}
		if denominacion := translationOr(tr, "denominacion_origen"); denominacion != "" {
			sources[i].denominacion = denominacion
		}
	}
	return sources, nil
}

// loadSeoRouteRows reads the registry of routes for a restaurant.
func (s *Server) loadSeoRouteRows(ctx context.Context, restaurantID int) ([]seoRouteRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, entity_type, entity_id, slug, COALESCE(seo_title, ''), COALESCE(seo_description, ''), active, COALESCE(updated_at, '')
		FROM seo_item_routes
		WHERE restaurant_id = ?
		ORDER BY id ASC
	`, restaurantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]seoRouteRow, 0, 256)
	for rows.Next() {
		var row seoRouteRow
		var active int
		if err := rows.Scan(&row.id, &row.entityType, &row.entityID, &row.slug, &row.seoTitle, &row.seoDescription, &active, &row.updatedAt); err != nil {
			return nil, err
		}
		row.active = active != 0
		out = append(out, row)
	}
	return out, rows.Err()
}

// seoItemRoutes computes the routes of every active dish and wine, keeping the
// slug of the ones that already have a route and creating the missing ones.
// It returns the routes, the rows that must be inserted and the entity keys
// of the routes that stay published.
func seoItemRoutes(existing []seoRouteRow, sources []seoItemSource) (routes []seoItemRoute, inserts []seoRouteRow, usedKeys []string) {
	byID := make(map[string]seoRouteRow, len(existing))
	bySlug := make(map[string]seoRouteRow, len(existing))
	used := make(map[string]bool, len(existing))
	for _, row := range existing {
		byID[seoRouteKey(row.entityType, row.entityID)] = row
		bySlug[seoSlugKey(row.entityType, row.slug)] = row
		used[seoSlugKey(row.entityType, row.slug)] = true
	}

	for _, source := range sources {
		key := seoRouteKey(source.entityType, source.entityID)
		row, found := byID[key]
		if !found {
			slug := slugifySeoItem(source.name)
			if existingRow, clash := bySlug[seoSlugKey(source.entityType, slug)]; clash {
				// Same name, another entity (the same dish in two menus): one page.
				row = existingRow
			} else {
				candidate := slug
				for suffix := 2; used[seoSlugKey(source.entityType, candidate)]; suffix++ {
					candidate = slug + "-" + strconv.Itoa(suffix)
				}
				row = seoRouteRow{
					entityType: source.entityType,
					entityID:   source.entityID,
					slug:       candidate,
					active:     true,
				}
				used[seoSlugKey(source.entityType, candidate)] = true
				bySlug[seoSlugKey(source.entityType, candidate)] = row
				inserts = append(inserts, row)
			}
		}
		usedKeys = append(usedKeys, seoRouteKey(row.entityType, row.entityID))
		routes = append(routes, seoItemRoute{
			ID:                 row.id,
			EntityType:         source.entityType,
			EntityID:           source.entityID,
			Slug:               row.slug,
			Path:               seoItemPath(source.entityType, row.slug),
			Name:               source.name,
			NameEN:             source.nameEN,
			Description:        source.description,
			DescriptionEN:      source.descriptionEN,
			Price:              source.price,
			Allergens:          source.allergens,
			Photo:              source.photo,
			Section:            source.section,
			MenuTitle:          source.menuTitle,
			MenuPath:           source.menuPath,
			Tipo:               source.tipo,
			Bodega:             source.bodega,
			DenominacionOrigen: source.denominacion,
			Graduacion:         source.graduacion,
			Anyo:               source.anyo,
			SEOTitle:           row.seoTitle,
			SEODescription:     row.seoDescription,
			Active:             true,
			HasContent:         strings.TrimSpace(source.description) != "" || strings.TrimSpace(source.photo) != "",
			UpdatedAt:          source.updatedAt,
			RouteUpdatedAt:     row.updatedAt,
		})
	}
	return routes, inserts, usedKeys
}

func seoRouteKey(entityType string, entityID int64) string {
	return entityType + "/" + strconv.FormatInt(entityID, 10)
}

func seoSlugKey(entityType, slug string) string {
	return entityType + "/" + slug
}

// persistSeoItemRoutes writes the new routes, publishes the ones that are
// still in the carta and unpublishes the ones that are gone.
func (s *Server) persistSeoItemRoutes(ctx context.Context, restaurantID int, inserts []seoRouteRow, usedKeys []string) error {
	for _, row := range inserts {
		if _, err := s.db.ExecContext(ctx, `
			INSERT INTO seo_item_routes (restaurant_id, entity_type, entity_id, slug, active)
			VALUES (?, ?, ?, ?, 1)
			ON DUPLICATE KEY UPDATE active = 1
		`, restaurantID, row.entityType, row.entityID, row.slug); err != nil {
			return err
		}
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(usedKeys)), ",")
	args := make([]any, 0, len(usedKeys)+1)
	args = append(args, restaurantID)
	for _, key := range usedKeys {
		args = append(args, key)
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE seo_item_routes SET active = 1
		WHERE restaurant_id = ? AND CONCAT(entity_type, '/', entity_id) IN (`+placeholders+`)
	`, args...); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE seo_item_routes SET active = 0
		WHERE restaurant_id = ? AND active = 1 AND CONCAT(entity_type, '/', entity_id) NOT IN (`+placeholders+`)
	`, args...); err != nil {
		return err
	}
	return nil
}

// seoItemRoutesPayload syncs the registry and returns the public routes.
func (s *Server) seoItemRoutesPayload(ctx context.Context, restaurantID int) ([]seoItemRoute, error) {
	sources, err := s.seoDishSources(ctx, restaurantID)
	if err != nil {
		return nil, err
	}
	wines, err := s.seoWineSources(ctx, restaurantID)
	if err != nil {
		return nil, err
	}
	sources = append(sources, wines...)

	existing, err := s.loadSeoRouteRows(ctx, restaurantID)
	if err != nil {
		return nil, err
	}
	routes, inserts, usedKeys := seoItemRoutes(existing, sources)
	if err := s.persistSeoItemRoutes(ctx, restaurantID, inserts, usedKeys); err != nil {
		return nil, err
	}
	return routes, nil
}

// handlePublicSeoItemRoutes serves the routes the frontend prerenders.
func (s *Server) handlePublicSeoItemRoutes(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Missing restaurant"})
		return
	}
	routes, err := s.seoItemRoutesPayload(r.Context(), restaurantID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Error reading item routes"})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "items": routes})
}

// handleBOSeoItemRoutesList lists every route, including the inactive ones.
func (s *Server) handleBOSeoItemRoutesList(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Missing restaurant"})
		return
	}
	routes, err := s.seoItemRoutesPayload(r.Context(), restaurantID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Error reading item routes"})
		return
	}
	rows, err := s.loadSeoRouteRows(r.Context(), restaurantID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Error reading item routes"})
		return
	}
	inactive := make([]map[string]any, 0)
	activeIDs := make(map[int64]bool, len(routes))
	for _, route := range routes {
		activeIDs[route.ID] = true
	}
	for _, row := range rows {
		if activeIDs[row.id] {
			continue
		}
		inactive = append(inactive, map[string]any{
			"id":          row.id,
			"entity_type": row.entityType,
			"entity_id":   row.entityID,
			"slug":        row.slug,
			"path":        seoItemPath(row.entityType, row.slug),
			"active":      false,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "items": routes, "inactive": inactive})
}

// handleBOSeoItemRoutesPatch edits the SEO fields of one route.
func (s *Server) handleBOSeoItemRoutesPatch(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Missing restaurant"})
		return
	}
	routeID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || routeID <= 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid route id"})
		return
	}
	var body struct {
		SEOTitle       *string `json:"seo_title"`
		SEODescription *string `json:"seo_description"`
		Active         *bool   `json:"active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid body"})
		return
	}
	sets := []string{}
	args := []any{}
	if body.SEOTitle != nil {
		sets = append(sets, "seo_title = ?")
		args = append(args, strings.TrimSpace(*body.SEOTitle))
	}
	if body.SEODescription != nil {
		sets = append(sets, "seo_description = ?")
		args = append(args, strings.TrimSpace(*body.SEODescription))
	}
	if body.Active != nil {
		active := 0
		if *body.Active {
			active = 1
		}
		sets = append(sets, "active = ?")
		args = append(args, active)
	}
	if len(sets) == 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Nothing to update"})
		return
	}
	args = append(args, restaurantID, routeID)
	if _, err := s.db.ExecContext(r.Context(), `
		UPDATE seo_item_routes SET `+strings.Join(sets, ", ")+` WHERE restaurant_id = ? AND id = ?
	`, args...); err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Error updating route"})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

// handleBOSeoItemRoutesSync forces a rebuild of the registry.
func (s *Server) handleBOSeoItemRoutesSync(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Missing restaurant"})
		return
	}
	routes, err := s.seoItemRoutesPayload(r.Context(), restaurantID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Error syncing item routes"})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "items": len(routes)})
}

// syncSeoItemRoutes is the hook called by the backoffice after a dish or a
// wine changes, so the route follows the carta without waiting for a build.
func (s *Server) syncSeoItemRoutes(ctx context.Context, restaurantID int) {
	if restaurantID <= 0 {
		return
	}
	if _, err := s.seoItemRoutesPayload(ctx, restaurantID); err != nil {
		log.Printf("[seo_item_routes] sync failed for restaurant %d: %v", restaurantID, err)
	}
}
