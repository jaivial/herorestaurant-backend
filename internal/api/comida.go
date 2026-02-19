package api

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"github.com/go-chi/chi/v5"

	"preactvillacarmen/internal/httpx"
)

type comidaTipo string

const (
	comidaTipoPlatos  comidaTipo = "platos"
	comidaTipoPostres comidaTipo = "postres"
	comidaTipoVinos   comidaTipo = "vinos"
	comidaTipoBebidas comidaTipo = "bebidas"
	comidaTipoCafes   comidaTipo = "cafes"
)

var comidaTipoOrder = []comidaTipo{
	comidaTipoPlatos,
	comidaTipoPostres,
	comidaTipoVinos,
	comidaTipoBebidas,
	comidaTipoCafes,
}

var basePlatoCategories = []struct {
	Name string
	Slug string
}{
	{Name: "Entrantes", Slug: "entrantes"},
	{Name: "Principal", Slug: "principal"},
	{Name: "Arroz", Slug: "arroz"},
	{Name: "Postre", Slug: "postre"},
}

type comidaListQuery struct {
	Page       int
	PageSize   int
	Search     string
	Tipo       string
	Category   string
	Alergeno   string
	Suplemento *int
	Active     *int
}

type comidaItemResponse struct {
	Num                int      `json:"num"`
	SourceType         string   `json:"source_type,omitempty"`
	Tipo               string   `json:"tipo,omitempty"`
	Nombre             string   `json:"nombre"`
	Precio             float64  `json:"precio"`
	Descripcion        string   `json:"descripcion,omitempty"`
	Titulo             string   `json:"titulo,omitempty"`
	Suplemento         float64  `json:"suplemento,omitempty"`
	Alergenos          []string `json:"alergenos,omitempty"`
	Active             bool     `json:"active"`
	HasFoto            bool     `json:"has_foto"`
	FotoURL            string   `json:"foto_url,omitempty"`
	Categoria          string   `json:"categoria,omitempty"`
	CategoryID         *int     `json:"category_id,omitempty"`
	CategorySlug       string   `json:"category_slug,omitempty"`
	Bodega             string   `json:"bodega,omitempty"`
	DenominacionOrigen string   `json:"denominacion_origen,omitempty"`
	Graduacion         float64  `json:"graduacion,omitempty"`
	Anyo               string   `json:"anyo,omitempty"`
}

type comidaPostreResponse struct {
	Num         int      `json:"num"`
	Descripcion string   `json:"descripcion"`
	Alergenos   []string `json:"alergenos"`
	Active      bool     `json:"active"`
	Precio      float64  `json:"precio,omitempty"`
}

type comidaVinoResponse struct {
	Num                int     `json:"num"`
	Tipo               string  `json:"tipo"`
	Nombre             string  `json:"nombre"`
	Precio             float64 `json:"precio"`
	Descripcion        string  `json:"descripcion"`
	Bodega             string  `json:"bodega"`
	DenominacionOrigen string  `json:"denominacion_origen"`
	Graduacion         float64 `json:"graduacion"`
	Anyo               string  `json:"anyo"`
	Active             bool    `json:"active"`
	HasFoto            bool    `json:"has_foto"`
	FotoURL            string  `json:"foto_url,omitempty"`
}

type comidaCategoryResponse struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Slug   string `json:"slug"`
	Source string `json:"source"`
	Active bool   `json:"active"`
}

type comidaUpsertRequest struct {
	Tipo               *string   `json:"tipo,omitempty"`
	Nombre             *string   `json:"nombre,omitempty"`
	Precio             *float64  `json:"precio,omitempty"`
	Descripcion        *string   `json:"descripcion,omitempty"`
	Titulo             *string   `json:"titulo,omitempty"`
	Suplemento         *float64  `json:"suplemento,omitempty"`
	Alergenos          *[]string `json:"alergenos,omitempty"`
	Active             *bool     `json:"active,omitempty"`
	ImageBase64        *string   `json:"imageBase64,omitempty"`
	Categoria          *string   `json:"categoria,omitempty"`
	Category           *string   `json:"category,omitempty"`
	CategoryID         *int      `json:"category_id,omitempty"`
	Bodega             *string   `json:"bodega,omitempty"`
	DenominacionOrigen *string   `json:"denominacion_origen,omitempty"`
	Graduacion         *float64  `json:"graduacion,omitempty"`
	Anyo               *string   `json:"anyo,omitempty"`
}

type comidaCategoryCreateRequest struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	Slug  string `json:"slug"`
}

func normalizeComidaTipo(raw string) (comidaTipo, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "platos", "plato":
		return comidaTipoPlatos, true
	case "postres", "postre":
		return comidaTipoPostres, true
	case "vinos", "vino":
		return comidaTipoVinos, true
	case "bebidas", "bebida":
		return comidaTipoBebidas, true
	case "cafes", "cafe", "cafés", "café":
		return comidaTipoCafes, true
	default:
		return "", false
	}
}

func (t comidaTipo) isCatalogType() bool {
	return t == comidaTipoPlatos || t == comidaTipoBebidas || t == comidaTipoCafes
}

func parseComidaListQuery(r *http.Request) comidaListQuery {
	q := r.URL.Query()
	page := parsePositiveIntOr(q.Get("page"), 1)
	pageSize := parsePositiveIntOr(firstNonEmpty(q.Get("pageSize"), q.Get("limit"), q.Get("count")), 24)
	if pageSize > 100 {
		pageSize = 100
	}
	if pageSize <= 0 {
		pageSize = 24
	}
	return comidaListQuery{
		Page:       page,
		PageSize:   pageSize,
		Search:     strings.TrimSpace(q.Get("q")),
		Tipo:       strings.TrimSpace(q.Get("tipo")),
		Category:   strings.TrimSpace(firstNonEmpty(q.Get("categoria"), q.Get("category"))),
		Alergeno:   strings.TrimSpace(firstNonEmpty(q.Get("alergeno"), q.Get("allergen"))),
		Suplemento: parseOptionalBooleanAsInt(strings.TrimSpace(q.Get("suplemento"))),
		Active:     parseOptionalBooleanAsInt(strings.TrimSpace(q.Get("active"))),
	}
}

func parseOptionalBooleanAsInt(raw string) *int {
	if raw == "" {
		return nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return nil
	}
	if n != 0 {
		n = 1
	}
	return &n
}

func parsePositiveIntOr(raw string, fallback int) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" {
			return v
		}
	}
	return ""
}

func comidaRestaurantIDFromRequest(r *http.Request) (int, bool) {
	if a, ok := boAuthFromContext(r.Context()); ok && a.ActiveRestaurantID > 0 {
		return a.ActiveRestaurantID, true
	}
	return restaurantIDFromContext(r.Context())
}

func writeComidaValidationError(w http.ResponseWriter, msg string) {
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": false,
		"message": msg,
	})
}

func offsetForPage(page, pageSize int) int {
	if page <= 1 {
		return 0
	}
	return (page - 1) * pageSize
}

func slugifyCategoryName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	replacer := strings.NewReplacer(
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
	)
	s = replacer.Replace(s)

	var b strings.Builder
	prevDash := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
			continue
		}
		if unicode.IsSpace(r) || r == '-' || r == '_' || r == '/' || r == '.' {
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		slug = "categoria"
	}
	return slug
}

