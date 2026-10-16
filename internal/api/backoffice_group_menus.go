package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"preactvillacarmen/internal/httpx"
)

func (s *Server) handleBOGroupMenusList(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	status := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))
	where := "WHERE restaurant_id = ?"
	args := []any{a.ActiveRestaurantID}
	switch status {
	case "active":
		where += " AND active = 1"
	case "inactive":
		where += " AND active = 0"
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id, menu_title, price, included_coffee, active, min_party_size, created_at, modified_at
		FROM menus
	`+where+`
		ORDER BY active DESC, created_at DESC, id DESC
	`, args...)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando menus")
		return
	}
	defer rows.Close()

	var out []map[string]any
	for rows.Next() {
		var (
			id           int
			title        string
			price        string
			inclCoffee   int
			activeInt    int
			minPartySize int
			createdAt    sql.NullString
			modifiedAt   sql.NullString
		)
		if err := rows.Scan(&id, &title, &price, &inclCoffee, &activeInt, &minPartySize, &createdAt, &modifiedAt); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo menus")
			return
		}
		out = append(out, map[string]any{
			"id":              id,
			"menu_title":      title,
			"price":           price,
			"included_coffee": inclCoffee != 0,
			"active":          activeInt != 0,
			"min_party_size":  minPartySize,
			"created_at":      createdAt.String,
			"modified_at":     modifiedAt.String,
		})
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"count":   len(out),
		"menus":   out,
	})
}

//PrincipalesArrozDerivadosKey names the read-only list of principales that the
// server derived (arroz section + arroz catalog) instead of the user storing it.
// It is served on every read and dropped on every write.
const PrincipalesArrozDerivadosKey = "items_arroz_derivados"

// arrozNameKey is the comparison key for dish names: trimmed and lowercased, so
// "Arroz a Banda" and "arroz a banda " are the same principal.
func arrozNameKey(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// boGroupMenuDerivedArrozNames returns the arroz dish names that the read path
// merges into principales.items, so the booking can offer them as principales.
// Read and write MUST resolve this set through the same function: if the write
// path guessed differently, a save would persist names the next read considers
// derived, which is exactly how the arroz ended up stored as plain text.
// Coordination id: booking_principales_include_arroz_v1
func (s *Server) boGroupMenuDerivedArrozNames(ctx context.Context, restaurantID int, menuID int64) []string {
	out := make([]string, 0, 16)
	seen := make(map[string]bool, 16)
	add := func(raw string) {
		t := strings.TrimSpace(raw)
		if t == "" {
			return
		}
		key := arrozNameKey(t)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, t)
	}

	// 1) Dishes that live in an "arroces" section of this menu. A menu that does
	// not exist yet (create) has no sections, so skip the query entirely.
	if menuID > 0 {
		if rows, err := s.db.QueryContext(ctx, `
			SELECT d.title_snapshot
			FROM group_menu_sections_v2 sec
			JOIN group_menu_section_dishes_v2 d
			  ON d.section_id = sec.id AND d.restaurant_id = sec.restaurant_id
			WHERE sec.restaurant_id = ? AND sec.menu_id = ? AND d.active = 1
			  AND sec.section_kind IN ('arroces', 'rice')
			ORDER BY d.position ASC, d.id ASC
		`, restaurantID, menuID); err == nil {
			for rows.Next() {
				var name string
				if scanErr := rows.Scan(&name); scanErr != nil {
					break
				}
				add(name)
			}
			rows.Close()
		}
	}

	// 2) Catalog dishes whose type or category is "arroz".
	if rows, err := s.db.QueryContext(ctx, `
		SELECT nombre
		FROM comida_items
		WHERE restaurant_id = ? AND active = 1
		  AND (UPPER(TRIM(COALESCE(tipo, ''))) = 'ARROZ'
		       OR LOWER(TRIM(COALESCE(categoria, ''))) LIKE 'arroz%')
		ORDER BY nombre ASC
	`, restaurantID); err == nil {
		for rows.Next() {
			var name string
			if scanErr := rows.Scan(&name); scanErr != nil {
				break
			}
			add(name)
		}
		rows.Close()
	}

	return out
}

// boGroupMenuPrincipalesWithArroz returns the legacy "principales" block with
// the arroz dishes merged in, so the "add/edit booking" group-menu section can
// offer them as principales.
// Coordination id: booking_principales_include_arroz_v1
//
// The merge is read-only: the arroz names also come back under
// PrincipalesArrozDerivadosKey so a client can tell them apart from the ones a
// human typed, but they are plain names without price, allergens or dish id.
// That is why boGroupMenuPrincipalesForPersist never lets them reach storage.
func (s *Server) boGroupMenuPrincipalesWithArroz(ctx context.Context, restaurantID int, menuID int64, principalesRaw string) map[string]any {
	out, _ := decodeJSONOrFallback(principalesRaw, map[string]any{}).(map[string]any)
	if out == nil {
		out = map[string]any{}
	}
	title := strings.TrimSpace(anyToString(out["titulo_principales"]))
	if title == "" {
		title = "Principal a elegir"
	}
	items := anySliceToStringList(out["items"])
	seen := make(map[string]bool, len(items))
	for _, it := range items {
		seen[arrozNameKey(it)] = true
	}

	derived := s.boGroupMenuDerivedArrozNames(ctx, restaurantID, menuID)
	added := make([]string, 0, len(derived))
	for _, name := range derived {
		if seen[arrozNameKey(name)] {
			continue
		}
		seen[arrozNameKey(name)] = true
		items = append(items, name)
		added = append(added, name)
	}

	out["titulo_principales"] = title
	out["items"] = items
	out[PrincipalesArrozDerivadosKey] = added
	return out
}

// boGroupMenuPrincipalesForPersist strips an incoming "principales" block down
// to what a human actually chose, so a save can never store the arroz names the
// read path derives. Without this the arroz are round-tripped into the row as
// bare text: the next save then keeps them forever, they never pick up price,
// allergens or dish id when the catalog changes, and a rename in the catalog
// leaves a stale copy behind.
//
// Stripping a derived arroz is behaviour-preserving: the read merges exactly the
// same set back in, so the booking offers the same list either way. A principal
// the user typed by hand is only stripped when the read would derive that very
// name, and one that is not derived (an arroz-shaped title absent from the arroz
// section and the catalog) is left untouched.
//
// Non-object payloads are returned unchanged: this must not reshape legacy rows
// that are not a {titulo_principales, items} block.
func (s *Server) boGroupMenuPrincipalesForPersist(ctx context.Context, restaurantID int, menuID int64, incoming any) any {
	// Not an object: leave the legacy payload exactly as it arrived rather than
	// reshaping a row that is not a {titulo_principales, items} block.
	out, ok := incoming.(map[string]any)
	if !ok {
		return incoming
	}

	derived := make(map[string]bool)
	for _, name := range s.boGroupMenuDerivedArrozNames(ctx, restaurantID, menuID) {
		derived[arrozNameKey(name)] = true
	}

	kept := make([]string, 0)
	for _, it := range anySliceToStringList(out["items"]) {
		if derived[arrozNameKey(it)] {
			continue
		}
		kept = append(kept, it)
	}

	delete(out, PrincipalesArrozDerivadosKey)
	out["items"] = kept
	return out
}

func (s *Server) handleBOGroupMenuGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	idStr := strings.TrimSpace(chi.URLParam(r, "id"))
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid menu id",
		})
		return
	}

	query := `
		SELECT id, menu_title, price, included_coffee, active,
		       menu_subtitle, entrantes, principales, postre, beverage, comments,
		       min_party_size, main_dishes_limit, main_dishes_limit_number,
		       created_at, modified_at
		FROM menus
		WHERE id = ? AND restaurant_id = ?
	`

	var (
		menuTitle    string
		price        string
		inclCoffee   int
		activeInt    int
		menuSubtitle sql.NullString
		entrantes    sql.NullString
		principales  sql.NullString
		postre       sql.NullString
		beverage     sql.NullString
		comments     sql.NullString
		minPartySize int
		mainLimit    int
		mainLimitNum int
		createdAt    sql.NullString
		modifiedAt   sql.NullString
	)

	err = s.db.QueryRowContext(r.Context(), query, id, a.ActiveRestaurantID).Scan(
		&id,
		&menuTitle,
		&price,
		&inclCoffee,
		&activeInt,
		&menuSubtitle,
		&entrantes,
		&principales,
		&postre,
		&beverage,
		&comments,
		&minPartySize,
		&mainLimit,
		&mainLimitNum,
		&createdAt,
		&modifiedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "Menu not found",
			})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando menus")
		return
	}

	menu := map[string]any{
		"id":                       id,
		"menu_title":               menuTitle,
		"price":                    price,
		"included_coffee":          inclCoffee != 0,
		"active":                   activeInt != 0,
		"menu_subtitle":            decodeJSONOrFallback(menuSubtitle.String, []any{}),
		"entrantes":                decodeJSONOrFallback(entrantes.String, []any{}),
		"principales":              s.boGroupMenuPrincipalesWithArroz(r.Context(), a.ActiveRestaurantID, int64(id), principales.String),
		"postre":                   decodeJSONOrFallback(postre.String, []any{}),
		"beverage":                 decodeJSONOrFallback(beverage.String, map[string]any{}),
		"comments":                 decodeJSONOrFallback(comments.String, []any{}),
		"min_party_size":           minPartySize,
		"main_dishes_limit":        mainLimit != 0,
		"main_dishes_limit_number": mainLimitNum,
		"created_at":               createdAt.String,
		"modified_at":              modifiedAt.String,
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"menu":    menu,
	})
}

func (s *Server) handleBOGroupMenuCreate(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var input map[string]any
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid JSON body",
		})
		return
	}

	menuTitle := strings.TrimSpace(anyToString(input["menu_title"]))
	if menuTitle == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Menu title is required",
		})
		return
	}

	price, err := anyToFloat64(input["price"])
	if err != nil || price <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Price is required",
		})
		return
	}

	includedCoffee := boolToTinyint(parseLooseBoolOrDefault(input["included_coffee"], false))
	active := boolToTinyint(parseLooseBoolOrDefault(input["active"], true))

	minPartySize, _ := anyToInt(input["min_party_size"])
	if minPartySize <= 0 {
		minPartySize = 8
	}
	mainLimit := boolToTinyint(parseLooseBoolOrDefault(input["main_dishes_limit"], false))
	mainLimitNum, _ := anyToInt(input["main_dishes_limit_number"])
	if mainLimitNum <= 0 {
		mainLimitNum = 1
	}

	menuSubtitleJSON := mustJSON(input["menu_subtitle"], []any{})
	entrantesJSON := mustJSON(input["entrantes"], []any{})
	// The menu has no id yet, so only the arroz catalog can be derived here.
	principalesJSON := mustJSON(s.boGroupMenuPrincipalesForPersist(r.Context(), a.ActiveRestaurantID, 0, input["principales"]), map[string]any{})
	postreJSON := mustJSON(input["postre"], []any{})
	beverageJSON := mustJSON(input["beverage"], map[string]any{})
	commentsJSON := mustJSON(input["comments"], []any{})

	res, err := s.db.ExecContext(
		r.Context(),
		`INSERT INTO menus
		 (restaurant_id, menu_title, price, included_coffee, active, menu_subtitle, entrantes, principales, postre, beverage, comments, min_party_size, main_dishes_limit, main_dishes_limit_number)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ActiveRestaurantID,
		menuTitle,
		price,
		includedCoffee,
		active,
		menuSubtitleJSON,
		entrantesJSON,
		principalesJSON,
		postreJSON,
		beverageJSON,
		commentsJSON,
		minPartySize,
		mainLimit,
		mainLimitNum,
	)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creando menus")
		return
	}

	menuID, _ := res.LastInsertId()
	entrantesList := anySliceToStringList(decodeJSONOrFallback(entrantesJSON, []any{}))
	principalesTitle := decodePrincipalesTitleJSON(principalesJSON)
	principalesItemsList := decodePrincipalesItemsJSON(principalesJSON)
	postreList := anySliceToStringList(decodeJSONOrFallback(postreJSON, []any{}))
	s.translateMenuConventionalArrays(r.Context(), a.ActiveRestaurantID, menuID, entrantesList, principalesTitle, principalesItemsList, postreList)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":    true,
		"message":    "Menu created successfully",
		"menu_id":    menuID,
		"menu_title": menuTitle,
	})
}

