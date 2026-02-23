package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"math"
	"net"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"

	"preactvillacarmen/internal/httpx"
	"preactvillacarmen/internal/lib/specialmenuimage"
)

const (
	boPremiumDomainMarkupFactor = 1.5
	boPremiumWhatsAppFeatureKey = "whatsapp_pack"
	boWebsiteDefaultThemeID     = "villa-carmen"
)

var boPremiumDomainRe = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,24}$`)

var boWebsiteMenuTypes = map[string]struct{}{
	"closed_conventional": {},
	"closed_group":        {},
	"a_la_carte":          {},
	"a_la_carte_group":    {},
	"special":             {},
}

var boWebsiteThemeCatalog = []map[string]any{
	{"id": "villa-carmen", "name": "Villa Carmen", "thumbnail_url": "", "active": true},
	{"id": "lumen-gold", "name": "Lumen Gold", "thumbnail_url": "", "active": true},
	{"id": "terra-olive", "name": "Terra Olive", "thumbnail_url": "", "active": true},
	{"id": "nocturne-copper", "name": "Nocturne Copper", "thumbnail_url": "", "active": true},
	{"id": "sea-breeze", "name": "Sea Breeze", "thumbnail_url": "", "active": true},
}

type boSQLExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

type boSQLVariant struct {
	query string
	args  []any
}

type boTablesHub struct {
	mu    sync.RWMutex
	rooms map[int]map[*boTablesClient]struct{}
}

type boTablesClient struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

type boPremiumWebsiteUpsertRequest struct {
	TemplateID   *string `json:"template_id"`
	CustomHTML   *string `json:"custom_html"`
	Domain       *string `json:"domain"`
	DomainStatus *string `json:"domain_status"`
	IsPublished  *bool   `json:"is_published"`
	Draft        *struct {
		HTMLContent *string        `json:"html_content"`
		Meta        map[string]any `json:"meta"`
	} `json:"draft"`
}

type boPremiumWebsiteAIGenerateRequest struct {
	Prompt string `json:"prompt"`
}

type boPremiumWebsiteMenuTemplatesUpsertRequest struct {
	DefaultThemeID *string           `json:"default_theme_id"`
	Overrides      map[string]string `json:"overrides"`
}

type boPremiumDomainQuoteRequest struct {
	Domain string `json:"domain"`
	Query  string `json:"query"`
	Years  int    `json:"years"`
}

type boPremiumDomainRegisterRequest struct {
	Domain    string `json:"domain"`
	Years     int    `json:"years"`
	IsPrimary *bool  `json:"is_primary"`
}

type boPremiumDomainVerifyRequest struct {
	Domain string `json:"domain"`
}

type boPremiumTablesMutationRequest struct {
	Entity          string         `json:"entity"`
	ID              int64          `json:"id"`
	Date            string         `json:"date"`
	FloorNumber     *int           `json:"floor_number"`
	AreaID          *int64         `json:"area_id"`
	Name            *string        `json:"name"`
	Capacity        *int           `json:"capacity"`
	Seats           *int           `json:"seats"`
	Status          *string        `json:"status"`
	XPos            *float64       `json:"x_pos"`
	YPos            *float64       `json:"y_pos"`
	DisplayOrder    *int           `json:"display_order"`
	SortOrder       *int           `json:"sort_order"`
	IsActive        *bool          `json:"is_active"`
	Shape           *string        `json:"shape"`
	FillColor       *string        `json:"fill_color"`
	OutlineColor    *string        `json:"outline_color"`
	StylePreset     *string        `json:"style_preset"`
	TextureImageURL *string        `json:"texture_image_url"`
	Metadata        map[string]any `json:"metadata"`
}

type boMembersWhatsAppSendRequest struct {
	MemberID *int64 `json:"member_id"`
	Phone    string `json:"phone"`
	Text     string `json:"text"`
	Message  string `json:"message"`
}

type boMembersWhatsAppSubscribeRequest struct {
	Amount   *float64 `json:"amount"`
	Currency string   `json:"currency"`
}

func newBOTablesHub() *boTablesHub {
	return &boTablesHub{rooms: map[int]map[*boTablesClient]struct{}{}}
}

func (h *boTablesHub) add(restaurantID int, c *boTablesClient) {
	if h == nil || restaurantID <= 0 || c == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	room := h.rooms[restaurantID]
	if room == nil {
		room = map[*boTablesClient]struct{}{}
		h.rooms[restaurantID] = room
	}
	room[c] = struct{}{}
}

func (h *boTablesHub) remove(restaurantID int, c *boTablesClient) {
	if h == nil || restaurantID <= 0 || c == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	room := h.rooms[restaurantID]
	if room == nil {
		return
	}
	delete(room, c)
	if len(room) == 0 {
		delete(h.rooms, restaurantID)
	}
}

func (h *boTablesHub) list(restaurantID int) []*boTablesClient {
	if h == nil || restaurantID <= 0 {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	room := h.rooms[restaurantID]
	if len(room) == 0 {
		return nil
	}
	out := make([]*boTablesClient, 0, len(room))
	for c := range room {
		out = append(out, c)
	}
	return out
}

func (h *boTablesHub) broadcast(restaurantID int, payload any) {
	if h == nil || restaurantID <= 0 {
		return
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	for _, c := range h.list(restaurantID) {
		if err := c.writeText(raw); err != nil {
			h.remove(restaurantID, c)
			_ = c.close()
		}
	}
}

func (c *boTablesClient) writeText(raw []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(7 * time.Second))
	return c.conn.WriteMessage(websocket.TextMessage, raw)
}

func (c *boTablesClient) writeJSON(v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.writeText(raw)
}

func (c *boTablesClient) ping() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(7 * time.Second))
	return c.conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(7*time.Second))
}

func (c *boTablesClient) close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.Close()
}

var boTablesWSUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func (s *Server) handleBOPremiumWebsiteGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	website, found, err := s.loadBOPremiumWebsite(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		writeBOPremiumError(w, http.StatusInternalServerError, "WEBSITE_READ_FAILED", "No se pudo cargar website")
		return
	}
	if !found {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": true,
			"data":    nil,
			"config":  nil,
			"website": nil,
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data":    website,
		"config":  website,
		"website": website,
	})
}

func (s *Server) handleBOPremiumWebsiteUpsert(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req boPremiumWebsiteUpsertRequest
	if err := readJSONBody(r, &req); err != nil {
		writeBOPremiumError(w, http.StatusBadRequest, "BAD_REQUEST", "JSON inválido")
		return
	}

	currentWebsite, currentFound, err := s.loadBOPremiumWebsite(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		writeBOPremiumError(w, http.StatusInternalServerError, "WEBSITE_READ_FAILED", "No se pudo cargar website")
		return
	}

	templateID := ""
	customHTML := ""
	domain := ""
	domainStatus := "pending"
	isPublished := false
	draftHTML := ""
	draftMeta := map[string]any{}

	if currentFound {
		templateID = firstStringFromMap(currentWebsite, "template_id")
		customHTML = firstStringFromMap(currentWebsite, "custom_html")
		domain = firstStringFromMap(currentWebsite, "domain")
		domainStatus = firstStringFromMap(currentWebsite, "domain_status")
		isPublished = anyToBool(currentWebsite["is_published"])
		if existingDraft, ok := asStringAnyMap(currentWebsite["draft"]); ok {
			draftHTML = firstStringFromMap(existingDraft, "html_content")
			if metaMap, ok := asStringAnyMap(existingDraft["meta"]); ok {
				draftMeta = metaMap
			}
		}
	}

	if req.TemplateID != nil {
		templateID = strings.TrimSpace(*req.TemplateID)
	}
	if req.CustomHTML != nil {
		customHTML = strings.TrimSpace(*req.CustomHTML)
	}
	if req.Domain != nil {
		rawDomain := strings.TrimSpace(*req.Domain)
		if rawDomain == "" {
			domain = ""
		} else {
			normalizedDomain := normalizePremiumDomain(rawDomain)
			if normalizedDomain == "" {
				writeBOPremiumError(w, http.StatusBadRequest, "BAD_REQUEST", "dominio inválido")
				return
			}
			domain = normalizedDomain
		}
	}
	if req.DomainStatus != nil {
		domainStatus = strings.ToLower(strings.TrimSpace(*req.DomainStatus))
	}
	if req.IsPublished != nil {
		isPublished = *req.IsPublished
	}
	if req.Draft != nil {
		if req.Draft.HTMLContent != nil {
			draftHTML = strings.TrimSpace(*req.Draft.HTMLContent)
		}
		if req.Draft.Meta != nil {
			draftMeta = req.Draft.Meta
		}
	}

	if req.TemplateID != nil && req.CustomHTML == nil {
		customHTML = ""
	}
	if req.CustomHTML != nil && req.TemplateID == nil {
		templateID = ""
	}
	if strings.TrimSpace(domainStatus) == "" {
		domainStatus = "pending"
	}

	if err := s.upsertBOPremiumWebsite(r.Context(), a.ActiveRestaurantID, templateID, customHTML, domain, domainStatus, isPublished, draftHTML, draftMeta); err != nil {
		writeBOPremiumError(w, http.StatusInternalServerError, "WEBSITE_UPSERT_FAILED", "No se pudo guardar website")
		return
	}

	website, found, err := s.loadBOPremiumWebsite(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		writeBOPremiumError(w, http.StatusInternalServerError, "WEBSITE_READ_FAILED", "No se pudo cargar website")
		return
	}
	if !found {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": true,
			"data":    nil,
			"config":  nil,
			"website": nil,
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data":    website,
		"config":  website,
		"website": website,
	})
}

func (s *Server) handleBOPremiumWebsiteTemplates(w http.ResponseWriter, r *http.Request) {
	_, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":   true,
		"templates": boWebsiteThemeCatalog,
	})
}

func isValidWebsiteThemeID(themeID string) bool {
	normalized := normalizeWebsiteThemeID(themeID)
	if normalized == "" {
		return false
	}
	for _, item := range boWebsiteThemeCatalog {
		if strings.EqualFold(strings.TrimSpace(firstStringFromMap(item, "id")), normalized) {
			return true
		}
	}
	return false
}

func normalizeWebsiteThemeAlias(raw string) string {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if trimmed == "" {
		return ""
	}
	replaced := strings.NewReplacer("_", "-", " ", "-").Replace(trimmed)
	var out strings.Builder
	prevDash := false
	for _, r := range replaced {
		isAlphaNum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isAlphaNum {
			out.WriteRune(r)
			prevDash = false
			continue
		}
		if r == '-' {
			if !prevDash && out.Len() > 0 {
				out.WriteRune('-')
				prevDash = true
			}
			continue
		}
	}
	return strings.Trim(out.String(), "-")
}

func compactWebsiteThemeAlias(raw string) string {
	return strings.ReplaceAll(normalizeWebsiteThemeAlias(raw), "-", "")
}

func normalizeWebsiteThemeID(themeID string) string {
	trimmed := strings.TrimSpace(themeID)
	if trimmed == "" {
		return ""
	}
	if strings.EqualFold(trimmed, "preact-copy") {
		return boWebsiteDefaultThemeID
	}
	candidateAlias := normalizeWebsiteThemeAlias(trimmed)
	candidateCompact := compactWebsiteThemeAlias(trimmed)
	for _, item := range boWebsiteThemeCatalog {
		id := strings.TrimSpace(firstStringFromMap(item, "id"))
		name := strings.TrimSpace(firstStringFromMap(item, "name"))
		if strings.EqualFold(id, trimmed) {
			return id
		}
		if candidateAlias != "" {
			if candidateAlias == normalizeWebsiteThemeAlias(id) || candidateAlias == normalizeWebsiteThemeAlias(name) {
				return id
			}
		}
		if candidateCompact != "" {
			if candidateCompact == compactWebsiteThemeAlias(id) || candidateCompact == compactWebsiteThemeAlias(name) {
				return id
			}
		}
		if strings.EqualFold(name, trimmed) {
			return id
		}
	}
	return trimmed
}

func normalizeWebsiteMenuType(menuType string) string {
	out := strings.TrimSpace(strings.ToLower(menuType))
	if out == "group" {
		return "closed_group"
	}
	return out
}

func (s *Server) loadBOPremiumWebsiteMenuTemplates(ctx context.Context, restaurantID int) (string, map[string]string, bool, error) {
	defaultThemeID := ""
	hasDefaultAssignment := false
	if row, found, err := s.queryOneAsMap(ctx, `SELECT template_id FROM restaurant_websites WHERE restaurant_id = ? LIMIT 1`, restaurantID); err != nil {
		return "", nil, false, err
	} else if found {
		defaultThemeID = strings.TrimSpace(firstStringFromMap(row, "template_id"))
		if isValidWebsiteThemeID(defaultThemeID) || strings.EqualFold(defaultThemeID, "preact-copy") {
			hasDefaultAssignment = true
		}
	}
	defaultThemeID = normalizeWebsiteThemeID(defaultThemeID)
	if !isValidWebsiteThemeID(defaultThemeID) {
		defaultThemeID = boWebsiteDefaultThemeID
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT menu_type, theme_id
		FROM restaurant_menu_templates
		WHERE restaurant_id = ?
	`, restaurantID)
	if err != nil {
		return "", nil, false, err
	}
	defer rows.Close()

	overrides := map[string]string{}
	for rows.Next() {
		var menuTypeRaw string
		var themeIDRaw string
		if scanErr := rows.Scan(&menuTypeRaw, &themeIDRaw); scanErr != nil {
			return "", nil, false, scanErr
		}
		menuType := normalizeWebsiteMenuType(menuTypeRaw)
		if _, ok := boWebsiteMenuTypes[menuType]; !ok {
			continue
		}
		themeID := normalizeWebsiteThemeID(themeIDRaw)
		if !isValidWebsiteThemeID(themeID) {
			continue
		}
		overrides[menuType] = themeID
	}
	if err := rows.Err(); err != nil {
		return "", nil, false, err
	}
	hasAssignment := hasDefaultAssignment || len(overrides) > 0
	return defaultThemeID, overrides, hasAssignment, nil
}

