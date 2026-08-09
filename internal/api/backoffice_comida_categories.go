package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-sql-driver/mysql"

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
// type-scoped rows and the global rows. Writes only ever touch comida_categories.

// comidaCategoryGlobalType is the sentinel stored in comida_categories.food_type for a
// category that belongs to every food type. An empty string is used instead of NULL
// because MySQL treats NULLs as distinct inside a UNIQUE index, which would let
// duplicate global categories through.
const comidaCategoryGlobalType = ""

// Column widths of comida_categories. Longer values would be rejected by MySQL in
// strict mode with a 1406, which would surface as a 500 instead of a validation error.
const (
	comidaCategoryNameMaxLen = 120
	comidaCategorySlugMaxLen = 160
)

// comidaCategoryMaxBodyBytes bounds the request body of the write endpoints.
const comidaCategoryMaxBodyBytes = 1 << 20

// comidaCategoryFoodTypes lists the food types a category may be scoped to.
var comidaCategoryFoodTypes = []string{"platos", "bebidas", "vinos", "cafes", "postres"}

// comidaCategoryLegacyTables maps a food type to the pre-existing table holding its
// categories. Only platos and bebidas have one; the other types read exclusively from
// comida_categories.
var comidaCategoryLegacyTables = map[string]string{
	"platos":  "comida_plato_categories",
	"bebidas": "comida_bebida_categories",
}

// Origins reported on the wire. Deliberately not the table names: the client should
// only need to know whether a row is writable through these endpoints.
const (
	comidaCategoryOriginUnified = "unified"
	comidaCategoryOriginLegacy  = "legacy"
)

// comidaUnifiedCategoryResponse is the wire shape of a catalogue entry.
//
// Only rows from comida_categories are addressable by the write endpoints, so legacy
// rows report id 0 and editable false. Their auto-increment sequences are independent
// and their ids collide, so exposing a legacy id as if it were writable would let a
// client rename or delete an unrelated row that happens to share the number. Key is
// the stable identifier a client should use to tell two entries apart.
type comidaUnifiedCategoryResponse struct {
	ID       int    `json:"id"`
	Key      string `json:"key"`
	Name     string `json:"name"`
	Slug     string `json:"slug"`
	FoodType string `json:"foodType"`
	Scope    string `json:"scope"`
	IsGlobal bool   `json:"isGlobal"`
	Origin   string `json:"origin"`
	Editable bool   `json:"editable"`
	Active   bool   `json:"active"`
}

type comidaUnifiedCategoryWriteRequest struct {
	Name     *string `json:"name"`
	FoodType *string `json:"foodType"`
	Global   *bool   `json:"global"`
	Active   *bool   `json:"active"`
}

// normalizeComidaCategoryFoodType validates a food type coming from the wire. An empty
// value, or the explicit "global", means the global scope. Singular and accented forms
// are accepted through normalizeComidaTipo so this endpoint speaks the same food-type
// vocabulary as the rest of the comida module. The bool reports whether the value was
// recognised.
func normalizeComidaCategoryFoodType(raw string) (string, bool) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" || v == "global" {
		return comidaCategoryGlobalType, true
	}
	t, ok := normalizeComidaTipo(v)
	if !ok {
		return "", false
	}
	return string(t), true
}

func comidaCategoryScope(foodType string) string {
	if foodType == comidaCategoryGlobalType {
		return "global"
	}
	return foodType
}

// comidaCategoryKey identifies a catalogue entry across the three tables it can come
// from. Legacy ids collide with unified ids, so the table has to be part of the key.
func comidaCategoryKey(origin string, foodType string, id int) string {
	if origin == comidaCategoryOriginUnified {
		return fmt.Sprintf("%s:%d", comidaCategoryOriginUnified, id)
	}
	return fmt.Sprintf("%s:%s:%d", comidaCategoryOriginLegacy, comidaCategoryScope(foodType), id)
}

// isDuplicateKeyErr reports whether err is a MySQL unique-constraint violation.
func isDuplicateKeyErr(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}