func (s *Server) ensureBasePlatoCategories(r *http.Request, restaurantID int) error {
	for _, cat := range basePlatoCategories {
		_, err := s.db.ExecContext(r.Context(), `
			INSERT INTO comida_plato_categories (restaurant_id, name, slug, source, active)
			VALUES (?, ?, ?, 'base', 1)
			ON DUPLICATE KEY UPDATE
				name = VALUES(name),
				source = IF(source = 'custom', source, VALUES(source)),
				active = 1
		`, restaurantID, cat.Name, cat.Slug)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) getCategoryByID(r *http.Request, restaurantID, categoryID int) (comidaCategoryResponse, bool, error) {
	var c comidaCategoryResponse
	var activeInt int
	err := s.db.QueryRowContext(r.Context(), `
		SELECT id, COALESCE(name, ''), COALESCE(slug, ''), COALESCE(source, 'custom'), active
		FROM comida_plato_categories
		WHERE restaurant_id = ? AND id = ?
		LIMIT 1
	`, restaurantID, categoryID).Scan(&c.ID, &c.Name, &c.Slug, &c.Source, &activeInt)
	if err == sql.ErrNoRows {
		return comidaCategoryResponse{}, false, nil
	}
	if err != nil {
		return comidaCategoryResponse{}, false, err
	}
	c.Active = activeInt != 0
	return c, true, nil
}

func (s *Server) getCategoryBySlugOrName(r *http.Request, restaurantID int, raw string) (comidaCategoryResponse, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return comidaCategoryResponse{}, false, nil
	}
	slug := slugifyCategoryName(raw)
	var c comidaCategoryResponse
	var activeInt int
	err := s.db.QueryRowContext(r.Context(), `
		SELECT id, COALESCE(name, ''), COALESCE(slug, ''), COALESCE(source, 'custom'), active
		FROM comida_plato_categories
		WHERE restaurant_id = ? AND (slug = ? OR LOWER(name) = LOWER(?))
		LIMIT 1
	`, restaurantID, slug, raw).Scan(&c.ID, &c.Name, &c.Slug, &c.Source, &activeInt)
	if err == sql.ErrNoRows {
		return comidaCategoryResponse{}, false, nil
	}
	if err != nil {
		return comidaCategoryResponse{}, false, err
	}
	c.Active = activeInt != 0
	return c, true, nil
}

func (s *Server) createCustomCategory(r *http.Request, restaurantID int, name string) (comidaCategoryResponse, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return comidaCategoryResponse{}, sql.ErrNoRows
	}
	slug := slugifyCategoryName(name)
	res, err := s.db.ExecContext(r.Context(), `
		INSERT INTO comida_plato_categories (restaurant_id, name, slug, source, active)
		VALUES (?, ?, ?, 'custom', 1)
		ON DUPLICATE KEY UPDATE
			name = VALUES(name),
			active = 1
	`, restaurantID, name, slug)
	if err != nil {
		return comidaCategoryResponse{}, err
	}
	id64, _ := res.LastInsertId()
	if id64 > 0 {
		return comidaCategoryResponse{
			ID:     int(id64),
			Name:   name,
			Slug:   slug,
			Source: "custom",
			Active: true,
		}, nil
	}
	c, _, err := s.getCategoryBySlugOrName(r, restaurantID, name)
	return c, err
}

func (s *Server) resolvePlatoCategory(r *http.Request, restaurantID int, req comidaUpsertRequest, allowCreate bool) (sql.NullString, sql.NullInt64, error) {
	if err := s.ensureBasePlatoCategories(r, restaurantID); err != nil {
		return sql.NullString{}, sql.NullInt64{}, err
	}

	if req.CategoryID != nil {
		if *req.CategoryID <= 0 {
			return sql.NullString{}, sql.NullInt64{}, nil
		}
		cat, ok, err := s.getCategoryByID(r, restaurantID, *req.CategoryID)
		if err != nil {
			return sql.NullString{}, sql.NullInt64{}, err
		}
		if !ok || !cat.Active {
			return sql.NullString{}, sql.NullInt64{}, sql.ErrNoRows
		}
		return sql.NullString{String: cat.Name, Valid: true}, sql.NullInt64{Int64: int64(cat.ID), Valid: true}, nil
	}

	raw := strings.TrimSpace(firstNonEmpty(comidaPtrString(req.Categoria), comidaPtrString(req.Category)))
	if raw == "" {
		return sql.NullString{}, sql.NullInt64{}, nil
	}
	cat, ok, err := s.getCategoryBySlugOrName(r, restaurantID, raw)
	if err != nil {
		return sql.NullString{}, sql.NullInt64{}, err
	}
	if ok {
		return sql.NullString{String: cat.Name, Valid: true}, sql.NullInt64{Int64: int64(cat.ID), Valid: true}, nil
	}
	if !allowCreate {
		return sql.NullString{String: raw, Valid: true}, sql.NullInt64{}, nil
	}
	custom, err := s.createCustomCategory(r, restaurantID, raw)
	if err != nil {
		return sql.NullString{}, sql.NullInt64{}, err
	}
	return sql.NullString{String: custom.Name, Valid: true}, sql.NullInt64{Int64: int64(custom.ID), Valid: true}, nil
}

func (s *Server) handleBOComidaList(w http.ResponseWriter, r *http.Request) {
	s.handleComidaList(w, r)
}

func (s *Server) handleComidaPublicList(w http.ResponseWriter, r *http.Request) {
	s.handleComidaList(w, r)
}

func (s *Server) handleComidaList(w http.ResponseWriter, r *http.Request) {
	t, ok := normalizeComidaTipo(chi.URLParam(r, "tipo"))
	if !ok {
		writeComidaValidationError(w, "Tipo de comida invalido")
		return
	}
	s.handleComidaListByType(w, r, t)
}

func (s *Server) handleBOComidaGet(w http.ResponseWriter, r *http.Request) {
	s.handleComidaGet(w, r)
}

func (s *Server) handleComidaPublicGet(w http.ResponseWriter, r *http.Request) {
	s.handleComidaGet(w, r)
}

func (s *Server) handleComidaGet(w http.ResponseWriter, r *http.Request) {
	t, ok := normalizeComidaTipo(chi.URLParam(r, "tipo"))
	if !ok {
		writeComidaValidationError(w, "Tipo de comida invalido")
		return
	}
	s.handleComidaGetByType(w, r, t)
}

func (s *Server) handleBOComidaCreate(w http.ResponseWriter, r *http.Request) {
	s.handleComidaCreate(w, r)
}

func (s *Server) handleComidaPublicCreate(w http.ResponseWriter, r *http.Request) {
	s.handleComidaCreate(w, r)
}

func (s *Server) handleComidaCreate(w http.ResponseWriter, r *http.Request) {
	t, ok := normalizeComidaTipo(chi.URLParam(r, "tipo"))
	if !ok {
		writeComidaValidationError(w, "Tipo de comida invalido")
		return
	}
	s.handleComidaCreateByType(w, r, t)
}

func (s *Server) handleBOComidaPatch(w http.ResponseWriter, r *http.Request) {
	s.handleComidaPatch(w, r)
}

func (s *Server) handleComidaPublicPatch(w http.ResponseWriter, r *http.Request) {
	s.handleComidaPatch(w, r)
}

