package api

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"preactvillacarmen/internal/httpx"
)

// Unified comida category catalogue.
//
// Categories used to exist only for platos and bebidas, in two separate tables
// (comida_plato_categories, comida_bebida_categories). comida_items.category_id has a
// FK into the platos one, so both legacy tables stay exactly as they are. The new
// comida_categories table is additive: it covers the food types that never had a
// catalogue (vinos, cafes, postres) and adds global categories shared by every type.
//
// Reads for platos and bebidas therefore merge three sources: the legacy table, the
// type-scoped rows and the global rows.

// comidaCategoryGlobalType is the sentinel stored in comida_categories.food_type for a
// category that belongs to every food type. An empty string is used instead of NULL
// because MySQL treats NULLs as distinct inside a UNIQUE index, which would let
// duplicate global categories through.
const comidaCategoryGlobalType = ""

// comidaCategoryFoodTypes lists the food types a category may be scoped to.
var comidaCategoryFoodTypes = []string{"platos", "bebidas", "vinos", "cafes", "postres"}

// comidaCategoryLegacyTables maps a food type to the pre-existing table holding its
// categories. Only platos and bebidas have one; the other types read exclusively from
// comida_categories.
var comidaCategoryLegacyTables = map[string]string{
	"platos":  "comida_plato_categories",
	"bebidas": "comida_bebida_categories",
}

// comidaUnifiedCategoryResponse is the wire shape of a catalogue entry.
//
// Scope is "global" or the food type the category is restricted to. Origin tells the
// client which table the row lives in, because a legacy id and a comida_categories id
// can collide: they are independent auto-increment sequences.
type comidaUnifiedCategoryResponse struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Slug     string `json:"slug"`
	FoodType string `json:"foodType"`
	Scope    string `json:"scope"`
	IsGlobal bool   `json:"isGlobal"`
	Origin   string `json:"origin"`
	Active   bool   `json:"active"`
}

type comidaUnifiedCategoryWriteRequest struct {
	Name     *string `json:"name"`
	FoodType *string `json:"foodType"`
	Global   *bool   `json:"global"`
	Active   *bool   `json:"active"`
}

// normalizeComidaCategoryFoodType validates a food type coming from the wire. An empty
// value means global. The bool reports whether the value was recognised.
func normalizeComidaCategoryFoodType(raw string) (string, bool) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" || v == "global" || v == "all" {
		return comidaCategoryGlobalType, true
	}
	for _, allowed := range comidaCategoryFoodTypes {
		if v == allowed {
			return v, true
		}
	}
	return "", false
}

func comidaCategoryScope(foodType string) string {
	if foodType == comidaCategoryGlobalType {
		return "global"
	}
	return foodType
}

// scanComidaUnifiedCategories reads rows shaped as (id, name, slug, food_type, active).
func scanComidaUnifiedCategories(rows *sql.Rows, origin string) ([]comidaUnifiedCategoryResponse, error) {
	defer rows.Close()
	out := make([]comidaUnifiedCategoryResponse, 0, 8)
	for rows.Next() {
		var (
			c         comidaUnifiedCategoryResponse
			activeInt int
		)
		if err := rows.Scan(&c.ID, &c.Name, &c.Slug, &c.FoodType, &activeInt); err != nil {
			return nil, err
		}
		c.Active = activeInt != 0
		c.Origin = origin
		c.IsGlobal = c.FoodType == comidaCategoryGlobalType
		c.Scope = comidaCategoryScope(c.FoodType)
		out = append(out, c)
	}
	return out, rows.Err()
}

// handleBOComidaCategoriesList returns the catalogue for one food type: its own rows,
// the global rows, and — for platos and bebidas — the legacy rows too.
//
// GET /admin/comida/categorias?foodType=cafes
//
// Without foodType the whole catalogue for the restaurant is returned, which is what the
// config screen uses.
func (s *Server) handleBOComidaCategoriesList(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := comidaRestaurantIDFromRequest(r)
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	rawType := strings.TrimSpace(r.URL.Query().Get("foodType"))
	if rawType == "" {
		rawType = strings.TrimSpace(r.URL.Query().Get("tipo"))
	}
	scoped := rawType != ""
	foodType, valid := normalizeComidaCategoryFoodType(rawType)
	if !valid {
		writeComidaValidationError(w, "Tipo de comida invalido")
		return
	}

	out := make([]comidaUnifiedCategoryResponse, 0, 16)

	if scoped && foodType != comidaCategoryGlobalType {
		if legacy, err := s.listLegacyComidaCategories(r, restaurantID, foodType); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error cargando categorias")
			return
		} else {
			out = append(out, legacy...)
		}
	}

	var (
		rows *sql.Rows
		err  error
	)
	if scoped {
		// Type-scoped request: the type's own rows plus every global row.
		rows, err = s.db.QueryContext(r.Context(), `
			SELECT id, COALESCE(name, ''), COALESCE(slug, ''), COALESCE(food_type, ''), active
			FROM comida_categories
			WHERE restaurant_id = ? AND active = 1 AND (food_type = ? OR food_type = '')
			ORDER BY name ASC
		`, restaurantID, foodType)
	} else {
		rows, err = s.db.QueryContext(r.Context(), `
			SELECT id, COALESCE(name, ''), COALESCE(slug, ''), COALESCE(food_type, ''), active
			FROM comida_categories
			WHERE restaurant_id = ?
			ORDER BY food_type ASC, name ASC
		`, restaurantID)
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error cargando categorias")
		return
	}
	unified, err := scanComidaUnifiedCategories(rows, "comida_categories")
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo categorias")
		return
	}
	out = append(out, unified...)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":    true,
		"categories": out,
	})
}

