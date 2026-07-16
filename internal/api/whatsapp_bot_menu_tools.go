package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// botMenuCategoryLabel maps a menu_type to its Spanish label.
func botMenuCategoryLabel(menuType string) string {
	switch normalizeV2MenuType(menuType) {
	case "closed_conventional":
		return "Menú cerrado convencional"
	case "closed_group":
		return "Menú cerrado de grupo"
	case "a_la_carte":
		return "A la carta convencional"
	case "a_la_carte_group":
		return "A la carta de grupo"
	case "special":
		return "Menú especial"
	default:
		return "Menú"
	}
}

// botBeverageLabel gives a human Spanish label for a beverage type.
func botBeverageLabel(bevType string) string {
	switch strings.ToLower(strings.TrimSpace(bevType)) {
	case "ilimitada":
		return "Bebida ilimitada"
	case "incluida":
		return "Bebida incluida"
	case "opcion", "opción":
		return "Bebida opcional"
	case "no_incluida", "":
		return "Bebida no incluida"
	default:
		return "Bebida: " + bevType
	}
}

// botMenuBeverageSettings interprets the beverage JSON blob stored per menu.
func botMenuBeverageSettings(raw string) map[string]any {
	out := map[string]any{
		"beverage_type":          "no_incluida",
		"beverage_label":         botBeverageLabel(""),
		"unlimited_drinks":       false,
		"drink_price_per_person": nil,
		"has_supplement":         false,
		"supplement_price":       nil,
	}
	decoded, ok := decodeJSONOrFallback(raw, map[string]any{}).(map[string]any)
	if !ok {
		return out
	}
	bevType := strings.ToLower(strings.TrimSpace(anyToString(decoded["type"])))
	if bevType == "" {
		bevType = "no_incluida"
	}
	out["beverage_type"] = bevType
	out["beverage_label"] = botBeverageLabel(bevType)
	out["unlimited_drinks"] = bevType == "ilimitada"
	if v, ok := decoded["price_per_person"]; ok && v != nil {
		out["drink_price_per_person"] = v
	}
	if v, ok := decoded["has_supplement"].(bool); ok {
		out["has_supplement"] = v
	}
	if v, ok := decoded["supplement_price"]; ok && v != nil {
		out["supplement_price"] = v
	}
	return out
}