func (s *Server) handleComidaPatch(w http.ResponseWriter, r *http.Request) {
	t, ok := normalizeComidaTipo(chi.URLParam(r, "tipo"))
	if !ok {
		writeComidaValidationError(w, "Tipo de comida invalido")
		return
	}
	s.handleComidaPatchByType(w, r, t)
}

func (s *Server) handleBOComidaDelete(w http.ResponseWriter, r *http.Request) {
	s.handleComidaDelete(w, r)
}

func (s *Server) handleComidaPublicDelete(w http.ResponseWriter, r *http.Request) {
	s.handleComidaDelete(w, r)
}

func (s *Server) handleComidaDelete(w http.ResponseWriter, r *http.Request) {
	t, ok := normalizeComidaTipo(chi.URLParam(r, "tipo"))
	if !ok {
		writeComidaValidationError(w, "Tipo de comida invalido")
		return
	}
	s.handleComidaDeleteByType(w, r, t)
}

func (s *Server) handleBOComidaPlatoCategoriesList(w http.ResponseWriter, r *http.Request) {
	s.handleComidaPlatoCategoriesList(w, r)
}

func (s *Server) handleComidaPublicPlatoCategoriesList(w http.ResponseWriter, r *http.Request) {
	s.handleComidaPlatoCategoriesList(w, r)
}

