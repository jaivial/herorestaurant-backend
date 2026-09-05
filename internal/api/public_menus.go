package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

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
	ID                 int64    `json:"id"`
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	DescriptionEnabled bool     `json:"description_enabled"`
	FotoURL            string   `json:"foto_url"`
	Allergens          []string `json:"allergens"`
	SupplementEnabled  bool     `json:"supplement_enabled"`
	SupplementPrice    *float64 `json:"supplement_price"`
	Price              *float64 `json:"price"`
	Position           int      `json:"position"`
	TitleEnglish       string   `json:"title_english,omitempty"`
	DescriptionEnglish string   `json:"description_english,omitempty"`
}

// publicMenuDescription returns the dish description only when the per-dish
// description_enabled flag is on; disabled dishes carry an empty description so
// no consumer can leak text the backoffice asked to hide.
func publicMenuDescription(description string, enabled bool) string {
	if !enabled {
		return ""
	}
	return strings.TrimSpace(description)
}

type publicMenuSection struct {
	ID                  int64            `json:"id"`
	Title               string           `json:"title"`
	DisplayTitle        string           `json:"display_title"`
	Subtitle            string           `json:"subtitle"`
	TabLabel            string           `json:"tab_label"`
	Kind                string           `json:"kind"`
	Position            int              `json:"position"`
	Annotations         []string         `json:"annotations"`
	Dishes              []publicMenuDish `json:"dishes"`
	TitleEnglish        string           `json:"title_english,omitempty"`
	DisplayTitleEnglish string           `json:"display_title_english,omitempty"`
	SubtitleEnglish     string           `json:"subtitle_english,omitempty"`
	TabLabelEnglish     string           `json:"tab_label_english,omitempty"`
	AnnotationsEnglish  []string         `json:"annotations_english,omitempty"`
}

type publicMenuPrincipales struct {
	TituloPrincipales string   `json:"titulo_principales"`
	Items             []string `json:"items"`
}

type publicMenuSettings struct {
	IncludedCoffee       bool             `json:"included_coffee"`
	Beverage             map[string]any   `json:"beverage"`
	Comments             []string         `json:"comments"`
	MinPartySize         int              `json:"min_party_size"`
	MainDishesLimit      bool             `json:"main_dishes_limit"`
	MainDishesLimitCount int              `json:"main_dishes_limit_number"`
	BeverageOptions      []map[string]any `json:"beverage_options"`
	CommentsEnglish      []string         `json:"comments_english,omitempty"`
}

type publicMenuItem struct {
	ID                   int64                 `json:"id"`
	Slug                 string                `json:"slug"`
	MenuTitle            string                `json:"menu_title"`
	MenuType             string                `json:"menu_type"`
	Price                string                `json:"price"`
	Active               bool                  `json:"active"`
	MenuSubtitle         []string              `json:"menu_subtitle"`
	ShowDishImages       bool                  `json:"show_dish_images"`
	ShowSectionTabs      bool                  `json:"show_section_tabs"`
	Entrantes            []string              `json:"entrantes"`
	Principales          publicMenuPrincipales `json:"principales"`
	Postre               []string              `json:"postre"`
	Settings             publicMenuSettings    `json:"settings"`
	Sections             []publicMenuSection   `json:"sections"`
	ShowMenuPreviewImage bool                  `json:"show_menu_preview_image"`
	MenuPreviewImageURL  string                `json:"menu_preview_image_url"`
	SpecialMenuImageURL  string                `json:"special_menu_image_url"`
	LegacySourceTable    string                `json:"legacy_source_table,omitempty"`
	CreatedAt            string                `json:"created_at"`
	ModifiedAt           string                `json:"modified_at"`
	MenuTitleEnglish     string                `json:"menu_title_english,omitempty"`
	MenuSubtitleEnglish  []string              `json:"menu_subtitle_english,omitempty"`
	SliderMode           string                `json:"slider_mode"`
	SliderImages         []string              `json:"slider_images"`
}

// publicMenuItemHome is a lightweight version for the home page
type publicMenuItemHome struct {
	ID                   int64    `json:"id"`
	Slug                 string   `json:"slug"`
	MenuTitle            string   `json:"menu_title"`
	MenuTitleEnglish     string   `json:"menu_title_english,omitempty"`
	MenuType             string   `json:"menu_type"`
	Active               bool     `json:"active"`
	MenuSubtitle         []string `json:"menu_subtitle"`
	MenuSubtitleEnglish  []string `json:"menu_subtitle_english,omitempty"`
	ShowDishImages       bool     `json:"show_dish_images"`
	ShowSectionTabs      bool     `json:"show_section_tabs"`
	ShowMenuPreviewImage bool     `json:"show_menu_preview_image"`
	MenuPreviewImageURL  string   `json:"menu_preview_image_url"`
	SpecialMenuImageURL  string   `json:"special_menu_image_url"`
}