// listLegacyComidaCategories reads the pre-existing per-type tables so platos and
// bebidas keep seeing everything they saw before this catalogue existed.
func (s *Server) listLegacyComidaCategories(r *http.Request, restaurantID int, foodType string) ([]comidaUnifiedCategoryResponse, error) {
	table, ok := comidaCategoryLegacyTables[foodType]
	if !ok {
		return nil, nil
	}

	switch foodType {
	case "platos":
		if err := s.ensureBasePlatoCategories(r, restaurantID); err != nil {
			return nil, err
		}
	case "bebidas":
		if err := s.ensureBaseBebidaCategories(r, restaurantID); err != nil {
			return nil, err
		}
	}

	// #nosec G202 -- table is resolved from comidaCategoryLegacyTables, never from input.
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id, COALESCE(name, ''), COALESCE(slug, ''), ? AS food_type, active
		FROM `+table+`
		WHERE restaurant_id = ? AND active = 1
		ORDER BY
			CASE WHEN source = 'base' THEN 0 ELSE 1 END,
			name ASC
	`, foodType, restaurantID)
	if err != nil {
		return nil, err
	}
	return scanComidaUnifiedCategories(rows, table)
}

// handleBOComidaCategoryCreate adds a category to the unified catalogue.
//
// POST /admin/comida/categorias  {name, foodType|null, global?}
func (s *Server) handleBOComidaCategoryCreate(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := comidaRestaurantIDFromRequest(r)
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req comidaUnifiedCategoryWriteRequest
	if err := readJSONBody(r, &req); err != nil {
		writeComidaValidationError(w, "Invalid JSON")
		return
	}

	name := ""
	if req.Name != nil {
		name = strings.TrimSpace(*req.Name)
	}
	if name == "" {
		writeComidaValidationError(w, "Nombre de categoria requerido")
		return
	}

	rawType := ""
	if req.FoodType != nil {
		rawType = *req.FoodType
	}
	// An explicit global flag wins over foodType, so the client cannot send a
	// contradictory pair and get a type-scoped row when it asked for a global one.
	if req.Global != nil && *req.Global {
		rawType = ""
	}
	foodType, valid := normalizeComidaCategoryFoodType(rawType)
	if !valid {
		writeComidaValidationError(w, "Tipo de comida invalido")
		return
	}

	slug := slugifyCategoryName(name)

	res, err := s.db.ExecContext(r.Context(), `
		INSERT INTO comida_categories (restaurant_id, food_type, name, slug, active)
		VALUES (?, ?, ?, ?, 1)
		ON DUPLICATE KEY UPDATE
			name = VALUES(name),
			active = 1
	`, restaurantID, foodType, name, slug)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creando categoria")
		return
	}

	category := comidaUnifiedCategoryResponse{
		Name:     name,
		Slug:     slug,
		FoodType: foodType,
		Scope:    comidaCategoryScope(foodType),
		IsGlobal: foodType == comidaCategoryGlobalType,
		Origin:   "comida_categories",
		Active:   true,
	}
	if id64, _ := res.LastInsertId(); id64 > 0 {
		category.ID = int(id64)
	} else {
		// The row already existed and was reactivated, so LastInsertId is unusable.
		if err := s.db.QueryRowContext(r.Context(), `
			SELECT id FROM comida_categories
			WHERE restaurant_id = ? AND food_type = ? AND slug = ?
			LIMIT 1
		`, restaurantID, foodType, slug).Scan(&category.ID); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error creando categoria")
			return
		}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"category": category,
	})
}

// handleBOComidaCategoryPatch renames a category, moves it between scopes or toggles it.
//
// PATCH /admin/comida/categorias/{id}  {name?, foodType?, global?, active?}
func (s *Server) handleBOComidaCategoryPatch(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := comidaRestaurantIDFromRequest(r)
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	id, err := strconv.Atoi(strings.TrimSpace(chi.URLParam(r, "id")))
	if err != nil || id <= 0 {
		writeComidaValidationError(w, "Identificador de categoria invalido")
		return
	}

	var req comidaUnifiedCategoryWriteRequest
	if err := readJSONBody(r, &req); err != nil {
		writeComidaValidationError(w, "Invalid JSON")
		return
	}

	current, found, err := s.getComidaUnifiedCategory(r, restaurantID, id)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error cargando categoria")
		return
	}
	if !found {
		httpx.WriteError(w, http.StatusNotFound, "Categoria no encontrada")
		return
	}

	next := current
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			writeComidaValidationError(w, "Nombre de categoria requerido")
			return
		}
		next.Name = name
		next.Slug = slugifyCategoryName(name)
	}
	if req.Global != nil {
		if *req.Global {
			next.FoodType = comidaCategoryGlobalType
		} else if req.FoodType == nil {
			writeComidaValidationError(w, "Tipo de comida requerido")
			return
		}
	}
	if req.FoodType != nil && (req.Global == nil || !*req.Global) {
		foodType, valid := normalizeComidaCategoryFoodType(*req.FoodType)
		if !valid {
			writeComidaValidationError(w, "Tipo de comida invalido")
			return
		}
		next.FoodType = foodType
	}
	if req.Active != nil {
		next.Active = *req.Active
	}

	activeInt := 0
	if next.Active {
		activeInt = 1
	}
	if _, err := s.db.ExecContext(r.Context(), `
		UPDATE comida_categories
		SET name = ?, slug = ?, food_type = ?, active = ?
		WHERE restaurant_id = ? AND id = ?
	`, next.Name, next.Slug, next.FoodType, activeInt, restaurantID, id); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando categoria")
		return
	}

	next.Scope = comidaCategoryScope(next.FoodType)
	next.IsGlobal = next.FoodType == comidaCategoryGlobalType

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"category": next,
	})
}

// handleBOComidaCategoryDelete removes a category, refusing when products still
// reference it by name.
//
// DELETE /admin/comida/categorias/{id}
func (s *Server) handleBOComidaCategoryDelete(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := comidaRestaurantIDFromRequest(r)
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	id, err := strconv.Atoi(strings.TrimSpace(chi.URLParam(r, "id")))
	if err != nil || id <= 0 {
		writeComidaValidationError(w, "Identificador de categoria invalido")
		return
	}

	current, found, err := s.getComidaUnifiedCategory(r, restaurantID, id)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error cargando categoria")
		return
	}
	if !found {
		httpx.WriteError(w, http.StatusNotFound, "Categoria no encontrada")
		return
	}

	// comida_items.categoria is a plain VARCHAR shared by platos, bebidas and cafes, so
	// usage is checked by name rather than by id.
	var inUse int
	if err := s.db.QueryRowContext(r.Context(), `
		SELECT COUNT(*)
		FROM comida_items
		WHERE restaurant_id = ? AND LOWER(COALESCE(categoria, '')) = LOWER(?)
	`, restaurantID, current.Name).Scan(&inUse); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error verificando categoria")
		return
	}
	if inUse > 0 {
		httpx.WriteError(w, http.StatusConflict, "La categoria esta en uso")
		return
	}

	if _, err := s.db.ExecContext(r.Context(), `
		DELETE FROM comida_categories WHERE restaurant_id = ? AND id = ?
	`, restaurantID, id); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error eliminando categoria")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) getComidaUnifiedCategory(r *http.Request, restaurantID, id int) (comidaUnifiedCategoryResponse, bool, error) {
	var (
		c         comidaUnifiedCategoryResponse
		activeInt int
	)
	err := s.db.QueryRowContext(r.Context(), `
		SELECT id, COALESCE(name, ''), COALESCE(slug, ''), COALESCE(food_type, ''), active
		FROM comida_categories
		WHERE restaurant_id = ? AND id = ?
		LIMIT 1
	`, restaurantID, id).Scan(&c.ID, &c.Name, &c.Slug, &c.FoodType, &activeInt)
	if err == sql.ErrNoRows {
		return comidaUnifiedCategoryResponse{}, false, nil
	}
	if err != nil {
		return comidaUnifiedCategoryResponse{}, false, err
	}
	c.Active = activeInt != 0
	c.Origin = "comida_categories"
	c.IsGlobal = c.FoodType == comidaCategoryGlobalType
	c.Scope = comidaCategoryScope(c.FoodType)
	return c, true, nil
}