func (s *Server) handleComidaPlatoCategoriesList(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := comidaRestaurantIDFromRequest(r)
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	if err := s.ensureBasePlatoCategories(r, restaurantID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error cargando categorias")
		return
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id, COALESCE(name, ''), COALESCE(slug, ''), COALESCE(source, 'custom'), active
		FROM comida_plato_categories
		WHERE restaurant_id = ? AND active = 1
		ORDER BY
			CASE WHEN source = 'base' THEN 0 ELSE 1 END,
			name ASC
	`, restaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error cargando categorias")
		return
	}
	defer rows.Close()

	out := make([]comidaCategoryResponse, 0, 8)
	for rows.Next() {
		var (
			c         comidaCategoryResponse
			activeInt int
		)
		if err := rows.Scan(&c.ID, &c.Name, &c.Slug, &c.Source, &activeInt); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo categorias")
			return
		}
		c.Active = activeInt != 0
		out = append(out, c)
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":    true,
		"categories": out,
		"categorias": out,
		"tipos":      out,
	})
}

func (s *Server) handleBOComidaPlatoCategoriesCreate(w http.ResponseWriter, r *http.Request) {
	s.handleComidaPlatoCategoriesCreate(w, r)
}

func (s *Server) handleComidaPublicPlatoCategoriesCreate(w http.ResponseWriter, r *http.Request) {
	s.handleComidaPlatoCategoriesCreate(w, r)
}

func (s *Server) handleComidaPlatoCategoriesCreate(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := comidaRestaurantIDFromRequest(r)
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req comidaCategoryCreateRequest
	if err := readJSONBody(r, &req); err != nil {
		writeComidaValidationError(w, "Invalid JSON")
		return
	}

	name := strings.TrimSpace(firstNonEmpty(req.Name, req.Label))
	if name == "" {
		writeComidaValidationError(w, "Nombre de categoria requerido")
		return
	}
	slug := strings.TrimSpace(req.Slug)
	if slug == "" {
		slug = slugifyCategoryName(name)
	}

	if err := s.ensureBasePlatoCategories(r, restaurantID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creando categoria")
		return
	}

	res, err := s.db.ExecContext(r.Context(), `
		INSERT INTO comida_plato_categories (restaurant_id, name, slug, source, active)
		VALUES (?, ?, ?, 'custom', 1)
		ON DUPLICATE KEY UPDATE
			name = VALUES(name),
			active = 1
	`, restaurantID, name, slug)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creando categoria")
		return
	}

	id64, _ := res.LastInsertId()
	category := comidaCategoryResponse{
		Name:   name,
		Slug:   slug,
		Source: "custom",
		Active: true,
	}
	if id64 > 0 {
		category.ID = int(id64)
	} else {
		c, found, err := s.getCategoryBySlugOrName(r, restaurantID, slug)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error creando categoria")
			return
		}
		if found {
			category = c
		}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":   true,
		"category":  category,
		"categoria": category,
	})
}

func (s *Server) handleComidaListByType(w http.ResponseWriter, r *http.Request, t comidaTipo) {
	restaurantID, ok := comidaRestaurantIDFromRequest(r)
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	query := parseComidaListQuery(r)
	switch t {
	case comidaTipoVinos:
		items, total, err := s.listVinos(r, restaurantID, query)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error consultando vinos")
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":  true,
			"items":    items,
			"vinos":    items,
			"total":    total,
			"page":     query.Page,
			"limit":    query.PageSize,
			"pageSize": query.PageSize,
		})
	case comidaTipoPostres:
		items, postres, total, err := s.listPostres(r, restaurantID, query)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error consultando postres")
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":  true,
			"items":    items,
			"postres":  postres,
			"total":    total,
			"page":     query.Page,
			"limit":    query.PageSize,
			"pageSize": query.PageSize,
		})
	default:
		items, total, err := s.listCatalogItems(r, restaurantID, t, query)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error consultando comida")
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":  true,
			"items":    items,
			"total":    total,
			"page":     query.Page,
			"limit":    query.PageSize,
			"pageSize": query.PageSize,
		})
	}
}

func (s *Server) listCatalogItems(r *http.Request, restaurantID int, t comidaTipo, query comidaListQuery) ([]comidaItemResponse, int, error) {
	where := []string{"ci.restaurant_id = ?", "ci.source_type = ?"}
	args := []any{restaurantID, string(t)}

	if query.Active != nil {
		where = append(where, "ci.active = ?")
		args = append(args, *query.Active)
	}
	if query.Search != "" {
		like := "%" + query.Search + "%"
		where = append(where, "(ci.nombre LIKE ? OR ci.descripcion LIKE ? OR ci.titulo LIKE ?)")
		args = append(args, like, like, like)
	}
	if query.Tipo != "" {
		where = append(where, "UPPER(COALESCE(ci.tipo,'')) = ?")
		args = append(args, strings.ToUpper(query.Tipo))
	}
	if query.Category != "" {
		if catID, err := strconv.Atoi(query.Category); err == nil && catID > 0 {
			where = append(where, "ci.category_id = ?")
			args = append(args, catID)
		} else {
			slug := slugifyCategoryName(query.Category)
			where = append(where, "(LOWER(COALESCE(ci.categoria,'')) = LOWER(?) OR COALESCE(c.slug,'') = ?)")
			args = append(args, query.Category, slug)
		}
	}
	if query.Alergeno != "" {
		where = append(where, "JSON_CONTAINS(COALESCE(ci.alergenos_json, JSON_ARRAY()), JSON_QUOTE(?))")
		args = append(args, query.Alergeno)
	}
	if query.Suplemento != nil {
		if *query.Suplemento != 0 {
			where = append(where, "COALESCE(ci.suplemento, 0) > 0")
		} else {
			where = append(where, "COALESCE(ci.suplemento, 0) <= 0")
		}
	}

	whereSQL := strings.Join(where, " AND ")
	var total int
	if err := s.db.QueryRowContext(r.Context(), `
		SELECT COUNT(*)
		FROM comida_items ci
		LEFT JOIN comida_plato_categories c ON c.id = ci.category_id
		WHERE `+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	offset := offsetForPage(query.Page, query.PageSize)
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT
			ci.id,
			COALESCE(ci.tipo, ''),
			COALESCE(ci.nombre, ''),
			COALESCE(ci.precio, 0),
			COALESCE(ci.descripcion, ''),
			COALESCE(ci.titulo, ''),
			COALESCE(ci.suplemento, 0),
			ci.alergenos_json,
			ci.active,
			((ci.foto_path IS NOT NULL AND LENGTH(ci.foto_path) > 0) OR ci.foto IS NOT NULL) AS has_foto,
			COALESCE(ci.foto_path, ''),
			ci.foto,
			COALESCE(ci.categoria, ''),
			ci.category_id,
			COALESCE(c.slug, '')
		FROM comida_items ci
		LEFT JOIN comida_plato_categories c ON c.id = ci.category_id
		WHERE `+whereSQL+`
		ORDER BY ci.active DESC, ci.updated_at DESC, ci.id DESC
		LIMIT ? OFFSET ?
	`, append(args, query.PageSize, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	items := make([]comidaItemResponse, 0, query.PageSize)
	for rows.Next() {
		var (
			item          comidaItemResponse
			alergRaw      sql.NullString
			activeInt     int
			hasFotoInt    int
			fotoPath      string
			fotoBlob      []byte
			categoryIDRaw sql.NullInt64
		)
		if err := rows.Scan(
			&item.Num,
			&item.Tipo,
			&item.Nombre,
			&item.Precio,
			&item.Descripcion,
			&item.Titulo,
			&item.Suplemento,
			&alergRaw,
			&activeInt,
			&hasFotoInt,
			&fotoPath,
			&fotoBlob,
			&item.Categoria,
			&categoryIDRaw,
			&item.CategorySlug,
		); err != nil {
			return nil, 0, err
		}
		item.SourceType = string(t)
		item.Alergenos = parseAlergenos(alergRaw)
		item.Active = activeInt != 0
		item.HasFoto = hasFotoInt != 0
		if categoryIDRaw.Valid {
			v := int(categoryIDRaw.Int64)
			item.CategoryID = &v
		}
		if fotoPath != "" && s.bunnyConfigured() {
			item.FotoURL = s.bunnyPullURL(fotoPath)
		} else if len(fotoBlob) > 0 {
			item.FotoURL = "data:" + http.DetectContentType(fotoBlob) + ";base64," + base64.StdEncoding.EncodeToString(fotoBlob)
		}
		items = append(items, item)
	}
	return items, total, nil
}

func (s *Server) listPostres(r *http.Request, restaurantID int, query comidaListQuery) ([]comidaItemResponse, []comidaPostreResponse, int, error) {
	where := []string{"restaurant_id = ?"}
	args := []any{restaurantID}
	if query.Active != nil {
		where = append(where, "active = ?")
		args = append(args, *query.Active)
	}
	if query.Search != "" {
		like := "%" + query.Search + "%"
		where = append(where, "DESCRIPCION LIKE ?")
		args = append(args, like)
	}
	if query.Alergeno != "" {
		where = append(where, "JSON_CONTAINS(COALESCE(alergenos, JSON_ARRAY()), JSON_QUOTE(?))")
		args = append(args, query.Alergeno)
	}
	whereSQL := strings.Join(where, " AND ")

	var total int
	if err := s.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM POSTRES WHERE "+whereSQL, args...).Scan(&total); err != nil {
		return nil, nil, 0, err
	}

	offset := offsetForPage(query.Page, query.PageSize)
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT NUM, COALESCE(DESCRIPCION, ''), alergenos, active
		FROM POSTRES
		WHERE `+whereSQL+`
		ORDER BY active DESC, NUM DESC
		LIMIT ? OFFSET ?
	`, append(args, query.PageSize, offset)...)
	if err != nil {
		return nil, nil, 0, err
	}
	defer rows.Close()

	items := make([]comidaItemResponse, 0, query.PageSize)
	postres := make([]comidaPostreResponse, 0, query.PageSize)
	for rows.Next() {
		var (
			num       int
			desc      string
			alergRaw  sql.NullString
			activeInt int
		)
		if err := rows.Scan(&num, &desc, &alergRaw, &activeInt); err != nil {
			return nil, nil, 0, err
		}
		alerg := parseAlergenos(alergRaw)
		active := activeInt != 0

		items = append(items, comidaItemResponse{
			Num:         num,
			SourceType:  string(comidaTipoPostres),
			Tipo:        "POSTRE",
			Nombre:      desc,
			Precio:      0,
			Descripcion: desc,
			Alergenos:   alerg,
			Active:      active,
			HasFoto:     false,
		})
		postres = append(postres, comidaPostreResponse{
			Num:         num,
			Descripcion: desc,
			Alergenos:   alerg,
			Active:      active,
		})
	}
	return items, postres, total, nil
}

func (s *Server) listVinos(r *http.Request, restaurantID int, query comidaListQuery) ([]comidaVinoResponse, int, error) {
	where := []string{"restaurant_id = ?"}
	args := []any{restaurantID}
	if query.Active != nil {
		where = append(where, "active = ?")
		args = append(args, *query.Active)
	}
	if query.Tipo != "" {
		where = append(where, "UPPER(COALESCE(tipo, '')) = ?")
		args = append(args, strings.ToUpper(query.Tipo))
	}
	if query.Search != "" {
		like := "%" + query.Search + "%"
		where = append(where, "(nombre LIKE ? OR descripcion LIKE ? OR bodega LIKE ? OR denominacion_origen LIKE ? OR anyo LIKE ?)")
		args = append(args, like, like, like, like, like)
	}
	whereSQL := strings.Join(where, " AND ")

	var total int
	if err := s.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM VINOS WHERE "+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	offset := offsetForPage(query.Page, query.PageSize)
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT
			num,
			COALESCE(tipo, ''),
			COALESCE(nombre, ''),
			COALESCE(precio, 0),
			COALESCE(descripcion, ''),
			COALESCE(bodega, ''),
			COALESCE(denominacion_origen, ''),
			COALESCE(graduacion, 0),
			COALESCE(anyo, ''),
			active,
			((foto_path IS NOT NULL AND LENGTH(foto_path) > 0) OR foto IS NOT NULL) AS has_foto,
			COALESCE(foto_path, ''),
			foto
		FROM VINOS
		WHERE `+whereSQL+`
		ORDER BY active DESC, tipo ASC, nombre ASC, num ASC
		LIMIT ? OFFSET ?
	`, append(args, query.PageSize, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := make([]comidaVinoResponse, 0, query.PageSize)
	for rows.Next() {
		var (
			v          comidaVinoResponse
			activeInt  int
			hasFotoInt int
			fotoPath   string
			fotoBlob   []byte
		)
		if err := rows.Scan(
			&v.Num,
			&v.Tipo,
			&v.Nombre,
			&v.Precio,
			&v.Descripcion,
			&v.Bodega,
			&v.DenominacionOrigen,
			&v.Graduacion,
			&v.Anyo,
			&activeInt,
			&hasFotoInt,
			&fotoPath,
			&fotoBlob,
		); err != nil {
			return nil, 0, err
		}
		v.Active = activeInt != 0
		v.HasFoto = hasFotoInt != 0
		if fotoPath != "" && s.bunnyConfigured() {
			v.FotoURL = s.bunnyPullURL(fotoPath)
		} else if len(fotoBlob) > 0 {
			v.FotoURL = "data:" + http.DetectContentType(fotoBlob) + ";base64," + base64.StdEncoding.EncodeToString(fotoBlob)
		}
		out = append(out, v)
	}
	return out, total, nil
}

func parseIDParam(r *http.Request) (int, error) {
	return strconv.Atoi(strings.TrimSpace(chi.URLParam(r, "id")))
}

func (s *Server) handleComidaGetByType(w http.ResponseWriter, r *http.Request, t comidaTipo) {
	restaurantID, ok := comidaRestaurantIDFromRequest(r)
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	id, err := parseIDParam(r)
	if err != nil || id <= 0 {
		writeComidaValidationError(w, "ID invalido")
		return
	}

	switch t {
	case comidaTipoVinos:
		item, found, err := s.getVinoByID(r, restaurantID, id)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error consultando vino")
			return
		}
		if !found {
			writeComidaValidationError(w, "Elemento no encontrado")
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": true,
			"item":    item,
			"vino":    item,
		})
	case comidaTipoPostres:
		item, postre, found, err := s.getPostreByID(r, restaurantID, id)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error consultando postre")
			return
		}
		if !found {
			writeComidaValidationError(w, "Elemento no encontrado")
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": true,
			"item":    item,
			"postre":  postre,
		})
	default:
		item, found, err := s.getCatalogItemByID(r, restaurantID, t, id)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error consultando comida")
			return
		}
		if !found {
			writeComidaValidationError(w, "Elemento no encontrado")
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": true,
			"item":    item,
		})
	}
}