func (s *Server) upsertBOPremiumWebsiteMenuTemplateOverrides(ctx context.Context, restaurantID int, overrides map[string]string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := tx.ExecContext(ctx, `DELETE FROM restaurant_menu_templates WHERE restaurant_id = ?`, restaurantID); err != nil {
		return err
	}

	for menuType, themeID := range overrides {
		if _, ok := boWebsiteMenuTypes[menuType]; !ok {
			continue
		}
		if !isValidWebsiteThemeID(themeID) {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO restaurant_menu_templates
				(restaurant_id, menu_type, theme_id, created_at, updated_at)
			VALUES (?, ?, ?, NOW(), NOW())
			ON DUPLICATE KEY UPDATE
				theme_id = VALUES(theme_id),
				updated_at = NOW()
		`, restaurantID, menuType, themeID); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *Server) handleBOPremiumWebsiteMenuTemplatesGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	defaultThemeID, overrides, hasAssignment, err := s.loadBOPremiumWebsiteMenuTemplates(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		writeBOPremiumError(w, http.StatusInternalServerError, "WEBSITE_MENU_TEMPLATES_READ_FAILED", "No se pudo cargar configuracion de plantillas")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":          true,
		"default_theme_id": defaultThemeID,
		"overrides":        overrides,
		"themes":           boWebsiteThemeCatalog,
		"assigned":         hasAssignment,
	})
}

func (s *Server) handleBOPremiumWebsiteMenuTemplatesUpsert(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req boPremiumWebsiteMenuTemplatesUpsertRequest
	if err := readJSONBody(r, &req); err != nil {
		writeBOPremiumError(w, http.StatusBadRequest, "BAD_REQUEST", "JSON invalido")
		return
	}

	defaultThemeID := boWebsiteDefaultThemeID
	if req.DefaultThemeID != nil {
		candidate := normalizeWebsiteThemeID(*req.DefaultThemeID)
		if candidate != "" {
			if !isValidWebsiteThemeID(candidate) {
				writeBOPremiumError(w, http.StatusBadRequest, "BAD_REQUEST", "Plantilla por defecto invalida")
				return
			}
			defaultThemeID = candidate
		}
	}

	overrides := map[string]string{}
	for menuTypeRaw, themeIDRaw := range req.Overrides {
		menuType := normalizeWebsiteMenuType(menuTypeRaw)
		if _, ok := boWebsiteMenuTypes[menuType]; !ok {
			continue
		}
		themeID := normalizeWebsiteThemeID(themeIDRaw)
		if themeID == "" {
			continue
		}
		if !isValidWebsiteThemeID(themeID) {
			writeBOPremiumError(w, http.StatusBadRequest, "BAD_REQUEST", "Plantilla invalida para tipo de menu")
			return
		}
		overrides[menuType] = themeID
	}

	currentWebsite, currentFound, err := s.loadBOPremiumWebsite(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		writeBOPremiumError(w, http.StatusInternalServerError, "WEBSITE_READ_FAILED", "No se pudo cargar website")
		return
	}

	customHTML := ""
	domain := ""
	domainStatus := "pending"
	isPublished := false
	draftHTML := ""
	draftMeta := map[string]any{}
	if currentFound {
		customHTML = firstStringFromMap(currentWebsite, "custom_html")
		domain = firstStringFromMap(currentWebsite, "domain")
		domainStatus = firstStringFromMap(currentWebsite, "domain_status")
		isPublished = anyToBool(currentWebsite["is_published"])
		if draftMap, ok := asStringAnyMap(currentWebsite["draft"]); ok {
			draftHTML = firstStringFromMap(draftMap, "html_content")
			if metaMap, ok := asStringAnyMap(draftMap["meta"]); ok {
				draftMeta = metaMap
			}
		}
	}

	if err := s.upsertBOPremiumWebsite(r.Context(), a.ActiveRestaurantID, defaultThemeID, customHTML, domain, domainStatus, isPublished, draftHTML, draftMeta); err != nil {
		writeBOPremiumError(w, http.StatusInternalServerError, "WEBSITE_UPSERT_FAILED", "No se pudo guardar plantilla por defecto")
		return
	}
	if err := s.upsertBOPremiumWebsiteMenuTemplateOverrides(r.Context(), a.ActiveRestaurantID, overrides); err != nil {
		writeBOPremiumError(w, http.StatusInternalServerError, "WEBSITE_MENU_TEMPLATES_UPSERT_FAILED", "No se pudo guardar configuracion de plantillas")
		return
	}

	defaultThemeOut, overridesOut, hasAssignmentOut, err := s.loadBOPremiumWebsiteMenuTemplates(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		writeBOPremiumError(w, http.StatusInternalServerError, "WEBSITE_MENU_TEMPLATES_READ_FAILED", "No se pudo cargar configuracion de plantillas")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":          true,
		"default_theme_id": defaultThemeOut,
		"overrides":        overridesOut,
		"themes":           boWebsiteThemeCatalog,
		"assigned":         hasAssignmentOut,
	})
}

func (s *Server) handleBOPremiumWebsiteAIGenerate(w http.ResponseWriter, r *http.Request) {
	_, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req boPremiumWebsiteAIGenerateRequest
	if err := readJSONBody(r, &req); err != nil {
		writeBOPremiumError(w, http.StatusBadRequest, "BAD_REQUEST", "JSON inválido")
		return
	}

	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		writeBOPremiumError(w, http.StatusBadRequest, "BAD_REQUEST", "prompt requerido")
		return
	}

	escapedPrompt := html.EscapeString(prompt)
	customHTML := fmt.Sprintf(`<section class="hero"><h1>%s</h1><p>Reserva en segundos y disfruta una experiencia gastronómica inolvidable.</p><a href="#reservas">Reservar mesa</a></section>`, escapedPrompt)
	draft := map[string]any{
		"html_content": customHTML,
		"meta": map[string]any{
			"prompt":       prompt,
			"generated_at": time.Now().UTC().Format(time.RFC3339),
			"version":      "premium-v1",
		},
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":     true,
		"custom_html": customHTML,
		"draft":       draft,
	})
}

func (s *Server) loadBOPremiumWebsite(ctx context.Context, restaurantID int) (map[string]any, bool, error) {
	row, found, err := s.queryOneAsMap(ctx, `SELECT * FROM restaurant_websites WHERE restaurant_id = ? LIMIT 1`, restaurantID)
	if err != nil {
		return nil, false, err
	}
	if !found {
		return nil, false, nil
	}

	customHTML := firstStringFromMap(row, "custom_html", "html_content")
	templateID := firstStringFromMap(row, "template_id")
	domain := firstStringFromMap(row, "domain")
	domainStatus := strings.ToLower(firstStringFromMap(row, "domain_status"))
	isPublished := anyToBool(row["is_published"])
	draftHTML := firstStringFromMap(row, "draft_html_content", "draft_html")
	draftMetaRaw := firstStringFromMap(row, "draft_meta_json", "draft_meta", "meta_json")
	updatedAt := firstStringFromMap(row, "updated_at")

	draftMeta := map[string]any{}
	if strings.TrimSpace(draftMetaRaw) != "" {
		_ = json.Unmarshal([]byte(draftMetaRaw), &draftMeta)
	}

	activeDomain := ""
	activeDomainStatus := ""
	domainRow, domainFound, domainErr := s.queryOneAsMap(ctx, `SELECT * FROM restaurant_domains WHERE restaurant_id = ? ORDER BY is_primary DESC, id DESC LIMIT 1`, restaurantID)
	if domainErr != nil && !isSQLSchemaError(domainErr) {
		return nil, false, domainErr
	}
	if domainFound {
		activeDomain = firstStringFromMap(domainRow, "domain")
		activeDomainStatus = strings.ToLower(firstStringFromMap(domainRow, "registration_status"))
	}

	if domain == "" {
		domain = activeDomain
	}
	if strings.TrimSpace(domainStatus) == "" {
		domainStatus = activeDomainStatus
	}
	if strings.TrimSpace(domainStatus) == "" {
		if domain != "" {
			domainStatus = "active"
		} else {
			domainStatus = "pending"
		}
	}
	if activeDomain == "" {
		activeDomain = domain
	}

	id, _ := anyToInt64OK(row["id"])

	out := map[string]any{
		"id":            id,
		"restaurant_id": restaurantID,
		"template_id":   emptyStringToNil(templateID),
		"custom_html":   emptyStringToNil(customHTML),
		"domain":        emptyStringToNil(domain),
		"active_domain": emptyStringToNil(activeDomain),
		"domain_status": emptyStringToNil(domainStatus),
		"is_published":  isPublished,
		"draft": map[string]any{
			"html_content": draftHTML,
			"meta":         draftMeta,
		},
	}
	if updatedAt != "" {
		out["updated_at"] = updatedAt
		if isPublished {
			out["published_at"] = updatedAt
		}
	}
	return out, true, nil
}

func (s *Server) upsertBOPremiumWebsite(ctx context.Context, restaurantID int, templateID string, customHTML string, domain string, domainStatus string, isPublished bool, draftHTML string, draftMeta map[string]any) error {
	templateArg := nullableString(strings.TrimSpace(templateID))
	customArg := nullableString(strings.TrimSpace(customHTML))
	domainArg := nullableString(strings.TrimSpace(domain))
	if strings.TrimSpace(domainStatus) == "" {
		domainStatus = "pending"
	}
	draftHTMLArg := nullableString(strings.TrimSpace(draftHTML))
	metaRaw, _ := json.Marshal(draftMeta)
	metaArg := nullableString(string(metaRaw))
	isPublishedArg := boolToTinyInt(isPublished)

	updateVariants := []boSQLVariant{
		{
			query: `UPDATE restaurant_websites
				SET template_id = ?, custom_html = ?, domain = ?, domain_status = ?, is_published = ?, draft_html_content = ?, draft_meta_json = ?, updated_at = NOW()
				WHERE restaurant_id = ?`,
			args: []any{templateArg, customArg, domainArg, domainStatus, isPublishedArg, draftHTMLArg, metaArg, restaurantID},
		},
		{
			query: `UPDATE restaurant_websites
				SET template_id = ?, custom_html = ?, domain = ?, domain_status = ?, is_published = ?, draft_html_content = ?, draft_meta = ?, updated_at = NOW()
				WHERE restaurant_id = ?`,
			args: []any{templateArg, customArg, domainArg, domainStatus, isPublishedArg, draftHTMLArg, metaArg, restaurantID},
		},
		{
			query: `UPDATE restaurant_websites
				SET template_id = ?, custom_html = ?, domain = ?, domain_status = ?, is_published = ?, updated_at = NOW()
				WHERE restaurant_id = ?`,
			args: []any{templateArg, customArg, domainArg, domainStatus, isPublishedArg, restaurantID},
		},
		{
			query: `UPDATE restaurant_websites
				SET custom_html = ?, is_published = ?, updated_at = NOW()
				WHERE restaurant_id = ?`,
			args: []any{customArg, isPublishedArg, restaurantID},
		},
	}

	for _, variant := range updateVariants {
		res, err := s.db.ExecContext(ctx, variant.query, variant.args...)
		if err != nil {
			if isSQLSchemaError(err) {
				continue
			}
			return err
		}
		affected, _ := res.RowsAffected()
		if affected > 0 {
			return nil
		}
	}

	insertVariants := []boSQLVariant{
		{
			query: `INSERT INTO restaurant_websites
				(restaurant_id, template_id, custom_html, domain, domain_status, is_published, draft_html_content, draft_meta_json, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, NOW(), NOW())`,
			args: []any{restaurantID, templateArg, customArg, domainArg, domainStatus, isPublishedArg, draftHTMLArg, metaArg},
		},
		{
			query: `INSERT INTO restaurant_websites
				(restaurant_id, template_id, custom_html, domain, domain_status, is_published, draft_html_content, draft_meta, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, NOW(), NOW())`,
			args: []any{restaurantID, templateArg, customArg, domainArg, domainStatus, isPublishedArg, draftHTMLArg, metaArg},
		},
		{
			query: `INSERT INTO restaurant_websites
				(restaurant_id, template_id, custom_html, domain, domain_status, is_published, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, NOW(), NOW())`,
			args: []any{restaurantID, templateArg, customArg, domainArg, domainStatus, isPublishedArg},
		},
		{
			query: `INSERT INTO restaurant_websites
				(restaurant_id, custom_html, is_published, created_at, updated_at)
				VALUES (?, ?, ?, NOW(), NOW())`,
			args: []any{restaurantID, customArg, isPublishedArg},
		},
	}

	var lastErr error
	for _, variant := range insertVariants {
		_, err := s.db.ExecContext(ctx, variant.query, variant.args...)
		if err == nil || isSQLDuplicateError(err) {
			return nil
		}
		if isSQLSchemaError(err) {
			lastErr = err
			continue
		}
		return err
	}
	if lastErr != nil {
		return lastErr
	}
	return errors.New("website upsert failed")
}

func (s *Server) handleBOPremiumDomainsSearch(w http.ResponseWriter, r *http.Request) {
	_, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		q = strings.TrimSpace(r.URL.Query().Get("query"))
	}
	if q == "" {
		q = strings.TrimSpace(r.URL.Query().Get("domain"))
	}
	if q == "" {
		writeBOPremiumError(w, http.StatusBadRequest, "BAD_REQUEST", "query requerida")
		return
	}

	seed, fullDomain := premiumDomainSeed(q)
	if seed == "" {
		writeBOPremiumError(w, http.StatusBadRequest, "BAD_REQUEST", "dominio inválido")
		return
	}

	candidates := []string{}
	if fullDomain {
		candidates = append(candidates, seed)
	} else {
		candidates = buildPremiumDomainCandidates(seed)
	}

	taken, err := s.lookupTakenDomains(r.Context(), candidates)
	if err != nil {
		writeBOPremiumError(w, http.StatusInternalServerError, "DOMAIN_SEARCH_FAILED", "No se pudo consultar dominios")
		return
	}

	results := make([]map[string]any, 0, len(candidates))
	for _, domain := range candidates {
		providerPrice := premiumRound2(premiumDomainBasePrice(domain))
		markedPrice := premiumRound2(providerPrice * boPremiumDomainMarkupFactor)
		results = append(results, map[string]any{
			"domain":             domain,
			"available":          !taken[domain],
			"provider_price":     providerPrice,
			"marked_price":       markedPrice,
			"base_price_eur":     providerPrice,
			"markup_multiplier":  boPremiumDomainMarkupFactor,
			"yearly_price_eur":   markedPrice,
			"currency":           "EUR",
			"recommended_years":  1,
			"registration_ready": !taken[domain],
		})
	}

	var first any
	if len(results) > 0 {
		first = results[0]
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"query":   q,
		"domain":  first,
		"data":    first,
		"results": results,
	})
}

func (s *Server) handleBOPremiumDomainsQuote(w http.ResponseWriter, r *http.Request) {
	_, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req boPremiumDomainQuoteRequest
	if err := readJSONBody(r, &req); err != nil {
		writeBOPremiumError(w, http.StatusBadRequest, "BAD_REQUEST", "JSON inválido")
		return
	}

	domain := normalizePremiumDomain(req.Domain)
	if domain == "" {
		domain = normalizePremiumDomain(req.Query)
	}
	if domain == "" {
		writeBOPremiumError(w, http.StatusBadRequest, "BAD_REQUEST", "dominio inválido")
		return
	}

	years := req.Years
	if years <= 0 {
		years = 1
	}
	if years > 10 {
		years = 10
	}

	takenMap, err := s.lookupTakenDomains(r.Context(), []string{domain})
	if err != nil {
		writeBOPremiumError(w, http.StatusInternalServerError, "DOMAIN_QUOTE_FAILED", "No se pudo calcular presupuesto")
		return
	}
	available := !takenMap[domain]

	providerPrice := premiumRound2(premiumDomainBasePrice(domain) * float64(years))
	markedPrice := premiumRound2(providerPrice * boPremiumDomainMarkupFactor)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"quote": map[string]any{
			"domain":            domain,
			"years":             years,
			"available":         available,
			"provider_price":    providerPrice,
			"marked_price":      markedPrice,
			"base_price_eur":    providerPrice,
			"markup_multiplier": boPremiumDomainMarkupFactor,
			"total_eur":         markedPrice,
			"currency":          "EUR",
		},
	})
}

func (s *Server) handleBOPremiumDomainsRegister(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req boPremiumDomainRegisterRequest
	if err := readJSONBody(r, &req); err != nil {
		writeBOPremiumError(w, http.StatusBadRequest, "BAD_REQUEST", "JSON inválido")
		return
	}

	domain := normalizePremiumDomain(req.Domain)
	if domain == "" {
		writeBOPremiumError(w, http.StatusBadRequest, "BAD_REQUEST", "dominio inválido")
		return
	}

	years := req.Years
	if years <= 0 {
		years = 1
	}
	if years > 10 {
		years = 10
	}

	isPrimary := false
	if req.IsPrimary != nil {
		isPrimary = *req.IsPrimary
	}

	base := premiumDomainBasePrice(domain) * float64(years)
	total := premiumRound2(base * boPremiumDomainMarkupFactor)

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeBOPremiumError(w, http.StatusInternalServerError, "DOMAIN_REGISTER_FAILED", "No se pudo iniciar transacción")
		return
	}
	defer func() { _ = tx.Rollback() }()

	var existingRestaurantID int
	err = tx.QueryRowContext(r.Context(), `SELECT restaurant_id FROM restaurant_domains WHERE domain = ? LIMIT 1 FOR UPDATE`, domain).Scan(&existingRestaurantID)
	if err == nil && existingRestaurantID != a.ActiveRestaurantID {
		writeBOPremiumError(w, http.StatusConflict, "DOMAIN_UNAVAILABLE", "El dominio ya está registrado")
		return
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		writeBOPremiumError(w, http.StatusInternalServerError, "DOMAIN_REGISTER_FAILED", "No se pudo validar el dominio")
		return
	}

	if isPrimary {
		if _, err := tx.ExecContext(r.Context(), `UPDATE restaurant_domains SET is_primary = 0 WHERE restaurant_id = ?`, a.ActiveRestaurantID); err != nil {
			writeBOPremiumError(w, http.StatusInternalServerError, "DOMAIN_REGISTER_FAILED", "No se pudo actualizar dominios")
			return
		}
	}

	domainInsertVariants := []boSQLVariant{
		{
			query: `INSERT INTO restaurant_domains (restaurant_id, domain, is_primary, created_at)
				VALUES (?, ?, ?, NOW())
				ON DUPLICATE KEY UPDATE restaurant_id = VALUES(restaurant_id), is_primary = VALUES(is_primary)`,
			args: []any{a.ActiveRestaurantID, domain, boolToTinyInt(isPrimary)},
		},
		{
			query: `INSERT INTO restaurant_domains (restaurant_id, domain, is_primary)
				VALUES (?, ?, ?)
				ON DUPLICATE KEY UPDATE restaurant_id = VALUES(restaurant_id), is_primary = VALUES(is_primary)`,
			args: []any{a.ActiveRestaurantID, domain, boolToTinyInt(isPrimary)},
		},
	}
	domainInserted := false
	for _, variant := range domainInsertVariants {
		_, err := tx.ExecContext(r.Context(), variant.query, variant.args...)
		if err == nil {
			domainInserted = true
			break
		}
		if isSQLSchemaError(err) {
			continue
		}
		writeBOPremiumError(w, http.StatusInternalServerError, "DOMAIN_REGISTER_FAILED", "No se pudo registrar el dominio")
		return
	}
	if !domainInserted {
		writeBOPremiumError(w, http.StatusInternalServerError, "DOMAIN_REGISTER_FAILED", "No se pudo registrar el dominio")
		return
	}

	if err := s.ensurePremiumWebsiteRowTx(r.Context(), tx, a.ActiveRestaurantID); err != nil {
		writeBOPremiumError(w, http.StatusInternalServerError, "DOMAIN_REGISTER_FAILED", "No se pudo preparar website")
		return
	}

	featureKey := "domain_pack_" + strings.ReplaceAll(domain, ".", "_")
	recurringMeta := map[string]any{
		"kind":            "domain_registration",
		"domain":          domain,
		"years":           years,
		"base_price_eur":  premiumRound2(base),
		"total_price_eur": total,
	}
	if err := insertRecurringInvoiceRecord(r.Context(), tx, a.ActiveRestaurantID, featureKey, total, "monthly", "EUR", recurringMeta); err != nil {
		writeBOPremiumError(w, http.StatusInternalServerError, "DOMAIN_REGISTER_FAILED", "No se pudo crear factura recurrente")
		return
	}

	for _, variant := range []boSQLVariant{
		{
			query: `UPDATE restaurant_websites
				SET domain = ?, domain_status = 'pending', updated_at = NOW()
				WHERE restaurant_id = ?`,
			args: []any{domain, a.ActiveRestaurantID},
		},
		{
			query: `UPDATE restaurant_websites
				SET domain = ?, updated_at = NOW()
				WHERE restaurant_id = ?`,
			args: []any{domain, a.ActiveRestaurantID},
		},
	} {
		_, err := tx.ExecContext(r.Context(), variant.query, variant.args...)
		if err == nil {
			break
		}
		if !isSQLSchemaError(err) {
			writeBOPremiumError(w, http.StatusInternalServerError, "DOMAIN_REGISTER_FAILED", "No se pudo actualizar website")
			return
		}
	}

	if err := tx.Commit(); err != nil {
		writeBOPremiumError(w, http.StatusInternalServerError, "DOMAIN_REGISTER_FAILED", "No se pudo confirmar registro")
		return
	}

	status := "pending"
	message := "Dominio registrado correctamente"
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"registration": map[string]any{
			"domain":  domain,
			"status":  status,
			"message": message,
		},
		"domain":  domain,
		"status":  status,
		"message": message,
		"quote": map[string]any{
			"provider_price": premiumRound2(base),
			"marked_price":   total,
			"currency":       "EUR",
			"years":          years,
			"is_primary":     isPrimary,
		},
	})
}

func (s *Server) handleBOPremiumDomainsVerify(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req boPremiumDomainVerifyRequest
	if err := readJSONBody(r, &req); err != nil {
		writeBOPremiumError(w, http.StatusBadRequest, "BAD_REQUEST", "JSON inválido")
		return
	}

	domain := normalizePremiumDomain(req.Domain)
	if domain == "" {
		writeBOPremiumError(w, http.StatusBadRequest, "BAD_REQUEST", "dominio inválido")
		return
	}

	domainRow, domainFound, err := s.queryOneAsMap(r.Context(), `SELECT * FROM restaurant_domains WHERE restaurant_id = ? AND domain = ? LIMIT 1`, a.ActiveRestaurantID, domain)
	if err != nil {
		writeBOPremiumError(w, http.StatusInternalServerError, "DOMAIN_VERIFY_FAILED", "No se pudo verificar dominio")
		return
	}
	_, websiteFound, err := s.queryOneAsMap(r.Context(), `SELECT * FROM restaurant_websites WHERE restaurant_id = ? LIMIT 1`, a.ActiveRestaurantID)
	if err != nil {
		writeBOPremiumError(w, http.StatusInternalServerError, "DOMAIN_VERIFY_FAILED", "No se pudo verificar website")
		return
	}

	if !domainFound {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":        true,
			"verified":       false,
			"domain":         domain,
			"status":         "failed",
			"website_exists": websiteFound,
		})
		return
	}

	verified := websiteFound
	status := "pending"
	switch strings.ToLower(firstStringFromMap(domainRow, "registration_status")) {
	case "active":
		status = "active"
		verified = true
	case "failed":
		status = "failed"
		verified = false
	case "pending":
		status = "pending"
	default:
		if verified {
			status = "active"
		}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":        true,
		"verified":       verified,
		"domain":         domain,
		"status":         status,
		"is_primary":     anyToBool(domainRow["is_primary"]),
		"website_exists": websiteFound,
	})
}

func (s *Server) ensurePremiumWebsiteRowTx(ctx context.Context, tx *sql.Tx, restaurantID int) error {
	var one int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM restaurant_websites WHERE restaurant_id = ? LIMIT 1`, restaurantID).Scan(&one)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	variants := []boSQLVariant{
		{
			query: `INSERT INTO restaurant_websites
				(restaurant_id, custom_html, draft_html_content, draft_meta_json, created_at, updated_at)
				VALUES (?, '', '', NULL, NOW(), NOW())`,
			args: []any{restaurantID},
		},
		{
			query: `INSERT INTO restaurant_websites
				(restaurant_id, custom_html, created_at, updated_at)
				VALUES (?, '', NOW(), NOW())`,
			args: []any{restaurantID},
		},
		{
			query: `INSERT INTO restaurant_websites
				(restaurant_id, created_at, updated_at)
				VALUES (?, NOW(), NOW())`,
			args: []any{restaurantID},
		},
	}

	var lastErr error
	for _, variant := range variants {
		_, err := tx.ExecContext(ctx, variant.query, variant.args...)
		if err == nil || isSQLDuplicateError(err) {
			return nil
		}
		if isSQLSchemaError(err) {
			lastErr = err
			continue
		}
		return err
	}
	if lastErr != nil {
		return lastErr
	}
	return errors.New("website seed failed")
}

func (s *Server) handleBOPremiumTablesList(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	dateISO := strings.TrimSpace(r.URL.Query().Get("date"))
	if dateISO != "" && !isDateISO(dateISO) {
		writeBOPremiumError(w, http.StatusBadRequest, "BAD_REQUEST", "date invalida")
		return
	}
	var floorNumber *int
	if raw := strings.TrimSpace(r.URL.Query().Get("floor_number")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			writeBOPremiumError(w, http.StatusBadRequest, "BAD_REQUEST", "floor_number invalido")
			return
		}
		floorNumber = &parsed
	}

	areas, tables, layout, err := s.loadBOPremiumTablesSnapshot(r.Context(), a.ActiveRestaurantID, dateISO, floorNumber)
	if err != nil {
		writeBOPremiumError(w, http.StatusInternalServerError, "TABLES_READ_FAILED", "No se pudieron cargar mesas")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data":    areas,
		"layout": map[string]any{
			"areas":        areas,
			"map":          layout,
			"date":         dateISO,
			"floor_number": intPtrValue(floorNumber, 0),
			"generated_at": time.Now().UTC().Format(time.RFC3339),
		},
		"areas":  areas,
		"tables": tables,
	})
}

