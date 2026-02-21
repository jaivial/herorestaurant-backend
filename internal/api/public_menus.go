package api

import (
	"database/sql"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"preactvillacarmen/internal/httpx"
)

var publicMenuSlugPattern = regexp.MustCompile(`[^a-z0-9]+`)

var publicMenuSlugReplacer = strings.NewReplacer(
	"á", "a",
	"à", "a",
	"ä", "a",
	"â", "a",
	"é", "e",
	"è", "e",
	"ë", "e",
	"ê", "e",
	"í", "i",
	"ì", "i",
	"ï", "i",
	"î", "i",
	"ó", "o",
	"ò", "o",
	"ö", "o",
	"ô", "o",
	"ú", "u",
	"ù", "u",
	"ü", "u",
	"û", "u",
	"ñ", "n",
	"ç", "c",
)

type publicMenuDish struct {
	ID                int64    `json:"id"`
	Title             string   `json:"title"`
	Description       string   `json:"description"`
	FotoURL           string   `json:"foto_url"`
	Allergens         []string `json:"allergens"`
	SupplementEnabled bool     `json:"supplement_enabled"`
	SupplementPrice   *float64 `json:"supplement_price"`
	Price             *float64 `json:"price"`
	Position          int      `json:"position"`
}

type publicMenuSection struct {
	ID       int64            `json:"id"`
	Title    string           `json:"title"`
	Kind     string           `json:"kind"`
	Position int              `json:"position"`
	Dishes   []publicMenuDish `json:"dishes"`
}

type publicMenuPrincipales struct {
	TituloPrincipales string   `json:"titulo_principales"`
	Items             []string `json:"items"`
}

type publicMenuSettings struct {
	IncludedCoffee       bool           `json:"included_coffee"`
	Beverage             map[string]any `json:"beverage"`
	Comments             []string       `json:"comments"`
	MinPartySize         int            `json:"min_party_size"`
	MainDishesLimit      bool           `json:"main_dishes_limit"`
	MainDishesLimitCount int            `json:"main_dishes_limit_number"`
}

type publicMenuItem struct {
	ID                  int64                 `json:"id"`
	Slug                string                `json:"slug"`
	MenuTitle           string                `json:"menu_title"`
	MenuType            string                `json:"menu_type"`
	Price               string                `json:"price"`
	Active              bool                  `json:"active"`
	MenuSubtitle        []string              `json:"menu_subtitle"`
	ShowDishImages      bool                  `json:"show_dish_images"`
	Entrantes           []string              `json:"entrantes"`
	Principales         publicMenuPrincipales `json:"principales"`
	Postre              []string              `json:"postre"`
	Settings            publicMenuSettings    `json:"settings"`
	Sections            []publicMenuSection   `json:"sections"`
	SpecialMenuImageURL string                `json:"special_menu_image_url"`
	LegacySourceTable   string                `json:"legacy_source_table,omitempty"`
	CreatedAt           string                `json:"created_at"`
	ModifiedAt          string                `json:"modified_at"`
}

func isPublicMenuType(menuType string) bool {
	switch menuType {
	case "closed_conventional", "closed_group", "a_la_carte", "a_la_carte_group", "special":
		return true
	default:
		return false
	}
}

func buildPublicMenuSlug(title string, menuID int64) string {
	base := strings.ToLower(strings.TrimSpace(title))
	base = publicMenuSlugReplacer.Replace(base)
	base = publicMenuSlugPattern.ReplaceAllString(base, "-")
	base = strings.Trim(base, "-")
	if base == "" {
		base = "menu"
	}
	return fmt.Sprintf("%s-%d", base, menuID)
}

func (s *Server) publicMenuMediaURL(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return value
	}
	return s.bunnyPullURL(value)
}

func buildFallbackPublicSectionDishes(items []string) []publicMenuDish {
	out := make([]publicMenuDish, 0, len(items))
	for idx, item := range items {
		title := strings.TrimSpace(item)
		if title == "" {
			continue
		}
		out = append(out, publicMenuDish{
			ID:                0,
			Title:             title,
			Description:       "",
			Allergens:         []string{},
			SupplementEnabled: false,
			SupplementPrice:   nil,
			Price:             nil,
			Position:          idx,
		})
	}
	return out
}