func (s *Server) getCatalogItemByID(r *http.Request, restaurantID int, t comidaTipo, id int) (comidaItemResponse, bool, error) {
	var (
		item          comidaItemResponse
		alergRaw      sql.NullString
		activeInt     int
		hasFotoInt    int
		fotoPath      sql.NullString
		fotoBlob      []byte
		categoryIDRaw sql.NullInt64
	)
	err := s.db.QueryRowContext(r.Context(), `
		SELECT
			ci.id,
			COALESCE(ci.tipo, ''),
			COALESCE(ci.nombre, ''),
			COALESCE(ci.precio, 0),
			COALESCE(ci.descripcion, ''),
			COALESCE(ci.titulo, ''),
			COALESCE(ci.suplemento, 0),
			ci.alergenos_json,
			ci.active,
			((ci.foto_path IS NOT NULL AND LENGTH(ci.foto_path) > 0) OR ci.foto IS NOT NULL) AS has_foto,
			ci.foto_path,
			ci.foto,
			COALESCE(ci.categoria, ''),
			ci.category_id,
			COALESCE(c.slug, '')
		FROM comida_items ci
		LEFT JOIN comida_plato_categories c ON c.id = ci.category_id
		WHERE ci.restaurant_id = ? AND ci.source_type = ? AND ci.id = ?
		LIMIT 1
	`, restaurantID, string(t), id).Scan(
		&item.Num,
		&item.Tipo,
		&item.Nombre,
		&item.Precio,
		&item.Descripcion,
		&item.Titulo,
		&item.Suplemento,
		&alergRaw,
		&activeInt,
		&hasFotoInt,
		&fotoPath,
		&fotoBlob,
		&item.Categoria,
		&categoryIDRaw,
		&item.CategorySlug,
	)
	if err == sql.ErrNoRows {
		return comidaItemResponse{}, false, nil
	}
	if err != nil {
		return comidaItemResponse{}, false, err
	}

	item.SourceType = string(t)
	item.Alergenos = parseAlergenos(alergRaw)
	item.Active = activeInt != 0
	item.HasFoto = hasFotoInt != 0
	if categoryIDRaw.Valid {
		v := int(categoryIDRaw.Int64)
		item.CategoryID = &v
	}

	if fotoPath.Valid && strings.TrimSpace(fotoPath.String) != "" && s.bunnyConfigured() {
		item.FotoURL = s.bunnyPullURL(fotoPath.String)
	} else if len(fotoBlob) > 0 {
		item.FotoURL = "data:" + http.DetectContentType(fotoBlob) + ";base64," + base64.StdEncoding.EncodeToString(fotoBlob)
	}

	return item, true, nil
}

func (s *Server) getPostreByID(r *http.Request, restaurantID int, id int) (comidaItemResponse, comidaPostreResponse, bool, error) {
	var (
		desc      string
		alergRaw  sql.NullString
		activeInt int
	)
	err := s.db.QueryRowContext(r.Context(), `
		SELECT COALESCE(DESCRIPCION, ''), alergenos, active
		FROM POSTRES
		WHERE restaurant_id = ? AND NUM = ?
		LIMIT 1
	`, restaurantID, id).Scan(&desc, &alergRaw, &activeInt)
	if err == sql.ErrNoRows {
		return comidaItemResponse{}, comidaPostreResponse{}, false, nil
	}
	if err != nil {
		return comidaItemResponse{}, comidaPostreResponse{}, false, err
	}
	alerg := parseAlergenos(alergRaw)
	active := activeInt != 0
	item := comidaItemResponse{
		Num:         id,
		SourceType:  string(comidaTipoPostres),
		Tipo:        "POSTRE",
		Nombre:      desc,
		Descripcion: desc,
		Precio:      0,
		Alergenos:   alerg,
		Active:      active,
		HasFoto:     false,
	}
	postre := comidaPostreResponse{
		Num:         id,
		Descripcion: desc,
		Alergenos:   alerg,
		Active:      active,
	}
	return item, postre, true, nil
}

func (s *Server) getVinoByID(r *http.Request, restaurantID int, id int) (comidaVinoResponse, bool, error) {
	var (
		v          comidaVinoResponse
		activeInt  int
		hasFotoInt int
		fotoPath   sql.NullString
		fotoBlob   []byte
	)
	err := s.db.QueryRowContext(r.Context(), `
		SELECT
			num,
			COALESCE(tipo, ''),
			COALESCE(nombre, ''),
			COALESCE(precio, 0),
			COALESCE(descripcion, ''),
			COALESCE(bodega, ''),
			COALESCE(denominacion_origen, ''),
			COALESCE(graduacion, 0),
			COALESCE(anyo, ''),
			active,
			((foto_path IS NOT NULL AND LENGTH(foto_path) > 0) OR foto IS NOT NULL) AS has_foto,
			foto_path,
			foto
		FROM VINOS
		WHERE restaurant_id = ? AND num = ?
		LIMIT 1
	`, restaurantID, id).Scan(
		&v.Num,
		&v.Tipo,
		&v.Nombre,
		&v.Precio,
		&v.Descripcion,
		&v.Bodega,
		&v.DenominacionOrigen,
		&v.Graduacion,
		&v.Anyo,
		&activeInt,
		&hasFotoInt,
		&fotoPath,
		&fotoBlob,
	)
	if err == sql.ErrNoRows {
		return comidaVinoResponse{}, false, nil
	}
	if err != nil {
		return comidaVinoResponse{}, false, err
	}
	v.Active = activeInt != 0
	v.HasFoto = hasFotoInt != 0
	if fotoPath.Valid && strings.TrimSpace(fotoPath.String) != "" && s.bunnyConfigured() {
		v.FotoURL = s.bunnyPullURL(fotoPath.String)
	} else if len(fotoBlob) > 0 {
		v.FotoURL = "data:" + http.DetectContentType(fotoBlob) + ";base64," + base64.StdEncoding.EncodeToString(fotoBlob)
	}
	return v, true, nil
}