// publicMenuItemSpecial is a minimal response for special menus
type publicMenuItemSpecial struct {
	ID                  int64    `json:"id"`
	MenuTitle           string   `json:"menu_title"`
	MenuSubtitle        []string `json:"menu_subtitle"`
	Comments            []string `json:"comments"`
	SpecialMenuImageURL string   `json:"special_menu_image_url"`
}

func (s *Server) getPageVisibility(ctx context.Context, restaurantID int) (cafeActive bool, bebidasActive bool) {
	cafeActive = true
	bebidasActive = true
	var cafe, bebidas int
	err := s.db.QueryRowContext(ctx,
		"SELECT cafe_page_active, bebidas_page_active FROM restaurant_page_visibility WHERE restaurant_id = ?",
		restaurantID,
	).Scan(&cafe, &bebidas)
	if err == nil {
		cafeActive = cafe != 0
		bebidasActive = bebidas != 0
	}
	return
}

func (s *Server) updatePageVisibility(ctx context.Context, restaurantID int, cafeActive *bool, bebidasActive *bool) error {
	if cafeActive == nil && bebidasActive == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO restaurant_page_visibility (restaurant_id, cafe_page_active, bebidas_page_active)
		VALUES (?, COALESCE(?, 1), COALESCE(?, 1))
		ON DUPLICATE KEY UPDATE
		  cafe_page_active = COALESCE(?, cafe_page_active),
		  bebidas_page_active = COALESCE(?, bebidas_page_active)
	`, restaurantID,
		cafeActive, bebidasActive,
		cafeActive, bebidasActive,
	)
	return err
}

func (s *Server) handleGetPageVisibility(w http.ResponseWriter, r *http.Request) {
	var restaurantID int
	if a, ok := boAuthFromContext(r.Context()); ok && a.ActiveRestaurantID > 0 {
		restaurantID = a.ActiveRestaurantID
	} else {
		httpx.WriteError(w, http.StatusInternalServerError, "Restaurant not found")
		return
	}

	cafeActive, bebidasActive := s.getPageVisibility(r.Context(), restaurantID)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":             true,
		"cafe_page_active":    cafeActive,
		"bebidas_page_active": bebidasActive,
	})
}

func (s *Server) handlePageVisibilityPatch(w http.ResponseWriter, r *http.Request) {
	var restaurantID int
	if a, ok := boAuthFromContext(r.Context()); ok && a.ActiveRestaurantID > 0 {
		restaurantID = a.ActiveRestaurantID
	} else {
		httpx.WriteError(w, http.StatusInternalServerError, "Restaurant not found")
		return
	}

	var input struct {
		CafeActive    *bool `json:"cafe_page_active,omitempty"`
		BebidasActive *bool `json:"bebidas_page_active,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if err := s.updatePageVisibility(r.Context(), restaurantID, input.CafeActive, input.BebidasActive); err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Error updating page visibility",
		})
		return
	}

	cafeActive, bebidasActive := s.getPageVisibility(r.Context(), restaurantID)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":             true,
		"cafe_page_active":    cafeActive,
		"bebidas_page_active": bebidasActive,
	})
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

func (s *Server) publicMenuMediaURL(ctx context.Context, restaurantID int, raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return value
	}
	return s.bunnyPullURL(ctx, restaurantID, value)
}