func (s *Server) handleBOPremiumTablesCreate(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req boPremiumTablesMutationRequest
	if err := readJSONBody(r, &req); err != nil {
		writeBOPremiumError(w, http.StatusBadRequest, "BAD_REQUEST", "JSON inválido")
		return
	}
	req.Date = strings.TrimSpace(req.Date)
	if req.Date != "" && !isDateISO(req.Date) {
		writeBOPremiumError(w, http.StatusBadRequest, "BAD_REQUEST", "date invalida")
		return
	}
	if req.Date != "" && (req.FloorNumber == nil || *req.FloorNumber < 0) {
		writeBOPremiumError(w, http.StatusBadRequest, "BAD_REQUEST", "floor_number invalido")
		return
	}

	entity := strings.ToLower(strings.TrimSpace(req.Entity))
	if entity == "" {
		entity = "table"
	}

	switch entity {
	case "area":
		item, err := s.createBOPremiumArea(r.Context(), a.ActiveRestaurantID, req)
		if err != nil {
			writeBOPremiumError(w, http.StatusInternalServerError, "TABLES_CREATE_FAILED", "No se pudo crear área")
			return
		}
		s.broadcastBOTablesEvent(a.ActiveRestaurantID, "area_created", item)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": true,
			"entity":  "area",
			"item":    item,
		})
	case "table":
		item, err := s.createBOPremiumTable(r.Context(), a.ActiveRestaurantID, req)
		if err != nil {
			writeBOPremiumError(w, http.StatusInternalServerError, "TABLES_CREATE_FAILED", "No se pudo crear mesa")
			return
		}
		if req.Date != "" && req.FloorNumber != nil {
			tableID, _ := anyToInt64OK(item["id"])
			if tableID > 0 {
				x := int64(0)
				y := int64(0)
				if rawX, ok := anyToInt64OK(item["x_pos"]); ok {
					x = rawX
				}
				if rawY, ok := anyToInt64OK(item["y_pos"]); ok {
					y = rawY
				}
				if _, err := s.upsertBOPremiumTableLayoutPosition(r.Context(), a.ActiveRestaurantID, req.Date, *req.FloorNumber, tableID, x, y); err != nil {
					writeBOPremiumError(w, http.StatusInternalServerError, "TABLES_CREATE_FAILED", "No se pudo guardar posicion de layout")
					return
				}
			}
		}
		s.broadcastBOTablesEvent(a.ActiveRestaurantID, "table_created", item)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": true,
			"entity":  "table",
			"item":    item,
			"table":   item,
			"tables":  []map[string]any{item},
		})
	default:
		writeBOPremiumError(w, http.StatusBadRequest, "BAD_REQUEST", "entity inválida")
	}
}