func comidaPtrString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func comidaPtrFloat(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

func (s *Server) handleComidaCreateByType(w http.ResponseWriter, r *http.Request, t comidaTipo) {
	restaurantID, ok := comidaRestaurantIDFromRequest(r)
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req comidaUpsertRequest
	if err := readJSONBody(r, &req); err != nil {
		writeComidaValidationError(w, "Invalid JSON")
		return
	}

	switch t {
	case comidaTipoVinos:
		s.createVino(w, r, restaurantID, req)
	case comidaTipoPostres:
		s.createPostre(w, r, restaurantID, req)
	default:
		s.createCatalogItem(w, r, restaurantID, t, req)
	}
}

func (s *Server) createCatalogItem(w http.ResponseWriter, r *http.Request, restaurantID int, t comidaTipo, req comidaUpsertRequest) {
	nombre := strings.TrimSpace(comidaPtrString(req.Nombre))
	if nombre == "" {
		writeComidaValidationError(w, "Nombre requerido")
		return
	}

	precio := comidaPtrFloat(req.Precio)
	if precio < 0 {
		writeComidaValidationError(w, "Precio invalido")
		return
	}
	suplemento := comidaPtrFloat(req.Suplemento)
	if suplemento < 0 {
		suplemento = 0
	}
	activeInt := 1
	if req.Active != nil && !*req.Active {
		activeInt = 0
	}

	var (
		categoria  sql.NullString
		categoryID sql.NullInt64
		err        error
	)
	if t == comidaTipoPlatos {
		categoria, categoryID, err = s.resolvePlatoCategory(r, restaurantID, req, true)
		if err == sql.ErrNoRows {
			writeComidaValidationError(w, "Categoria invalida")
			return
		}
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error resolviendo categoria")
			return
		}
	} else {
		rawCat := strings.TrimSpace(firstNonEmpty(comidaPtrString(req.Categoria), comidaPtrString(req.Category)))
		if rawCat != "" {
			categoria = sql.NullString{String: rawCat, Valid: true}
		}
	}

	alergJSON, _ := json.Marshal([]string{})
	if req.Alergenos != nil {
		alergJSON, _ = json.Marshal(*req.Alergenos)
	}

	var foto []byte
	if req.ImageBase64 != nil && strings.TrimSpace(*req.ImageBase64) != "" {
		b, err := decodeBase64Image(*req.ImageBase64)
		if err != nil {
			writeComidaValidationError(w, "Imagen invalida")
			return
		}
		foto = b
	}

	res, err := s.db.ExecContext(r.Context(), `
		INSERT INTO comida_items
			(restaurant_id, source_type, nombre, tipo, categoria, category_id, titulo, precio, suplemento, descripcion, alergenos_json, active, foto_path, foto)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?)
	`, restaurantID,
		string(t),
		nombre,
		strings.ToUpper(strings.TrimSpace(comidaPtrString(req.Tipo))),
		categoria,
		categoryID,
		strings.TrimSpace(comidaPtrString(req.Titulo)),
		precio,
		suplemento,
		strings.TrimSpace(comidaPtrString(req.Descripcion)),
		string(alergJSON),
		activeInt,
		foto,
	)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creando comida")
		return
	}
	newID, _ := res.LastInsertId()
	item, _, err := s.getCatalogItemByID(r, restaurantID, t, int(newID))
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creando comida")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"num":     int(newID),
		"item":    item,
	})
}

func (s *Server) createPostre(w http.ResponseWriter, r *http.Request, restaurantID int, req comidaUpsertRequest) {
	desc := strings.TrimSpace(firstNonEmpty(comidaPtrString(req.Descripcion), comidaPtrString(req.Nombre)))
	if desc == "" {
		writeComidaValidationError(w, "Descripcion requerida")
		return
	}
	activeInt := 1
	if req.Active != nil && !*req.Active {
		activeInt = 0
	}
	alergJSON, _ := json.Marshal([]string{})
	if req.Alergenos != nil {
		alergJSON, _ = json.Marshal(*req.Alergenos)
	}
	res, err := s.db.ExecContext(r.Context(), `
		INSERT INTO POSTRES (restaurant_id, DESCRIPCION, alergenos, active)
		VALUES (?, ?, ?, ?)
	`, restaurantID, desc, string(alergJSON), activeInt)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creando postre")
		return
	}
	newID, _ := res.LastInsertId()
	item, postre, _, err := s.getPostreByID(r, restaurantID, int(newID))
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creando postre")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"num":     int(newID),
		"item":    item,
		"postre":  postre,
	})
}

