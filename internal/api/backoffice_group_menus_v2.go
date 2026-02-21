package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"preactvillacarmen/internal/httpx"
)

type boV2Section struct {
	ID       int64      `json:"id"`
	Title    string     `json:"title"`
	Kind     string     `json:"kind"`
	Position int        `json:"position"`
	Dishes   []boV2Dish `json:"dishes"`
}

type boV2Dish struct {
	ID                int64    `json:"id"`
	SectionID         int64    `json:"section_id"`
	CatalogDishID     *int64   `json:"catalog_dish_id,omitempty"`
	Title             string   `json:"title"`
	Description       string   `json:"description"`
	Allergens         []string `json:"allergens"`
	SupplementEnabled bool     `json:"supplement_enabled"`
	SupplementPrice   *float64 `json:"supplement_price"`
	Price             *float64 `json:"price"`
	Active            bool     `json:"active"`
	Position          int      `json:"position"`
	FotoURL           *string  `json:"foto_url,omitempty"`
	ImageURL          *string  `json:"image_url,omitempty"`
	AIRequestedImg    bool     `json:"ai_requested_img"`
	AIGeneratingImg   bool     `json:"ai_generating_img"`
	AIGeneratedImg    *string  `json:"ai_generated_img,omitempty"`
}

func normalizeV2MenuType(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	switch s {
	case "closed_group":
		return "closed_group"
	case "a_la_carte", "a_la_carta":
		return "a_la_carte"
	case "a_la_carte_group", "a_la_carta_grupo":
		return "a_la_carte_group"
	case "a_la_carte_time":
		return "a_la_carte_time"
	case "special":
		return "special"
	case "closed_conventional", "closed", "":
		return "closed_conventional"
	default:
		return "closed_conventional"
	}
}

func normalizeV2SectionKind(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	switch s {
	case "entrantes", "starter", "starters":
		return "entrantes"
	case "principales", "main", "mains":
		return "principales"
	case "postre", "postres", "dessert", "desserts":
		return "postres"
	case "arroces", "rice":
		return "arroces"
	case "bebidas", "beverages", "drinks":
		return "bebidas"
	default:
		return "custom"
	}
}

func clampIntBound(v, min, max, def int) int {
	if v < min || v > max {
		return def
	}
	return v
}

func parseChiPositiveInt64(r *http.Request, key string) (int64, error) {
	raw := strings.TrimSpace(chi.URLParam(r, key))
	if raw == "" {
		return 0, errors.New("missing")
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid")
	}
	return id, nil
}

func placeholderList(n int) string {
	if n <= 0 {
		return ""
	}
	parts := make([]string, n)
	for i := range parts {
		parts[i] = "?"
	}
	return strings.Join(parts, ",")
}

func anySliceToStringList(v any) []string {
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, it := range x {
			s := strings.TrimSpace(anyToString(it))
			if s == "" {
				continue
			}
			out = append(out, s)
		}
		return out
	case nil:
		return []string{}
	default:
		return []string{}
	}
}

func (s *Server) ensureBOMenuV2Belongs(restaurantID int, menuID int64) (bool, error) {
	var tmp int64
	err := s.db.QueryRow("SELECT id FROM menusDeGrupos WHERE id = ? AND restaurant_id = ? LIMIT 1", menuID, restaurantID).Scan(&tmp)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, err
}