// botToolListMenus returns the active bookable menus grouped by category type.
func (s *Server) botToolListMenus(ctx context.Context, restaurantID int) (string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, menu_title, COALESCE(NULLIF(TRIM(menu_type), ''), 'closed_conventional'),
		       COALESCE(price, ''), COALESCE(menu_subtitle, '')
		FROM menus
		WHERE restaurant_id = ? AND active = 1 AND is_draft = 0
		  AND COALESCE(NULLIF(TRIM(menu_type), ''), 'closed_conventional')
		      IN ('closed_conventional', 'closed_group', 'a_la_carte', 'a_la_carte_group', 'special')
		ORDER BY
		  CASE COALESCE(NULLIF(TRIM(menu_type), ''), 'closed_conventional')
		    WHEN 'closed_conventional' THEN 1
		    WHEN 'a_la_carte' THEN 2
		    WHEN 'closed_group' THEN 3
		    WHEN 'a_la_carte_group' THEN 4
		    WHEN 'special' THEN 5 ELSE 9 END ASC,
		  modified_at DESC, id DESC
	`, restaurantID)
	if err != nil {
		return botJSON(map[string]any{"error": "error consultando los menús"}), nil
	}
	defer rows.Close()

	menus := []map[string]any{}
	for rows.Next() {
		var (
			id          int64
			title       string
			menuType    string
			price       string
			subtitleRaw string
		)
		if err := rows.Scan(&id, &title, &menuType, &price, &subtitleRaw); err != nil {
			return botJSON(map[string]any{"error": "error leyendo los menús"}), nil
		}
		menuType = normalizeV2MenuType(menuType)
		price = strings.TrimSpace(price)
		if price == "" {
			price = "0"
		}
		menus = append(menus, map[string]any{
			"menu_id":        id,
			"title":          strings.TrimSpace(title),
			"category":       menuType,
			"category_label": botMenuCategoryLabel(menuType),
			"price":          price,
			"subtitle":       anySliceToStringList(decodeJSONOrFallback(subtitleRaw, []any{})),
		})
	}
	return botJSON(map[string]any{"count": len(menus), "menus": menus}), nil
}

// botToolMenuDetails returns full information for a single menu: settings,
// sections and their dishes.
func (s *Server) botToolMenuDetails(ctx context.Context, restaurantID int, input json.RawMessage) (string, error) {
	var in struct {
		MenuID int64 `json:"menu_id"`
	}
	if err := json.Unmarshal(input, &in); err != nil || in.MenuID <= 0 {
		return botJSON(map[string]any{"error": "menu_id inválido"}), nil
	}

	var (
		title, menuType, price, subtitleRaw     string
		entrantesRaw, principalesRaw, postreRaw string
		beverageRaw, commentsRaw                string
		minPartySize, mainLimitNum              int
		mainLimit, includedCoffee               int
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT menu_title, COALESCE(NULLIF(TRIM(menu_type), ''), 'closed_conventional'),
		       COALESCE(price, ''), COALESCE(menu_subtitle, ''),
		       COALESCE(entrantes, ''), COALESCE(principales, ''), COALESCE(postre, ''),
		       COALESCE(beverage, ''), COALESCE(comments, ''),
		       COALESCE(min_party_size, 0), COALESCE(main_dishes_limit, 0),
		       COALESCE(main_dishes_limit_number, 0), COALESCE(included_coffee, 0)
		FROM menus
		WHERE restaurant_id = ? AND id = ? AND active = 1 AND is_draft = 0
		LIMIT 1
	`, restaurantID, in.MenuID).Scan(
		&title, &menuType, &price, &subtitleRaw,
		&entrantesRaw, &principalesRaw, &postreRaw,
		&beverageRaw, &commentsRaw,
		&minPartySize, &mainLimit, &mainLimitNum, &includedCoffee,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return botJSON(map[string]any{"error": "menú no encontrado"}), nil
	}
	if err != nil {
		return botJSON(map[string]any{"error": "error consultando el menú"}), nil
	}

	menuType = normalizeV2MenuType(menuType)
	price = strings.TrimSpace(price)
	if price == "" {
		price = "0"
	}
	if minPartySize <= 0 {
		minPartySize = 1
	}

	settings := botMenuBeverageSettings(beverageRaw)
	settings["coffee_included"] = includedCoffee != 0
	settings["min_party_size"] = minPartySize
	settings["has_max_main_dishes_per_table"] = mainLimit != 0
	if mainLimit != 0 && mainLimitNum > 0 {
		settings["max_main_dishes_per_table"] = mainLimitNum
	} else {
		settings["max_main_dishes_per_table"] = nil
	}
	settings["comments"] = anySliceToStringList(decodeJSONOrFallback(commentsRaw, []any{}))

	// Principales legacy block (title + items).
	principalesTitle := ""
	principalesItems := []string{}
	if decoded, ok := decodeJSONOrFallback(principalesRaw, map[string]any{}).(map[string]any); ok {
		principalesTitle = strings.TrimSpace(anyToString(decoded["titulo_principales"]))
		principalesItems = anySliceToStringList(decoded["items"])
	}

	sections := s.botLoadMenuSections(ctx, restaurantID, in.MenuID)

	return botJSON(map[string]any{
		"menu_id":        in.MenuID,
		"title":          strings.TrimSpace(title),
		"category":       menuType,
		"category_label": botMenuCategoryLabel(menuType),
		"price":          price,
		"subtitle":       anySliceToStringList(decodeJSONOrFallback(subtitleRaw, []any{})),
		"settings":       settings,
		"sections":       sections,
		"entrantes":      anySliceToStringList(decodeJSONOrFallback(entrantesRaw, []any{})),
		"principales": map[string]any{
			"title": principalesTitle,
			"items": principalesItems,
		},
		"postre": anySliceToStringList(decodeJSONOrFallback(postreRaw, []any{})),
	}), nil
}

// botLoadMenuSections loads the v2 sections and their active dishes for a menu.
func (s *Server) botLoadMenuSections(ctx context.Context, restaurantID int, menuID int64) []map[string]any {
	sectionRows, err := s.db.QueryContext(ctx, `
		SELECT id, title, section_kind, position
		FROM group_menu_sections_v2
		WHERE restaurant_id = ? AND menu_id = ?
		ORDER BY position ASC, id ASC
	`, restaurantID, menuID)
	if err != nil {
		return []map[string]any{}
	}
	type sectionAcc struct {
		payload map[string]any
		dishes  []map[string]any
	}
	order := []int64{}
	byID := map[int64]*sectionAcc{}
	for sectionRows.Next() {
		var (
			id       int64
			title    string
			kind     string
			position int
		)
		if err := sectionRows.Scan(&id, &title, &kind, &position); err != nil {
			sectionRows.Close()
			return []map[string]any{}
		}
		acc := &sectionAcc{
			payload: map[string]any{
				"title": strings.TrimSpace(title),
				"kind":  normalizeV2SectionKind(kind),
			},
			dishes: []map[string]any{},
		}
		byID[id] = acc
		order = append(order, id)
	}
	sectionRows.Close()

	if len(order) == 0 {
		return []map[string]any{}
	}

	dishRows, err := s.db.QueryContext(ctx, `
		SELECT section_id, title_snapshot, COALESCE(description_snapshot, ''),
		       price, supplement_enabled, supplement_price
		FROM group_menu_section_dishes_v2
		WHERE restaurant_id = ? AND menu_id = ? AND active = 1
		ORDER BY section_id ASC, position ASC, id ASC
	`, restaurantID, menuID)
	if err == nil {
		for dishRows.Next() {
			var (
				sectionID       int64
				title           string
				description     string
				price           sql.NullFloat64
				supplementInt   int
				supplementPrice sql.NullFloat64
			)
			if err := dishRows.Scan(&sectionID, &title, &description, &price, &supplementInt, &supplementPrice); err != nil {
				break
			}
			acc := byID[sectionID]
			if acc == nil {
				continue
			}
			dish := map[string]any{
				"title":              strings.TrimSpace(title),
				"description":        strings.TrimSpace(description),
				"supplement_enabled": supplementInt != 0,
			}
			if price.Valid {
				dish["price"] = price.Float64
			}
			if supplementPrice.Valid {
				dish["supplement_price"] = supplementPrice.Float64
			}
			acc.dishes = append(acc.dishes, dish)
		}
		dishRows.Close()
	}

	out := make([]map[string]any, 0, len(order))
	for _, id := range order {
		acc := byID[id]
		acc.payload["dishes"] = acc.dishes
		out = append(out, acc.payload)
	}
	return out
}