func (s *Server) handleBOPremiumTablesUpdate(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req boPremiumTablesMutationRequest
	if err := readJSONBody(r, &req); err != nil {
		writeBOPremiumError(w, http.StatusBadRequest, "BAD_REQUEST", "JSON inválido")
		return
	}
	req.Date = strings.TrimSpace(req.Date)
	if req.Date != "" && !isDateISO(req.Date) {
		writeBOPremiumError(w, http.StatusBadRequest, "BAD_REQUEST", "date invalida")
		return
	}

	entity := strings.ToLower(strings.TrimSpace(req.Entity))
	if entity == "" {
		entity = "table"
	}

	if entity == "layout" {
		if req.Date == "" || req.FloorNumber == nil || *req.FloorNumber < 0 {
			writeBOPremiumError(w, http.StatusBadRequest, "BAD_REQUEST", "date y floor_number requeridos")
			return
		}
		layout, err := s.patchBOPremiumTableLayout(r.Context(), a.ActiveRestaurantID, req.Date, *req.FloorNumber, req.Metadata)
		if err != nil {
			writeBOPremiumError(w, http.StatusInternalServerError, "TABLES_UPDATE_FAILED", "No se pudo actualizar layout")
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": true,
			"entity":  "layout",
			"layout":  layout,
		})
		return
	}

	if req.ID <= 0 {
		writeBOPremiumError(w, http.StatusBadRequest, "BAD_REQUEST", "id requerido")
		return
	}

	switch entity {
	case "area":
		item, err := s.updateBOPremiumArea(r.Context(), a.ActiveRestaurantID, req)
		if err != nil {
			writeBOPremiumError(w, http.StatusInternalServerError, "TABLES_UPDATE_FAILED", "No se pudo actualizar área")
			return
		}
		s.broadcastBOTablesEvent(a.ActiveRestaurantID, "area_updated", item)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": true,
			"entity":  "area",
			"item":    item,
		})
	case "table":
		reqForDB := req
		if req.Date != "" && req.FloorNumber != nil {
			reqForDB.XPos = nil
			reqForDB.YPos = nil
		}
		item, err := s.updateBOPremiumTable(r.Context(), a.ActiveRestaurantID, reqForDB)
		if err != nil {
			writeBOPremiumError(w, http.StatusInternalServerError, "TABLES_UPDATE_FAILED", "No se pudo actualizar mesa")
			return
		}
		if req.Date != "" && req.FloorNumber != nil && (req.XPos != nil || req.YPos != nil) {
			tableID, _ := anyToInt64OK(item["id"])
			if tableID > 0 {
				x := int64(0)
				y := int64(0)
				if req.XPos != nil {
					x = int64(math.Round(*req.XPos))
				} else if rawX, ok := anyToInt64OK(item["x_pos"]); ok {
					x = rawX
				}
				if req.YPos != nil {
					y = int64(math.Round(*req.YPos))
				} else if rawY, ok := anyToInt64OK(item["y_pos"]); ok {
					y = rawY
				}
				if _, err := s.upsertBOPremiumTableLayoutPosition(r.Context(), a.ActiveRestaurantID, req.Date, *req.FloorNumber, tableID, x, y); err != nil {
					writeBOPremiumError(w, http.StatusInternalServerError, "TABLES_UPDATE_FAILED", "No se pudo actualizar posicion del layout")
					return
				}
			}
		}
		s.broadcastBOTablesEvent(a.ActiveRestaurantID, "table_updated", item)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": true,
			"entity":  "table",
			"item":    item,
			"table":   item,
		})
	default:
		writeBOPremiumError(w, http.StatusBadRequest, "BAD_REQUEST", "entity inválida")
	}
}

func (s *Server) handleBOPremiumTablesWS(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	conn, err := boTablesWSUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	client := &boTablesClient{conn: conn}
	s.tablesHub.add(a.ActiveRestaurantID, client)
	defer func() {
		s.tablesHub.remove(a.ActiveRestaurantID, client)
		_ = client.close()
	}()

	conn.SetReadLimit(1 << 20)
	_ = conn.SetReadDeadline(time.Now().Add(70 * time.Second))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(70 * time.Second))
	})

	if areas, tables, layout, err := s.loadBOPremiumTablesSnapshot(r.Context(), a.ActiveRestaurantID, "", nil); err == nil {
		_ = client.writeJSON(map[string]any{
			"type":         "hello",
			"restaurantId": a.ActiveRestaurantID,
			"at":           time.Now().UTC().Format(time.RFC3339),
			"areas":        areas,
			"tables":       tables,
			"layout":       layout,
		})
	}

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if len(raw) == 0 {
				continue
			}
			var msg struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(raw, &msg); err != nil {
				continue
			}
			typ := strings.ToLower(strings.TrimSpace(msg.Type))
			if typ != "sync" && typ != "refresh" && typ != "join_tables" {
				continue
			}
			areas, tables, layout, err := s.loadBOPremiumTablesSnapshot(r.Context(), a.ActiveRestaurantID, "", nil)
			if err != nil {
				continue
			}
			_ = client.writeJSON(map[string]any{
				"type":         "snapshot",
				"restaurantId": a.ActiveRestaurantID,
				"at":           time.Now().UTC().Format(time.RFC3339),
				"areas":        areas,
				"tables":       tables,
				"layout":       layout,
			})
		}
	}()

	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-readDone:
			return
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if err := client.ping(); err != nil {
				return
			}
		}
	}
}

func (s *Server) handleBOPremiumTablesTextureImageUpload(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	if !s.bunnyConfigured() {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "BunnyCDN no configurado"})
		return
	}

	tableID, err := strconv.ParseInt(strings.TrimSpace(chi.URLParam(r, "id")), 10, 64)
	if err != nil || tableID <= 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "id invalido"})
		return
	}

	if err := r.ParseMultipartForm(10 << 20); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Error parsing form"})
		return
	}
	file, header, err := r.FormFile("image")
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "No image file provided"})
		return
	}
	defer file.Close()

	raw, err := io.ReadAll(file)
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Error reading file"})
		return
	}

	normalizedWebP, err := specialmenuimage.NormalizeToWebP(
		r.Context(),
		raw,
		header.Filename,
		header.Header.Get("Content-Type"),
	)
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Error processing file: " + err.Error()})
		return
	}

	objectPath := path.Join(
		strconv.Itoa(a.ActiveRestaurantID),
		"pictures",
		"tables",
		fmt.Sprintf("%d.webp", tableID),
	)
	if err := s.bunnyPut(r.Context(), objectPath, normalizedWebP, "image/webp"); err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Error uploading file: " + err.Error()})
		return
	}
	imageURL := s.bunnyPullURL(objectPath)

	_, err = s.db.ExecContext(
		r.Context(),
		`UPDATE restaurant_tables
		 SET texture_image_url = ?, updated_at = NOW()
		 WHERE id = ? AND restaurant_id = ?`,
		imageURL,
		tableID,
		a.ActiveRestaurantID,
	)
	if err != nil {
		if !isSQLSchemaError(err) {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Error guardando URL de imagen"})
			return
		}
		metaPatch := boPremiumTablesMutationRequest{
			ID:              tableID,
			TextureImageURL: &imageURL,
			Metadata: map[string]any{
				"texture_image_url": imageURL,
			},
		}
		if _, upErr := s.updateBOPremiumTable(r.Context(), a.ActiveRestaurantID, metaPatch); upErr != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Error guardando URL de imagen"})
			return
		}
	}

	s.broadcastBOTablesEvent(a.ActiveRestaurantID, "table_updated", map[string]any{
		"id":                tableID,
		"texture_image_url": imageURL,
		"metadata": map[string]any{
			"texture_image_url": imageURL,
		},
	})

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"id":       tableID,
		"imageUrl": imageURL,
	})
}