func (s *Server) handleBOGroupMenusV2List(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	includeDrafts := strings.TrimSpace(r.URL.Query().Get("includeDrafts")) == "1"

	where := "WHERE restaurant_id = ?"
	args := []any{a.ActiveRestaurantID}
	if !includeDrafts {
		where += " AND is_draft = 0"
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id, menu_title, price, active, is_draft, menu_type, created_at, modified_at
		FROM menusDeGrupos
	`+where+`
		ORDER BY active DESC, modified_at DESC, id DESC
	`, args...)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando menusDeGrupos")
		return
	}
	defer rows.Close()

	out := make([]map[string]any, 0, 16)
	for rows.Next() {
		var (
			id         int64
			title      string
			price      string
			activeInt  int
			draftInt   int
			menuType   sql.NullString
			createdAt  sql.NullString
			modifiedAt sql.NullString
		)
		if err := rows.Scan(&id, &title, &price, &activeInt, &draftInt, &menuType, &createdAt, &modifiedAt); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo menusDeGrupos")
			return
		}
		out = append(out, map[string]any{
			"id":          id,
			"menu_title":  title,
			"price":       price,
			"active":      activeInt != 0,
			"is_draft":    draftInt != 0,
			"menu_type":   normalizeV2MenuType(menuType.String),
			"created_at":  createdAt.String,
			"modified_at": modifiedAt.String,
		})
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"count":   len(out),
		"menus":   out,
	})
}

func (s *Server) handleBOGroupMenusV2CreateDraft(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req struct {
		MenuType string `json:"menu_type"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	menuType := normalizeV2MenuType(req.MenuType)

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creando borrador")
		return
	}
	defer tx.Rollback()

	menuSubtitle := mustJSON([]string{}, []any{})
	entrantes := mustJSON([]string{}, []any{})
	principales := mustJSON(map[string]any{"titulo_principales": "Principal a elegir", "items": []string{}}, map[string]any{})
	postre := mustJSON([]string{}, []any{})
	beverage := mustJSON(map[string]any{"type": "no_incluida", "price_per_person": nil, "has_supplement": false, "supplement_price": nil}, map[string]any{})
	comments := mustJSON([]string{}, []any{})

	res, err := tx.ExecContext(r.Context(), `
		INSERT INTO menusDeGrupos
			(restaurant_id, menu_title, price, included_coffee, active, menu_type, is_draft, editor_version,
			 menu_subtitle, entrantes, principales, postre, beverage, comments,
			 min_party_size, main_dishes_limit, main_dishes_limit_number)
		VALUES (?, ?, ?, ?, ?, ?, 1, 2, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		a.ActiveRestaurantID,
		"Nuevo menu",
		0,
		0,
		0,
		menuType,
		menuSubtitle,
		entrantes,
		principales,
		postre,
		beverage,
		comments,
		8,
		0,
		1,
	)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creando menu borrador")
		return
	}
	menuID, _ := res.LastInsertId()

	defaultSections := []struct {
		Title string
		Kind  string
	}{
		{Title: "Entrantes", Kind: "entrantes"},
		{Title: "Principales", Kind: "principales"},
		{Title: "Postres", Kind: "postres"},
	}

	for i, sec := range defaultSections {
		if _, err := tx.ExecContext(r.Context(), `
			INSERT INTO group_menu_sections_v2 (restaurant_id, menu_id, title, section_kind, position)
			VALUES (?, ?, ?, ?, ?)
		`, a.ActiveRestaurantID, menuID, sec.Title, sec.Kind, i); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error creando secciones por defecto")
			return
		}
	}

	if err := tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creando borrador")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"menu_id": menuID,
	})
}

func (s *Server) ensureBOMenuV2SectionsFromSnapshot(ctx *http.Request, restaurantID int, menuID int64) error {
	var count int
	if err := s.db.QueryRowContext(ctx.Context(), `
		SELECT COUNT(*) FROM group_menu_sections_v2 WHERE restaurant_id = ? AND menu_id = ?
	`, restaurantID, menuID).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	var (
		entrantesRaw sql.NullString
		principRaw   sql.NullString
		postreRaw    sql.NullString
	)
	if err := s.db.QueryRowContext(ctx.Context(), `
		SELECT entrantes, principales, postre
		FROM menusDeGrupos
		WHERE id = ? AND restaurant_id = ?
		LIMIT 1
	`, menuID, restaurantID).Scan(&entrantesRaw, &principRaw, &postreRaw); err != nil {
		return err
	}

	entrantesList := anySliceToStringList(decodeJSONOrFallback(entrantesRaw.String, []any{}))
	postresList := anySliceToStringList(decodeJSONOrFallback(postreRaw.String, []any{}))

	principTitle := "Principal a elegir"
	principItems := []string{}
	if p, ok := decodeJSONOrFallback(principRaw.String, map[string]any{}).(map[string]any); ok {
		if t := strings.TrimSpace(anyToString(p["titulo_principales"])); t != "" {
			principTitle = t
		}
		principItems = anySliceToStringList(p["items"])
	}

	tx, err := s.db.BeginTx(ctx.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	insertSection := func(title string, kind string, pos int) (int64, error) {
		res, err := tx.ExecContext(ctx.Context(), `
			INSERT INTO group_menu_sections_v2 (restaurant_id, menu_id, title, section_kind, position)
			VALUES (?, ?, ?, ?, ?)
		`, restaurantID, menuID, title, kind, pos)
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	}

	insertDish := func(sectionID int64, menuID int64, title string, pos int) error {
		title = strings.TrimSpace(title)
		if title == "" {
			return nil
		}
		_, err := tx.ExecContext(ctx.Context(), `
			INSERT INTO group_menu_section_dishes_v2
				(restaurant_id, menu_id, section_id, title_snapshot, description_snapshot, allergens_json,
				 supplement_enabled, supplement_price, active, position)
			VALUES (?, ?, ?, ?, '', ?, 0, NULL, 1, ?)
		`, restaurantID, menuID, sectionID, title, mustJSON([]string{}, []any{}), pos)
		return err
	}

	sec1, err := insertSection("Entrantes", "entrantes", 0)
	if err != nil {
		return err
	}
	sec2, err := insertSection(principTitle, "principales", 1)
	if err != nil {
		return err
	}
	sec3, err := insertSection("Postres", "postres", 2)
	if err != nil {
		return err
	}

	for i, item := range entrantesList {
		if err := insertDish(sec1, menuID, item, i); err != nil {
			return err
		}
	}
	for i, item := range principItems {
		if err := insertDish(sec2, menuID, item, i); err != nil {
			return err
		}
	}
	for i, item := range postresList {
		if err := insertDish(sec3, menuID, item, i); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *Server) loadBOMenuV2SectionsWithDishes(r *http.Request, restaurantID int, menuID int64) ([]boV2Section, error) {
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id, title, section_kind, position
		FROM group_menu_sections_v2
		WHERE restaurant_id = ? AND menu_id = ?
		ORDER BY position ASC, id ASC
	`, restaurantID, menuID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	sections := make([]boV2Section, 0, 8)
	sectionByID := map[int64]int{}
	for rows.Next() {
		var sec boV2Section
		if err := rows.Scan(&sec.ID, &sec.Title, &sec.Kind, &sec.Position); err != nil {
			return nil, err
		}
		sec.Kind = normalizeV2SectionKind(sec.Kind)
		sec.Dishes = []boV2Dish{}
		sectionByID[sec.ID] = len(sections)
		sections = append(sections, sec)
	}
	if len(sections) == 0 {
		return sections, nil
	}

	dRows, err := s.db.QueryContext(r.Context(), `
		SELECT id, section_id, catalog_dish_id, title_snapshot, description_snapshot, allergens_json,
		       supplement_enabled, supplement_price, price, active, position, COALESCE(foto_path, ''),
		       COALESCE(ai_requested_img, 0), COALESCE(ai_generating_img, 0), ai_generated_img
		FROM group_menu_section_dishes_v2
		WHERE restaurant_id = ? AND menu_id = ?
		ORDER BY section_id ASC, position ASC, id ASC
	`, restaurantID, menuID)
	if err != nil {
		return nil, err
	}
	defer dRows.Close()

	for dRows.Next() {
		var (
			d               boV2Dish
			catalogID       sql.NullInt64
			allergensRaw    sql.NullString
			suppPriceRaw    sql.NullFloat64
			priceRaw        sql.NullFloat64
			suppEnabledInt  int
			activeInt       int
			fotoPath        string
			aiRequestedInt  int
			aiGeneratingInt int
			aiGeneratedRaw  sql.NullString
		)
		if err := dRows.Scan(
			&d.ID,
			&d.SectionID,
			&catalogID,
			&d.Title,
			&d.Description,
			&allergensRaw,
			&suppEnabledInt,
			&suppPriceRaw,
			&priceRaw,
			&activeInt,
			&d.Position,
			&fotoPath,
			&aiRequestedInt,
			&aiGeneratingInt,
			&aiGeneratedRaw,
		); err != nil {
			return nil, err
		}
		if catalogID.Valid {
			v := catalogID.Int64
			d.CatalogDishID = &v
		}
		d.Allergens = anySliceToStringList(decodeJSONOrFallback(allergensRaw.String, []any{}))
		d.SupplementEnabled = suppEnabledInt != 0
		if suppPriceRaw.Valid {
			p := suppPriceRaw.Float64
			d.SupplementPrice = &p
		}
		if priceRaw.Valid {
			p := priceRaw.Float64
			d.Price = &p
		}
		d.Active = activeInt != 0
		d.AIRequestedImg = aiRequestedInt != 0
		d.AIGeneratingImg = aiGeneratingInt != 0
		if aiGeneratedRaw.Valid {
			if v := strings.TrimSpace(aiGeneratedRaw.String); v != "" {
				d.AIGeneratedImg = &v
			}
		}
		if p := strings.TrimSpace(fotoPath); p != "" {
			url := s.bunnyPullURL(p)
			d.FotoURL = &url
			d.ImageURL = &url
		}

		idx, ok := sectionByID[d.SectionID]
		if !ok {
			continue
		}
		sections[idx].Dishes = append(sections[idx].Dishes, d)
	}

	return sections, nil
}

func (s *Server) loadBOMenuV2SectionDishes(r *http.Request, restaurantID int, menuID int64, sectionID int64) ([]boV2Dish, error) {
	dRows, err := s.db.QueryContext(r.Context(), `
		SELECT id, section_id, catalog_dish_id, title_snapshot, description_snapshot, allergens_json,
		       supplement_enabled, supplement_price, price, active, position, COALESCE(foto_path, ''),
		       COALESCE(ai_requested_img, 0), COALESCE(ai_generating_img, 0), ai_generated_img
		FROM group_menu_section_dishes_v2
		WHERE restaurant_id = ? AND menu_id = ? AND section_id = ?
		ORDER BY position ASC, id ASC
	`, restaurantID, menuID, sectionID)
	if err != nil {
		return nil, err
	}
	defer dRows.Close()

	out := make([]boV2Dish, 0, 16)
	for dRows.Next() {
		var (
			d               boV2Dish
			catalogID       sql.NullInt64
			allergensRaw    sql.NullString
			suppPriceRaw    sql.NullFloat64
			priceRaw        sql.NullFloat64
			suppEnabledInt  int
			activeInt       int
			fotoPath        string
			aiRequestedInt  int
			aiGeneratingInt int
			aiGeneratedRaw  sql.NullString
		)
		if err := dRows.Scan(
			&d.ID,
			&d.SectionID,
			&catalogID,
			&d.Title,
			&d.Description,
			&allergensRaw,
			&suppEnabledInt,
			&suppPriceRaw,
			&priceRaw,
			&activeInt,
			&d.Position,
			&fotoPath,
			&aiRequestedInt,
			&aiGeneratingInt,
			&aiGeneratedRaw,
		); err != nil {
			return nil, err
		}
		if catalogID.Valid {
			v := catalogID.Int64
			d.CatalogDishID = &v
		}
		d.Allergens = anySliceToStringList(decodeJSONOrFallback(allergensRaw.String, []any{}))
		d.SupplementEnabled = suppEnabledInt != 0
		if suppPriceRaw.Valid {
			p := suppPriceRaw.Float64
			d.SupplementPrice = &p
		}
		if priceRaw.Valid {
			p := priceRaw.Float64
			d.Price = &p
		}
		d.Active = activeInt != 0
		d.AIRequestedImg = aiRequestedInt != 0
		d.AIGeneratingImg = aiGeneratingInt != 0
		if aiGeneratedRaw.Valid {
			if v := strings.TrimSpace(aiGeneratedRaw.String); v != "" {
				d.AIGeneratedImg = &v
			}
		}
		if p := strings.TrimSpace(fotoPath); p != "" {
			url := s.bunnyPullURL(p)
			d.FotoURL = &url
			d.ImageURL = &url
		}
		out = append(out, d)
	}
	return out, nil
}

func (s *Server) loadBOMenuV2DishByID(r *http.Request, restaurantID int, menuID int64, sectionID int64, dishID int64) (boV2Dish, error) {
	var (
		d               boV2Dish
		catalogID       sql.NullInt64
		allergensRaw    sql.NullString
		suppPriceRaw    sql.NullFloat64
		priceRaw        sql.NullFloat64
		suppEnabledInt  int
		activeInt       int
		fotoPath        string
		aiRequestedInt  int
		aiGeneratingInt int
		aiGeneratedRaw  sql.NullString
	)
	err := s.db.QueryRowContext(r.Context(), `
		SELECT id, section_id, catalog_dish_id, title_snapshot, description_snapshot, allergens_json,
		       supplement_enabled, supplement_price, price, active, position, COALESCE(foto_path, ''),
		       COALESCE(ai_requested_img, 0), COALESCE(ai_generating_img, 0), ai_generated_img
		FROM group_menu_section_dishes_v2
		WHERE id = ? AND section_id = ? AND menu_id = ? AND restaurant_id = ?
		LIMIT 1
	`, dishID, sectionID, menuID, restaurantID).Scan(
		&d.ID,
		&d.SectionID,
		&catalogID,
		&d.Title,
		&d.Description,
		&allergensRaw,
		&suppEnabledInt,
		&suppPriceRaw,
		&priceRaw,
		&activeInt,
		&d.Position,
		&fotoPath,
		&aiRequestedInt,
		&aiGeneratingInt,
		&aiGeneratedRaw,
	)
	if err != nil {
		return boV2Dish{}, err
	}
	if catalogID.Valid {
		v := catalogID.Int64
		d.CatalogDishID = &v
	}
	d.Allergens = anySliceToStringList(decodeJSONOrFallback(allergensRaw.String, []any{}))
	d.SupplementEnabled = suppEnabledInt != 0
	if suppPriceRaw.Valid {
		v := suppPriceRaw.Float64
		d.SupplementPrice = &v
	}
	if priceRaw.Valid {
		v := priceRaw.Float64
		d.Price = &v
	}
	d.Active = activeInt != 0
	d.AIRequestedImg = aiRequestedInt != 0
	d.AIGeneratingImg = aiGeneratingInt != 0
	if aiGeneratedRaw.Valid {
		if v := strings.TrimSpace(aiGeneratedRaw.String); v != "" {
			d.AIGeneratedImg = &v
		}
	}
	if p := strings.TrimSpace(fotoPath); p != "" {
		url := s.bunnyPullURL(p)
		d.FotoURL = &url
		d.ImageURL = &url
	}
	return d, nil
}

func (s *Server) syncBOMenuV2LegacySnapshot(r *http.Request, restaurantID int, menuID int64) error {
	sections, err := s.loadBOMenuV2SectionsWithDishes(r, restaurantID, menuID)
	if err != nil {
		return err
	}

	entrantes := make([]string, 0, 16)
	postres := make([]string, 0, 16)
	principalesTitle := "Principal a elegir"
	principalesItems := make([]string, 0, 16)

	for _, sec := range sections {
		if strings.TrimSpace(sec.Title) != "" && sec.Kind == "principales" && principalesTitle == "Principal a elegir" {
			principalesTitle = strings.TrimSpace(sec.Title)
		}
		for _, d := range sec.Dishes {
			if !d.Active {
				continue
			}
			title := strings.TrimSpace(d.Title)
			if title == "" {
				continue
			}
			switch sec.Kind {
			case "postres", "postre":
				postres = append(postres, title)
			case "principales":
				principalesItems = append(principalesItems, title)
			default:
				entrantes = append(entrantes, title)
			}
		}
	}

	principales := map[string]any{
		"titulo_principales": principalesTitle,
		"items":              principalesItems,
	}

	_, err = s.db.ExecContext(r.Context(), `
		UPDATE menusDeGrupos
		SET entrantes = ?, principales = ?, postre = ?
		WHERE id = ? AND restaurant_id = ?
	`, mustJSON(entrantes, []any{}), mustJSON(principales, map[string]any{}), mustJSON(postres, []any{}), menuID, restaurantID)
	return err
}

func (s *Server) handleBOGroupMenusV2Get(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	menuID, err := parseChiPositiveInt64(r, "id")
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid menu id"})
		return
	}

	if err := s.ensureBOMenuV2SectionsFromSnapshot(r, a.ActiveRestaurantID, menuID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error inicializando secciones")
		return
	}

	var (
		title             string
		price             string
		activeInt         int
		draftInt          int
		menuType          sql.NullString
		menuSubtitleRaw   sql.NullString
		showDishImagesInt int
		beverageRaw       sql.NullString
		commentsRaw       sql.NullString
		minPartySize      int
		mainLimitInt      int
		mainLimitNumber   int
		includedCoffeeInt int
	)

	err = s.db.QueryRowContext(r.Context(), `
		SELECT menu_title, price, active, is_draft, menu_type, menu_subtitle, show_dish_images, beverage, comments,
		       min_party_size, main_dishes_limit, main_dishes_limit_number, included_coffee
		FROM menusDeGrupos
		WHERE id = ? AND restaurant_id = ?
		LIMIT 1
	`, menuID, a.ActiveRestaurantID).Scan(
		&title,
		&price,
		&activeInt,
		&draftInt,
		&menuType,
		&menuSubtitleRaw,
		&showDishImagesInt,
		&beverageRaw,
		&commentsRaw,
		&minPartySize,
		&mainLimitInt,
		&mainLimitNumber,
		&includedCoffeeInt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Menu not found"})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error cargando menu")
		return
	}

	sections, err := s.loadBOMenuV2SectionsWithDishes(r, a.ActiveRestaurantID, menuID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error cargando secciones")
		return
	}
	aiImages, err := s.loadBOMenuV2AIImageTracker(r.Context(), a.ActiveRestaurantID, menuID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error cargando tracker AI")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"menu": map[string]any{
			"id":               menuID,
			"menu_title":       title,
			"price":            price,
			"active":           activeInt != 0,
			"is_draft":         draftInt != 0,
			"menu_type":        normalizeV2MenuType(menuType.String),
			"menu_subtitle":    anySliceToStringList(decodeJSONOrFallback(menuSubtitleRaw.String, []any{})),
			"show_dish_images": showDishImagesInt != 0,
			"settings": map[string]any{
				"included_coffee":          includedCoffeeInt != 0,
				"beverage":                 decodeJSONOrFallback(beverageRaw.String, map[string]any{"type": "no_incluida", "price_per_person": nil, "has_supplement": false, "supplement_price": nil}),
				"comments":                 anySliceToStringList(decodeJSONOrFallback(commentsRaw.String, []any{})),
				"min_party_size":           minPartySize,
				"main_dishes_limit":        mainLimitInt != 0,
				"main_dishes_limit_number": mainLimitNumber,
			},
			"sections":  sections,
			"ai_images": aiImages,
		},
	})
}