func (s *Server) createVino(w http.ResponseWriter, r *http.Request, restaurantID int, req comidaUpsertRequest) {
	tipo := strings.ToUpper(strings.TrimSpace(comidaPtrString(req.Tipo)))
	nombre := strings.TrimSpace(comidaPtrString(req.Nombre))
	bodega := strings.TrimSpace(comidaPtrString(req.Bodega))
	if tipo == "" || nombre == "" || bodega == "" {
		writeComidaValidationError(w, "tipo, nombre y bodega son requeridos")
		return
	}
	precio := comidaPtrFloat(req.Precio)
	if precio <= 0 {
		writeComidaValidationError(w, "precio requerido")
		return
	}
	activeInt := 1
	if req.Active != nil && !*req.Active {
		activeInt = 0
	}

	res, err := s.db.ExecContext(r.Context(), `
		INSERT INTO VINOS
			(restaurant_id, tipo, nombre, precio, descripcion, bodega, denominacion_origen, graduacion, anyo, active, foto_path, foto)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, NULL)
	`, restaurantID,
		tipo,
		nombre,
		precio,
		strings.TrimSpace(comidaPtrString(req.Descripcion)),
		bodega,
		strings.TrimSpace(comidaPtrString(req.DenominacionOrigen)),
		comidaPtrFloat(req.Graduacion),
		strings.TrimSpace(comidaPtrString(req.Anyo)),
		activeInt,
	)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creando vino")
		return
	}
	newID, _ := res.LastInsertId()

	warning := ""
	if req.ImageBase64 != nil && strings.TrimSpace(*req.ImageBase64) != "" {
		img, err := decodeBase64Image(*req.ImageBase64)
		if err != nil {
			writeComidaValidationError(w, "Imagen invalida")
			return
		}
		objectPath, err := s.UploadWineImage(r.Context(), tipo, int(newID), img)
		if err != nil {
			warning = "Vino creado, pero la imagen no se pudo subir"
		} else {
			if _, err := s.db.ExecContext(r.Context(), "UPDATE VINOS SET foto_path = ?, foto = NULL WHERE restaurant_id = ? AND num = ?", objectPath, restaurantID, int(newID)); err != nil {
				warning = "Vino creado, pero la imagen no se pudo guardar"
			}
		}
	}

	item, _, err := s.getVinoByID(r, restaurantID, int(newID))
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creando vino")
		return
	}
	resp := map[string]any{
		"success": true,
		"num":     int(newID),
		"item":    item,
		"vino":    item,
	}
	if warning != "" {
		resp["warning"] = warning
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

func (s *Server) handleComidaPatchByType(w http.ResponseWriter, r *http.Request, t comidaTipo) {
	restaurantID, ok := comidaRestaurantIDFromRequest(r)
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	id, err := parseIDParam(r)
	if err != nil || id <= 0 {
		writeComidaValidationError(w, "ID invalido")
		return
	}
	var req comidaUpsertRequest
	if err := readJSONBody(r, &req); err != nil {
		writeComidaValidationError(w, "Invalid JSON")
		return
	}

	switch t {
	case comidaTipoVinos:
		s.patchVino(w, r, restaurantID, id, req)
	case comidaTipoPostres:
		s.patchPostre(w, r, restaurantID, id, req)
	default:
		s.patchCatalogItem(w, r, restaurantID, t, id, req)
	}
}

func (s *Server) patchCatalogItem(w http.ResponseWriter, r *http.Request, restaurantID int, t comidaTipo, id int, req comidaUpsertRequest) {
	sets := make([]string, 0, 12)
	args := make([]any, 0, 16)

	if req.Tipo != nil {
		sets = append(sets, "tipo = ?")
		args = append(args, strings.ToUpper(strings.TrimSpace(*req.Tipo)))
	}
	if req.Nombre != nil {
		n := strings.TrimSpace(*req.Nombre)
		if n == "" {
			writeComidaValidationError(w, "Nombre invalido")
			return
		}
		sets = append(sets, "nombre = ?")
		args = append(args, n)
	}
	if req.Precio != nil {
		if *req.Precio < 0 {
			writeComidaValidationError(w, "Precio invalido")
			return
		}
		sets = append(sets, "precio = ?")
		args = append(args, *req.Precio)
	}
	if req.Descripcion != nil {
		sets = append(sets, "descripcion = ?")
		args = append(args, strings.TrimSpace(*req.Descripcion))
	}
	if req.Titulo != nil {
		sets = append(sets, "titulo = ?")
		args = append(args, strings.TrimSpace(*req.Titulo))
	}
	if req.Suplemento != nil {
		if *req.Suplemento < 0 {
			writeComidaValidationError(w, "Suplemento invalido")
			return
		}
		sets = append(sets, "suplemento = ?")
		args = append(args, *req.Suplemento)
	}
	if req.Alergenos != nil {
		alergJSON, _ := json.Marshal(*req.Alergenos)
		sets = append(sets, "alergenos_json = ?")
		args = append(args, string(alergJSON))
	}
	if req.Active != nil {
		activeInt := 0
		if *req.Active {
			activeInt = 1
		}
		sets = append(sets, "active = ?")
		args = append(args, activeInt)
	}
	if req.ImageBase64 != nil {
		raw := strings.TrimSpace(*req.ImageBase64)
		if raw == "" {
			sets = append(sets, "foto_path = NULL", "foto = NULL")
		} else {
			b, err := decodeBase64Image(raw)
			if err != nil {
				writeComidaValidationError(w, "Imagen invalida")
				return
			}
			sets = append(sets, "foto_path = NULL", "foto = ?")
			args = append(args, b)
		}
	}

	if req.CategoryID != nil || req.Categoria != nil || req.Category != nil {
		if t == comidaTipoPlatos {
			categoria, categoryID, err := s.resolvePlatoCategory(r, restaurantID, req, true)
			if err == sql.ErrNoRows {
				writeComidaValidationError(w, "Categoria invalida")
				return
			}
			if err != nil {
				httpx.WriteError(w, http.StatusInternalServerError, "Error resolviendo categoria")
				return
			}
			sets = append(sets, "categoria = ?")
			args = append(args, categoria)
			if categoryID.Valid {
				sets = append(sets, "category_id = ?")
				args = append(args, categoryID.Int64)
			} else {
				sets = append(sets, "category_id = NULL")
			}
		} else {
			rawCat := strings.TrimSpace(firstNonEmpty(comidaPtrString(req.Categoria), comidaPtrString(req.Category)))
			sets = append(sets, "categoria = ?", "category_id = NULL")
			args = append(args, rawCat)
		}
	}

	if len(sets) == 0 {
		writeComidaValidationError(w, "No fields to update")
		return
	}

	args = append(args, id, restaurantID, string(t))
	q := "UPDATE comida_items SET " + strings.Join(sets, ", ") + " WHERE id = ? AND restaurant_id = ? AND source_type = ?"
	res, err := s.db.ExecContext(r.Context(), q, args...)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando comida")
		return
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		writeComidaValidationError(w, "Elemento no encontrado")
		return
	}

	item, _, err := s.getCatalogItemByID(r, restaurantID, t, id)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando comida")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"item":    item,
	})
}

func (s *Server) patchPostre(w http.ResponseWriter, r *http.Request, restaurantID, id int, req comidaUpsertRequest) {
	sets := make([]string, 0, 6)
	args := make([]any, 0, 8)

	if req.Descripcion != nil || req.Nombre != nil {
		desc := strings.TrimSpace(firstNonEmpty(comidaPtrString(req.Descripcion), comidaPtrString(req.Nombre)))
		if desc == "" {
			writeComidaValidationError(w, "Descripcion invalida")
			return
		}
		sets = append(sets, "DESCRIPCION = ?")
		args = append(args, desc)
	}
	if req.Alergenos != nil {
		alergJSON, _ := json.Marshal(*req.Alergenos)
		sets = append(sets, "alergenos = ?")
		args = append(args, string(alergJSON))
	}
	if req.Active != nil {
		activeInt := 0
		if *req.Active {
			activeInt = 1
		}
		sets = append(sets, "active = ?")
		args = append(args, activeInt)
	}
	if len(sets) == 0 {
		writeComidaValidationError(w, "No fields to update")
		return
	}

	args = append(args, id, restaurantID)
	q := "UPDATE POSTRES SET " + strings.Join(sets, ", ") + " WHERE NUM = ? AND restaurant_id = ?"
	res, err := s.db.ExecContext(r.Context(), q, args...)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando postre")
		return
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		writeComidaValidationError(w, "Elemento no encontrado")
		return
	}
	item, postre, _, err := s.getPostreByID(r, restaurantID, id)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando postre")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"item":    item,
		"postre":  postre,
	})
}