// loadPublicSliderImages returns the slider mode and the image URLs a public
// menu should show, filtered by mode: default→is_default=1, custom→is_default=0,
// both→all, hidden→none. Falls back gracefully if the schema is missing.
func (s *Server) loadPublicSliderImages(ctx context.Context, restaurantID int, menuID int64) (string, []string) {
	mode := "default"
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(slider_mode, 'default') FROM menus WHERE id = ? AND restaurant_id = ?`,
		menuID, restaurantID).Scan(&mode); err != nil {
		mode = "default"
	}
	if mode == "hidden" {
		return mode, []string{}
	}

	query := `SELECT image_path FROM menu_slider_images WHERE restaurant_id = ? AND menu_id = ?`
	switch mode {
	case "default":
		query += ` AND is_default = 1`
	case "custom":
		query += ` AND is_default = 0`
	}
	query += ` ORDER BY position ASC, id ASC`

	rows, err := s.db.QueryContext(ctx, query, restaurantID, menuID)
	if err != nil {
		return mode, []string{}
	}
	defer rows.Close()

	images := make([]string, 0, 8)
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			continue
		}
		if url := s.publicMenuMediaURL(ctx, restaurantID, path); url != "" {
			images = append(images, url)
		}
	}
	return mode, images
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
			ID:          0,
			Title:       "Entrantes",
			Kind:        "entrantes",
			Position:    len(out),
			Annotations: []string{},
			Dishes:      entrantes,
		})
	}

	principales := buildFallbackPublicSectionDishes(menu.Principales.Items)
	if len(principales) > 0 {
		sectionTitle := strings.TrimSpace(menu.Principales.TituloPrincipales)
		if sectionTitle == "" {
			sectionTitle = "Principales"
		}
		out = append(out, publicMenuSection{
			ID:          0,
			Title:       sectionTitle,
			Kind:        "principales",
			Position:    len(out),
			Annotations: []string{},
			Dishes:      principales,
		})
	}

	postres := buildFallbackPublicSectionDishes(menu.Postre)
	if len(postres) > 0 {
		out = append(out, publicMenuSection{
			ID:          0,
			Title:       "Postres",
			Kind:        "postres",
			Position:    len(out),
			Annotations: []string{},
			Dishes:      postres,
		})
	}

	return out
}

func (s *Server) handlePublicMenus(w http.ResponseWriter, r *http.Request) {
	menuIDParam := r.URL.Query().Get("id")
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusInternalServerError, "Restaurant not found")
		return
	}
	if menuIDParam != "" {
		s.handlePublicMenuByID(w, r, restaurantID, menuIDParam)
		return
	}

	isHomePage := r.URL.Query().Get("home_page") == "true"

	selectFields := "id, menu_title, menu_type, active, menu_subtitle, show_dish_images, show_section_tabs, show_menu_preview_image, menu_preview_image_path, special_menu_image_url"
	if !isHomePage {
		selectFields = `id, menu_title, price, active, menu_type, menu_subtitle,
		       show_dish_images, show_section_tabs, show_menu_preview_image, menu_preview_image_path, entrantes, principales, postre, beverage, comments,
		       min_party_size, main_dishes_limit, main_dishes_limit_number, included_coffee,
		       special_menu_image_url, legacy_source_table, created_at, modified_at`
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT `+selectFields+`
		FROM menus
		WHERE restaurant_id = ?
		  AND active = 1
		  AND is_draft = 0
		  AND COALESCE(NULLIF(TRIM(menu_type), ''), 'closed_conventional') IN ('closed_conventional', 'closed_group', 'a_la_carte', 'a_la_carte_group', 'special')
		ORDER BY
		  CASE COALESCE(NULLIF(TRIM(menu_type), ''), 'closed_conventional')
		    WHEN 'closed_conventional' THEN 1
		    WHEN 'a_la_carte' THEN 2
		    WHEN 'closed_group' THEN 3
		    WHEN 'a_la_carte_group' THEN 4
		    WHEN 'special' THEN 5
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

	// Handle home page case (lightweight response)
	if isHomePage {
		menus := make([]publicMenuItemHome, 0, 24)
		for rows.Next() {
			var (
				menuID                  int64
				menuTitle               string
				menuTypeRaw             sql.NullString
				activeInt               int
				menuSubtitleRaw         sql.NullString
				showDishImagesInt       int
				showSectionTabsInt      int
				showMenuPreviewImageInt int
				menuPreviewPathRaw      sql.NullString
				specialImageURLRaw      sql.NullString
			)
			if err := rows.Scan(
				&menuID,
				&menuTitle,
				&menuTypeRaw,
				&activeInt,
				&menuSubtitleRaw,
				&showDishImagesInt,
				&showSectionTabsInt,
				&showMenuPreviewImageInt,
				&menuPreviewPathRaw,
				&specialImageURLRaw,
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

			menus = append(menus, publicMenuItemHome{
				ID:                   menuID,
				Slug:                 buildPublicMenuSlug(menuTitle, menuID),
				MenuTitle:            menuTitle,
				MenuType:             menuType,
				Active:               activeInt != 0,
				MenuSubtitle:         anySliceToStringList(decodeJSONOrFallback(menuSubtitleRaw.String, []any{})),
				ShowDishImages:       showDishImagesInt != 0,
				ShowSectionTabs:      showSectionTabsInt != 0,
				ShowMenuPreviewImage: showMenuPreviewImageInt != 0,
				MenuPreviewImageURL:  s.publicMenuMediaURL(r.Context(), restaurantID, menuPreviewPathRaw.String),
				SpecialMenuImageURL:  s.publicMenuMediaURL(r.Context(), restaurantID, specialImageURLRaw.String),
			})
		}

		s.enrichPublicHomeMenus(r.Context(), restaurantID, menus)

		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": true,
			"count":   len(menus),
			"menus":   menus,
		})
		return
	}

	// Full response (non-home page)
	menus := make([]publicMenuItem, 0, 24)
	menuIndexByID := make(map[int64]int, 24)
	menuIDs := make([]int64, 0, 24)

	for rows.Next() {
		var (
			menuID                  int64
			menuTitle               string
			priceRaw                sql.NullString
			activeInt               int
			menuTypeRaw             sql.NullString
			menuSubtitleRaw         sql.NullString
			showDishImagesInt       int
			showSectionTabsInt      int
			showMenuPreviewImageInt int
			menuPreviewPathRaw      sql.NullString
			entrantesRaw            sql.NullString
			principalesRaw          sql.NullString
			postreRaw               sql.NullString
			beverageRaw             sql.NullString
			commentsRaw             sql.NullString
			minPartySize            int
			mainDishesLimitInt      int
			mainDishesLimitNum      int
			includedCoffeeInt       int
			specialImageURLRaw      sql.NullString
			legacySourceTable       sql.NullString
			createdAtRaw            sql.NullString
			modifiedAtRaw           sql.NullString
		)

		if err := rows.Scan(
			&menuID,
			&menuTitle,
			&priceRaw,
			&activeInt,
			&menuTypeRaw,
			&menuSubtitleRaw,
			&showDishImagesInt,
			&showSectionTabsInt,
			&showMenuPreviewImageInt,
			&menuPreviewPathRaw,
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
			ShowDishImages:  showDishImagesInt != 0,
			ShowSectionTabs: showSectionTabsInt != 0,
			Entrantes:       anySliceToStringList(decodeJSONOrFallback(entrantesRaw.String, []any{})),
			Principales: publicMenuPrincipales{
				TituloPrincipales: principalesTitle,
				Items:             principalesItems,
			},
			Postre: anySliceToStringList(decodeJSONOrFallback(postreRaw.String, []any{})),
			Settings: publicMenuSettings{
				IncludedCoffee:       includedCoffeeInt != 0,
				Beverage:             beverage,
				BeverageOptions:      s.menuBeverageOptionsPayload(restaurantID, menuID),
				Comments:             anySliceToStringList(decodeJSONOrFallback(commentsRaw.String, []any{})),
				MinPartySize:         minPartySize,
				MainDishesLimit:      mainDishesLimitInt != 0,
				MainDishesLimitCount: mainDishesLimitNum,
			},
			Sections:             []publicMenuSection{},
			ShowMenuPreviewImage: showMenuPreviewImageInt != 0,
			MenuPreviewImageURL:  s.publicMenuMediaURL(r.Context(), restaurantID, menuPreviewPathRaw.String),
			SpecialMenuImageURL:  s.publicMenuMediaURL(r.Context(), restaurantID, specialImageURLRaw.String),
			LegacySourceTable:    strings.ToUpper(strings.TrimSpace(legacySourceTable.String)),
			CreatedAt:            createdAtRaw.String,
			ModifiedAt:           modifiedAtRaw.String,
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
			SELECT id, menu_id, title, COALESCE(display_title, ''), COALESCE(subtitle, ''), COALESCE(tab_label, ''), section_kind, position, COALESCE(annotations_json, '')
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
				sectionID      int64
				menuID         int64
				title          string
				displayTitle   string
				subtitle       string
				tabLabel       string
				sectionKind    string
				position       int
				annotationsRaw sql.NullString
			)
			if err := sectionRows.Scan(&sectionID, &menuID, &title, &displayTitle, &subtitle, &tabLabel, &sectionKind, &position, &annotationsRaw); err != nil {
				sectionRows.Close()
				httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
					"success": false,
					"message": "Error leyendo secciones de menu",
				})
				return
			}
			// Fall back to the legacy title when display_title has not been
			// populated yet so older menus still render.
			if strings.TrimSpace(displayTitle) == "" {
				displayTitle = title
			}
			section := &publicMenuSection{
				ID:           sectionID,
				Title:        title,
				DisplayTitle: displayTitle,
				Subtitle:     subtitle,
				TabLabel:     tabLabel,
				Kind:         normalizeV2SectionKind(sectionKind),
				Position:     position,
				Annotations:  normalizeV2SectionAnnotations(anySliceToStringList(decodeJSONOrFallback(annotationsRaw.String, []any{}))),
				Dishes:       []publicMenuDish{},
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
			SELECT id, menu_id, section_id, title_snapshot, description_snapshot, COALESCE(description_enabled, 1), allergens_json, foto_path,
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
				dishID             int64
				menuID             int64
				sectionID          int64
				title              string
				description        string
				descriptionEnabled int
				allergensRaw       sql.NullString
				fotoPath           sql.NullString
				supplementInt      int
				supplementPrice    sql.NullFloat64
				priceRaw           sql.NullFloat64
				position           int
			)
			if err := dishRows.Scan(
				&dishID,
				&menuID,
				&sectionID,
				&title,
				&description,
				&descriptionEnabled,
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
				ID:                 dishID,
				Title:              strings.TrimSpace(title),
				Description:        publicMenuDescription(description, descriptionEnabled != 0),
				DescriptionEnabled: descriptionEnabled != 0,
				FotoURL:            s.publicMenuMediaURL(r.Context(), restaurantID, fotoPath.String),
				Allergens:          anySliceToStringList(decodeJSONOrFallback(allergensRaw.String, []any{})),
				SupplementEnabled:  supplementInt != 0,
				SupplementPrice:    nil,
				Price:              nil,
				Position:           position,
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

	s.enrichPublicMenus(r.Context(), restaurantID, menus)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"count":   len(menus),
		"menus":   menus,
	})
}

func (s *Server) handlePublicMenuByID(w http.ResponseWriter, r *http.Request, restaurantID int, menuIDParam string) {
	echoCorrelationID(w, r)
	logCheckpoint(r, "public_menu_by_id_request_received", "menu_id", menuIDParam)

	menuID, err := strconv.ParseInt(menuIDParam, 10, 64)
	if err != nil || menuID <= 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid menu id",
		})
		return
	}

	// First, get the menu type to determine response format
	var menuType string
	logCheckpoint(r, "public_menu_by_id_db_query_started", "menu_id", menuIDParam)
	err = s.db.QueryRowContext(r.Context(), `
		SELECT COALESCE(NULLIF(TRIM(menu_type), ''), 'closed_conventional')
		FROM menus
		WHERE id = ? AND restaurant_id = ? AND active = 1 AND is_draft = 0
	`, menuID, restaurantID).Scan(&menuType)
	if err != nil {
		if err == sql.ErrNoRows {
			httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
				"success": false,
				"message": "Menu not found",
			})
			return
		}
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Error fetching menu",
		})
		return
	}
	logCheckpoint(r, "public_menu_by_id_db_query_completed", "menu_type", menuType)

	// If menu type is "special", return minimal response
	if menuType == "special" {
		var (
			menuTitle       string
			menuSubtitleRaw sql.NullString
			commentsRaw     sql.NullString
			specialImageURL sql.NullString
		)
		err = s.db.QueryRowContext(r.Context(), `
			SELECT menu_title, menu_subtitle, comments, special_menu_image_url
			FROM menus
			WHERE id = ? AND restaurant_id = ?
		`, menuID, restaurantID).Scan(&menuTitle, &menuSubtitleRaw, &commentsRaw, &specialImageURL)
		if err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
				"success": false,
				"message": "Error fetching special menu",
			})
			return
		}

		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": true,
			"menu": publicMenuItemSpecial{
				ID:                  menuID,
				MenuTitle:           menuTitle,
				MenuSubtitle:        anySliceToStringList(decodeJSONOrFallback(menuSubtitleRaw.String, []any{})),
				Comments:            anySliceToStringList(decodeJSONOrFallback(commentsRaw.String, []any{})),
				SpecialMenuImageURL: s.publicMenuMediaURL(r.Context(), restaurantID, specialImageURL.String),
			},
		})
		return
	}

	// For non-special menus, return full menu data (reuse existing logic)
	// This would be the same as the full response in handlePublicMenus
	logCheckpoint(r, "public_menu_by_id_response_sent", "menu_id", menuIDParam, "menu_type", menuType)
	s.handleFullPublicMenuByID(w, r, int64(restaurantID), menuID)
}

func (s *Server) handleFullPublicMenuByID(w http.ResponseWriter, r *http.Request, restaurantID int64, menuID int64) {
	var (
		menuTitle               string
		priceRaw                sql.NullString
		activeInt               int
		menuTypeRaw             sql.NullString
		menuSubtitleRaw         sql.NullString
		showDishImagesInt       int
		showSectionTabsInt      int
		showMenuPreviewImageInt int
		menuPreviewPathRaw      sql.NullString
		entrantesRaw            sql.NullString
		principalesRaw          sql.NullString
		postreRaw               sql.NullString
		beverageRaw             sql.NullString
		commentsRaw             sql.NullString
		minPartySize            int
		mainDishesLimitInt      int
		mainDishesLimitNum      int
		includedCoffeeInt       int
		specialImageURLRaw      sql.NullString
		legacySourceTable       sql.NullString
		createdAtRaw            sql.NullString
		modifiedAtRaw           sql.NullString
	)

	err := s.db.QueryRowContext(r.Context(), `
		SELECT id, menu_title, price, active, menu_type, menu_subtitle,
		       show_dish_images, show_section_tabs, show_menu_preview_image, menu_preview_image_path, entrantes, principales, postre, beverage, comments,
		       min_party_size, main_dishes_limit, main_dishes_limit_number, included_coffee,
		       special_menu_image_url, legacy_source_table, created_at, modified_at
		FROM menus
		WHERE id = ? AND restaurant_id = ?
	`, menuID, restaurantID).Scan(
		&menuID,
		&menuTitle,
		&priceRaw,
		&activeInt,
		&menuTypeRaw,
		&menuSubtitleRaw,
		&showDishImagesInt,
		&showSectionTabsInt,
		&showMenuPreviewImageInt,
		&menuPreviewPathRaw,
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
	)
	if err != nil {
		if err == sql.ErrNoRows {
			httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
				"success": false,
				"message": "Menu not found",
			})
			return
		}
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Error fetching menu",
		})
		return
	}

	menuType := normalizeV2MenuType(menuTypeRaw.String)
	if !isPublicMenuType(menuType) {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Menu not found",
		})
		return
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
		ShowDishImages:  showDishImagesInt != 0,
		ShowSectionTabs: showSectionTabsInt != 0,
		Entrantes:       anySliceToStringList(decodeJSONOrFallback(entrantesRaw.String, []any{})),
		Principales: publicMenuPrincipales{
			TituloPrincipales: principalesTitle,
			Items:             principalesItems,
		},
		Postre: anySliceToStringList(decodeJSONOrFallback(postreRaw.String, []any{})),
		Settings: publicMenuSettings{
			IncludedCoffee:       includedCoffeeInt != 0,
			Beverage:             beverage,
			BeverageOptions:      s.menuBeverageOptionsPayload(int(restaurantID), menuID),
			Comments:             anySliceToStringList(decodeJSONOrFallback(commentsRaw.String, []any{})),
			MinPartySize:         minPartySize,
			MainDishesLimit:      mainDishesLimitInt != 0,
			MainDishesLimitCount: mainDishesLimitNum,
		},
		Sections:             []publicMenuSection{},
		ShowMenuPreviewImage: showMenuPreviewImageInt != 0,
		MenuPreviewImageURL:  s.publicMenuMediaURL(r.Context(), int(restaurantID), menuPreviewPathRaw.String),
		SpecialMenuImageURL:  s.publicMenuMediaURL(r.Context(), int(restaurantID), specialImageURLRaw.String),
		LegacySourceTable:    strings.ToUpper(strings.TrimSpace(legacySourceTable.String)),
		CreatedAt:            createdAtRaw.String,
		ModifiedAt:           modifiedAtRaw.String,
	}

	// Slider mode + images (filtered by mode). Absent column → default.
	item.SliderMode, item.SliderImages = s.loadPublicSliderImages(r.Context(), int(restaurantID), menuID)

	// Fetch sections and dishes
	sectionByID := make(map[int64]*publicMenuSection, 64)
	sectionsByMenu := make(map[int64][]*publicMenuSection, 1)

	sectionRows, err := s.db.QueryContext(r.Context(), `
		SELECT id, menu_id, title, COALESCE(display_title, ''), COALESCE(subtitle, ''), COALESCE(tab_label, ''), section_kind, position, COALESCE(annotations_json, '')
		FROM group_menu_sections_v2
		WHERE restaurant_id = ? AND menu_id = ?
		ORDER BY position ASC, id ASC
	`, restaurantID, menuID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Error fetching menu sections",
		})
		return
	}
	for sectionRows.Next() {
		var (
			sectionID      int64
			secMenuID      int64
			title          string
			displayTitle   string
			subtitle       string
			tabLabel       string
			sectionKind    string
			position       int
			annotationsRaw sql.NullString
		)
		if err := sectionRows.Scan(&sectionID, &secMenuID, &title, &displayTitle, &subtitle, &tabLabel, &sectionKind, &position, &annotationsRaw); err != nil {
			sectionRows.Close()
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
				"success": false,
				"message": "Error reading menu sections",
			})
			return
		}
		if strings.TrimSpace(displayTitle) == "" {
			displayTitle = title
		}
		section := &publicMenuSection{
			ID:           sectionID,
			Title:        title,
			DisplayTitle: displayTitle,
			Subtitle:     subtitle,
			TabLabel:     tabLabel,
			Kind:         normalizeV2SectionKind(sectionKind),
			Position:     position,
			Annotations:  normalizeV2SectionAnnotations(anySliceToStringList(decodeJSONOrFallback(annotationsRaw.String, []any{}))),
			Dishes:       []publicMenuDish{},
		}
		sectionsByMenu[secMenuID] = append(sectionsByMenu[secMenuID], section)
		sectionByID[sectionID] = section
	}
	sectionRows.Close()

	dishRows, err := s.db.QueryContext(r.Context(), `
		SELECT id, menu_id, section_id, title_snapshot, description_snapshot, COALESCE(description_enabled, 1), allergens_json, foto_path,
		       supplement_enabled, supplement_price, price, position
		FROM group_menu_section_dishes_v2
		WHERE restaurant_id = ? AND menu_id = ? AND active = 1
		ORDER BY section_id ASC, position ASC, id ASC
	`, restaurantID, menuID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Error fetching menu dishes",
		})
		return
	}
	for dishRows.Next() {
		var (
			dishID             int64
			dishMenuID         int64
			sectionID          int64
			title              string
			description        string
			descriptionEnabled int
			allergensRaw       sql.NullString
			fotoPath           sql.NullString
			supplementInt      int
			supplementPrice    sql.NullFloat64
			priceRaw           sql.NullFloat64
			position           int
		)
		if err := dishRows.Scan(
			&dishID,
			&dishMenuID,
			&sectionID,
			&title,
			&description,
			&descriptionEnabled,
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
				"message": "Error reading menu dishes",
			})
			return
		}

		section := sectionByID[sectionID]
		if section == nil {
			continue
		}

		dish := publicMenuDish{
			ID:                 dishID,
			Title:              strings.TrimSpace(title),
			Description:        publicMenuDescription(description, descriptionEnabled != 0),
			DescriptionEnabled: descriptionEnabled != 0,
			FotoURL:            s.publicMenuMediaURL(r.Context(), int(restaurantID), fotoPath.String),
			Allergens:          anySliceToStringList(decodeJSONOrFallback(allergensRaw.String, []any{})),
			SupplementEnabled:  supplementInt != 0,
			SupplementPrice:    nil,
			Price:              nil,
			Position:           position,
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
	dishRows.Close()

	sectionPointers := sectionsByMenu[menuID]
	if len(sectionPointers) == 0 {
		item.Sections = buildFallbackPublicSections(item)
	} else {
		sections := make([]publicMenuSection, 0, len(sectionPointers))
		hasAnyDish := false
		for _, section := range sectionPointers {
			if len(section.Dishes) > 0 {
				hasAnyDish = true
			}
			sections = append(sections, *section)
		}

		if !hasAnyDish {
			item.Sections = buildFallbackPublicSections(item)
		} else {
			item.Sections = sections
		}
	}

	s.enrichPublicMenu(r.Context(), int(restaurantID), &item)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"menu":    item,
	})
}

// publicMenuSidebarItem is a minimal representation for the burger nav sidebar.
type publicMenuSidebarItem struct {
	ID                int64  `json:"id"`
	Slug              string `json:"slug"`
	MenuTitle         string `json:"menu_title"`
	MenuType          string `json:"menu_type"`
	Active            bool   `json:"active"`
	LegacySourceTable string `json:"legacy_source_table,omitempty"`
}

// activePublicMenuOrderBy returns the same ORDER BY clause used across menu list endpoints.
func activePublicMenuOrderBy() string {
	return `CASE COALESCE(NULLIF(TRIM(menu_type), ''), 'closed_conventional')
		    WHEN 'closed_conventional' THEN 1
		    WHEN 'a_la_carte' THEN 2
		    WHEN 'closed_group' THEN 3
		    WHEN 'a_la_carte_group' THEN 4
		    WHEN 'special' THEN 5
		    ELSE 9
		  END ASC,
		  modified_at DESC,
		  id DESC`
}

// activePublicMenuWhere returns the common WHERE clause for public active non-draft menus.
func activePublicMenuWhere() string {
	return `restaurant_id = ?
		  AND active = 1
		  AND is_draft = 0
		  AND COALESCE(NULLIF(TRIM(menu_type), ''), 'closed_conventional') IN ('closed_conventional', 'closed_group', 'a_la_carte', 'a_la_carte_group', 'special')`
}

// activePublicMenuWhereAliased is activePublicMenuWhere for queries that join
// the menus table under an alias. Keeps the public-visibility criteria single-sourced.
func activePublicMenuWhereAliased(alias string) string {
	return alias + `.restaurant_id = ?
		  AND ` + alias + `.active = 1
		  AND ` + alias + `.is_draft = 0
		  AND COALESCE(NULLIF(TRIM(` + alias + `.menu_type), ''), 'closed_conventional') IN ('closed_conventional', 'closed_group', 'a_la_carte', 'a_la_carte_group', 'special')`
}

// handlePublicMenuByRouteID handles GET /menus/{menuID}.
// It extracts the menuID from the chi URL param and delegates to handlePublicMenuByID.
func (s *Server) handlePublicMenuByRouteID(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusInternalServerError, "Restaurant not found")
		return
	}
	menuIDParam := chi.URLParam(r, "menuID")
	s.handlePublicMenuByID(w, r, restaurantID, menuIDParam)
}

// handlePublicMenusSidebar handles GET /menus/sidebar.
// Returns a lightweight list of active menus with only id, slug, menu_title, menu_type, active,
// plus page visibility flags for cafe/bebidas public pages.
func (s *Server) handlePublicMenusSidebar(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusInternalServerError, "Restaurant not found")
		return
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id, menu_title, COALESCE(NULLIF(TRIM(menu_type), ''), 'closed_conventional'), COALESCE(legacy_source_table, '')
		FROM menus
		WHERE `+activePublicMenuWhere()+`
		ORDER BY `+activePublicMenuOrderBy(),
		restaurantID,
	)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Error querying menus",
		})
		return
	}
	defer rows.Close()

	menus := make([]publicMenuSidebarItem, 0, 24)
	for rows.Next() {
		var (
			menuID            int64
			menuTitle         string
			menuTypeRaw       string
			legacySourceTable string
		)
		if err := rows.Scan(&menuID, &menuTitle, &menuTypeRaw, &legacySourceTable); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
				"success": false,
				"message": "Error reading menus",
			})
			return
		}

		menuType := normalizeV2MenuType(menuTypeRaw)
		if !isPublicMenuType(menuType) {
			continue
		}

		menus = append(menus, publicMenuSidebarItem{
			ID:                menuID,
			Slug:              buildPublicMenuSlug(menuTitle, menuID),
			MenuTitle:         menuTitle,
			MenuType:          menuType,
			Active:            true, // All results are active due to WHERE clause
			LegacySourceTable: strings.ToUpper(strings.TrimSpace(legacySourceTable)),
		})
	}

	cafeActive, bebidasActive := s.getPageVisibility(r.Context(), restaurantID)

	visibleSections, err := s.loadPublicVisibleSections(r, restaurantID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Error querying visible sections",
		})
		return
	}

	logCheckpoint(r, "public_sidebar_sections_resolved",
		"visible_sections", strconv.Itoa(len(visibleSections)))

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":             true,
		"count":               len(menus),
		"menus":               menus,
		"cafe_page_active":    cafeActive,
		"bebidas_page_active": bebidasActive,
		"visible_sections":    visibleSections,
	})
}