func buildFallbackPublicSections(menu publicMenuItem) []publicMenuSection {
	out := make([]publicMenuSection, 0, 3)

	entrantes := buildFallbackPublicSectionDishes(menu.Entrantes)
	if len(entrantes) > 0 {
		out = append(out, publicMenuSection{
			ID:       0,
			Title:    "Entrantes",
			Kind:     "entrantes",
			Position: len(out),
			Dishes:   entrantes,
		})
	}

	principales := buildFallbackPublicSectionDishes(menu.Principales.Items)
	if len(principales) > 0 {
		sectionTitle := strings.TrimSpace(menu.Principales.TituloPrincipales)
		if sectionTitle == "" {
			sectionTitle = "Principales"
		}
		out = append(out, publicMenuSection{
			ID:       0,
			Title:    sectionTitle,
			Kind:     "principales",
			Position: len(out),
			Dishes:   principales,
		})
	}

	postres := buildFallbackPublicSectionDishes(menu.Postre)
	if len(postres) > 0 {
		out = append(out, publicMenuSection{
			ID:       0,
			Title:    "Postres",
			Kind:     "postres",
			Position: len(out),
			Dishes:   postres,
		})
	}

	return out
}

func (s *Server) handlePublicMenus(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id, menu_title, price, active, menu_type, menu_subtitle,
		       show_dish_images, entrantes, principales, postre, beverage, comments,
		       min_party_size, main_dishes_limit, main_dishes_limit_number, included_coffee,
		       special_menu_image_url, legacy_source_table, created_at, modified_at
		FROM menusDeGrupos
		WHERE restaurant_id = ?
		  AND active = 1
		  AND is_draft = 0
		  AND COALESCE(NULLIF(TRIM(menu_type), ''), 'closed_conventional') IN ('closed_conventional', 'closed_group', 'a_la_carte', 'a_la_carte_group', 'special')
		ORDER BY
		  CASE COALESCE(NULLIF(TRIM(menu_type), ''), 'closed_conventional')
		    WHEN 'closed_conventional' THEN 1
		    WHEN 'a_la_carte' THEN 2
		    WHEN 'special' THEN 3
		    WHEN 'closed_group' THEN 4
		    WHEN 'a_la_carte_group' THEN 5
		    ELSE 9
		  END ASC,
		  modified_at DESC,
		  id DESC
	`, restaurantID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Error consultando menusDeGrupos",
		})
		return
	}
	defer rows.Close()

	menus := make([]publicMenuItem, 0, 24)
	menuIndexByID := make(map[int64]int, 24)
	menuIDs := make([]int64, 0, 24)

	for rows.Next() {
		var (
			menuID             int64
			menuTitle          string
			priceRaw           sql.NullString
			activeInt          int
			menuTypeRaw        sql.NullString
			menuSubtitleRaw    sql.NullString
			showDishImagesInt  int
			entrantesRaw       sql.NullString
			principalesRaw     sql.NullString
			postreRaw          sql.NullString
			beverageRaw        sql.NullString
			commentsRaw        sql.NullString
			minPartySize       int
			mainDishesLimitInt int
			mainDishesLimitNum int
			includedCoffeeInt  int
			specialImageURLRaw sql.NullString
			legacySourceTable  sql.NullString
			createdAtRaw       sql.NullString
			modifiedAtRaw      sql.NullString
		)

		if err := rows.Scan(
			&menuID,
			&menuTitle,
			&priceRaw,
			&activeInt,
			&menuTypeRaw,
			&menuSubtitleRaw,
			&showDishImagesInt,
			&entrantesRaw,
			&principalesRaw,
			&postreRaw,
			&beverageRaw,
			&commentsRaw,
			&minPartySize,
			&mainDishesLimitInt,
			&mainDishesLimitNum,
			&includedCoffeeInt,
			&specialImageURLRaw,
			&legacySourceTable,
			&createdAtRaw,
			&modifiedAtRaw,
		); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
				"success": false,
				"message": "Error leyendo menusDeGrupos",
			})
			return
		}

		menuType := normalizeV2MenuType(menuTypeRaw.String)
		if !isPublicMenuType(menuType) {
			continue
		}

		beverage := map[string]any{
			"type":             "no_incluida",
			"price_per_person": nil,
			"has_supplement":   false,
			"supplement_price": nil,
		}
		if decoded, ok := decodeJSONOrFallback(beverageRaw.String, beverage).(map[string]any); ok {
			beverage = decoded
		}

		price := strings.TrimSpace(priceRaw.String)
		if price == "" {
			price = "0"
		}

		principalesTitle := "Principal a elegir"
		principalesItems := []string{}
		if decoded, ok := decodeJSONOrFallback(principalesRaw.String, map[string]any{}).(map[string]any); ok {
			if title := strings.TrimSpace(anyToString(decoded["titulo_principales"])); title != "" {
				principalesTitle = title
			}
			principalesItems = anySliceToStringList(decoded["items"])
		}

		if minPartySize <= 0 {
			minPartySize = 1
		}
		if mainDishesLimitNum <= 0 {
			mainDishesLimitNum = 1
		}

		item := publicMenuItem{
			ID:        menuID,
			Slug:      buildPublicMenuSlug(menuTitle, menuID),
			MenuTitle: menuTitle,
			MenuType:  menuType,
			Price:     price,
			Active:    activeInt != 0,
			MenuSubtitle: anySliceToStringList(
				decodeJSONOrFallback(menuSubtitleRaw.String, []any{}),
			),
			ShowDishImages: showDishImagesInt != 0,
			Entrantes:      anySliceToStringList(decodeJSONOrFallback(entrantesRaw.String, []any{})),
			Principales: publicMenuPrincipales{
				TituloPrincipales: principalesTitle,
				Items:             principalesItems,
			},
			Postre: anySliceToStringList(decodeJSONOrFallback(postreRaw.String, []any{})),
			Settings: publicMenuSettings{
				IncludedCoffee:       includedCoffeeInt != 0,
				Beverage:             beverage,
				Comments:             anySliceToStringList(decodeJSONOrFallback(commentsRaw.String, []any{})),
				MinPartySize:         minPartySize,
				MainDishesLimit:      mainDishesLimitInt != 0,
				MainDishesLimitCount: mainDishesLimitNum,
			},
			Sections:            []publicMenuSection{},
			SpecialMenuImageURL: s.publicMenuMediaURL(specialImageURLRaw.String),
			LegacySourceTable:   strings.ToUpper(strings.TrimSpace(legacySourceTable.String)),
			CreatedAt:           createdAtRaw.String,
			ModifiedAt:          modifiedAtRaw.String,
		}

		menuIndexByID[menuID] = len(menus)
		menus = append(menus, item)
		menuIDs = append(menuIDs, menuID)
	}

	if len(menuIDs) > 0 {
		sectionByID := make(map[int64]*publicMenuSection, 64)
		sectionsByMenu := make(map[int64][]*publicMenuSection, 24)

		sectionArgs := make([]any, 0, 1+len(menuIDs))
		sectionArgs = append(sectionArgs, restaurantID)
		for _, menuID := range menuIDs {
			sectionArgs = append(sectionArgs, menuID)
		}

		sectionsQuery := fmt.Sprintf(`
			SELECT id, menu_id, title, section_kind, position
			FROM group_menu_sections_v2
			WHERE restaurant_id = ?
			  AND menu_id IN (%s)
			ORDER BY menu_id ASC, position ASC, id ASC
		`, placeholderList(len(menuIDs)))

		sectionRows, err := s.db.QueryContext(r.Context(), sectionsQuery, sectionArgs...)
		if err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
				"success": false,
				"message": "Error consultando secciones de menu",
			})
			return
		}
		for sectionRows.Next() {
			var (
				sectionID   int64
				menuID      int64
				title       string
				sectionKind string
				position    int
			)
			if err := sectionRows.Scan(&sectionID, &menuID, &title, &sectionKind, &position); err != nil {
				sectionRows.Close()
				httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
					"success": false,
					"message": "Error leyendo secciones de menu",
				})
				return
			}
			section := &publicMenuSection{
				ID:       sectionID,
				Title:    title,
				Kind:     normalizeV2SectionKind(sectionKind),
				Position: position,
				Dishes:   []publicMenuDish{},
			}
			sectionsByMenu[menuID] = append(sectionsByMenu[menuID], section)
			sectionByID[sectionID] = section
		}
		if err := sectionRows.Err(); err != nil {
			sectionRows.Close()
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
				"success": false,
				"message": "Error leyendo secciones de menu",
			})
			return
		}
		sectionRows.Close()

		dishArgs := make([]any, 0, 1+len(menuIDs))
		dishArgs = append(dishArgs, restaurantID)
		for _, menuID := range menuIDs {
			dishArgs = append(dishArgs, menuID)
		}

		dishesQuery := fmt.Sprintf(`
			SELECT id, menu_id, section_id, title_snapshot, description_snapshot, allergens_json, foto_path,
			       supplement_enabled, supplement_price, price, position
			FROM group_menu_section_dishes_v2
			WHERE restaurant_id = ?
			  AND menu_id IN (%s)
			  AND active = 1
			ORDER BY menu_id ASC, section_id ASC, position ASC, id ASC
		`, placeholderList(len(menuIDs)))

		dishRows, err := s.db.QueryContext(r.Context(), dishesQuery, dishArgs...)
		if err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
				"success": false,
				"message": "Error consultando platos de menu",
			})
			return
		}
		for dishRows.Next() {
			var (
				dishID          int64
				menuID          int64
				sectionID       int64
				title           string
				description     string
				allergensRaw    sql.NullString
				fotoPath        sql.NullString
				supplementInt   int
				supplementPrice sql.NullFloat64
				priceRaw        sql.NullFloat64
				position        int
			)
			if err := dishRows.Scan(
				&dishID,
				&menuID,
				&sectionID,
				&title,
				&description,
				&allergensRaw,
				&fotoPath,
				&supplementInt,
				&supplementPrice,
				&priceRaw,
				&position,
			); err != nil {
				dishRows.Close()
				httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
					"success": false,
					"message": "Error leyendo platos de menu",
				})
				return
			}

			if _, found := menuIndexByID[menuID]; !found {
				continue
			}
			section := sectionByID[sectionID]
			if section == nil {
				continue
			}

			dish := publicMenuDish{
				ID:                dishID,
				Title:             strings.TrimSpace(title),
				Description:       strings.TrimSpace(description),
				FotoURL:           s.publicMenuMediaURL(fotoPath.String),
				Allergens:         anySliceToStringList(decodeJSONOrFallback(allergensRaw.String, []any{})),
				SupplementEnabled: supplementInt != 0,
				SupplementPrice:   nil,
				Price:             nil,
				Position:          position,
			}
			if supplementPrice.Valid {
				value := supplementPrice.Float64
				dish.SupplementPrice = &value
			}
			if priceRaw.Valid {
				value := priceRaw.Float64
				dish.Price = &value
			}

			section.Dishes = append(section.Dishes, dish)
		}
		if err := dishRows.Err(); err != nil {
			dishRows.Close()
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
				"success": false,
				"message": "Error leyendo platos de menu",
			})
			return
		}
		dishRows.Close()

		for menuID, idx := range menuIndexByID {
			sectionPointers := sectionsByMenu[menuID]
			if len(sectionPointers) == 0 {
				menus[idx].Sections = buildFallbackPublicSections(menus[idx])
				continue
			}

			sections := make([]publicMenuSection, 0, len(sectionPointers))
			hasAnyDish := false
			for _, section := range sectionPointers {
				if len(section.Dishes) > 0 {
					hasAnyDish = true
				}
				sections = append(sections, *section)
			}

			if !hasAnyDish {
				menus[idx].Sections = buildFallbackPublicSections(menus[idx])
				continue
			}
			menus[idx].Sections = sections
		}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"count":   len(menus),
		"menus":   menus,
	})
}