func (s *Server) patchVino(w http.ResponseWriter, r *http.Request, restaurantID, id int, req comidaUpsertRequest) {
	sets := make([]string, 0, 12)
	args := make([]any, 0, 16)
	imageWarning := ""

	if req.Tipo != nil {
		tipo := strings.ToUpper(strings.TrimSpace(*req.Tipo))
		if tipo == "" {
			writeComidaValidationError(w, "tipo invalido")
			return
		}
		sets = append(sets, "tipo = ?")
		args = append(args, tipo)
	}
	if req.Nombre != nil {
		nombre := strings.TrimSpace(*req.Nombre)
		if nombre == "" {
			writeComidaValidationError(w, "nombre invalido")
			return
		}
		sets = append(sets, "nombre = ?")
		args = append(args, nombre)
	}
	if req.Precio != nil {
		if *req.Precio <= 0 {
			writeComidaValidationError(w, "precio invalido")
			return
		}
		sets = append(sets, "precio = ?")
		args = append(args, *req.Precio)
	}
	if req.Descripcion != nil {
		sets = append(sets, "descripcion = ?")
		args = append(args, strings.TrimSpace(*req.Descripcion))
	}
	if req.Bodega != nil {
		b := strings.TrimSpace(*req.Bodega)
		if b == "" {
			writeComidaValidationError(w, "bodega invalida")
			return
		}
		sets = append(sets, "bodega = ?")
		args = append(args, b)
	}
	if req.DenominacionOrigen != nil {
		sets = append(sets, "denominacion_origen = ?")
		args = append(args, strings.TrimSpace(*req.DenominacionOrigen))
	}
	if req.Graduacion != nil {
		sets = append(sets, "graduacion = ?")
		args = append(args, *req.Graduacion)
	}
	if req.Anyo != nil {
		sets = append(sets, "anyo = ?")
		args = append(args, strings.TrimSpace(*req.Anyo))
	}
	if req.Active != nil {
		activeInt := 0
		if *req.Active {
			activeInt = 1
		}
		sets = append(sets, "active = ?")
		args = append(args, activeInt)
	}
	if req.ImageBase64 != nil {
		raw := strings.TrimSpace(*req.ImageBase64)
		if raw == "" {
			sets = append(sets, "foto_path = NULL", "foto = NULL")
		} else {
			img, err := decodeBase64Image(raw)
			if err != nil {
				writeComidaValidationError(w, "Imagen invalida")
				return
			}
			wineTipo := strings.ToUpper(strings.TrimSpace(comidaPtrString(req.Tipo)))
			if wineTipo == "" {
				if err := s.db.QueryRowContext(r.Context(), "SELECT COALESCE(tipo,'') FROM VINOS WHERE num = ? AND restaurant_id = ? LIMIT 1", id, restaurantID).Scan(&wineTipo); err != nil || strings.TrimSpace(wineTipo) == "" {
					wineTipo = "OTROS"
				}
			}
			objectPath, err := s.UploadWineImage(r.Context(), wineTipo, id, img)
			if err != nil {
				imageWarning = "Vino actualizado, pero la imagen no se pudo subir"
			} else {
				sets = append(sets, "foto_path = ?", "foto = NULL")
				args = append(args, objectPath)
			}
		}
	}

	if len(sets) == 0 {
		writeComidaValidationError(w, "No fields to update")
		return
	}

	args = append(args, id, restaurantID)
	q := "UPDATE VINOS SET " + strings.Join(sets, ", ") + " WHERE num = ? AND restaurant_id = ?"
	res, err := s.db.ExecContext(r.Context(), q, args...)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando vino")
		return
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		writeComidaValidationError(w, "Elemento no encontrado")
		return
	}
	item, _, err := s.getVinoByID(r, restaurantID, id)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando vino")
		return
	}
	out := map[string]any{
		"success": true,
		"item":    item,
		"vino":    item,
	}
	if imageWarning != "" {
		out["warning"] = imageWarning
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (s *Server) handleComidaDeleteByType(w http.ResponseWriter, r *http.Request, t comidaTipo) {
	restaurantID, ok := comidaRestaurantIDFromRequest(r)
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	id, err := parseIDParam(r)
	if err != nil || id <= 0 {
		writeComidaValidationError(w, "ID invalido")
		return
	}

	var res sql.Result
	switch t {
	case comidaTipoVinos:
		res, err = s.db.ExecContext(r.Context(), "DELETE FROM VINOS WHERE num = ? AND restaurant_id = ?", id, restaurantID)
	case comidaTipoPostres:
		res, err = s.db.ExecContext(r.Context(), "DELETE FROM POSTRES WHERE NUM = ? AND restaurant_id = ?", id, restaurantID)
	default:
		res, err = s.db.ExecContext(r.Context(), "DELETE FROM comida_items WHERE id = ? AND restaurant_id = ? AND source_type = ?", id, restaurantID, string(t))
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error eliminando comida")
		return
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		writeComidaValidationError(w, "Elemento no encontrado")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
	})
}

// Legacy /api/admin/platos|bebidas|cafes aliases used by existing backoffice screen.
func (s *Server) handleBOPlatosList(w http.ResponseWriter, r *http.Request) {
	s.handleComidaListByType(w, r, comidaTipoPlatos)
}
func (s *Server) handleBOPlatosCreate(w http.ResponseWriter, r *http.Request) {
	s.handleComidaCreateByType(w, r, comidaTipoPlatos)
}
func (s *Server) handleBOPlatosPatch(w http.ResponseWriter, r *http.Request) {
	s.handleComidaPatchByType(w, r, comidaTipoPlatos)
}
func (s *Server) handleBOPlatosDelete(w http.ResponseWriter, r *http.Request) {
	s.handleComidaDeleteByType(w, r, comidaTipoPlatos)
}

func (s *Server) handleBOBebidasList(w http.ResponseWriter, r *http.Request) {
	s.handleComidaListByType(w, r, comidaTipoBebidas)
}
func (s *Server) handleBOBebidasCreate(w http.ResponseWriter, r *http.Request) {
	s.handleComidaCreateByType(w, r, comidaTipoBebidas)
}
func (s *Server) handleBOBebidasPatch(w http.ResponseWriter, r *http.Request) {
	s.handleComidaPatchByType(w, r, comidaTipoBebidas)
}
func (s *Server) handleBOBebidasDelete(w http.ResponseWriter, r *http.Request) {
	s.handleComidaDeleteByType(w, r, comidaTipoBebidas)
}

func (s *Server) handleBOCafesList(w http.ResponseWriter, r *http.Request) {
	s.handleComidaListByType(w, r, comidaTipoCafes)
}
func (s *Server) handleBOCafesCreate(w http.ResponseWriter, r *http.Request) {
	s.handleComidaCreateByType(w, r, comidaTipoCafes)
}
func (s *Server) handleBOCafesPatch(w http.ResponseWriter, r *http.Request) {
	s.handleComidaPatchByType(w, r, comidaTipoCafes)
}
func (s *Server) handleBOCafesDelete(w http.ResponseWriter, r *http.Request) {
	s.handleComidaDeleteByType(w, r, comidaTipoCafes)
}

func (s *Server) handleBOPlatosToggle(w http.ResponseWriter, r *http.Request) {
	s.handleBOCatalogToggle(w, r, comidaTipoPlatos)
}
func (s *Server) handleBOBebidasToggle(w http.ResponseWriter, r *http.Request) {
	s.handleBOCatalogToggle(w, r, comidaTipoBebidas)
}
func (s *Server) handleBOCafesToggle(w http.ResponseWriter, r *http.Request) {
	s.handleBOCatalogToggle(w, r, comidaTipoCafes)
}

func (s *Server) handleBOCatalogToggle(w http.ResponseWriter, r *http.Request, t comidaTipo) {
	restaurantID, ok := comidaRestaurantIDFromRequest(r)
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	id, err := parseIDParam(r)
	if err != nil || id <= 0 {
		writeComidaValidationError(w, "ID invalido")
		return
	}

	var current int
	if err := s.db.QueryRowContext(r.Context(), `
		SELECT active
		FROM comida_items
		WHERE restaurant_id = ? AND source_type = ? AND id = ?
		LIMIT 1
	`, restaurantID, string(t), id).Scan(&current); err != nil {
		if err == sql.ErrNoRows {
			writeComidaValidationError(w, "Elemento no encontrado")
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando estado")
		return
	}

	next := 1
	if current != 0 {
		next = 0
	}
	if _, err := s.db.ExecContext(r.Context(), `
		UPDATE comida_items
		SET active = ?
		WHERE restaurant_id = ? AND source_type = ? AND id = ?
	`, next, restaurantID, string(t), id); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando estado")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"active":  next != 0,
	})
}