func (s *Server) createBOPremiumArea(ctx context.Context, restaurantID int, req boPremiumTablesMutationRequest) (map[string]any, error) {
	name := strings.TrimSpace(stringPtrOr(req.Name, "Área"))
	if name == "" {
		name = "Área"
	}
	displayOrder := intPtrValue(req.DisplayOrder, intPtrValue(req.SortOrder, 0))
	isActive := boolPtrValue(req.IsActive, true)

	metaRaw := ""
	if req.Metadata != nil {
		b, _ := json.Marshal(req.Metadata)
		metaRaw = string(b)
	}
	metaArg := nullableString(metaRaw)

	variants := []boSQLVariant{
		{
			query: `INSERT INTO restaurant_areas
				(restaurant_id, name, display_order, is_active, metadata_json, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, NOW(), NOW())`,
			args: []any{restaurantID, name, displayOrder, boolToTinyInt(isActive), metaArg},
		},
		{
			query: `INSERT INTO restaurant_areas
				(restaurant_id, name, sort_order, is_active, metadata_json, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, NOW(), NOW())`,
			args: []any{restaurantID, name, displayOrder, boolToTinyInt(isActive), metaArg},
		},
		{
			query: `INSERT INTO restaurant_areas
				(restaurant_id, name, is_active, created_at, updated_at)
				VALUES (?, ?, ?, NOW(), NOW())`,
			args: []any{restaurantID, name, boolToTinyInt(isActive)},
		},
	}

	var insertRes sql.Result
	var insertErr error
	for _, variant := range variants {
		insertRes, insertErr = s.db.ExecContext(ctx, variant.query, variant.args...)
		if insertErr == nil {
			break
		}
		if isSQLSchemaError(insertErr) {
			continue
		}
		return nil, insertErr
	}
	if insertErr != nil {
		return nil, insertErr
	}

	id, _ := insertRes.LastInsertId()
	out := map[string]any{
		"id":            id,
		"restaurant_id": restaurantID,
		"name":          name,
		"display_order": displayOrder,
		"is_active":     isActive,
	}
	if req.Metadata != nil {
		out["metadata"] = req.Metadata
	}
	return out, nil
}

func (s *Server) updateBOPremiumArea(ctx context.Context, restaurantID int, req boPremiumTablesMutationRequest) (map[string]any, error) {
	nameArg := nullableString(stringPtrOr(req.Name, ""))
	displayOrderArg := any(nil)
	if req.DisplayOrder != nil {
		displayOrderArg = *req.DisplayOrder
	} else if req.SortOrder != nil {
		displayOrderArg = *req.SortOrder
	}
	isActiveArg := any(nil)
	if req.IsActive != nil {
		isActiveArg = boolToTinyInt(*req.IsActive)
	}
	metaArg := any(nil)
	if req.Metadata != nil {
		b, _ := json.Marshal(req.Metadata)
		metaArg = nullableString(string(b))
	}

	variants := []boSQLVariant{
		{
			query: `UPDATE restaurant_areas
				SET name = COALESCE(?, name),
				    display_order = COALESCE(?, display_order),
				    is_active = COALESCE(?, is_active),
				    metadata_json = COALESCE(?, metadata_json),
				    updated_at = NOW()
				WHERE id = ? AND restaurant_id = ?`,
			args: []any{nameArg, displayOrderArg, isActiveArg, metaArg, req.ID, restaurantID},
		},
		{
			query: `UPDATE restaurant_areas
				SET name = COALESCE(?, name),
				    sort_order = COALESCE(?, sort_order),
				    is_active = COALESCE(?, is_active),
				    metadata_json = COALESCE(?, metadata_json),
				    updated_at = NOW()
				WHERE id = ? AND restaurant_id = ?`,
			args: []any{nameArg, displayOrderArg, isActiveArg, metaArg, req.ID, restaurantID},
		},
		{
			query: `UPDATE restaurant_areas
				SET name = COALESCE(?, name),
				    is_active = COALESCE(?, is_active),
				    updated_at = NOW()
				WHERE id = ? AND restaurant_id = ?`,
			args: []any{nameArg, isActiveArg, req.ID, restaurantID},
		},
	}

	var updateErr error
	for _, variant := range variants {
		_, updateErr = s.db.ExecContext(ctx, variant.query, variant.args...)
		if updateErr == nil {
			break
		}
		if isSQLSchemaError(updateErr) {
			continue
		}
		return nil, updateErr
	}
	if updateErr != nil {
		return nil, updateErr
	}

	out := map[string]any{
		"id":            req.ID,
		"restaurant_id": restaurantID,
	}
	if req.Name != nil {
		out["name"] = strings.TrimSpace(*req.Name)
	}
	if displayOrderArg != nil {
		out["display_order"] = displayOrderArg
	}
	if req.IsActive != nil {
		out["is_active"] = *req.IsActive
	}
	if req.Metadata != nil {
		out["metadata"] = req.Metadata
	}
	return out, nil
}

func (s *Server) createBOPremiumTable(ctx context.Context, restaurantID int, req boPremiumTablesMutationRequest) (map[string]any, error) {
	name := strings.TrimSpace(stringPtrOr(req.Name, "Mesa"))
	if name == "" {
		name = "Mesa"
	}
	areaID := int64(0)
	if req.AreaID != nil {
		areaID = *req.AreaID
	}
	capacity := intPtrValue(req.Capacity, intPtrValue(req.Seats, 4))
	isActive := boolPtrValue(req.IsActive, true)
	status := normalizePremiumTableStatus(stringPtrOr(req.Status, "available"))
	shape := normalizePremiumTableShape(stringPtrOr(req.Shape, "round"))
	fillColor := strings.TrimSpace(stringPtrOr(req.FillColor, ""))
	outlineColor := strings.TrimSpace(stringPtrOr(req.OutlineColor, ""))
	stylePreset := strings.TrimSpace(stringPtrOr(req.StylePreset, ""))
	textureImageURL := strings.TrimSpace(stringPtrOr(req.TextureImageURL, ""))
	xPos := int(math.Round(floatPtrValue(req.XPos, 0)))
	yPos := int(math.Round(floatPtrValue(req.YPos, 0)))

	metaMap := map[string]any{}
	for k, v := range req.Metadata {
		metaMap[k] = v
	}
	metaMap["status"] = status
	metaMap["x_pos"] = xPos
	metaMap["y_pos"] = yPos
	metaMap["shape"] = shape
	if fillColor != "" {
		metaMap["fill_color"] = fillColor
	}
	if outlineColor != "" {
		metaMap["outline_color"] = outlineColor
	}
	if stylePreset != "" {
		metaMap["style_preset"] = stylePreset
	}
	if textureImageURL != "" {
		metaMap["texture_image_url"] = textureImageURL
	}
	metaRaw, _ := json.Marshal(metaMap)
	metaArg := nullableString(string(metaRaw))
	statusArg := nullableString(status)
	shapeArg := nullableString(shape)
	fillColorArg := nullableString(fillColor)
	outlineColorArg := nullableString(outlineColor)
	stylePresetArg := nullableString(stylePreset)
	textureImageURLArg := nullableString(textureImageURL)

	variants := []boSQLVariant{
		{
			query: `INSERT INTO restaurant_tables
				(restaurant_id, area_id, name, capacity, status, shape, fill_color, outline_color, style_preset, texture_image_url, x_pos, y_pos, is_active, metadata_json, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NOW(), NOW())`,
			args: []any{restaurantID, areaID, name, capacity, statusArg, shapeArg, fillColorArg, outlineColorArg, stylePresetArg, textureImageURLArg, xPos, yPos, boolToTinyInt(isActive), metaArg},
		},
		{
			query: `INSERT INTO restaurant_tables
				(restaurant_id, area_id, name, capacity, shape, fill_color, outline_color, style_preset, texture_image_url, x_pos, y_pos, is_active, metadata_json, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NOW(), NOW())`,
			args: []any{restaurantID, areaID, name, capacity, shapeArg, fillColorArg, outlineColorArg, stylePresetArg, textureImageURLArg, xPos, yPos, boolToTinyInt(isActive), metaArg},
		},
		{
			query: `INSERT INTO restaurant_tables
				(restaurant_id, area_id, name, capacity, status, x_pos, y_pos, is_active, metadata_json, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NOW(), NOW())`,
			args: []any{restaurantID, areaID, name, capacity, statusArg, xPos, yPos, boolToTinyInt(isActive), metaArg},
		},
		{
			query: `INSERT INTO restaurant_tables
				(restaurant_id, area_id, name, capacity, x_pos, y_pos, is_active, metadata_json, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, NOW(), NOW())`,
			args: []any{restaurantID, areaID, name, capacity, xPos, yPos, boolToTinyInt(isActive), metaArg},
		},
		{
			query: `INSERT INTO restaurant_tables
				(restaurant_id, area_id, name, capacity, status, is_active, metadata_json, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, NOW(), NOW())`,
			args: []any{restaurantID, areaID, name, capacity, statusArg, boolToTinyInt(isActive), metaArg},
		},
		{
			query: `INSERT INTO restaurant_tables
				(restaurant_id, area_id, name, capacity, is_active, metadata_json, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, NOW(), NOW())`,
			args: []any{restaurantID, areaID, name, capacity, boolToTinyInt(isActive), metaArg},
		},
		{
			query: `INSERT INTO restaurant_tables
				(restaurant_id, restaurant_area_id, name, capacity, is_active, metadata_json, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, NOW(), NOW())`,
			args: []any{restaurantID, areaID, name, capacity, boolToTinyInt(isActive), metaArg},
		},
		{
			query: `INSERT INTO restaurant_tables
				(restaurant_id, area_id, table_name, seats, is_active, metadata_json, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, NOW(), NOW())`,
			args: []any{restaurantID, areaID, name, capacity, boolToTinyInt(isActive), metaArg},
		},
		{
			query: `INSERT INTO restaurant_tables
				(restaurant_id, restaurant_area_id, table_name, seats, is_active, metadata_json, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, NOW(), NOW())`,
			args: []any{restaurantID, areaID, name, capacity, boolToTinyInt(isActive), metaArg},
		},
		{
			query: `INSERT INTO restaurant_tables
				(restaurant_id, area_id, name, created_at, updated_at)
				VALUES (?, ?, ?, NOW(), NOW())`,
			args: []any{restaurantID, areaID, name},
		},
	}

	var insertRes sql.Result
	var insertErr error
	for _, variant := range variants {
		insertRes, insertErr = s.db.ExecContext(ctx, variant.query, variant.args...)
		if insertErr == nil {
			break
		}
		if isSQLSchemaError(insertErr) {
			continue
		}
		return nil, insertErr
	}
	if insertErr != nil {
		return nil, insertErr
	}

	id, _ := insertRes.LastInsertId()
	out := map[string]any{
		"id":                id,
		"restaurant_id":     restaurantID,
		"area_id":           areaID,
		"name":              name,
		"capacity":          capacity,
		"status":            status,
		"shape":             shape,
		"fill_color":        fillColor,
		"outline_color":     outlineColor,
		"style_preset":      stylePreset,
		"texture_image_url": textureImageURL,
		"x_pos":             xPos,
		"y_pos":             yPos,
		"is_active":         isActive,
		"metadata":          metaMap,
	}
	return normalizeBOPremiumTableRow(out), nil
}