func (s *Server) handleBOGroupMenusV2PatchBasics(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	menuID, err := parseChiPositiveInt64(r, "id")
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid menu id"})
		return
	}

	var input map[string]any
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid JSON body"})
		return
	}

	var (
		currentTitle             string
		currentPrice             string
		currentActiveInt         int
		currentDraftInt          int
		currentType              sql.NullString
		currentMenuSubtitle      sql.NullString
		currentShowDishImagesInt int
		currentBeverage          sql.NullString
		currentComments          sql.NullString
		currentMinParty          int
		currentMainLimitInt      int
		currentMainLimitNumber   int
		currentIncludedCoffeeInt int
	)

	err = s.db.QueryRowContext(r.Context(), `
		SELECT menu_title, price, active, is_draft, menu_type, menu_subtitle, show_dish_images, beverage, comments,
		       min_party_size, main_dishes_limit, main_dishes_limit_number, included_coffee
		FROM menusDeGrupos
		WHERE id = ? AND restaurant_id = ?
		LIMIT 1
	`, menuID, a.ActiveRestaurantID).Scan(
		&currentTitle,
		&currentPrice,
		&currentActiveInt,
		&currentDraftInt,
		&currentType,
		&currentMenuSubtitle,
		&currentShowDishImagesInt,
		&currentBeverage,
		&currentComments,
		&currentMinParty,
		&currentMainLimitInt,
		&currentMainLimitNumber,
		&currentIncludedCoffeeInt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Menu not found"})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando menu")
		return
	}

	title := strings.TrimSpace(currentTitle)
	if v, ok := input["menu_title"]; ok {
		title = strings.TrimSpace(anyToString(v))
	}
	if title == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Menu title is required"})
		return
	}

	priceFloat, err := anyToFloat64(currentPrice)
	if err != nil {
		priceFloat = 0
	}
	if v, ok := input["price"]; ok {
		if parsed, err := anyToFloat64(v); err == nil {
			priceFloat = parsed
		}
	}
	if priceFloat < 0 {
		priceFloat = 0
	}

	active := currentActiveInt != 0
	if v, ok := input["active"]; ok {
		active = parseLooseBoolOrDefault(v, active)
	}

	isDraft := currentDraftInt != 0
	if v, ok := input["is_draft"]; ok {
		isDraft = parseLooseBoolOrDefault(v, isDraft)
	}

	menuType := normalizeV2MenuType(currentType.String)
	if v, ok := input["menu_type"]; ok {
		menuType = normalizeV2MenuType(anyToString(v))
	}

	menuSubtitleJSON := currentMenuSubtitle.String
	if v, ok := input["menu_subtitle"]; ok {
		menuSubtitleJSON = mustJSON(anySliceToStringList(v), []any{})
	}

	showDishImages := currentShowDishImagesInt != 0
	if v, ok := input["show_dish_images"]; ok {
		showDishImages = parseLooseBoolOrDefault(v, showDishImages)
	}

	beverageJSON := currentBeverage.String
	if v, ok := input["beverage"]; ok {
		beverageJSON = mustJSON(v, map[string]any{"type": "no_incluida", "price_per_person": nil, "has_supplement": false, "supplement_price": nil})
	}

	commentsJSON := currentComments.String
	if v, ok := input["comments"]; ok {
		commentsJSON = mustJSON(anySliceToStringList(v), []any{})
	}

	minParty := currentMinParty
	if v, ok := input["min_party_size"]; ok {
		if parsed, err := anyToInt(v); err == nil {
			minParty = parsed
		}
	}
	if minParty <= 0 {
		minParty = 1
	}

	mainLimit := currentMainLimitInt != 0
	if v, ok := input["main_dishes_limit"]; ok {
		mainLimit = parseLooseBoolOrDefault(v, mainLimit)
	}

	mainLimitNumber := currentMainLimitNumber
	if v, ok := input["main_dishes_limit_number"]; ok {
		if parsed, err := anyToInt(v); err == nil {
			mainLimitNumber = parsed
		}
	}
	if mainLimitNumber <= 0 {
		mainLimitNumber = 1
	}

	includedCoffee := currentIncludedCoffeeInt != 0
	if v, ok := input["included_coffee"]; ok {
		includedCoffee = parseLooseBoolOrDefault(v, includedCoffee)
	}

	_, err = s.db.ExecContext(r.Context(), `
		UPDATE menusDeGrupos
			SET menu_title = ?,
			    price = ?,
			    active = ?,
			    is_draft = ?,
			    menu_type = ?,
			    menu_subtitle = ?,
			    show_dish_images = ?,
			    beverage = ?,
			    comments = ?,
			    min_party_size = ?,
			    main_dishes_limit = ?,
		    main_dishes_limit_number = ?,
		    included_coffee = ?,
		    editor_version = 2
		WHERE id = ? AND restaurant_id = ?
	`,
		title,
		priceFloat,
		boolToTinyint(active),
		boolToTinyint(isDraft),
		menuType,
		menuSubtitleJSON,
		boolToTinyint(showDishImages),
		beverageJSON,
		commentsJSON,
		minParty,
		boolToTinyint(mainLimit),
		mainLimitNumber,
		boolToTinyint(includedCoffee),
		menuID,
		a.ActiveRestaurantID,
	)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando menu")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleBOGroupMenusV2PatchMenuType(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	menuID, err := parseChiPositiveInt64(r, "id")
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid menu id"})
		return
	}

	var req struct {
		MenuType string `json:"menu_type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid JSON body"})
		return
	}

	if strings.TrimSpace(req.MenuType) == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Menu type is required"})
		return
	}

	var existingID int64
	if err := s.db.QueryRowContext(r.Context(), `
		SELECT id
		FROM menusDeGrupos
		WHERE id = ? AND restaurant_id = ?
		LIMIT 1
	`, menuID, a.ActiveRestaurantID).Scan(&existingID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Menu not found"})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando menu")
		return
	}

	menuType := normalizeV2MenuType(req.MenuType)

	if _, err := s.db.ExecContext(r.Context(), `
		UPDATE menusDeGrupos
		SET menu_type = ?,
		    editor_version = 2
		WHERE id = ? AND restaurant_id = ?
	`, menuType, menuID, a.ActiveRestaurantID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando menu")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":   true,
		"menu_id":   menuID,
		"menu_type": menuType,
	})
}

func (s *Server) handleBOGroupMenusV2PutSections(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	menuID, err := parseChiPositiveInt64(r, "id")
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid menu id"})
		return
	}

	var req struct {
		Sections []struct {
			ID       int64  `json:"id"`
			Title    string `json:"title"`
			Kind     string `json:"kind"`
			Position int    `json:"position"`
		} `json:"sections"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid JSON body"})
		return
	}
	if len(req.Sections) == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "At least one section is required"})
		return
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando secciones")
		return
	}
	defer tx.Rollback()

	var owns int
	if err := tx.QueryRowContext(r.Context(), `
		SELECT COUNT(*) FROM menusDeGrupos WHERE id = ? AND restaurant_id = ?
	`, menuID, a.ActiveRestaurantID).Scan(&owns); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando secciones")
		return
	}
	if owns == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Menu not found"})
		return
	}

	existing := map[int64]bool{}
	rows, err := tx.QueryContext(r.Context(), `
		SELECT id FROM group_menu_sections_v2 WHERE restaurant_id = ? AND menu_id = ?
	`, a.ActiveRestaurantID, menuID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo secciones")
		return
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo secciones")
			return
		}
		existing[id] = true
	}
	rows.Close()

	keep := make([]int64, 0, len(req.Sections))
	for idx, sec := range req.Sections {
		title := strings.TrimSpace(sec.Title)
		if title == "" {
			title = "Seccion"
		}
		kind := normalizeV2SectionKind(sec.Kind)
		position := idx

		if sec.ID > 0 && existing[sec.ID] {
			if _, err := tx.ExecContext(r.Context(), `
				UPDATE group_menu_sections_v2
				SET title = ?, section_kind = ?, position = ?
				WHERE id = ? AND restaurant_id = ? AND menu_id = ?
			`, title, kind, position, sec.ID, a.ActiveRestaurantID, menuID); err != nil {
				httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando seccion")
				return
			}
			keep = append(keep, sec.ID)
			continue
		}

		res, err := tx.ExecContext(r.Context(), `
			INSERT INTO group_menu_sections_v2 (restaurant_id, menu_id, title, section_kind, position)
			VALUES (?, ?, ?, ?, ?)
		`, a.ActiveRestaurantID, menuID, title, kind, position)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error creando seccion")
			return
		}
		newID, _ := res.LastInsertId()
		keep = append(keep, newID)
	}

	if len(keep) > 0 {
		args := make([]any, 0, 2+len(keep))
		args = append(args, a.ActiveRestaurantID, menuID)
		for _, id := range keep {
			args = append(args, id)
		}
		q := fmt.Sprintf(`
			DELETE FROM group_menu_sections_v2
			WHERE restaurant_id = ? AND menu_id = ? AND id NOT IN (%s)
		`, placeholderList(len(keep)))
		if _, err := tx.ExecContext(r.Context(), q, args...); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error limpiando secciones")
			return
		}
	}

	if err := tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error guardando secciones")
		return
	}

	if err := s.syncBOMenuV2LegacySnapshot(r, a.ActiveRestaurantID, menuID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error sincronizando snapshot")
		return
	}

	sections, err := s.loadBOMenuV2SectionsWithDishes(r, a.ActiveRestaurantID, menuID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error recargando secciones")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "sections": sections})
}