// botToolCoffeeMenu returns the active coffee items (CAFES).
func (s *Server) botToolCoffeeMenu(ctx context.Context, restaurantID int) (string, error) {
	return s.botLoadSimpleMenu(ctx, restaurantID, "CAFES", "cafés")
}

// botToolDrinksMenu returns the active drink items (BEBIDAS).
func (s *Server) botToolDrinksMenu(ctx context.Context, restaurantID int) (string, error) {
	return s.botLoadSimpleMenu(ctx, restaurantID, "BEBIDAS", "bebidas")
}

func (s *Server) botLoadSimpleMenu(ctx context.Context, restaurantID int, table string, human string) (string, error) {
	//nolint:gosec // table is a hardcoded constant, never user input.
	rows, err := s.db.QueryContext(ctx, `
		SELECT nombre, precio, COALESCE(descripcion, ''), COALESCE(titulo, ''), COALESCE(suplemento, 0)
		FROM `+table+`
		WHERE restaurant_id = ? AND active = 1
		ORDER BY COALESCE(titulo, ''), nombre
	`, restaurantID)
	if err != nil {
		return botJSON(map[string]any{"error": "error consultando " + human}), nil
	}
	defer rows.Close()

	items := []map[string]any{}
	for rows.Next() {
		var (
			nombre      string
			precio      float64
			descripcion string
			titulo      string
			suplemento  float64
		)
		if err := rows.Scan(&nombre, &precio, &descripcion, &titulo, &suplemento); err != nil {
			return botJSON(map[string]any{"error": "error leyendo " + human}), nil
		}
		item := map[string]any{
			"name":  strings.TrimSpace(nombre),
			"price": precio,
		}
		if d := strings.TrimSpace(descripcion); d != "" {
			item["description"] = d
		}
		if g := strings.TrimSpace(titulo); g != "" {
			item["group"] = g
		}
		if suplemento > 0 {
			item["supplement"] = suplemento
		}
		items = append(items, item)
	}
	return botJSON(map[string]any{"count": len(items), "items": items}), nil
}

// botToolWinesMenu returns the active wines (VINOS) grouped by type.
func (s *Server) botToolWinesMenu(ctx context.Context, restaurantID int) (string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT nombre, COALESCE(precio, 0), COALESCE(descripcion, ''), COALESCE(tipo, ''),
		       COALESCE(bodega, ''), COALESCE(denominacion_origen, ''), COALESCE(anyo, ''),
		       graduacion
		FROM VINOS
		WHERE restaurant_id = ? AND active = 1
		ORDER BY COALESCE(tipo, ''), nombre
	`, restaurantID)
	if err != nil {
		return botJSON(map[string]any{"error": "error consultando los vinos"}), nil
	}
	defer rows.Close()

	wines := []map[string]any{}
	for rows.Next() {
		var (
			nombre       string
			precio       float64
			descripcion  string
			tipo         string
			bodega       string
			denominacion string
			anyo         string
			graduacion   sql.NullFloat64
		)
		if err := rows.Scan(&nombre, &precio, &descripcion, &tipo, &bodega, &denominacion, &anyo, &graduacion); err != nil {
			return botJSON(map[string]any{"error": "error leyendo los vinos"}), nil
		}
		wine := map[string]any{
			"name":  strings.TrimSpace(nombre),
			"price": precio,
			"type":  strings.TrimSpace(tipo),
		}
		if d := strings.TrimSpace(descripcion); d != "" {
			wine["description"] = d
		}
		if b := strings.TrimSpace(bodega); b != "" {
			wine["winery"] = b
		}
		if o := strings.TrimSpace(denominacion); o != "" {
			wine["denomination"] = o
		}
		if y := strings.TrimSpace(anyo); y != "" {
			wine["year"] = y
		}
		if graduacion.Valid && graduacion.Float64 > 0 {
			wine["abv"] = graduacion.Float64
		}
		wines = append(wines, wine)
	}
	return botJSON(map[string]any{"count": len(wines), "wines": wines}), nil
}