func (s *Server) handleBOGroupMenuUpdate(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	idStr := strings.TrimSpace(chi.URLParam(r, "id"))
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid menu id",
		})
		return
	}

	var input map[string]any
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid JSON body",
		})
		return
	}

	menuTitle := strings.TrimSpace(anyToString(input["menu_title"]))
	if menuTitle == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Menu title is required",
		})
		return
	}
	price, err := anyToFloat64(input["price"])
	if err != nil || price <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Price is required",
		})
		return
	}

	// Ensure menu belongs to restaurant.
	var tmp int
	if err := s.db.QueryRowContext(r.Context(), "SELECT id FROM menus WHERE id = ? AND restaurant_id = ? LIMIT 1", id, a.ActiveRestaurantID).Scan(&tmp); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "Menu not found",
			})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando menus")
		return
	}

	includedCoffee := boolToTinyint(parseLooseBoolOrDefault(input["included_coffee"], false))
	active := boolToTinyint(parseLooseBoolOrDefault(input["active"], true))

	minPartySize, _ := anyToInt(input["min_party_size"])
	if minPartySize <= 0 {
		minPartySize = 8
	}
	mainLimit := boolToTinyint(parseLooseBoolOrDefault(input["main_dishes_limit"], false))
	mainLimitNum, _ := anyToInt(input["main_dishes_limit_number"])
	if mainLimitNum <= 0 {
		mainLimitNum = 1
	}

	menuSubtitleJSON := mustJSON(input["menu_subtitle"], []any{})
	entrantesJSON := mustJSON(input["entrantes"], []any{})
	principalesJSON := mustJSON(s.boGroupMenuPrincipalesForPersist(r.Context(), a.ActiveRestaurantID, int64(id), input["principales"]), map[string]any{})
	postreJSON := mustJSON(input["postre"], []any{})
	beverageJSON := mustJSON(input["beverage"], map[string]any{})
	commentsJSON := mustJSON(input["comments"], []any{})

	_, err = s.db.ExecContext(
		r.Context(),
		`UPDATE menus SET
			menu_title = ?,
			price = ?,
			included_coffee = ?,
			active = ?,
			menu_subtitle = ?,
			entrantes = ?,
			principales = ?,
			postre = ?,
			beverage = ?,
			comments = ?,
			min_party_size = ?,
			main_dishes_limit = ?,
			main_dishes_limit_number = ?
		WHERE id = ? AND restaurant_id = ?`,
		menuTitle,
		price,
		includedCoffee,
		active,
		menuSubtitleJSON,
		entrantesJSON,
		principalesJSON,
		postreJSON,
		beverageJSON,
		commentsJSON,
		minPartySize,
		mainLimit,
		mainLimitNum,
		id,
		a.ActiveRestaurantID,
	)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando menus")
		return
	}

	entrantesList := anySliceToStringList(decodeJSONOrFallback(entrantesJSON, []any{}))
	principalesTitle := decodePrincipalesTitleJSON(principalesJSON)
	principalesItemsList := decodePrincipalesItemsJSON(principalesJSON)
	postreList := anySliceToStringList(decodeJSONOrFallback(postreJSON, []any{}))
	s.translateMenuConventionalArrays(r.Context(), a.ActiveRestaurantID, int64(id), entrantesList, principalesTitle, principalesItemsList, postreList)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":    true,
		"message":    "Menu updated successfully",
		"menu_id":    id,
		"menu_title": menuTitle,
	})
}

func (s *Server) handleBOGroupMenuToggleActive(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	idStr := strings.TrimSpace(chi.URLParam(r, "id"))
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid menu id",
		})
		return
	}

	var current int
	if err := s.db.QueryRowContext(r.Context(), "SELECT active FROM menus WHERE id = ? AND restaurant_id = ? LIMIT 1", id, a.ActiveRestaurantID).Scan(&current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "Menu not found",
			})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando menus")
		return
	}

	newStatus := 1
	if current != 0 {
		newStatus = 0
	}

	if _, err := s.db.ExecContext(r.Context(), "UPDATE menus SET active = ? WHERE id = ? AND restaurant_id = ?", newStatus, id, a.ActiveRestaurantID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando menus")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"menu_id": id,
		"active":  newStatus != 0,
	})
}

func (s *Server) handleBOGroupMenuDelete(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	idStr := strings.TrimSpace(chi.URLParam(r, "id"))
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid menu id",
		})
		return
	}

	res, err := s.db.ExecContext(r.Context(), "DELETE FROM menus WHERE id = ? AND restaurant_id = ?", id, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error eliminando menus")
		return
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Menu not found",
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
	})
}