// publicSidebarSection is a menu section the restaurant chose to surface in the
// public navigation, plus where it should appear.
// Coordination id: menu_section_public_placement_v1
type publicSidebarSection struct {
	ID           int64  `json:"id"`
	MenuID       int64  `json:"menu_id"`
	Title        string `json:"title"`
	Kind         string `json:"kind"`
	WebPlacement string `json:"web_placement"`
	Href         string `json:"href"`
}

// loadPublicVisibleSections returns sections flagged public_page_active that
// belong to an active, publicly visible menu.
func (s *Server) loadPublicVisibleSections(r *http.Request, restaurantID int) ([]publicSidebarSection, error) {
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT sec.id, sec.menu_id, sec.title, sec.section_kind,
		       COALESCE(sec.web_placement, 'inside_menus'), m.menu_title
		FROM group_menu_sections_v2 sec
		INNER JOIN menus m ON m.id = sec.menu_id AND m.restaurant_id = sec.restaurant_id
		WHERE sec.restaurant_id = ? AND COALESCE(sec.public_page_active, 0) = 1
		  AND `+activePublicMenuWhereAliased("m")+`
		ORDER BY sec.position ASC, sec.id ASC
	`, restaurantID, restaurantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]publicSidebarSection, 0, 8)
	for rows.Next() {
		var sec publicSidebarSection
		var menuTitle string
		if err := rows.Scan(&sec.ID, &sec.MenuID, &sec.Title, &sec.Kind, &sec.WebPlacement, &menuTitle); err != nil {
			return nil, err
		}
		sec.Kind = normalizeV2SectionKind(sec.Kind)
		sec.WebPlacement = normalizeV2SectionWebPlacement(sec.WebPlacement)
		sec.Href = fmt.Sprintf("/menu/%d/%s#seccion-%d", sec.MenuID, buildPublicMenuSlug(menuTitle, sec.MenuID), sec.ID)
		out = append(out, sec)
	}
	return out, rows.Err()
}

// handlePublicMenusHome handles GET /menus/home.
// Returns a lightweight list of active menus for the homepage cards section.
func (s *Server) handlePublicMenusHome(w http.ResponseWriter, r *http.Request) {
	echoCorrelationID(w, r)
	logCheckpoint(r, "home_menus_request_received")
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusInternalServerError, "Restaurant not found")
		return
	}

	logCheckpoint(r, "home_menus_db_query_started", "restaurant_id", strconv.Itoa(restaurantID))
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id, menu_title, menu_type, active, menu_subtitle,
		       show_menu_preview_image, menu_preview_image_path
		FROM menus
		WHERE `+activePublicMenuWhere()+`
		ORDER BY `+activePublicMenuOrderBy(),
		restaurantID,
	)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Error querying menus",
		})
		return
	}
	defer rows.Close()

	menus := make([]publicMenuItemHome, 0, 24)
	for rows.Next() {
		var (
			menuID                  int64
			menuTitle               string
			menuTypeRaw             sql.NullString
			activeInt               int
			menuSubtitleRaw         sql.NullString
			showMenuPreviewImageInt int
			menuPreviewPathRaw      sql.NullString
		)
		if err := rows.Scan(
			&menuID,
			&menuTitle,
			&menuTypeRaw,
			&activeInt,
			&menuSubtitleRaw,
			&showMenuPreviewImageInt,
			&menuPreviewPathRaw,
		); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
				"success": false,
				"message": "Error reading menus",
			})
			return
		}

		menuType := normalizeV2MenuType(menuTypeRaw.String)
		if !isPublicMenuType(menuType) {
			continue
		}

		menus = append(menus, publicMenuItemHome{
			ID:                   menuID,
			Slug:                 buildPublicMenuSlug(menuTitle, menuID),
			MenuTitle:            menuTitle,
			MenuType:             menuType,
			Active:               activeInt != 0,
			MenuSubtitle:         anySliceToStringList(decodeJSONOrFallback(menuSubtitleRaw.String, []any{})),
			ShowMenuPreviewImage: showMenuPreviewImageInt != 0,
			MenuPreviewImageURL:  s.publicMenuMediaURL(r.Context(), restaurantID, menuPreviewPathRaw.String),
		})
	}

	s.enrichPublicHomeMenus(r.Context(), restaurantID, menus)
	logCheckpoint(r, "home_menus_db_query_completed", "menu_count", strconv.Itoa(len(menus)))

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"count":   len(menus),
		"menus":   menus,
	})
	logCheckpoint(r, "home_menus_response_sent", "menu_count", strconv.Itoa(len(menus)))
}