func (s *Server) handleBOGroupMenusV2PutSectionDishes(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	menuID, err := parseChiPositiveInt64(r, "id")
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid menu id"})
		return
	}
	sectionID, err := parseChiPositiveInt64(r, "sectionId")
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid section id"})
		return
	}

	var req struct {
		Dishes []struct {
			ID                int64    `json:"id"`
			CatalogDishID     *int64   `json:"catalog_dish_id"`
			Title             string   `json:"title"`
			Description       string   `json:"description"`
			Allergens         []string `json:"allergens"`
			SupplementEnabled bool     `json:"supplement_enabled"`
			SupplementPrice   *float64 `json:"supplement_price"`
			Price             *float64 `json:"price"`
			Active            *bool    `json:"active"`
		} `json:"dishes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid JSON body"})
		return
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error guardando platos")
		return
	}
	defer tx.Rollback()

	var sectionExists int
	if err := tx.QueryRowContext(r.Context(), `
		SELECT COUNT(*)
		FROM group_menu_sections_v2
		WHERE id = ? AND menu_id = ? AND restaurant_id = ?
	`, sectionID, menuID, a.ActiveRestaurantID).Scan(&sectionExists); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error verificando seccion")
		return
	}
	if sectionExists == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Section not found"})
		return
	}

	type existingDishState struct {
		Title    string
		Active   bool
		Position int
	}
	existing := map[int64]existingDishState{}
	rows, err := tx.QueryContext(r.Context(), `
		SELECT id, title_snapshot, active, position
		FROM group_menu_section_dishes_v2
		WHERE section_id = ? AND menu_id = ? AND restaurant_id = ?
	`, sectionID, menuID, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo platos")
		return
	}
	for rows.Next() {
		var (
			id       int64
			title    string
			active   int
			position int
		)
		if err := rows.Scan(&id, &title, &active, &position); err != nil {
			rows.Close()
			httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo platos")
			return
		}
		existing[id] = existingDishState{
			Title:    strings.TrimSpace(title),
			Active:   active != 0,
			Position: position,
		}
	}
	rows.Close()

	keep := make([]int64, 0, len(req.Dishes))
	keepSet := make(map[int64]struct{}, len(req.Dishes))
	needsLegacySync := false
	for idx, dish := range req.Dishes {
		title := strings.TrimSpace(dish.Title)
		if title == "" {
			continue
		}
		description := strings.TrimSpace(dish.Description)
		allergens := make([]string, 0, len(dish.Allergens))
		for _, aName := range dish.Allergens {
			aName = strings.TrimSpace(aName)
			if aName == "" {
				continue
			}
			allergens = append(allergens, aName)
		}
		active := true
		if dish.Active != nil {
			active = *dish.Active
		}
		position := idx

		if dish.ID > 0 {
			prev, ok := existing[dish.ID]
			if !ok {
				// Invalid/foreign id for this section: treat as new dish to avoid touching unrelated rows.
				dish.ID = 0
			} else if prev.Title != title || prev.Active != active || (active && prev.Position != position) {
				needsLegacySync = true
			}
		}

		if dish.ID > 0 {
			_, err := tx.ExecContext(r.Context(), `
				UPDATE group_menu_section_dishes_v2
				SET catalog_dish_id = ?,
				    title_snapshot = ?,
				    description_snapshot = ?,
				    allergens_json = ?,
				    supplement_enabled = ?,
				    supplement_price = ?,
				    price = ?,
				    active = ?,
				    position = ?
				WHERE id = ? AND section_id = ? AND menu_id = ? AND restaurant_id = ?
			`,
				dish.CatalogDishID,
				title,
				description,
				mustJSON(allergens, []any{}),
				boolToTinyint(dish.SupplementEnabled),
				dish.SupplementPrice,
				dish.Price,
				boolToTinyint(active),
				position,
				dish.ID,
				sectionID,
				menuID,
				a.ActiveRestaurantID,
			)
			if err != nil {
				httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando plato")
				return
			}
			keep = append(keep, dish.ID)
			keepSet[dish.ID] = struct{}{}
			continue
		}

		if active {
			needsLegacySync = true
		}
		res, err := tx.ExecContext(r.Context(), `
			INSERT INTO group_menu_section_dishes_v2
				(restaurant_id, menu_id, section_id, catalog_dish_id, title_snapshot, description_snapshot,
				 allergens_json, supplement_enabled, supplement_price, price, active, position)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`,
			a.ActiveRestaurantID,
			menuID,
			sectionID,
			dish.CatalogDishID,
			title,
			description,
			mustJSON(allergens, []any{}),
			boolToTinyint(dish.SupplementEnabled),
			dish.SupplementPrice,
			dish.Price,
			boolToTinyint(active),
			position,
		)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error creando plato")
			return
		}
		newID, _ := res.LastInsertId()
		keep = append(keep, newID)
		keepSet[newID] = struct{}{}
	}

	if len(keep) == 0 {
		for _, prev := range existing {
			if prev.Active {
				needsLegacySync = true
				break
			}
		}
		if _, err := tx.ExecContext(r.Context(), `
			DELETE FROM group_menu_section_dishes_v2
			WHERE section_id = ? AND menu_id = ? AND restaurant_id = ?
		`, sectionID, menuID, a.ActiveRestaurantID); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error limpiando platos")
			return
		}
	} else {
		for id, prev := range existing {
			if _, ok := keepSet[id]; ok {
				continue
			}
			if prev.Active {
				needsLegacySync = true
				break
			}
		}
		args := make([]any, 0, 3+len(keep))
		args = append(args, sectionID, menuID, a.ActiveRestaurantID)
		for _, id := range keep {
			args = append(args, id)
		}
		q := fmt.Sprintf(`
			DELETE FROM group_menu_section_dishes_v2
			WHERE section_id = ? AND menu_id = ? AND restaurant_id = ? AND id NOT IN (%s)
		`, placeholderList(len(keep)))
		if _, err := tx.ExecContext(r.Context(), q, args...); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error limpiando platos")
			return
		}
	}

	if err := tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error guardando platos")
		return
	}

	// Legacy snapshot only depends on active dish titles/order. Skip expensive sync for
	// description/supplement/catalog-only edits where legacy payload remains unchanged.
	if needsLegacySync {
		if err := s.syncBOMenuV2LegacySnapshot(r, a.ActiveRestaurantID, menuID); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error sincronizando snapshot")
			return
		}
	}

	dishes, err := s.loadBOMenuV2SectionDishes(r, a.ActiveRestaurantID, menuID, sectionID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error recargando platos")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "dishes": dishes})
}

func (s *Server) handleBOGroupMenusV2PatchSectionDish(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	menuID, err := parseChiPositiveInt64(r, "id")
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid menu id"})
		return
	}
	sectionID, err := parseChiPositiveInt64(r, "sectionId")
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid section id"})
		return
	}
	dishID, err := parseChiPositiveInt64(r, "dishId")
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid dish id"})
		return
	}

	var input map[string]any
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid JSON body"})
		return
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error guardando plato")
		return
	}
	defer tx.Rollback()

	var sectionExists int
	if err := tx.QueryRowContext(r.Context(), `
		SELECT COUNT(*)
		FROM group_menu_sections_v2
		WHERE id = ? AND menu_id = ? AND restaurant_id = ?
	`, sectionID, menuID, a.ActiveRestaurantID).Scan(&sectionExists); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error verificando seccion")
		return
	}
	if sectionExists == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Section not found"})
		return
	}

	var (
		currentCatalogID sql.NullInt64
		currentTitle     string
		currentDesc      string
		currentAllergens sql.NullString
		currentSuppInt   int
		currentSuppPrice sql.NullFloat64
		currentPrice     sql.NullFloat64
		currentActiveInt int
		currentPosition  int
	)
	err = tx.QueryRowContext(r.Context(), `
		SELECT catalog_dish_id, title_snapshot, description_snapshot, allergens_json,
		       supplement_enabled, supplement_price, price, active, position
		FROM group_menu_section_dishes_v2
		WHERE id = ? AND section_id = ? AND menu_id = ? AND restaurant_id = ?
		LIMIT 1
	`, dishID, sectionID, menuID, a.ActiveRestaurantID).Scan(
		&currentCatalogID,
		&currentTitle,
		&currentDesc,
		&currentAllergens,
		&currentSuppInt,
		&currentSuppPrice,
		&currentPrice,
		&currentActiveInt,
		&currentPosition,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Dish not found"})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo plato")
		return
	}

	var catalogDishID *int64
	if currentCatalogID.Valid {
		v := currentCatalogID.Int64
		catalogDishID = &v
	}
	title := strings.TrimSpace(currentTitle)
	description := strings.TrimSpace(currentDesc)
	allergens := anySliceToStringList(decodeJSONOrFallback(currentAllergens.String, []any{}))
	supplementEnabled := currentSuppInt != 0
	var supplementPrice *float64
	if currentSuppPrice.Valid {
		v := currentSuppPrice.Float64
		supplementPrice = &v
	}
	var price *float64
	if currentPrice.Valid {
		v := currentPrice.Float64
		price = &v
	}
	active := currentActiveInt != 0

	if raw, ok := input["catalog_dish_id"]; ok {
		if raw == nil {
			catalogDishID = nil
		} else if parsed, err := anyToInt(raw); err == nil && parsed > 0 {
			v := int64(parsed)
			catalogDishID = &v
		}
	}
	if raw, ok := input["title"]; ok {
		if v := strings.TrimSpace(anyToString(raw)); v != "" {
			title = v
		}
	}
	if raw, ok := input["description"]; ok {
		description = strings.TrimSpace(anyToString(raw))
	}
	if raw, ok := input["allergens"]; ok {
		allergens = anySliceToStringList(raw)
	}
	if raw, ok := input["supplement_enabled"]; ok {
		supplementEnabled = parseLooseBoolOrDefault(raw, supplementEnabled)
	}
	if raw, ok := input["supplement_price"]; ok {
		if raw == nil {
			supplementPrice = nil
		} else if parsed, err := anyToFloat64(raw); err == nil {
			v := parsed
			supplementPrice = &v
		}
	}
	if raw, ok := input["price"]; ok {
		if raw == nil {
			price = nil
		} else if parsed, err := anyToFloat64(raw); err == nil {
			v := parsed
			price = &v
		}
	}
	if raw, ok := input["active"]; ok {
		active = parseLooseBoolOrDefault(raw, active)
	}

	_, err = tx.ExecContext(r.Context(), `
		UPDATE group_menu_section_dishes_v2
		SET catalog_dish_id = ?,
		    title_snapshot = ?,
		    description_snapshot = ?,
		    allergens_json = ?,
		    supplement_enabled = ?,
		    supplement_price = ?,
		    price = ?,
		    active = ?,
		    position = ?
		WHERE id = ? AND section_id = ? AND menu_id = ? AND restaurant_id = ?
	`,
		catalogDishID,
		title,
		description,
		mustJSON(allergens, []any{}),
		boolToTinyint(supplementEnabled),
		supplementPrice,
		price,
		boolToTinyint(active),
		currentPosition,
		dishID,
		sectionID,
		menuID,
		a.ActiveRestaurantID,
	)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando plato")
		return
	}

	if err := tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error guardando plato")
		return
	}

	needsLegacySync := (currentActiveInt != boolToTinyint(active)) || (currentActiveInt != 0 && active && strings.TrimSpace(currentTitle) != title)
	if needsLegacySync {
		if err := s.syncBOMenuV2LegacySnapshot(r, a.ActiveRestaurantID, menuID); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error sincronizando snapshot")
			return
		}
	}

	d, err := s.loadBOMenuV2DishByID(r, a.ActiveRestaurantID, menuID, sectionID, dishID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Dish not found after update"})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error recargando plato")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "dish": d})
}

func (s *Server) handleBODishesCatalogSearch(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "items": []any{}})
		return
	}
	limit := 12
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil {
			limit = clampIntBound(v, 1, 40, 12)
		}
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id, title, description, allergens_json, default_supplement_enabled, default_supplement_price, updated_at
		FROM menu_dishes_catalog
		WHERE restaurant_id = ? AND title LIKE ?
		ORDER BY updated_at DESC, id DESC
		LIMIT ?
	`, a.ActiveRestaurantID, "%"+q+"%", limit)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error buscando platos")
		return
	}
	defer rows.Close()

	items := make([]map[string]any, 0, limit)
	for rows.Next() {
		var (
			id           int64
			title        string
			description  sql.NullString
			allergensRaw sql.NullString
			suppInt      int
			suppPrice    sql.NullFloat64
			updatedAt    sql.NullString
		)
		if err := rows.Scan(&id, &title, &description, &allergensRaw, &suppInt, &suppPrice, &updatedAt); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo platos")
			return
		}
		row := map[string]any{
			"id":                         id,
			"title":                      title,
			"description":                description.String,
			"allergens":                  anySliceToStringList(decodeJSONOrFallback(allergensRaw.String, []any{})),
			"default_supplement_enabled": suppInt != 0,
			"updated_at":                 updatedAt.String,
		}
		if suppPrice.Valid {
			row["default_supplement_price"] = suppPrice.Float64
		} else {
			row["default_supplement_price"] = nil
		}
		items = append(items, row)
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "items": items})
}