func (s *Server) updateBOPremiumTable(ctx context.Context, restaurantID int, req boPremiumTablesMutationRequest) (map[string]any, error) {
	nameArg := nullableString(stringPtrOr(req.Name, ""))
	areaArg := any(nil)
	if req.AreaID != nil {
		areaArg = *req.AreaID
	}
	capacityArg := any(nil)
	if req.Capacity != nil {
		capacityArg = *req.Capacity
	} else if req.Seats != nil {
		capacityArg = *req.Seats
	}
	isActiveArg := any(nil)
	if req.IsActive != nil {
		isActiveArg = boolToTinyInt(*req.IsActive)
	}

	status := ""
	statusArg := any(nil)
	if req.Status != nil {
		status = normalizePremiumTableStatus(*req.Status)
		statusArg = nullableString(status)
	}
	shape := ""
	shapeArg := any(nil)
	if req.Shape != nil {
		shape = normalizePremiumTableShape(*req.Shape)
		shapeArg = nullableString(shape)
	}
	fillColor := ""
	fillColorArg := any(nil)
	if req.FillColor != nil {
		fillColor = strings.TrimSpace(*req.FillColor)
		fillColorArg = emptyStringToNil(fillColor)
	}
	outlineColor := ""
	outlineColorArg := any(nil)
	if req.OutlineColor != nil {
		outlineColor = strings.TrimSpace(*req.OutlineColor)
		outlineColorArg = emptyStringToNil(outlineColor)
	}
	stylePreset := ""
	stylePresetArg := any(nil)
	if req.StylePreset != nil {
		stylePreset = strings.TrimSpace(*req.StylePreset)
		stylePresetArg = emptyStringToNil(stylePreset)
	}
	textureImageURL := ""
	textureImageURLArg := any(nil)
	if req.TextureImageURL != nil {
		textureImageURL = strings.TrimSpace(*req.TextureImageURL)
		textureImageURLArg = emptyStringToNil(textureImageURL)
	}
	xPos := 0
	xPosArg := any(nil)
	if req.XPos != nil {
		xPos = int(math.Round(*req.XPos))
		xPosArg = xPos
	}
	yPos := 0
	yPosArg := any(nil)
	if req.YPos != nil {
		yPos = int(math.Round(*req.YPos))
		yPosArg = yPos
	}

	metaMap := map[string]any{}
	for k, v := range req.Metadata {
		metaMap[k] = v
	}
	if req.Status != nil {
		metaMap["status"] = status
	}
	if req.XPos != nil {
		metaMap["x_pos"] = xPos
	}
	if req.YPos != nil {
		metaMap["y_pos"] = yPos
	}
	if req.Shape != nil {
		metaMap["shape"] = shape
	}
	if req.FillColor != nil {
		metaMap["fill_color"] = fillColor
	}
	if req.OutlineColor != nil {
		metaMap["outline_color"] = outlineColor
	}
	if req.StylePreset != nil {
		metaMap["style_preset"] = stylePreset
	}
	if req.TextureImageURL != nil {
		metaMap["texture_image_url"] = textureImageURL
	}
	metaArg := any(nil)
	if len(metaMap) > 0 {
		b, _ := json.Marshal(metaMap)
		metaArg = nullableString(string(b))
	}

	variants := []boSQLVariant{
		{
			query: `UPDATE restaurant_tables
				SET name = COALESCE(?, name),
				    area_id = COALESCE(?, area_id),
				    capacity = COALESCE(?, capacity),
				    status = COALESCE(?, status),
				    shape = COALESCE(?, shape),
				    fill_color = COALESCE(?, fill_color),
				    outline_color = COALESCE(?, outline_color),
				    style_preset = COALESCE(?, style_preset),
				    texture_image_url = COALESCE(?, texture_image_url),
				    x_pos = COALESCE(?, x_pos),
				    y_pos = COALESCE(?, y_pos),
				    is_active = COALESCE(?, is_active),
				    metadata_json = COALESCE(?, metadata_json),
				    updated_at = NOW()
				WHERE id = ? AND restaurant_id = ?`,
			args: []any{nameArg, areaArg, capacityArg, statusArg, shapeArg, fillColorArg, outlineColorArg, stylePresetArg, textureImageURLArg, xPosArg, yPosArg, isActiveArg, metaArg, req.ID, restaurantID},
		},
		{
			query: `UPDATE restaurant_tables
				SET name = COALESCE(?, name),
				    area_id = COALESCE(?, area_id),
				    capacity = COALESCE(?, capacity),
				    shape = COALESCE(?, shape),
				    fill_color = COALESCE(?, fill_color),
				    outline_color = COALESCE(?, outline_color),
				    style_preset = COALESCE(?, style_preset),
				    texture_image_url = COALESCE(?, texture_image_url),
				    x_pos = COALESCE(?, x_pos),
				    y_pos = COALESCE(?, y_pos),
				    is_active = COALESCE(?, is_active),
				    metadata_json = COALESCE(?, metadata_json),
				    updated_at = NOW()
				WHERE id = ? AND restaurant_id = ?`,
			args: []any{nameArg, areaArg, capacityArg, shapeArg, fillColorArg, outlineColorArg, stylePresetArg, textureImageURLArg, xPosArg, yPosArg, isActiveArg, metaArg, req.ID, restaurantID},
		},
		{
			query: `UPDATE restaurant_tables
				SET name = COALESCE(?, name),
				    area_id = COALESCE(?, area_id),
				    capacity = COALESCE(?, capacity),
				    status = COALESCE(?, status),
				    x_pos = COALESCE(?, x_pos),
				    y_pos = COALESCE(?, y_pos),
				    is_active = COALESCE(?, is_active),
				    metadata_json = COALESCE(?, metadata_json),
				    updated_at = NOW()
				WHERE id = ? AND restaurant_id = ?`,
			args: []any{nameArg, areaArg, capacityArg, statusArg, xPosArg, yPosArg, isActiveArg, metaArg, req.ID, restaurantID},
		},
		{
			query: `UPDATE restaurant_tables
				SET name = COALESCE(?, name),
				    area_id = COALESCE(?, area_id),
				    capacity = COALESCE(?, capacity),
				    x_pos = COALESCE(?, x_pos),
				    y_pos = COALESCE(?, y_pos),
				    is_active = COALESCE(?, is_active),
				    metadata_json = COALESCE(?, metadata_json),
				    updated_at = NOW()
				WHERE id = ? AND restaurant_id = ?`,
			args: []any{nameArg, areaArg, capacityArg, xPosArg, yPosArg, isActiveArg, metaArg, req.ID, restaurantID},
		},
		{
			query: `UPDATE restaurant_tables
				SET name = COALESCE(?, name),
				    area_id = COALESCE(?, area_id),
				    capacity = COALESCE(?, capacity),
				    status = COALESCE(?, status),
				    is_active = COALESCE(?, is_active),
				    metadata_json = COALESCE(?, metadata_json),
				    updated_at = NOW()
				WHERE id = ? AND restaurant_id = ?`,
			args: []any{nameArg, areaArg, capacityArg, statusArg, isActiveArg, metaArg, req.ID, restaurantID},
		},
		{
			query: `UPDATE restaurant_tables
				SET name = COALESCE(?, name),
				    area_id = COALESCE(?, area_id),
				    capacity = COALESCE(?, capacity),
				    is_active = COALESCE(?, is_active),
				    metadata_json = COALESCE(?, metadata_json),
				    updated_at = NOW()
				WHERE id = ? AND restaurant_id = ?`,
			args: []any{nameArg, areaArg, capacityArg, isActiveArg, metaArg, req.ID, restaurantID},
		},
		{
			query: `UPDATE restaurant_tables
				SET name = COALESCE(?, name),
				    restaurant_area_id = COALESCE(?, restaurant_area_id),
				    capacity = COALESCE(?, capacity),
				    is_active = COALESCE(?, is_active),
				    metadata_json = COALESCE(?, metadata_json),
				    updated_at = NOW()
				WHERE id = ? AND restaurant_id = ?`,
			args: []any{nameArg, areaArg, capacityArg, isActiveArg, metaArg, req.ID, restaurantID},
		},
		{
			query: `UPDATE restaurant_tables
				SET table_name = COALESCE(?, table_name),
				    area_id = COALESCE(?, area_id),
				    seats = COALESCE(?, seats),
				    is_active = COALESCE(?, is_active),
				    updated_at = NOW()
				WHERE id = ? AND restaurant_id = ?`,
			args: []any{nameArg, areaArg, capacityArg, isActiveArg, req.ID, restaurantID},
		},
		{
			query: `UPDATE restaurant_tables
				SET table_name = COALESCE(?, table_name),
				    restaurant_area_id = COALESCE(?, restaurant_area_id),
				    seats = COALESCE(?, seats),
				    is_active = COALESCE(?, is_active),
				    updated_at = NOW()
				WHERE id = ? AND restaurant_id = ?`,
			args: []any{nameArg, areaArg, capacityArg, isActiveArg, req.ID, restaurantID},
		},
	}

	var updateErr error
	for _, variant := range variants {
		_, updateErr = s.db.ExecContext(ctx, variant.query, variant.args...)
		if updateErr == nil {
			break
		}
		if isSQLSchemaError(updateErr) {
			continue
		}
		return nil, updateErr
	}
	if updateErr != nil {
		return nil, updateErr
	}

	out := map[string]any{
		"id":            req.ID,
		"restaurant_id": restaurantID,
	}
	if req.Name != nil {
		out["name"] = strings.TrimSpace(*req.Name)
	}
	if req.AreaID != nil {
		out["area_id"] = *req.AreaID
	}
	if req.Capacity != nil {
		out["capacity"] = *req.Capacity
	} else if req.Seats != nil {
		out["capacity"] = *req.Seats
	}
	if req.Status != nil {
		out["status"] = status
	}
	if req.Shape != nil {
		out["shape"] = shape
	}
	if req.FillColor != nil {
		out["fill_color"] = fillColor
	}
	if req.OutlineColor != nil {
		out["outline_color"] = outlineColor
	}
	if req.StylePreset != nil {
		out["style_preset"] = stylePreset
	}
	if req.TextureImageURL != nil {
		out["texture_image_url"] = textureImageURL
	}
	if req.XPos != nil {
		out["x_pos"] = xPos
	}
	if req.YPos != nil {
		out["y_pos"] = yPos
	}
	if req.IsActive != nil {
		out["is_active"] = *req.IsActive
	}
	if len(metaMap) > 0 {
		out["metadata"] = metaMap
	}
	return normalizeBOPremiumTableRow(out), nil
}

func (s *Server) loadBOPremiumTablesSnapshot(ctx context.Context, restaurantID int, dateISO string, floorNumber *int) ([]map[string]any, []map[string]any, map[string]any, error) {
	areas, err := s.queryAllAsMaps(ctx, `SELECT * FROM restaurant_areas WHERE restaurant_id = ? ORDER BY id ASC`, restaurantID)
	if err != nil {
		return nil, nil, nil, err
	}
	rawTables, err := s.queryAllAsMaps(ctx, `SELECT * FROM restaurant_tables WHERE restaurant_id = ? ORDER BY id ASC`, restaurantID)
	if err != nil {
		return nil, nil, nil, err
	}

	layout := map[string]any{
		"table_positions": map[string]any{},
		"elements":        []any{},
		"booking_states":  map[string]any{},
	}
	if dateISO != "" && floorNumber != nil {
		loaded, lerr := s.loadBOPremiumTableLayout(ctx, restaurantID, dateISO, *floorNumber)
		if lerr != nil {
			return nil, nil, nil, lerr
		}
		for k, v := range loaded {
			layout[k] = v
		}
	}

	layoutPositions := map[string]any{}
	if raw, ok := asStringAnyMap(layout["table_positions"]); ok {
		layoutPositions = raw
	}

	tables := make([]map[string]any, 0, len(rawTables))
	for _, row := range rawTables {
		table := normalizeBOPremiumTableRow(row)
		if tableID, ok := anyToInt64OK(table["id"]); ok && tableID > 0 {
			if pos, exists := asStringAnyMap(layoutPositions[strconv.FormatInt(tableID, 10)]); exists {
				if x, ok := anyToInt64OK(pos["x_pos"]); ok {
					table["x_pos"] = x
				}
				if y, ok := anyToInt64OK(pos["y_pos"]); ok {
					table["y_pos"] = y
				}
			}
		}
		tables = append(tables, table)
	}

	areasOut := make([]map[string]any, 0, len(areas))
	for _, area := range areas {
		out := cloneStringAnyMap(area)
		areaID, _ := anyToInt64OK(area["id"])
		areaTables := make([]map[string]any, 0)
		for _, table := range tables {
			tableAreaID, _ := anyToInt64OK(table["area_id"])
			if areaID > 0 && tableAreaID == areaID {
				areaTables = append(areaTables, table)
			}
		}
		out["tables"] = areaTables
		areasOut = append(areasOut, out)
	}

	return areasOut, tables, layout, nil
}

func (s *Server) loadBOPremiumTableLayout(ctx context.Context, restaurantID int, dateISO string, floorNumber int) (map[string]any, error) {
	if dateISO == "" || floorNumber < 0 {
		return map[string]any{}, nil
	}
	var raw sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT data_json
		FROM restaurant_table_layouts
		WHERE restaurant_id = ? AND layout_date = ? AND floor_number = ?
		LIMIT 1
	`, restaurantID, dateISO, floorNumber).Scan(&raw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return map[string]any{}, nil
		}
		if isSQLSchemaError(err) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	parsed, ok := asStringAnyMap(raw.String)
	if !ok {
		return map[string]any{}, nil
	}
	return parsed, nil
}

func (s *Server) upsertBOPremiumTableLayout(ctx context.Context, restaurantID int, dateISO string, floorNumber int, data map[string]any) (map[string]any, error) {
	if dateISO == "" || floorNumber < 0 {
		return map[string]any{}, nil
	}
	raw, _ := json.Marshal(data)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO restaurant_table_layouts (restaurant_id, layout_date, floor_number, data_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, NOW(), NOW())
		ON DUPLICATE KEY UPDATE
			data_json = VALUES(data_json),
			updated_at = NOW()
	`, restaurantID, dateISO, floorNumber, string(raw))
	if err != nil {
		if isSQLSchemaError(err) {
			return data, nil
		}
		return nil, err
	}
	return data, nil
}