// validateComidaCategoryName trims and bounds a category name, returning the name and
// its slug.
func validateComidaCategoryName(raw string) (string, string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", "", errors.New("Nombre de categoria requerido")
	}
	if len([]rune(name)) > comidaCategoryNameMaxLen {
		return "", "", fmt.Errorf("El nombre no puede superar %d caracteres", comidaCategoryNameMaxLen)
	}
	slug := slugifyCategoryName(name)
	if len(slug) > comidaCategorySlugMaxLen {
		slug = slug[:comidaCategorySlugMaxLen]
	}
	return name, slug, nil
}

func readComidaCategoryBody(w http.ResponseWriter, r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, comidaCategoryMaxBodyBytes))
	return dec.Decode(dst)
}

// comidaCategoryIDFromRoute parses the {id} path parameter.
func comidaCategoryIDFromRoute(r *http.Request) (int, bool) {
	id, err := strconv.Atoi(strings.TrimSpace(chi.URLParam(r, "id")))
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// scanComidaUnifiedCategories reads rows shaped as (id, name, slug, active) and stamps
// them with the scope and origin the caller already knows.
func scanComidaUnifiedCategories(rows *sql.Rows, origin, foodType string) ([]comidaUnifiedCategoryResponse, error) {
	defer rows.Close()
	out := make([]comidaUnifiedCategoryResponse, 0, 8)
	for rows.Next() {
		var (
			c         comidaUnifiedCategoryResponse
			rowType   string
			activeInt int
		)
		if err := rows.Scan(&c.ID, &c.Name, &c.Slug, &rowType, &activeInt); err != nil {
			return nil, err
		}
		if origin == comidaCategoryOriginLegacy {
			rowType = foodType
		}
		c.Active = activeInt != 0
		c.FoodType = rowType
		c.Origin = origin
		c.Editable = origin == comidaCategoryOriginUnified
		c.IsGlobal = rowType == comidaCategoryGlobalType
		c.Scope = comidaCategoryScope(rowType)
		c.Key = comidaCategoryKey(origin, rowType, c.ID)
		if origin == comidaCategoryOriginLegacy {
			// Legacy ids are not addressable by the write endpoints and collide with
			// unified ids, so they are not exposed as if they were.
			c.ID = 0
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// dedupeComidaCategories collapses entries that would render as the same option and
// sorts the result by name.
//
// The same name can legitimately arrive three times for platos: the seeded legacy row,
// a type-scoped row and a global one. The UNIQUE index cannot prevent that, it spans
// neither tables nor the global sentinel. Editable entries win so the config screen can
// still manage what it owns; between two editable entries the type-scoped one wins,
// being the more specific.
func dedupeComidaCategories(in []comidaUnifiedCategoryResponse) []comidaUnifiedCategoryResponse {
	best := make(map[string]comidaUnifiedCategoryResponse, len(in))
	order := make([]string, 0, len(in))
	for _, c := range in {
		k := strings.ToLower(c.Slug)
		prev, seen := best[k]
		if !seen {
			best[k] = c
			order = append(order, k)
			continue
		}
		if comidaCategoryOutranks(c, prev) {
			best[k] = c
		}
	}
	out := make([]comidaUnifiedCategoryResponse, 0, len(order))
	for _, k := range order {
		out = append(out, best[k])
	}
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

func comidaCategoryOutranks(candidate, current comidaUnifiedCategoryResponse) bool {
	if candidate.Editable != current.Editable {
		return candidate.Editable
	}
	if candidate.IsGlobal != current.IsGlobal {
		return !candidate.IsGlobal
	}
	return false
}

// handleBOComidaCategoriesList returns the catalogue for one food type: its own rows,
// the global rows, and — for platos and bebidas — the legacy rows too.
//
// GET /admin/comida/categorias?foodType=cafes
//
// Without foodType the whole catalogue for the restaurant is returned, inactive rows
// included, which is what the config screen needs in order to reactivate them.
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
		legacy, err := s.listLegacyComidaCategories(r, restaurantID, foodType)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error cargando categorias")
			return
		}
		out = append(out, legacy...)
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
	unified, err := scanComidaUnifiedCategories(rows, comidaCategoryOriginUnified, "")
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo categorias")
		return
	}
	out = append(out, unified...)

	if scoped {
		// The unscoped listing is the management view: it must show every row it can
		// act on, duplicates included, or a duplicate would become impossible to
		// delete. Only the picker view collapses them.
		out = dedupeComidaCategories(out)
	}

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
		SELECT id, COALESCE(name, ''), COALESCE(slug, ''), COALESCE(source, 'custom'), active
		FROM `+table+`
		WHERE restaurant_id = ? AND active = 1
		ORDER BY
			CASE WHEN source = 'base' THEN 0 ELSE 1 END,
			name ASC
	`, restaurantID)
	if err != nil {
		return nil, err
	}
	return scanComidaUnifiedCategories(rows, comidaCategoryOriginLegacy, foodType)
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
	if err := readComidaCategoryBody(w, r, &req); err != nil {
		writeComidaValidationError(w, "Invalid JSON")
		return
	}

	raw := ""
	if req.Name != nil {
		raw = *req.Name
	}
	name, slug, err := validateComidaCategoryName(raw)
	if err != nil {
		writeComidaValidationError(w, err.Error())
		return
	}

	foodType, ok := resolveComidaCategoryScope(req)
	if !ok {
		writeComidaValidationError(w, "Tipo de comida invalido")
		return
	}

	// Only `active` is touched on conflict. Rewriting `name` here would silently
	// rename an existing category — comida_items.categoria links products by name, so
	// every product using the old spelling would be orphaned.
	if _, err := s.db.ExecContext(r.Context(), `
		INSERT INTO comida_categories (restaurant_id, food_type, name, slug, active)
		VALUES (?, ?, ?, ?, 1)
		ON DUPLICATE KEY UPDATE active = 1
	`, restaurantID, foodType, name, slug); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creando categoria")
		return
	}

	// LAST_INSERT_ID() is documented as not meaningful when ON DUPLICATE KEY UPDATE
	// updates instead of inserting, and the id is what the client will PATCH and
	// DELETE with, so it is always read back rather than inferred.
	category, found, err := s.findComidaUnifiedCategoryBySlug(r, restaurantID, foodType, slug)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creando categoria")
		return
	}
	if !found {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creando categoria")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"category": category,
	})
}

// resolveComidaCategoryScope decides the scope of a write request.
//
// `global` is authoritative when present, so a caller cannot send a contradictory pair
// and get the opposite of what it asked for. `global:false` requires an explicit,
// non-global foodType: silently falling back to the global sentinel would produce the
// exact scope the caller ruled out.
func resolveComidaCategoryScope(req comidaUnifiedCategoryWriteRequest) (string, bool) {
	rawType := ""
	if req.FoodType != nil {
		rawType = *req.FoodType
	}
	foodType, valid := normalizeComidaCategoryFoodType(rawType)
	if !valid {
		return "", false
	}
	if req.Global == nil {
		return foodType, true
	}
	if *req.Global {
		return comidaCategoryGlobalType, true
	}
	if foodType == comidaCategoryGlobalType {
		return "", false
	}
	return foodType, true
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

	id, ok := comidaCategoryIDFromRoute(r)
	if !ok {
		httpx.WriteError(w, http.StatusBadRequest, "Identificador de categoria invalido")
		return
	}

	var req comidaUnifiedCategoryWriteRequest
	if err := readComidaCategoryBody(w, r, &req); err != nil {
		writeComidaValidationError(w, "Invalid JSON")
		return
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando categoria")
		return
	}
	defer func() { _ = tx.Rollback() }()

	// Locked for update: the read-modify-write below would otherwise lose a concurrent
	// rename, and the cascade needs the old name to still be the one on disk.
	var (
		current   comidaUnifiedCategoryResponse
		activeInt int
	)
	err = tx.QueryRowContext(r.Context(), `
		SELECT id, COALESCE(name, ''), COALESCE(slug, ''), COALESCE(food_type, ''), active
		FROM comida_categories
		WHERE restaurant_id = ? AND id = ?
		FOR UPDATE
	`, restaurantID, id).Scan(&current.ID, &current.Name, &current.Slug, &current.FoodType, &activeInt)
	if errors.Is(err, sql.ErrNoRows) {
		httpx.WriteError(w, http.StatusNotFound, "Categoria no encontrada")
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error cargando categoria")
		return
	}
	current.Active = activeInt != 0

	next := current
	if req.Name != nil {
		name, slug, err := validateComidaCategoryName(*req.Name)
		if err != nil {
			writeComidaValidationError(w, err.Error())
			return
		}
		next.Name = name
		next.Slug = slug
	}
	if req.Global != nil || req.FoodType != nil {
		scoped := req
		if scoped.FoodType == nil {
			// Keep the current scope when only `global` was sent.
			currentType := current.FoodType
			scoped.FoodType = &currentType
		}
		foodType, ok := resolveComidaCategoryScope(scoped)
		if !ok {
			writeComidaValidationError(w, "Tipo de comida invalido")
			return
		}
		next.FoodType = foodType
	}
	if req.Active != nil {
		next.Active = *req.Active
	}

	nextActiveInt := 0
	if next.Active {
		nextActiveInt = 1
	}
	if _, err := tx.ExecContext(r.Context(), `
		UPDATE comida_categories
		SET name = ?, slug = ?, food_type = ?, active = ?
		WHERE restaurant_id = ? AND id = ?
	`, next.Name, next.Slug, next.FoodType, nextActiveInt, restaurantID, id); err != nil {
		if isDuplicateKeyErr(err) {
			httpx.WriteError(w, http.StatusConflict, "Ya existe una categoria con ese nombre en ese ambito")
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando categoria")
		return
	}

	// Products reference their category by name, not by id, so a rename that stopped
	// at this table would orphan every product using the old spelling and would then
	// let the category be deleted as if it were unused.
	if next.Name != current.Name {
		if err := s.renameComidaCategoryUsages(r, tx, restaurantID, current, next.Name); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando productos de la categoria")
			return
		}
	}

	if err := tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando categoria")
		return
	}

	next.Scope = comidaCategoryScope(next.FoodType)
	next.IsGlobal = next.FoodType == comidaCategoryGlobalType
	next.Origin = comidaCategoryOriginUnified
	next.Editable = true
	next.Key = comidaCategoryKey(comidaCategoryOriginUnified, next.FoodType, next.ID)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"category": next,
	})
}

// handleBOComidaCategoryDelete removes a category, refusing when products still
// reference it.
//
// DELETE /admin/comida/categorias/{id}
func (s *Server) handleBOComidaCategoryDelete(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := comidaRestaurantIDFromRequest(r)
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	id, ok := comidaCategoryIDFromRoute(r)
	if !ok {
		httpx.WriteError(w, http.StatusBadRequest, "Identificador de categoria invalido")
		return
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error eliminando categoria")
		return
	}
	defer func() { _ = tx.Rollback() }()

	// Locked so a product cannot be assigned to the category between the usage check
	// and the delete.
	var current comidaUnifiedCategoryResponse
	err = tx.QueryRowContext(r.Context(), `
		SELECT id, COALESCE(name, ''), COALESCE(food_type, '')
		FROM comida_categories
		WHERE restaurant_id = ? AND id = ?
		FOR UPDATE
	`, restaurantID, id).Scan(&current.ID, &current.Name, &current.FoodType)
	if errors.Is(err, sql.ErrNoRows) {
		httpx.WriteError(w, http.StatusNotFound, "Categoria no encontrada")
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error cargando categoria")
		return
	}

	inUse, err := s.countComidaCategoryUsages(r, tx, restaurantID, current)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error verificando categoria")
		return
	}
	if inUse > 0 {
		httpx.WriteError(w, http.StatusConflict, "La categoria esta en uso")
		return
	}

	if _, err := tx.ExecContext(r.Context(), `
		DELETE FROM comida_categories WHERE restaurant_id = ? AND id = ?
	`, restaurantID, id); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error eliminando categoria")
		return
	}

	if err := tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error eliminando categoria")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

// comidaCategoryItemSourceTypes lists the comida_items.source_type values a category of
// the given scope can be used by. A global category reaches all of them; a type-scoped
// one only its own type. Without this filter a cafes category named "Postre" would be
// reported as in use by any plato called a postre, and the seeded base plato categories
// would make several names permanently undeletable.
func comidaCategoryItemSourceTypes(foodType string) []string {
	catalogue := []string{"platos", "bebidas", "cafes"}
	if foodType == comidaCategoryGlobalType {
		return catalogue
	}
	for _, t := range catalogue {
		if t == foodType {
			return []string{t}
		}
	}
	return nil
}

// comidaCategoryTouchesVinos reports whether a category of this scope can be referenced
// by VINOS.tipo. Postres are excluded on purpose: the POSTRES table has no category
// column at all.
func comidaCategoryTouchesVinos(foodType string) bool {
	return foodType == comidaCategoryGlobalType || foodType == "vinos"
}

// countComidaCategoryUsages counts the products that reference the category by name.
//
// comida_items.categoria and VINOS.tipo are plain VARCHARs, not foreign keys, so usage
// is matched by name and has to be constrained to the scopes the category actually
// applies to.
func (s *Server) countComidaCategoryUsages(r *http.Request, tx *sql.Tx, restaurantID int, category comidaUnifiedCategoryResponse) (int, error) {
	total := 0

	if sources := comidaCategoryItemSourceTypes(category.FoodType); len(sources) > 0 {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(sources)), ",")
		args := []any{restaurantID, category.Name}
		for _, src := range sources {
			args = append(args, src)
		}
		var n int
		// #nosec G202 -- placeholders is a generated list of "?", never input.
		if err := tx.QueryRowContext(r.Context(), `
			SELECT COUNT(*)
			FROM comida_items
			WHERE restaurant_id = ?
			  AND LOWER(COALESCE(categoria, '')) = LOWER(?)
			  AND COALESCE(source_type, '') IN (`+placeholders+`)
		`, args...).Scan(&n); err != nil {
			return 0, err
		}
		total += n
	}

	if comidaCategoryTouchesVinos(category.FoodType) {
		var n int
		if err := tx.QueryRowContext(r.Context(), `
			SELECT COUNT(*)
			FROM VINOS
			WHERE restaurant_id = ? AND LOWER(COALESCE(tipo, '')) = LOWER(?)
		`, restaurantID, category.Name).Scan(&n); err != nil {
			return 0, err
		}
		total += n
	}

	return total, nil
}

// renameComidaCategoryUsages carries a rename through to the products that reference
// the category by name, in the same transaction as the rename itself.
func (s *Server) renameComidaCategoryUsages(r *http.Request, tx *sql.Tx, restaurantID int, current comidaUnifiedCategoryResponse, newName string) error {
	if sources := comidaCategoryItemSourceTypes(current.FoodType); len(sources) > 0 {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(sources)), ",")
		args := []any{newName, restaurantID, current.Name}
		for _, src := range sources {
			args = append(args, src)
		}
		// #nosec G202 -- placeholders is a generated list of "?", never input.
		if _, err := tx.ExecContext(r.Context(), `
			UPDATE comida_items
			SET categoria = ?
			WHERE restaurant_id = ?
			  AND LOWER(COALESCE(categoria, '')) = LOWER(?)
			  AND COALESCE(source_type, '') IN (`+placeholders+`)
		`, args...); err != nil {
			return err
		}
	}

	if comidaCategoryTouchesVinos(current.FoodType) {
		if _, err := tx.ExecContext(r.Context(), `
			UPDATE VINOS
			SET tipo = ?
			WHERE restaurant_id = ? AND LOWER(COALESCE(tipo, '')) = LOWER(?)
		`, newName, restaurantID, current.Name); err != nil {
			return err
		}
	}

	return nil
}

func (s *Server) findComidaUnifiedCategoryBySlug(r *http.Request, restaurantID int, foodType, slug string) (comidaUnifiedCategoryResponse, bool, error) {
	var (
		c         comidaUnifiedCategoryResponse
		activeInt int
	)
	err := s.db.QueryRowContext(r.Context(), `
		SELECT id, COALESCE(name, ''), COALESCE(slug, ''), COALESCE(food_type, ''), active
		FROM comida_categories
		WHERE restaurant_id = ? AND food_type = ? AND slug = ?
		LIMIT 1
	`, restaurantID, foodType, slug).Scan(&c.ID, &c.Name, &c.Slug, &c.FoodType, &activeInt)
	if errors.Is(err, sql.ErrNoRows) {
		return comidaUnifiedCategoryResponse{}, false, nil
	}
	if err != nil {
		return comidaUnifiedCategoryResponse{}, false, err
	}
	c.Active = activeInt != 0
	c.Origin = comidaCategoryOriginUnified
	c.Editable = true
	c.IsGlobal = c.FoodType == comidaCategoryGlobalType
	c.Scope = comidaCategoryScope(c.FoodType)
	c.Key = comidaCategoryKey(comidaCategoryOriginUnified, c.FoodType, c.ID)
	return c, true, nil
}