func (s *Server) handleBODishesCatalogUpsert(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req struct {
		ID                       int64    `json:"id"`
		Title                    string   `json:"title"`
		Description              string   `json:"description"`
		Allergens                []string `json:"allergens"`
		DefaultSupplementEnabled bool     `json:"default_supplement_enabled"`
		DefaultSupplementPrice   *float64 `json:"default_supplement_price"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid JSON body"})
		return
	}

	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Dish title is required"})
		return
	}

	req.Description = strings.TrimSpace(req.Description)
	allergens := make([]string, 0, len(req.Allergens))
	for _, aName := range req.Allergens {
		aName = strings.TrimSpace(aName)
		if aName == "" {
			continue
		}
		allergens = append(allergens, aName)
	}

	var dishID int64
	if req.ID > 0 {
		res, err := s.db.ExecContext(r.Context(), `
			UPDATE menu_dishes_catalog
			SET title = ?, description = ?, allergens_json = ?,
			    default_supplement_enabled = ?, default_supplement_price = ?
			WHERE id = ? AND restaurant_id = ?
		`, req.Title, req.Description, mustJSON(allergens, []any{}), boolToTinyint(req.DefaultSupplementEnabled), req.DefaultSupplementPrice, req.ID, a.ActiveRestaurantID)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando plato")
			return
		}
		affected, _ := res.RowsAffected()
		if affected == 0 {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Dish not found"})
			return
		}
		dishID = req.ID
	} else {
		res, err := s.db.ExecContext(r.Context(), `
			INSERT INTO menu_dishes_catalog
				(restaurant_id, title, description, allergens_json, default_supplement_enabled, default_supplement_price)
			VALUES (?, ?, ?, ?, ?, ?)
		`, a.ActiveRestaurantID, req.Title, req.Description, mustJSON(allergens, []any{}), boolToTinyint(req.DefaultSupplementEnabled), req.DefaultSupplementPrice)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error creando plato")
			return
		}
		dishID, _ = res.LastInsertId()
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"dish": map[string]any{
			"id":                         dishID,
			"title":                      req.Title,
			"description":                req.Description,
			"allergens":                  allergens,
			"default_supplement_enabled": req.DefaultSupplementEnabled,
			"default_supplement_price":   req.DefaultSupplementPrice,
		},
	})
}

func (s *Server) handleBOGroupMenusV2Publish(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	menuID, err := parseChiPositiveInt64(r, "id")
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid menu id"})
		return
	}

	var (
		sections int
		dishes   int
	)
	if err := s.db.QueryRowContext(r.Context(), `
		SELECT COUNT(*) FROM group_menu_sections_v2 WHERE restaurant_id = ? AND menu_id = ?
	`, a.ActiveRestaurantID, menuID).Scan(&sections); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error validando menu")
		return
	}
	if err := s.db.QueryRowContext(r.Context(), `
		SELECT COUNT(*) FROM group_menu_section_dishes_v2 WHERE restaurant_id = ? AND menu_id = ? AND active = 1
	`, a.ActiveRestaurantID, menuID).Scan(&dishes); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error validando menu")
		return
	}

	if sections == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Debe haber al menos una seccion"})
		return
	}
	if dishes == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Debes anadir al menos un plato"})
		return
	}

	if _, err := s.db.ExecContext(r.Context(), `
		UPDATE menusDeGrupos
		SET is_draft = 0, editor_version = 2
		WHERE id = ? AND restaurant_id = ?
	`, menuID, a.ActiveRestaurantID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error publicando menu")
		return
	}

	if err := s.syncBOMenuV2LegacySnapshot(r, a.ActiveRestaurantID, menuID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error sincronizando snapshot")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleBOGroupMenusV2ToggleActive(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	menuID, err := parseChiPositiveInt64(r, "id")
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid menu id"})
		return
	}

	var current int
	if err := s.db.QueryRowContext(r.Context(), `
		SELECT active FROM menusDeGrupos WHERE id = ? AND restaurant_id = ?
	`, menuID, a.ActiveRestaurantID).Scan(&current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Menu not found"})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando menu")
		return
	}

	next := 1
	if current != 0 {
		next = 0
	}

	if _, err := s.db.ExecContext(r.Context(), `
		UPDATE menusDeGrupos SET active = ? WHERE id = ? AND restaurant_id = ?
	`, next, menuID, a.ActiveRestaurantID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando menu")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"active":  next != 0,
	})
}

func (s *Server) handleBOGroupMenusV2UploadSectionDishImage(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	if !s.bunnyConfigured() {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Image storage not configured"})
		return
	}

	menuID, err := parseChiPositiveInt64(r, "id")
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid menu id"})
		return
	}
	sectionID, err := parseChiPositiveInt64(r, "sectionId")
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid section id"})
		return
	}
	dishID, err := parseChiPositiveInt64(r, "dishId")
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid dish id"})
		return
	}

	var exists int
	if err := s.db.QueryRowContext(r.Context(), `
		SELECT COUNT(*)
		FROM group_menu_section_dishes_v2
		WHERE id = ? AND section_id = ? AND menu_id = ? AND restaurant_id = ?
	`, dishID, sectionID, menuID, a.ActiveRestaurantID).Scan(&exists); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error verificando plato")
		return
	}
	if exists == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Dish not found"})
		return
	}

	const maxImageSize = 8 << 20
	if err := r.ParseMultipartForm(maxImageSize); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error parsing form"})
		return
	}

	file, _, err := r.FormFile("image")
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "No image file provided"})
		return
	}
	defer file.Close()

	raw, err := io.ReadAll(io.LimitReader(file, maxImageSize+1))
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error reading file"})
		return
	}
	if len(raw) == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Empty file"})
		return
	}
	if len(raw) > maxImageSize {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Image too large (max 8MB)"})
		return
	}

	contentType := http.DetectContentType(raw)
	allowedTypes := map[string]bool{
		"image/jpeg": true,
		"image/png":  true,
		"image/webp": true,
		"image/gif":  true,
	}
	if !allowedTypes[contentType] {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "File type not allowed"})
		return
	}

	ext := fileExtForContentType(contentType)
	pictureID := fmt.Sprintf("dish-%d-%d", dishID, time.Now().UnixMilli())
	objectPath := path.Join(
		strconv.Itoa(a.ActiveRestaurantID),
		"pictures",
		strconv.FormatInt(menuID, 10),
		pictureID+ext,
	)

	if err := s.bunnyPut(r.Context(), objectPath, raw, contentType); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error uploading image: " + err.Error()})
		return
	}

	if _, err := s.db.ExecContext(r.Context(), `
		UPDATE group_menu_section_dishes_v2
		SET foto_path = ?
		WHERE id = ? AND section_id = ? AND menu_id = ? AND restaurant_id = ?
	`, objectPath, dishID, sectionID, menuID, a.ActiveRestaurantID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error guardando imagen")
		return
	}

	dish, err := s.loadBOMenuV2DishByID(r, a.ActiveRestaurantID, menuID, sectionID, dishID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error recargando plato")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"dish":    dish,
	})
}

func (s *Server) handleBOGroupMenusV2Delete(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	menuID, err := parseChiPositiveInt64(r, "id")
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid menu id"})
		return
	}

	res, err := s.db.ExecContext(r.Context(), `
		DELETE FROM menusDeGrupos WHERE id = ? AND restaurant_id = ?
	`, menuID, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error eliminando menu")
		return
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Menu not found"})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleBOSpecialMenuImageUpload(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	menuID, err := parseChiPositiveInt64(r, "id")
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid menu id"})
		return
	}

	// Check menu exists and belongs to restaurant
	var menuType string
	err = s.db.QueryRowContext(r.Context(), `SELECT menu_type FROM menusDeGrupos WHERE id = ? AND restaurant_id = ?`, menuID, a.ActiveRestaurantID).Scan(&menuType)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Menu not found"})
		return
	}

	// Verify it's a special menu
	if menuType != "special" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Menu is not a special menu"})
		return
	}

	// Parse multipart form
	if err := r.ParseMultipartForm(10 << 20); err != nil { // 10MB max
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error parsing form"})
		return
	}

	file, header, err := r.FormFile("image")
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "No image file provided"})
		return
	}
	defer file.Close()

	// Read file content
	imgData, err := io.ReadAll(file)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error reading file"})
		return
	}

	// Validate file type
	contentType := http.DetectContentType(imgData)
	allowedTypes := map[string]bool{
		"image/jpeg":         true,
		"image/png":          true,
		"image/webp":         true,
		"image/gif":          true,
		"application/pdf":    true,
		"application/msword": true,
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document": true,
		"text/plain": true,
	}
	if !allowedTypes[contentType] {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "File type not allowed"})
		return
	}

	// Determine file extension
	ext := ".jpg"
	switch contentType {
	case "image/jpeg":
		ext = ".jpg"
	case "image/png":
		ext = ".png"
	case "image/webp":
		ext = ".webp"
	case "image/gif":
		ext = ".gif"
	case "application/pdf":
		ext = ".pdf"
	case "application/msword", "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		ext = ".docx"
	case "text/plain":
		ext = ".txt"
	}

	// Generate object path: restaurant_id/menus/special/menu_id/timestamp.ext
	objectPath := path.Join(
		strconv.Itoa(a.ActiveRestaurantID),
		"menus",
		"special",
		fmt.Sprintf("menu-%d-%d%s", menuID, time.Now().Unix(), ext),
	)

	// Upload to BunnyCDN
	if err := s.bunnyPut(r.Context(), objectPath, imgData, contentType); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error uploading file: " + err.Error()})
		return
	}

	// Update menu record with image URL
	imageURL := s.bunnyPullURL(objectPath)
	_, err = s.db.ExecContext(r.Context(), `UPDATE menusDeGrupos SET special_menu_image_url = ? WHERE id = ?`, imageURL, menuID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error saving image URL"})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "imageUrl": imageURL, "filename": header.Filename})
}