func (s *Server) upsertBOPremiumTableLayoutPosition(ctx context.Context, restaurantID int, dateISO string, floorNumber int, tableID int64, xPos int64, yPos int64) (map[string]any, error) {
	layout, err := s.loadBOPremiumTableLayout(ctx, restaurantID, dateISO, floorNumber)
	if err != nil {
		return nil, err
	}
	tablePositions := map[string]any{}
	if raw, ok := asStringAnyMap(layout["table_positions"]); ok {
		tablePositions = raw
	}
	tablePositions[strconv.FormatInt(tableID, 10)] = map[string]any{"x_pos": xPos, "y_pos": yPos}
	layout["table_positions"] = tablePositions
	return s.upsertBOPremiumTableLayout(ctx, restaurantID, dateISO, floorNumber, layout)
}

func (s *Server) patchBOPremiumTableLayout(ctx context.Context, restaurantID int, dateISO string, floorNumber int, patch map[string]any) (map[string]any, error) {
	layout, err := s.loadBOPremiumTableLayout(ctx, restaurantID, dateISO, floorNumber)
	if err != nil {
		return nil, err
	}
	for k, v := range patch {
		layout[k] = v
	}
	return s.upsertBOPremiumTableLayout(ctx, restaurantID, dateISO, floorNumber, layout)
}

func (s *Server) broadcastBOTablesEvent(restaurantID int, eventType string, data any) {
	if s == nil || s.tablesHub == nil || restaurantID <= 0 {
		return
	}
	now := time.Now().UTC()
	payload := map[string]any{
		"type":         eventType,
		"restaurantId": restaurantID,
		"sequence":     now.UnixMilli(),
		"timestamp":    now.Format(time.RFC3339),
		"at":           now.Format(time.RFC3339),
		"data":         data,
	}
	if m, ok := asStringAnyMap(data); ok {
		if strings.HasPrefix(eventType, "table_") {
			table := normalizeBOPremiumTableRow(m)
			payload["table"] = table
			if tableID, ok := anyToInt64OK(table["id"]); ok && tableID > 0 {
				payload["table_id"] = tableID
				payload["id"] = tableID
			}
			payload["status"] = firstStringFromMap(table, "status")
			if x, ok := anyToInt64OK(table["x_pos"]); ok {
				payload["x_pos"] = x
			}
			if y, ok := anyToInt64OK(table["y_pos"]); ok {
				payload["y_pos"] = y
			}
		}
	}
	s.tablesHub.broadcast(restaurantID, payload)
}

func (s *Server) handleBOMembersWhatsAppSend(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req boMembersWhatsAppSendRequest
	if err := readJSONBody(r, &req); err != nil {
		writeBOPremiumError(w, http.StatusBadRequest, "BAD_REQUEST", "JSON inválido")
		return
	}

	active, err := s.hasActiveRecurringFeature(r.Context(), a.ActiveRestaurantID, boPremiumWhatsAppFeatureKey)
	if err != nil {
		writeBOPremiumError(w, http.StatusInternalServerError, "WHATSAPP_SUBSCRIPTION_CHECK_FAILED", "No se pudo validar suscripción")
		return
	}
	if !active {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"code":    "NEEDS_SUBSCRIPTION",
			"message": "Necesitas una suscripción activa de WhatsApp Pack",
		})
		return
	}

	text := strings.TrimSpace(req.Message)
	if text == "" {
		text = strings.TrimSpace(req.Text)
	}
	if text == "" {
		writeBOPremiumError(w, http.StatusBadRequest, "BAD_REQUEST", "mensaje requerido")
		return
	}

	phone := normalizeWhatsAppNumber(req.Phone)
	if phone == "" && req.MemberID != nil {
		var dbWhatsApp sql.NullString
		var dbPhone sql.NullString
		err := s.db.QueryRowContext(r.Context(), `
			SELECT whatsapp_number, phone
			FROM restaurant_members
			WHERE restaurant_id = ? AND id = ?
			LIMIT 1
		`, a.ActiveRestaurantID, *req.MemberID).Scan(&dbWhatsApp, &dbPhone)
		if err != nil && isSQLSchemaError(err) {
			err = s.db.QueryRowContext(r.Context(), `
				SELECT phone
				FROM restaurant_members
				WHERE restaurant_id = ? AND id = ?
				LIMIT 1
			`, a.ActiveRestaurantID, *req.MemberID).Scan(&dbPhone)
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			writeBOPremiumError(w, http.StatusInternalServerError, "WHATSAPP_SEND_FAILED", "No se pudo resolver el teléfono del miembro")
			return
		}
		phone = normalizeWhatsAppNumber(dbWhatsApp.String)
		if phone == "" {
			phone = normalizeWhatsAppNumber(dbPhone.String)
		}
	}
	if phone == "" {
		writeBOPremiumError(w, http.StatusBadRequest, "BAD_REQUEST", "teléfono inválido")
		return
	}

	if err := s.sendWhatsAppMessage(r.Context(), a.ActiveRestaurantID, phone, text); err != nil {
		s.logMembersWhatsAppDelivery(r.Context(), a.ActiveRestaurantID, phone, req.MemberID, text, "failed", err.Error())
		errText := strings.ToLower(strings.TrimSpace(err.Error()))
		if strings.Contains(errText, "uazapi no configurado") || strings.Contains(errText, "token") {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"code":    "NEEDS_CONNECTION",
				"message": "Debes conectar WhatsApp en Premium antes de enviar mensajes",
			})
			return
		}
		writeBOPremiumError(w, http.StatusBadGateway, "WHATSAPP_SEND_FAILED", "No se pudo enviar WhatsApp")
		return
	}

	s.logMembersWhatsAppDelivery(r.Context(), a.ActiveRestaurantID, phone, req.MemberID, text, "sent", "")
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"sent":    true,
		"phone":   phone,
	})
}

func (s *Server) handleBOMembersWhatsAppSubscribe(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req boMembersWhatsAppSubscribeRequest
	if err := readJSONBody(r, &req); err != nil {
		writeBOPremiumError(w, http.StatusBadRequest, "BAD_REQUEST", "JSON inválido")
		return
	}

	amount := 29.0
	if req.Amount != nil && *req.Amount > 0 {
		amount = *req.Amount
	}
	currency := strings.ToUpper(strings.TrimSpace(req.Currency))
	if currency == "" {
		currency = "EUR"
	}

	meta := map[string]any{
		"kind":       "whatsapp_pack",
		"frequency":  "monthly",
		"activated":  time.Now().UTC().Format(time.RFC3339),
		"source":     "backoffice_admin",
		"featureKey": boPremiumWhatsAppFeatureKey,
	}

	if err := s.activateRecurringFeatureMonthly(r.Context(), a.ActiveRestaurantID, boPremiumWhatsAppFeatureKey, amount, currency, meta); err != nil {
		writeBOPremiumError(w, http.StatusInternalServerError, "WHATSAPP_SUBSCRIBE_FAILED", "No se pudo activar suscripción")
		return
	}

	resp := map[string]any{
		"success": true,
		"subscription": map[string]any{
			"feature_key": boPremiumWhatsAppFeatureKey,
			"frequency":   "monthly",
			"amount":      premiumRound2(amount),
			"currency":    currency,
			"is_active":   true,
		},
	}

	connection, connErr := s.provisionAndConnectRestaurantWhatsApp(r.Context(), a.ActiveRestaurantID, "")
	if connErr != nil {
		resp["message"] = "Suscripcion activada. Debes completar la conexion de WhatsApp para enviar mensajes."
		resp["code"] = "WHATSAPP_CONNECT_PENDING"
		resp["connected"] = false
	} else {
		resp["connection"] = connection
		resp["connected"] = anyToBool(connection["connected"])
		resp["message"] = whatsappConnectionMessage(connection)
	}

	httpx.WriteJSON(w, http.StatusOK, resp)
}

func (s *Server) hasActiveRecurringFeature(ctx context.Context, restaurantID int, featureKey string) (bool, error) {
	queries := []string{
		`SELECT 1 FROM recurring_invoices WHERE restaurant_id = ? AND feature_key = ? AND is_active = 1 LIMIT 1`,
		`SELECT 1 FROM recurring_invoices WHERE restaurant_id = ? AND feature_key = ? AND is_active = true LIMIT 1`,
		`SELECT 1 FROM recurring_invoices WHERE restaurant_id = ? AND feature_key = ? AND status = 'active' LIMIT 1`,
	}

	for _, q := range queries {
		var one int
		err := s.db.QueryRowContext(ctx, q, restaurantID, featureKey).Scan(&one)
		if err == nil {
			return true, nil
		}
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		if isSQLSchemaError(err) {
			continue
		}
		return false, err
	}
	return false, nil
}

func (s *Server) activateRecurringFeatureMonthly(ctx context.Context, restaurantID int, featureKey string, amount float64, currency string, meta map[string]any) error {
	metaRaw, _ := json.Marshal(meta)
	metaArg := nullableString(string(metaRaw))

	updateVariants := []boSQLVariant{
		{
			query: `UPDATE recurring_invoices
				SET is_active = 1,
				    amount = ?,
				    currency = ?,
				    interval_unit = 'month',
				    interval_count = 1,
				    meta_json = ?,
				    next_billing_at = DATE_ADD(NOW(), INTERVAL 1 MONTH),
				    updated_at = NOW()
				WHERE restaurant_id = ? AND feature_key = ?`,
			args: []any{amount, currency, metaArg, restaurantID, featureKey},
		},
		{
			query: `UPDATE recurring_invoices
				SET is_active = 1,
				    amount = ?,
				    currency = ?,
				    frequency = 'monthly',
				    next_billing_date = DATE_ADD(CURDATE(), INTERVAL 1 MONTH),
				    updated_at = NOW()
				WHERE restaurant_id = ? AND feature_key = ?`,
			args: []any{amount, currency, restaurantID, featureKey},
		},
		{
			query: `UPDATE recurring_invoices
				SET status = 'active',
				    amount = ?,
				    currency = ?,
				    period = 'monthly',
				    metadata_json = ?,
				    updated_at = NOW()
				WHERE restaurant_id = ? AND feature_key = ?`,
			args: []any{amount, currency, metaArg, restaurantID, featureKey},
		},
	}

	for _, variant := range updateVariants {
		res, err := s.db.ExecContext(ctx, variant.query, variant.args...)
		if err != nil {
			if isSQLSchemaError(err) {
				continue
			}
			return err
		}
		affected, _ := res.RowsAffected()
		if affected > 0 {
			return nil
		}
	}

	return insertRecurringInvoiceRecord(ctx, s.db, restaurantID, featureKey, amount, "monthly", currency, meta)
}

func insertRecurringInvoiceRecord(ctx context.Context, execer boSQLExecutor, restaurantID int, featureKey string, amount float64, frequency string, currency string, meta map[string]any) error {
	frequency = strings.ToLower(strings.TrimSpace(frequency))
	if frequency == "" {
		frequency = "monthly"
	}
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" {
		currency = "EUR"
	}

	metaRaw, _ := json.Marshal(meta)
	metaArg := nullableString(string(metaRaw))

	variants := []boSQLVariant{
		{
			query: `INSERT INTO recurring_invoices
				(restaurant_id, feature_key, amount, currency, interval_unit, interval_count, is_active, meta_json, next_billing_at, created_at, updated_at)
				VALUES (?, ?, ?, ?, 'month', 1, 1, ?, DATE_ADD(NOW(), INTERVAL 1 MONTH), NOW(), NOW())`,
			args: []any{restaurantID, featureKey, amount, currency, metaArg},
		},
		{
			query: `INSERT INTO recurring_invoices
				(restaurant_id, feature_key, amount, currency, frequency, start_date, next_billing_date, is_active, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, CURDATE(), DATE_ADD(CURDATE(), INTERVAL 1 MONTH), 1, NOW(), NOW())`,
			args: []any{restaurantID, featureKey, amount, currency, frequency},
		},
		{
			query: `INSERT INTO recurring_invoices
				(restaurant_id, feature_key, amount, currency, billing_cycle, status, metadata_json, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, 'active', ?, NOW(), NOW())`,
			args: []any{restaurantID, featureKey, amount, currency, frequency, metaArg},
		},
		{
			query: `INSERT INTO recurring_invoices
				(restaurant_id, feature_key, amount, currency, is_active, created_at, updated_at)
				VALUES (?, ?, ?, ?, 1, NOW(), NOW())`,
			args: []any{restaurantID, featureKey, amount, currency},
		},
	}

	var lastErr error
	for _, variant := range variants {
		_, err := execer.ExecContext(ctx, variant.query, variant.args...)
		if err == nil || isSQLDuplicateError(err) {
			return nil
		}
		if isSQLSchemaError(err) {
			lastErr = err
			continue
		}
		return err
	}
	if lastErr != nil {
		return lastErr
	}
	return errors.New("insert recurring invoice failed")
}

func (s *Server) logMembersWhatsAppDelivery(ctx context.Context, restaurantID int, recipient string, memberID *int64, text string, status string, errText string) {
	payload := map[string]any{
		"text": text,
	}
	if memberID != nil {
		payload["member_id"] = *memberID
	}
	payloadRaw, _ := json.Marshal(payload)

	if status == "sent" {
		_, _ = s.db.ExecContext(ctx, `
			INSERT INTO message_deliveries (restaurant_id, channel, event, recipient, payload_json, status, sent_at)
			VALUES (?, 'whatsapp', 'members_whatsapp_send', ?, ?, 'sent', NOW())
		`, restaurantID, recipient, nullableString(string(payloadRaw)))
		return
	}

	_, _ = s.db.ExecContext(ctx, `
		INSERT INTO message_deliveries (restaurant_id, channel, event, recipient, payload_json, status, error)
		VALUES (?, 'whatsapp', 'members_whatsapp_send', ?, ?, 'failed', ?)
	`, restaurantID, recipient, nullableString(string(payloadRaw)), nullableString(errText))
}

func (s *Server) lookupTakenDomains(ctx context.Context, domains []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(domains) == 0 {
		return out, nil
	}

	placeholders := make([]string, 0, len(domains))
	args := make([]any, 0, len(domains))
	for _, d := range domains {
		d = strings.ToLower(strings.TrimSpace(d))
		if d == "" {
			continue
		}
		placeholders = append(placeholders, "?")
		args = append(args, d)
		out[d] = false
	}
	if len(placeholders) == 0 {
		return out, nil
	}

	q := fmt.Sprintf(`SELECT domain FROM restaurant_domains WHERE domain IN (%s)`, strings.Join(placeholders, ","))
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		d = strings.ToLower(strings.TrimSpace(d))
		if d != "" {
			out[d] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Server) queryOneAsMap(ctx context.Context, query string, args ...any) (map[string]any, bool, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	list, err := rowsToMaps(rows)
	if err != nil {
		return nil, false, err
	}
	if len(list) == 0 {
		return nil, false, nil
	}
	return list[0], true, nil
}

func (s *Server) queryAllAsMaps(ctx context.Context, query string, args ...any) ([]map[string]any, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return rowsToMaps(rows)
}

func rowsToMaps(rows *sql.Rows) ([]map[string]any, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	out := []map[string]any{}
	for rows.Next() {
		values := make([]any, len(cols))
		scan := make([]any, len(cols))
		for i := range values {
			scan[i] = &values[i]
		}
		if err := rows.Scan(scan...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(cols))
		for i, col := range cols {
			row[col] = normalizeSQLValue(col, values[i])
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func normalizeSQLValue(col string, v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case []byte:
		s := string(t)
		colLower := strings.ToLower(strings.TrimSpace(col))
		if strings.HasSuffix(colLower, "_json") || strings.HasSuffix(colLower, "json") {
			var parsed any
			if err := json.Unmarshal([]byte(s), &parsed); err == nil {
				return parsed
			}
		}
		return s
	case time.Time:
		return t.UTC().Format(time.RFC3339)
	default:
		return t
	}
}

func firstStringFromMap(m map[string]any, keys ...string) string {
	for _, k := range keys {
		v, ok := m[k]
		if !ok || v == nil {
			continue
		}
		switch t := v.(type) {
		case string:
			return strings.TrimSpace(t)
		case []byte:
			return strings.TrimSpace(string(t))
		default:
			return strings.TrimSpace(fmt.Sprint(t))
		}
	}
	return ""
}

func writeBOPremiumError(w http.ResponseWriter, status int, code string, message string) {
	payload := map[string]any{
		"success": false,
		"message": message,
		"error":   message,
	}
	if strings.TrimSpace(code) != "" {
		payload["code"] = code
	}
	httpx.WriteJSON(w, status, payload)
}

func premiumDomainSeed(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	if strings.Contains(raw, ".") || strings.Contains(raw, "/") || strings.Contains(raw, ":") {
		normalized := normalizePremiumDomain(raw)
		if normalized == "" {
			return "", false
		}
		return normalized, true
	}
	label := normalizePremiumDomainLabel(raw)
	if label == "" {
		return "", false
	}
	return label, false
}

func normalizePremiumDomain(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return ""
	}

	raw = strings.TrimPrefix(raw, "https://")
	raw = strings.TrimPrefix(raw, "http://")
	if idx := strings.Index(raw, "/"); idx >= 0 {
		raw = raw[:idx]
	}
	if idx := strings.Index(raw, "?"); idx >= 0 {
		raw = raw[:idx]
	}
	if idx := strings.Index(raw, "#"); idx >= 0 {
		raw = raw[:idx]
	}

	if strings.Count(raw, ":") == 1 {
		if host, _, err := net.SplitHostPort(raw); err == nil {
			raw = host
		} else {
			h, _, ok := strings.Cut(raw, ":")
			if ok {
				raw = h
			}
		}
	}

	raw = strings.Trim(raw, ".")
	if raw == "" {
		return ""
	}
	if !boPremiumDomainRe.MatchString(raw) {
		return ""
	}
	return raw
}

func normalizePremiumDomainLabel(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return ""
	}
	var b strings.Builder
	lastHyphen := false
	for _, ch := range raw {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') {
			b.WriteRune(ch)
			lastHyphen = false
			continue
		}
		if ch == '-' || ch == '_' || ch == ' ' {
			if b.Len() > 0 && !lastHyphen {
				b.WriteByte('-')
				lastHyphen = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) < 2 {
		return ""
	}
	if len(out) > 63 {
		out = strings.Trim(out[:63], "-")
	}
	return out
}

func buildPremiumDomainCandidates(seed string) []string {
	tlds := []string{"com", "es", "net", "org", "io", "app", "dev", "online", "site", "ai"}
	out := make([]string, 0, len(tlds))
	for _, tld := range tlds {
		out = append(out, seed+"."+tld)
	}
	return out
}

func premiumDomainBasePrice(domain string) float64 {
	domain = strings.ToLower(strings.TrimSpace(domain))
	parts := strings.Split(domain, ".")
	if len(parts) < 2 {
		return 14.99
	}
	tld := parts[len(parts)-1]
	switch tld {
	case "es":
		return 8.99
	case "com":
		return 12.99
	case "net":
		return 11.99
	case "org":
		return 10.99
	case "io":
		return 34.99
	case "app":
		return 15.99
	case "dev":
		return 14.99
	case "ai":
		return 39.99
	default:
		return 14.99
	}
}

func premiumRound2(v float64) float64 {
	return math.Round(v*100) / 100
}

func stringPtrOr(v *string, def string) string {
	if v == nil {
		return def
	}
	return strings.TrimSpace(*v)
}

func intPtrValue(v *int, def int) int {
	if v == nil {
		return def
	}
	return *v
}

func boolPtrValue(v *bool, def bool) bool {
	if v == nil {
		return def
	}
	return *v
}

func boolToTinyInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func anyToBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case int:
		return t != 0
	case int64:
		return t != 0
	case float64:
		return t != 0
	case string:
		t = strings.ToLower(strings.TrimSpace(t))
		return t == "1" || t == "true" || t == "yes"
	default:
		return false
	}
}

func anyToInt64OK(v any) (int64, bool) {
	switch t := v.(type) {
	case int:
		return int64(t), true
	case int8:
		return int64(t), true
	case int16:
		return int64(t), true
	case int32:
		return int64(t), true
	case int64:
		return t, true
	case uint:
		return int64(t), true
	case uint8:
		return int64(t), true
	case uint16:
		return int64(t), true
	case uint32:
		return int64(t), true
	case uint64:
		maxInt64 := uint64(^uint64(0) >> 1)
		if t > maxInt64 {
			return int64(maxInt64), true
		}
		return int64(t), true
	case float32:
		return int64(math.Round(float64(t))), true
	case float64:
		return int64(math.Round(t)), true
	case string:
		raw := strings.TrimSpace(t)
		if raw == "" {
			return 0, false
		}
		if i, err := strconv.ParseInt(raw, 10, 64); err == nil {
			return i, true
		}
		if f, err := strconv.ParseFloat(raw, 64); err == nil {
			return int64(math.Round(f)), true
		}
		return 0, false
	case []byte:
		return anyToInt64OK(string(t))
	default:
		return 0, false
	}
}

func floatPtrValue(v *float64, def float64) float64 {
	if v == nil {
		return def
	}
	return *v
}

func normalizePremiumTableStatus(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "occupied":
		return "occupied"
	case "reserved":
		return "reserved"
	default:
		return "available"
	}
}

func normalizePremiumTableShape(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "square":
		return "square"
	default:
		return "round"
	}
}

func emptyStringToNil(raw string) any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	return raw
}

func cloneStringAnyMap(src map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range src {
		out[k] = v
	}
	return out
}

func asStringAnyMap(v any) (map[string]any, bool) {
	switch t := v.(type) {
	case map[string]any:
		return t, true
	case string:
		raw := strings.TrimSpace(t)
		if raw == "" {
			return nil, false
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			return nil, false
		}
		return out, true
	case []byte:
		return asStringAnyMap(string(t))
	default:
		return nil, false
	}
}

func normalizeBOPremiumTableRow(row map[string]any) map[string]any {
	id, _ := anyToInt64OK(row["id"])
	restaurantID, _ := anyToInt64OK(row["restaurant_id"])
	areaID, ok := anyToInt64OK(row["area_id"])
	if !ok {
		areaID, _ = anyToInt64OK(row["restaurant_area_id"])
	}

	name := firstStringFromMap(row, "name", "table_name")
	if name == "" {
		if id > 0 {
			name = fmt.Sprintf("Mesa %d", id)
		} else {
			name = "Mesa"
		}
	}

	capacity := 4
	if value, ok := anyToInt64OK(row["capacity"]); ok && value > 0 {
		capacity = int(value)
	} else if value, ok := anyToInt64OK(row["seats"]); ok && value > 0 {
		capacity = int(value)
	}

	meta := map[string]any{}
	if m, ok := asStringAnyMap(row["metadata_json"]); ok {
		for k, v := range m {
			meta[k] = v
		}
	}
	if m, ok := asStringAnyMap(row["metadata"]); ok {
		for k, v := range m {
			meta[k] = v
		}
	}

	status := firstStringFromMap(row, "status")
	if status == "" {
		status = firstStringFromMap(meta, "status")
	}
	status = normalizePremiumTableStatus(status)
	shape := firstStringFromMap(row, "shape")
	if shape == "" {
		shape = firstStringFromMap(meta, "shape")
	}
	shape = normalizePremiumTableShape(shape)
	fillColor := firstStringFromMap(row, "fill_color")
	if fillColor == "" {
		fillColor = firstStringFromMap(meta, "fill_color")
	}
	outlineColor := firstStringFromMap(row, "outline_color")
	if outlineColor == "" {
		outlineColor = firstStringFromMap(meta, "outline_color")
	}
	stylePreset := firstStringFromMap(row, "style_preset")
	if stylePreset == "" {
		stylePreset = firstStringFromMap(meta, "style_preset")
	}
	textureImageURL := firstStringFromMap(row, "texture_image_url")
	if textureImageURL == "" {
		textureImageURL = firstStringFromMap(meta, "texture_image_url")
	}

	xPos, hasXPos := anyToInt64OK(row["x_pos"])
	if !hasXPos {
		xPos, _ = anyToInt64OK(meta["x_pos"])
	}
	yPos, hasYPos := anyToInt64OK(row["y_pos"])
	if !hasYPos {
		yPos, _ = anyToInt64OK(meta["y_pos"])
	}

	out := map[string]any{
		"id":                id,
		"restaurant_id":     restaurantID,
		"area_id":           areaID,
		"name":              name,
		"capacity":          capacity,
		"status":            status,
		"shape":             shape,
		"fill_color":        fillColor,
		"outline_color":     outlineColor,
		"style_preset":      stylePreset,
		"texture_image_url": textureImageURL,
		"x_pos":             xPos,
		"y_pos":             yPos,
	}
	if updatedAt := firstStringFromMap(row, "updated_at"); updatedAt != "" {
		out["updated_at"] = updatedAt
	}
	if len(meta) > 0 {
		out["metadata"] = meta
	}
	return out
}

func isSQLSchemaError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(msg, "unknown column") ||
		strings.Contains(msg, "unknown table") ||
		strings.Contains(msg, "doesn't exist") ||
		strings.Contains(msg, "no such table") ||
		strings.Contains(msg, "no such column") ||
		strings.Contains(msg, "undefined table") ||
		strings.Contains(msg, "undefined column")
}

func isSQLDuplicateError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(msg, "duplicate entry") ||
		strings.Contains(msg, "unique constraint") ||
		strings.Contains(msg, "already exists")
}

func isDateISO(raw string) bool {
	raw = strings.TrimSpace(raw)
	if len(raw) != 10 {
		return false
	}
	_, err := time.Parse("2006-01-02", raw)
	return err == nil
}
