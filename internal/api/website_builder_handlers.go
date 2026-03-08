package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"preactvillacarmen/internal/httpx"
)

const websiteBuilderDBTimeout = 12 * time.Second

type websiteTemplateSeed struct {
	Sections []struct {
		Type     string         `json:"type"`
		Position int            `json:"position"`
		Settings map[string]any `json:"settings"`
	} `json:"sections"`
	Settings map[string]any `json:"settings"`
}

func (wb *WebsiteBuilder) ListTemplates(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), websiteBuilderDBTimeout)
	defer cancel()

	rows, err := wb.db.QueryContext(ctx, `
		SELECT id, slug, name, description, thumbnail_url, category, is_active
		FROM website_templates
		WHERE is_active = 1
		ORDER BY category, name
	`)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to fetch templates"})
		return
	}
	defer rows.Close()

	templates := make([]WebsiteTemplate, 0, 16)
	for rows.Next() {
		var item WebsiteTemplate
		if err := rows.Scan(&item.ID, &item.Slug, &item.Name, &item.Description, &item.ThumbnailURL, &item.Category, &item.IsActive); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to scan templates"})
			return
		}
		templates = append(templates, item)
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "templates": templates})
}

func (wb *WebsiteBuilder) GetTemplate(w http.ResponseWriter, r *http.Request) {
	templateID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || templateID <= 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid template id"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), websiteBuilderDBTimeout)
	defer cancel()

	var item WebsiteTemplate
	var createdAt sql.NullTime
	var updatedAt sql.NullTime
	err = wb.db.QueryRowContext(ctx, `
		SELECT id, slug, name, description, thumbnail_url, template_data, category, is_active, created_at, updated_at
		FROM website_templates
		WHERE id = ?
		LIMIT 1
	`, templateID).Scan(&item.ID, &item.Slug, &item.Name, &item.Description, &item.ThumbnailURL, &item.TemplateData, &item.Category, &item.IsActive, &createdAt, &updatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Template not found"})
			return
		}
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to load template"})
		return
	}
	if createdAt.Valid {
		item.CreatedAt = &createdAt.Time
	}
	if updatedAt.Valid {
		item.UpdatedAt = &updatedAt.Time
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "template": item})
}

func (wb *WebsiteBuilder) GetWebsite(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := wb.restaurantIDFromRequest(r)
	if !ok {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Unauthorized"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), websiteBuilderDBTimeout)
	defer cancel()

	website, found, err := wb.loadWebsiteForRestaurant(ctx, restaurantID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to load website"})
		return
	}
	if !found {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "website": nil})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "website": website})
}

func (wb *WebsiteBuilder) CreateWebsite(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := wb.restaurantIDFromRequest(r)
	if !ok {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Unauthorized"})
		return
	}

	var req struct {
		TemplateID *int            `json:"template_id"`
		Subdomain  *string         `json:"subdomain"`
		Domain     *string         `json:"domain"`
		Settings   json.RawMessage `json:"settings"`
	}
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid request body"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), websiteBuilderDBTimeout)
	defer cancel()

	if _, found, err := wb.loadWebsiteForRestaurant(ctx, restaurantID); err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to validate website"})
		return
	} else if found {
		httpx.WriteJSON(w, http.StatusConflict, map[string]any{"success": false, "message": "Website already exists"})
		return
	}

	if _, err := wb.insertWebsiteRow(ctx, restaurantID, req.TemplateID, req.Domain, req.Subdomain, req.Settings); err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to create website"})
		return
	}

	website, found, err := wb.loadWebsiteForRestaurant(ctx, restaurantID)
	if err != nil || !found {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Website created but could not be loaded"})
		return
	}
	if err := wb.ensureHomepage(ctx, website.ID); err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to create homepage"})
		return
	}
	if req.TemplateID != nil {
		_ = wb.seedTemplateSections(ctx, restaurantID, website.ID, *req.TemplateID)
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "id": website.ID, "website": website})
}

func (wb *WebsiteBuilder) UpdateWebsite(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := wb.restaurantIDFromRequest(r)
	if !ok {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Unauthorized"})
		return
	}

	var req struct {
		TemplateID *int            `json:"template_id"`
		Subdomain  *string         `json:"subdomain"`
		Domain     *string         `json:"domain"`
		Settings   json.RawMessage `json:"settings"`
		Status     *string         `json:"status"`
	}
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid request body"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), websiteBuilderDBTimeout)
	defer cancel()

	website, found, err := wb.loadWebsiteForRestaurant(ctx, restaurantID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to load website"})
		return
	}
	if !found {
		if _, err := wb.insertWebsiteRow(ctx, restaurantID, req.TemplateID, req.Domain, req.Subdomain, req.Settings); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to create website"})
			return
		}
		website, found, err = wb.loadWebsiteForRestaurant(ctx, restaurantID)
		if err != nil || !found {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to load website"})
			return
		}
	}

	if req.TemplateID != nil {
		if err := wb.execVariants(ctx, []boSQLVariant{
			{query: `UPDATE restaurant_websites SET template_id = ?, updated_at = NOW() WHERE restaurant_id = ?`, args: []any{strconv.Itoa(*req.TemplateID), restaurantID}},
		}); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to update template"})
			return
		}
	}
	if req.Domain != nil {
		if err := wb.execVariants(ctx, []boSQLVariant{
			{query: `UPDATE restaurant_websites SET domain = ?, updated_at = NOW() WHERE restaurant_id = ?`, args: []any{strings.TrimSpace(*req.Domain), restaurantID}},
		}); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to update domain"})
			return
		}
	}
	if req.Subdomain != nil {
		if err := wb.execVariants(ctx, []boSQLVariant{
			{query: `UPDATE restaurant_websites SET subdomain = ?, updated_at = NOW() WHERE restaurant_id = ?`, args: []any{strings.TrimSpace(*req.Subdomain), restaurantID}},
		}); err != nil && !isSQLSchemaError(err) {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to update subdomain"})
			return
		}
	}
	if len(req.Settings) > 0 {
		if err := wb.execVariants(ctx, []boSQLVariant{
			{query: `UPDATE restaurant_websites SET settings = ?, updated_at = NOW() WHERE restaurant_id = ?`, args: []any{req.Settings, restaurantID}},
		}); err != nil && !isSQLSchemaError(err) {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to update settings"})
			return
		}
	}
	if req.Status != nil {
		status := strings.TrimSpace(*req.Status)
		if err := wb.execVariants(ctx, []boSQLVariant{
			{query: `UPDATE restaurant_websites SET status = ?, updated_at = NOW() WHERE restaurant_id = ?`, args: []any{status, restaurantID}},
			{query: `UPDATE restaurant_websites SET is_published = ?, updated_at = NOW() WHERE restaurant_id = ?`, args: []any{boolToTinyInt(strings.EqualFold(status, "published")), restaurantID}},
		}); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to update status"})
			return
		}
	}

	if err := wb.ensureHomepage(ctx, website.ID); err == nil && req.TemplateID != nil {
		_ = wb.seedTemplateSections(ctx, restaurantID, website.ID, *req.TemplateID)
	}

	updated, found, err := wb.loadWebsiteForRestaurant(ctx, restaurantID)
	if err != nil || !found {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to load updated website"})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "website": updated})
}

func (wb *WebsiteBuilder) DeleteWebsite(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := wb.restaurantIDFromRequest(r)
	if !ok {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Unauthorized"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), websiteBuilderDBTimeout)
	defer cancel()

	website, found, err := wb.loadWebsiteForRestaurant(ctx, restaurantID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to load website"})
		return
	}
	if !found {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
		return
	}

	tx, err := wb.db.BeginTx(ctx, nil)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to start transaction"})
		return
	}
	defer func() { _ = tx.Rollback() }()

	_, _ = tx.ExecContext(ctx, `DELETE FROM website_assets WHERE website_id = ?`, website.ID)
	_, _ = tx.ExecContext(ctx, `DELETE FROM website_publish_history WHERE website_id = ?`, website.ID)
	_, _ = tx.ExecContext(ctx, `DELETE FROM website_pages WHERE website_id = ?`, website.ID)
	if _, err := tx.ExecContext(ctx, `DELETE FROM restaurant_websites WHERE id = ? AND restaurant_id = ?`, website.ID, restaurantID); err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to delete website"})
		return
	}
	if err := tx.Commit(); err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to commit website deletion"})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (wb *WebsiteBuilder) ListPages(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := wb.restaurantIDFromRequest(r)
	if !ok {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Unauthorized"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), websiteBuilderDBTimeout)
	defer cancel()

	websiteID, err := wb.resolveWebsiteID(ctx, r, restaurantID)
	if err != nil || websiteID == 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Website context required"})
		return
	}

	rows, err := wb.db.QueryContext(ctx, `
		SELECT id, website_id, slug, title, meta_description, meta_keywords, is_homepage, status, created_at, updated_at
		FROM website_pages
		WHERE website_id = ?
		ORDER BY is_homepage DESC, position ASC, slug ASC, id ASC
	`, websiteID)
	if err != nil {
		rows, err = wb.db.QueryContext(ctx, `
			SELECT id, website_id, slug, title, meta_description, meta_keywords, is_homepage, status, created_at, updated_at
			FROM website_pages
			WHERE website_id = ?
			ORDER BY is_homepage DESC, slug ASC, id ASC
		`, websiteID)
	}
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to fetch pages"})
		return
	}
	defer rows.Close()

	pages := make([]WebsitePage, 0, 8)
	for rows.Next() {
		var item WebsitePage
		var createdAt sql.NullTime
		var updatedAt sql.NullTime
		if err := rows.Scan(&item.ID, &item.WebsiteID, &item.Slug, &item.Title, &item.MetaDescription, &item.MetaKeywords, &item.IsHomepage, &item.Status, &createdAt, &updatedAt); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to scan pages"})
			return
		}
		if createdAt.Valid {
			item.CreatedAt = &createdAt.Time
		}
		if updatedAt.Valid {
			item.UpdatedAt = &updatedAt.Time
		}
		pages = append(pages, item)
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "pages": pages})
}

func (wb *WebsiteBuilder) CreatePage(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := wb.restaurantIDFromRequest(r)
	if !ok {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Unauthorized"})
		return
	}

	var req struct {
		WebsiteID       int    `json:"website_id"`
		Slug            string `json:"slug"`
		Title           string `json:"title"`
		MetaDescription string `json:"meta_description"`
		MetaKeywords    string `json:"meta_keywords"`
		IsHomepage      bool   `json:"is_homepage"`
	}
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid request body"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), websiteBuilderDBTimeout)
	defer cancel()

	websiteID := req.WebsiteID
	if websiteID == 0 {
		var err error
		websiteID, err = wb.resolveWebsiteID(ctx, r, restaurantID)
		if err != nil {
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Website context required"})
			return
		}
	}
	owned, err := wb.websiteBelongsToRestaurant(ctx, websiteID, restaurantID)
	if err != nil || !owned {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Website not found"})
		return
	}

	slug := strings.TrimSpace(req.Slug)
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = "Nueva página"
	}
	if slug == "" && !req.IsHomepage {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Slug is required"})
		return
	}

	if req.IsHomepage {
		_, _ = wb.db.ExecContext(ctx, `UPDATE website_pages SET is_homepage = 0 WHERE website_id = ?`, websiteID)
		slug = ""
	}

	result, err := wb.db.ExecContext(ctx, `
		INSERT INTO website_pages (website_id, slug, title, meta_description, meta_keywords, is_homepage, status)
		VALUES (?, ?, ?, ?, ?, ?, 'draft')
	`, websiteID, slug, title, strings.TrimSpace(req.MetaDescription), strings.TrimSpace(req.MetaKeywords), boolToTinyInt(req.IsHomepage))
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to create page"})
		return
	}
	id, _ := result.LastInsertId()

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "id": id})
}

func (wb *WebsiteBuilder) UpdatePage(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := wb.restaurantIDFromRequest(r)
	if !ok {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Unauthorized"})
		return
	}

	pageID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || pageID <= 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid page id"})
		return
	}

	var req struct {
		Slug            *string `json:"slug"`
		Title           *string `json:"title"`
		MetaDescription *string `json:"meta_description"`
		MetaKeywords    *string `json:"meta_keywords"`
		IsHomepage      *bool   `json:"is_homepage"`
		Status          *string `json:"status"`
	}
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid request body"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), websiteBuilderDBTimeout)
	defer cancel()

	websiteID, err := wb.websiteIDForPage(ctx, pageID, restaurantID)
	if err != nil || websiteID == 0 {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Page not found"})
		return
	}

	updates := make([]string, 0, 6)
	args := make([]any, 0, 7)
	if req.Slug != nil {
		updates = append(updates, "slug = ?")
		args = append(args, strings.TrimSpace(*req.Slug))
	}
	if req.Title != nil {
		updates = append(updates, "title = ?")
		args = append(args, strings.TrimSpace(*req.Title))
	}
	if req.MetaDescription != nil {
		updates = append(updates, "meta_description = ?")
		args = append(args, strings.TrimSpace(*req.MetaDescription))
	}
	if req.MetaKeywords != nil {
		updates = append(updates, "meta_keywords = ?")
		args = append(args, strings.TrimSpace(*req.MetaKeywords))
	}
	if req.IsHomepage != nil {
		if *req.IsHomepage {
			_, _ = wb.db.ExecContext(ctx, `UPDATE website_pages SET is_homepage = 0 WHERE website_id = ?`, websiteID)
		}
		updates = append(updates, "is_homepage = ?")
		args = append(args, boolToTinyInt(*req.IsHomepage))
	}
	if req.Status != nil {
		updates = append(updates, "status = ?")
		args = append(args, strings.TrimSpace(*req.Status))
	}
	if len(updates) == 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "No fields to update"})
		return
	}
	args = append(args, pageID)

	query := `UPDATE website_pages SET ` + strings.Join(updates, ", ") + ` WHERE id = ?`
	if _, err := wb.db.ExecContext(ctx, query, args...); err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to update page"})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (wb *WebsiteBuilder) DeletePage(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := wb.restaurantIDFromRequest(r)
	if !ok {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Unauthorized"})
		return
	}

	pageID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || pageID <= 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid page id"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), websiteBuilderDBTimeout)
	defer cancel()

	websiteID, err := wb.websiteIDForPage(ctx, pageID, restaurantID)
	if err != nil || websiteID == 0 {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Page not found"})
		return
	}

	var pageCount int
	if err := wb.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM website_pages WHERE website_id = ?`, websiteID).Scan(&pageCount); err == nil && pageCount <= 1 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Website must keep at least one page"})
		return
	}

	if _, err := wb.db.ExecContext(ctx, `DELETE FROM website_pages WHERE id = ?`, pageID); err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to delete page"})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (wb *WebsiteBuilder) ListSections(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := wb.restaurantIDFromRequest(r)
	if !ok {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Unauthorized"})
		return
	}

	pageID, err := strconv.Atoi(chi.URLParam(r, "pageId"))
	if err != nil || pageID <= 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid page id"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), websiteBuilderDBTimeout)
	defer cancel()

	websiteID, err := wb.websiteIDForPage(ctx, pageID, restaurantID)
	if err != nil || websiteID == 0 {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Page not found"})
		return
	}
	_ = websiteID

	rows, err := wb.db.QueryContext(ctx, `
		SELECT id, page_id, section_type, position, COALESCE(settings, JSON_OBJECT()), is_visible, created_at, updated_at
		FROM website_page_sections
		WHERE page_id = ?
		ORDER BY position ASC, id ASC
	`, pageID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to fetch sections"})
		return
	}
	defer rows.Close()

	sections := make([]WebsitePageSection, 0, 16)
	for rows.Next() {
		var item WebsitePageSection
		var createdAt sql.NullTime
		var updatedAt sql.NullTime
		if err := rows.Scan(&item.ID, &item.PageID, &item.SectionType, &item.Position, &item.Settings, &item.IsVisible, &createdAt, &updatedAt); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to scan sections"})
			return
		}
		if createdAt.Valid {
			item.CreatedAt = &createdAt.Time
		}
		if updatedAt.Valid {
			item.UpdatedAt = &updatedAt.Time
		}
		sections = append(sections, item)
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "sections": sections})
}

func (wb *WebsiteBuilder) CreateSection(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := wb.restaurantIDFromRequest(r)
	if !ok {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Unauthorized"})
		return
	}

	pageID, err := strconv.Atoi(chi.URLParam(r, "pageId"))
	if err != nil || pageID <= 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid page id"})
		return
	}

	var req struct {
		PageID      int             `json:"page_id"`
		SectionType string          `json:"section_type"`
		Position    *int            `json:"position"`
		Settings    json.RawMessage `json:"settings"`
	}
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid request body"})
		return
	}
	if req.PageID > 0 {
		pageID = req.PageID
	}

	ctx, cancel := context.WithTimeout(r.Context(), websiteBuilderDBTimeout)
	defer cancel()

	if _, err := wb.websiteIDForPage(ctx, pageID, restaurantID); err != nil {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Page not found"})
		return
	}

	sectionType := strings.TrimSpace(req.SectionType)
	if sectionType == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "section_type is required"})
		return
	}

	position := 0
	if req.Position != nil {
		position = *req.Position
	} else {
		_ = wb.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(position), -1) + 1 FROM website_page_sections WHERE page_id = ?`, pageID).Scan(&position)
	}
	settings := req.Settings
	if len(settings) == 0 {
		settings = json.RawMessage(`{}`)
	}

	result, err := wb.db.ExecContext(ctx, `
		INSERT INTO website_page_sections (page_id, section_type, position, settings, is_visible)
		VALUES (?, ?, ?, ?, 1)
	`, pageID, sectionType, position, settings)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to create section"})
		return
	}
	id, _ := result.LastInsertId()

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "id": id})
}

func (wb *WebsiteBuilder) UpdateSection(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := wb.restaurantIDFromRequest(r)
	if !ok {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Unauthorized"})
		return
	}

	sectionID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || sectionID <= 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid section id"})
		return
	}

	var req struct {
		SectionType *string         `json:"section_type"`
		Position    *int            `json:"position"`
		Settings    json.RawMessage `json:"settings"`
		IsVisible   *bool           `json:"is_visible"`
	}
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid request body"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), websiteBuilderDBTimeout)
	defer cancel()

	if _, err := wb.pageIDForSection(ctx, sectionID, restaurantID); err != nil {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Section not found"})
		return
	}

	updates := make([]string, 0, 4)
	args := make([]any, 0, 5)
	if req.SectionType != nil {
		updates = append(updates, "section_type = ?")
		args = append(args, strings.TrimSpace(*req.SectionType))
	}
	if req.Position != nil {
		updates = append(updates, "position = ?")
		args = append(args, *req.Position)
	}
	if len(req.Settings) > 0 {
		updates = append(updates, "settings = ?")
		args = append(args, req.Settings)
	}
	if req.IsVisible != nil {
		updates = append(updates, "is_visible = ?")
		args = append(args, boolToTinyInt(*req.IsVisible))
	}
	if len(updates) == 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "No fields to update"})
		return
	}
	args = append(args, sectionID)

	if _, err := wb.db.ExecContext(ctx, `UPDATE website_page_sections SET `+strings.Join(updates, ", ")+` WHERE id = ?`, args...); err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to update section"})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (wb *WebsiteBuilder) DeleteSection(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := wb.restaurantIDFromRequest(r)
	if !ok {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Unauthorized"})
		return
	}

	sectionID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || sectionID <= 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid section id"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), websiteBuilderDBTimeout)
	defer cancel()

	if _, err := wb.pageIDForSection(ctx, sectionID, restaurantID); err != nil {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Section not found"})
		return
	}

	if _, err := wb.db.ExecContext(ctx, `DELETE FROM website_page_sections WHERE id = ?`, sectionID); err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to delete section"})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (wb *WebsiteBuilder) ReorderSections(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := wb.restaurantIDFromRequest(r)
	if !ok {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Unauthorized"})
		return
	}

	var req struct {
		PageID     int   `json:"page_id"`
		SectionIDs []int `json:"section_ids"`
	}
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid request body"})
		return
	}
	if req.PageID <= 0 || len(req.SectionIDs) == 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "page_id and section_ids are required"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), websiteBuilderDBTimeout)
	defer cancel()

	if _, err := wb.websiteIDForPage(ctx, req.PageID, restaurantID); err != nil {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Page not found"})
		return
	}

	tx, err := wb.db.BeginTx(ctx, nil)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to start transaction"})
		return
	}
	defer func() { _ = tx.Rollback() }()

	for idx, sectionID := range req.SectionIDs {
		if _, err := tx.ExecContext(ctx, `UPDATE website_page_sections SET position = ? WHERE id = ? AND page_id = ?`, idx, sectionID, req.PageID); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to reorder sections"})
			return
		}
	}
	if err := tx.Commit(); err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to commit reorder"})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (wb *WebsiteBuilder) ListComponents(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), websiteBuilderDBTimeout)
	defer cancel()

	rows, err := wb.db.QueryContext(ctx, `
		SELECT id, component_type, name, description, icon, COALESCE(default_settings, JSON_OBJECT()), COALESCE(schema_json, JSON_OBJECT()), is_active, created_at, updated_at
		FROM website_components
		WHERE is_active = 1
		ORDER BY name ASC
	`)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to fetch components"})
		return
	}
	defer rows.Close()

	items := make([]WebsiteComponent, 0, 24)
	for rows.Next() {
		var item WebsiteComponent
		var createdAt sql.NullTime
		var updatedAt sql.NullTime
		if err := rows.Scan(&item.ID, &item.ComponentType, &item.Name, &item.Description, &item.Icon, &item.DefaultSettings, &item.SchemaJSON, &item.IsActive, &createdAt, &updatedAt); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to scan components"})
			return
		}
		if createdAt.Valid {
			item.CreatedAt = &createdAt.Time
		}
		if updatedAt.Valid {
			item.UpdatedAt = &updatedAt.Time
		}
		items = append(items, item)
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "components": items})
}

func (wb *WebsiteBuilder) GetComponent(w http.ResponseWriter, r *http.Request) {
	componentID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || componentID <= 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid component id"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), websiteBuilderDBTimeout)
	defer cancel()

	var item WebsiteComponent
	var createdAt sql.NullTime
	var updatedAt sql.NullTime
	err = wb.db.QueryRowContext(ctx, `
		SELECT id, component_type, name, description, icon, COALESCE(default_settings, JSON_OBJECT()), COALESCE(schema_json, JSON_OBJECT()), is_active, created_at, updated_at
		FROM website_components
		WHERE id = ?
		LIMIT 1
	`, componentID).Scan(&item.ID, &item.ComponentType, &item.Name, &item.Description, &item.Icon, &item.DefaultSettings, &item.SchemaJSON, &item.IsActive, &createdAt, &updatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Component not found"})
			return
		}
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to load component"})
		return
	}
	if createdAt.Valid {
		item.CreatedAt = &createdAt.Time
	}
	if updatedAt.Valid {
		item.UpdatedAt = &updatedAt.Time
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "component": item})
}

func (wb *WebsiteBuilder) ListSectionComponents(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := wb.restaurantIDFromRequest(r)
	if !ok {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Unauthorized"})
		return
	}

	sectionID, err := strconv.Atoi(chi.URLParam(r, "sectionId"))
	if err != nil || sectionID <= 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid section id"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), websiteBuilderDBTimeout)
	defer cancel()

	if _, err := wb.pageIDForSection(ctx, sectionID, restaurantID); err != nil {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Section not found"})
		return
	}

	items, err := wb.listSectionComponentsBySection(ctx, sectionID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to fetch section components"})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "components": items})
}

func (wb *WebsiteBuilder) CreateSectionComponent(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := wb.restaurantIDFromRequest(r)
	if !ok {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Unauthorized"})
		return
	}

	sectionID, err := strconv.Atoi(chi.URLParam(r, "sectionId"))
	if err != nil || sectionID <= 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid section id"})
		return
	}

	var req struct {
		SectionID     int             `json:"section_id"`
		ComponentID   int             `json:"component_id"`
		Position      *int            `json:"position"`
		Settings      json.RawMessage `json:"settings"`
		DynamicSource string          `json:"dynamic_source"`
		DynamicParams json.RawMessage `json:"dynamic_params"`
	}
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid request body"})
		return
	}
	if req.SectionID > 0 {
		sectionID = req.SectionID
	}
	if req.ComponentID <= 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "component_id is required"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), websiteBuilderDBTimeout)
	defer cancel()

	if _, err := wb.pageIDForSection(ctx, sectionID, restaurantID); err != nil {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Section not found"})
		return
	}

	position := 0
	if req.Position != nil {
		position = *req.Position
	} else {
		_ = wb.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(position), -1) + 1 FROM website_section_components WHERE section_id = ?`, sectionID).Scan(&position)
	}
	settings := req.Settings
	if len(settings) == 0 {
		settings = json.RawMessage(`{}`)
	}
	dynamicParams := req.DynamicParams
	if len(dynamicParams) == 0 {
		dynamicParams = json.RawMessage(`{}`)
	}

	result, err := wb.db.ExecContext(ctx, `
		INSERT INTO website_section_components (section_id, component_id, position, settings, dynamic_source, dynamic_params)
		VALUES (?, ?, ?, ?, ?, ?)
	`, sectionID, req.ComponentID, position, settings, strings.TrimSpace(req.DynamicSource), dynamicParams)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to create section component"})
		return
	}
	id, _ := result.LastInsertId()

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "id": id})
}

func (wb *WebsiteBuilder) UpdateSectionComponent(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := wb.restaurantIDFromRequest(r)
	if !ok {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Unauthorized"})
		return
	}

	componentID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || componentID <= 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid section component id"})
		return
	}

	var req struct {
		Position      *int            `json:"position"`
		Settings      json.RawMessage `json:"settings"`
		DynamicSource *string         `json:"dynamic_source"`
		DynamicParams json.RawMessage `json:"dynamic_params"`
	}
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid request body"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), websiteBuilderDBTimeout)
	defer cancel()

	if _, err := wb.sectionIDForPlacedComponent(ctx, componentID, restaurantID); err != nil {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Section component not found"})
		return
	}

	updates := make([]string, 0, 4)
	args := make([]any, 0, 5)
	if req.Position != nil {
		updates = append(updates, "position = ?")
		args = append(args, *req.Position)
	}
	if len(req.Settings) > 0 {
		updates = append(updates, "settings = ?")
		args = append(args, req.Settings)
	}
	if req.DynamicSource != nil {
		updates = append(updates, "dynamic_source = ?")
		args = append(args, strings.TrimSpace(*req.DynamicSource))
	}
	if len(req.DynamicParams) > 0 {
		updates = append(updates, "dynamic_params = ?")
		args = append(args, req.DynamicParams)
	}
	if len(updates) == 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "No fields to update"})
		return
	}
	args = append(args, componentID)

	if _, err := wb.db.ExecContext(ctx, `UPDATE website_section_components SET `+strings.Join(updates, ", ")+` WHERE id = ?`, args...); err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to update section component"})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (wb *WebsiteBuilder) DeleteSectionComponent(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := wb.restaurantIDFromRequest(r)
	if !ok {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Unauthorized"})
		return
	}

	componentID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || componentID <= 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid section component id"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), websiteBuilderDBTimeout)
	defer cancel()

	if _, err := wb.sectionIDForPlacedComponent(ctx, componentID, restaurantID); err != nil {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Section component not found"})
		return
	}

	if _, err := wb.db.ExecContext(ctx, `DELETE FROM website_section_components WHERE id = ?`, componentID); err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to delete section component"})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (wb *WebsiteBuilder) ReorderSectionComponents(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := wb.restaurantIDFromRequest(r)
	if !ok {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Unauthorized"})
		return
	}

	var req struct {
		SectionID    int   `json:"section_id"`
		ComponentIDs []int `json:"component_ids"`
	}
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid request body"})
		return
	}
	if req.SectionID <= 0 || len(req.ComponentIDs) == 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "section_id and component_ids are required"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), websiteBuilderDBTimeout)
	defer cancel()

	if _, err := wb.pageIDForSection(ctx, req.SectionID, restaurantID); err != nil {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Section not found"})
		return
	}

	tx, err := wb.db.BeginTx(ctx, nil)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to start transaction"})
		return
	}
	defer func() { _ = tx.Rollback() }()

	for idx, componentID := range req.ComponentIDs {
		if _, err := tx.ExecContext(ctx, `UPDATE website_section_components SET position = ? WHERE id = ? AND section_id = ?`, idx, componentID, req.SectionID); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to reorder components"})
			return
		}
	}
	if err := tx.Commit(); err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to commit reorder"})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (wb *WebsiteBuilder) ListAssets(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := wb.restaurantIDFromRequest(r)
	if !ok {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Unauthorized"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), websiteBuilderDBTimeout)
	defer cancel()

	websiteID, err := wb.resolveWebsiteID(ctx, r, restaurantID)
	if err != nil || websiteID == 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Website context required"})
		return
	}

	rows, err := wb.db.QueryContext(ctx, `
		SELECT id, website_id, asset_type, original_filename, storage_path, public_url, mime_type, file_size, width, height, alt_text, created_at
		FROM website_assets
		WHERE website_id = ?
		ORDER BY id DESC
	`, websiteID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to fetch assets"})
		return
	}
	defer rows.Close()

	assets := make([]WebsiteAsset, 0, 24)
	for rows.Next() {
		var item WebsiteAsset
		var createdAt sql.NullTime
		if err := rows.Scan(&item.ID, &item.WebsiteID, &item.AssetType, &item.OriginalFilename, &item.StoragePath, &item.PublicURL, &item.MimeType, &item.FileSize, &item.Width, &item.Height, &item.AltText, &createdAt); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to scan assets"})
			return
		}
		if createdAt.Valid {
			item.CreatedAt = &createdAt.Time
		}
		assets = append(assets, item)
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "assets": assets})
}

func (wb *WebsiteBuilder) UploadAsset(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := wb.restaurantIDFromRequest(r)
	if !ok {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Unauthorized"})
		return
	}
	if !wb.server.bunnyConfigured() {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "BunnyCDN storage is not configured"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), websiteBuilderDBTimeout)
	defer cancel()

	websiteID, err := wb.resolveWebsiteID(ctx, r, restaurantID)
	if err != nil || websiteID == 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Website context required"})
		return
	}

	if err := r.ParseMultipartForm(16 << 20); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid multipart form"})
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "file is required"})
		return
	}
	defer file.Close()

	raw, err := io.ReadAll(io.LimitReader(file, 16<<20+1))
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Failed to read file"})
		return
	}
	if len(raw) == 0 || len(raw) > 16<<20 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid file size"})
		return
	}

	assetType := strings.TrimSpace(r.FormValue("asset_type"))
	if assetType == "" {
		assetType = "image"
	}
	altText := strings.TrimSpace(r.FormValue("alt_text"))
	contentType := http.DetectContentType(raw)
	objectPath := path.Join(
		"websites",
		fmt.Sprintf("restaurant-%d", restaurantID),
		"assets",
		fmt.Sprintf("website-%d", websiteID),
		fmt.Sprintf("%d-%s", time.Now().UTC().UnixNano(), sanitizeWebsiteBuilderFileName(header.Filename)),
	)

	if err := wb.server.bunnyPut(ctx, objectPath, raw, contentType); err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to upload asset"})
		return
	}

	publicURL := wb.server.bunnyPullURL(objectPath)
	result, err := wb.db.ExecContext(ctx, `
		INSERT INTO website_assets (website_id, asset_type, original_filename, storage_path, public_url, mime_type, file_size, width, height, alt_text)
		VALUES (?, ?, ?, ?, ?, ?, ?, 0, 0, ?)
	`, websiteID, assetType, header.Filename, objectPath, publicURL, contentType, len(raw), altText)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to persist asset"})
		return
	}
	id, _ := result.LastInsertId()

	asset := WebsiteAsset{
		ID:               int(id),
		WebsiteID:        websiteID,
		AssetType:        assetType,
		OriginalFilename: header.Filename,
		StoragePath:      objectPath,
		PublicURL:        publicURL,
		MimeType:         contentType,
		FileSize:         len(raw),
		Width:            0,
		Height:           0,
		AltText:          altText,
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "asset": asset})
}

func (wb *WebsiteBuilder) DeleteAsset(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := wb.restaurantIDFromRequest(r)
	if !ok {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Unauthorized"})
		return
	}

	assetID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || assetID <= 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid asset id"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), websiteBuilderDBTimeout)
	defer cancel()

	var owned int
	err = wb.db.QueryRowContext(ctx, `
		SELECT 1
		FROM website_assets wa
		JOIN restaurant_websites rw ON rw.id = wa.website_id
		WHERE wa.id = ? AND rw.restaurant_id = ?
		LIMIT 1
	`, assetID, restaurantID).Scan(&owned)
	if err != nil {
		if err == sql.ErrNoRows {
			httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Asset not found"})
			return
		}
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to validate asset"})
		return
	}

	if _, err := wb.db.ExecContext(ctx, `DELETE FROM website_assets WHERE id = ?`, assetID); err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to delete asset"})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (wb *WebsiteBuilder) PublishWebsite(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok || a.ActiveRestaurantID <= 0 {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Unauthorized"})
		return
	}
	if !wb.server.bunnyConfigured() {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "BunnyCDN storage is not configured"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()

	website, found, err := wb.loadWebsiteForRestaurant(ctx, a.ActiveRestaurantID)
	if err != nil || !found {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Website not found"})
		return
	}

	generator := HTMXGenerator{server: wb.server, backendBaseURL: websiteBuilderBackendBaseURL(r)}
	files, err := generator.GenerateWebsite(ctx, website.ID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to generate website"})
		return
	}

	storagePrefix := path.Join("websites", fmt.Sprintf("restaurant-%d", a.ActiveRestaurantID), "live")
	for _, file := range files {
		objectPath := path.Join(storagePrefix, file.Path)
		if err := wb.server.bunnyPut(ctx, objectPath, []byte(file.Content), websiteBuilderContentType(objectPath)); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to upload generated website"})
			return
		}
	}

	version, historyErr := wb.insertPublishHistory(ctx, website.ID, int64(a.User.ID), storagePrefix, files)
	if historyErr != nil && !isSQLSchemaError(historyErr) {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to record publish history"})
		return
	}
	if err := wb.markWebsitePublished(ctx, a.ActiveRestaurantID); err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to mark website as published"})
		return
	}

	publicURL := wb.server.bunnyPullURL(path.Join(storagePrefix, "index.html"))
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":      true,
		"storage_path": storagePrefix,
		"public_url":   publicURL,
		"version":      version,
		"files":        files,
	})
}

func (wb *WebsiteBuilder) PreviewWebsite(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok || a.ActiveRestaurantID <= 0 {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Unauthorized"})
		return
	}
	if !wb.server.bunnyConfigured() {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "BunnyCDN storage is not configured"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()

	website, found, err := wb.loadWebsiteForRestaurant(ctx, a.ActiveRestaurantID)
	if err != nil || !found {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Website not found"})
		return
	}

	generator := HTMXGenerator{server: wb.server, backendBaseURL: websiteBuilderBackendBaseURL(r)}
	files, err := generator.GenerateWebsite(ctx, website.ID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to generate preview"})
		return
	}

	previewPrefix := path.Join("websites", fmt.Sprintf("restaurant-%d", a.ActiveRestaurantID), "preview", strconv.FormatInt(time.Now().UTC().Unix(), 10))
	for _, file := range files {
		objectPath := path.Join(previewPrefix, file.Path)
		if err := wb.server.bunnyPut(ctx, objectPath, []byte(file.Content), websiteBuilderContentType(objectPath)); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to upload preview"})
			return
		}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":      true,
		"preview_url":  wb.server.bunnyPullURL(path.Join(previewPrefix, "index.html")),
		"storage_path": previewPrefix,
		"files":        files,
	})
}

func (wb *WebsiteBuilder) PublishHistory(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := wb.restaurantIDFromRequest(r)
	if !ok {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Unauthorized"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), websiteBuilderDBTimeout)
	defer cancel()

	website, found, err := wb.loadWebsiteForRestaurant(ctx, restaurantID)
	if err != nil || !found {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "history": []WebsitePublishHistoryEntry{}})
		return
	}

	rows, err := wb.db.QueryContext(ctx, `
		SELECT id, website_id, version, snapshot_json, published_by, published_at, storage_path
		FROM website_publish_history
		WHERE website_id = ?
		ORDER BY version DESC, id DESC
	`, website.ID)
	if err != nil {
		if isSQLSchemaError(err) {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "history": []WebsitePublishHistoryEntry{}})
			return
		}
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to load publish history"})
		return
	}
	defer rows.Close()

	history := make([]WebsitePublishHistoryEntry, 0, 16)
	for rows.Next() {
		var item WebsitePublishHistoryEntry
		var publishedBy sql.NullInt64
		var publishedAt sql.NullTime
		if err := rows.Scan(&item.ID, &item.WebsiteID, &item.Version, &item.Snapshot, &publishedBy, &publishedAt, &item.StoragePath); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Failed to scan publish history"})
			return
		}
		if publishedBy.Valid {
			item.PublishedBy = &publishedBy.Int64
		}
		if publishedAt.Valid {
			item.PublishedAt = &publishedAt.Time
		}
		history = append(history, item)
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "history": history})
}

func (wb *WebsiteBuilder) restaurantIDFromRequest(r *http.Request) (int, bool) {
	if a, ok := boAuthFromContext(r.Context()); ok && a.ActiveRestaurantID > 0 {
		return a.ActiveRestaurantID, true
	}
	if restaurantID, ok := r.Context().Value("restaurant_id").(int); ok && restaurantID > 0 {
		return restaurantID, true
	}
	return 0, false
}

func (wb *WebsiteBuilder) loadWebsiteForRestaurant(ctx context.Context, restaurantID int) (Website, bool, error) {
	row, found, err := wb.server.queryOneAsMap(ctx, `SELECT * FROM restaurant_websites WHERE restaurant_id = ? LIMIT 1`, restaurantID)
	if err != nil || !found {
		return Website{}, found, err
	}
	return mapWebsiteRow(row), true, nil
}

func (wb *WebsiteBuilder) insertWebsiteRow(ctx context.Context, restaurantID int, templateID *int, domain *string, subdomain *string, settings json.RawMessage) (sql.Result, error) {
	templateArg := any(nil)
	if templateID != nil && *templateID > 0 {
		templateArg = strconv.Itoa(*templateID)
	}
	domainArg := any(nil)
	if domain != nil {
		domainArg = strings.TrimSpace(*domain)
	}
	subdomainArg := any(nil)
	if subdomain != nil {
		subdomainArg = strings.TrimSpace(*subdomain)
	}
	settingsArg := any(nil)
	if len(settings) > 0 {
		settingsArg = settings
	}

	return wb.execVariantsResult(ctx, []boSQLVariant{
		{query: `INSERT INTO restaurant_websites (restaurant_id, template_id, domain, subdomain, status, settings, created_at, updated_at) VALUES (?, ?, ?, ?, 'draft', ?, NOW(), NOW())`, args: []any{restaurantID, templateArg, domainArg, subdomainArg, settingsArg}},
		{query: `INSERT INTO restaurant_websites (restaurant_id, template_id, domain, status, settings, created_at, updated_at) VALUES (?, ?, ?, 'draft', ?, NOW(), NOW())`, args: []any{restaurantID, templateArg, domainArg, settingsArg}},
		{query: `INSERT INTO restaurant_websites (restaurant_id, template_id, domain, created_at, updated_at) VALUES (?, ?, ?, NOW(), NOW())`, args: []any{restaurantID, templateArg, domainArg}},
		{query: `INSERT INTO restaurant_websites (restaurant_id, template_id, created_at, updated_at) VALUES (?, ?, NOW(), NOW())`, args: []any{restaurantID, templateArg}},
		{query: `INSERT INTO restaurant_websites (restaurant_id, created_at, updated_at) VALUES (?, NOW(), NOW())`, args: []any{restaurantID}},
	})
}

func (wb *WebsiteBuilder) ensureHomepage(ctx context.Context, websiteID int) error {
	var existingID int
	err := wb.db.QueryRowContext(ctx, `SELECT id FROM website_pages WHERE website_id = ? AND is_homepage = 1 LIMIT 1`, websiteID).Scan(&existingID)
	if err == nil {
		return nil
	}
	if err != sql.ErrNoRows {
		return err
	}
	_, err = wb.db.ExecContext(ctx, `INSERT INTO website_pages (website_id, slug, title, is_homepage, status) VALUES (?, '', 'Inicio', 1, 'draft')`, websiteID)
	return err
}

func (wb *WebsiteBuilder) resolveWebsiteID(ctx context.Context, r *http.Request, restaurantID int) (int, error) {
	if raw := strings.TrimSpace(r.URL.Query().Get("website_id")); raw != "" {
		websiteID, err := strconv.Atoi(raw)
		if err != nil || websiteID <= 0 {
			return 0, fmt.Errorf("invalid website id")
		}
		owned, err := wb.websiteBelongsToRestaurant(ctx, websiteID, restaurantID)
		if err != nil || !owned {
			return 0, fmt.Errorf("website not found")
		}
		return websiteID, nil
	}
	website, found, err := wb.loadWebsiteForRestaurant(ctx, restaurantID)
	if err != nil || !found {
		return 0, fmt.Errorf("website not found")
	}
	return website.ID, nil
}

func (wb *WebsiteBuilder) websiteBelongsToRestaurant(ctx context.Context, websiteID int, restaurantID int) (bool, error) {
	var one int
	err := wb.db.QueryRowContext(ctx, `SELECT 1 FROM restaurant_websites WHERE id = ? AND restaurant_id = ? LIMIT 1`, websiteID, restaurantID).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (wb *WebsiteBuilder) websiteIDForPage(ctx context.Context, pageID int, restaurantID int) (int, error) {
	var websiteID int
	err := wb.db.QueryRowContext(ctx, `
		SELECT wp.website_id
		FROM website_pages wp
		JOIN restaurant_websites rw ON rw.id = wp.website_id
		WHERE wp.id = ? AND rw.restaurant_id = ?
		LIMIT 1
	`, pageID, restaurantID).Scan(&websiteID)
	return websiteID, err
}

func (wb *WebsiteBuilder) pageIDForSection(ctx context.Context, sectionID int, restaurantID int) (int, error) {
	var pageID int
	err := wb.db.QueryRowContext(ctx, `
		SELECT s.page_id
		FROM website_page_sections s
		JOIN website_pages p ON p.id = s.page_id
		JOIN restaurant_websites rw ON rw.id = p.website_id
		WHERE s.id = ? AND rw.restaurant_id = ?
		LIMIT 1
	`, sectionID, restaurantID).Scan(&pageID)
	return pageID, err
}

func (wb *WebsiteBuilder) sectionIDForPlacedComponent(ctx context.Context, sectionComponentID int, restaurantID int) (int, error) {
	var sectionID int
	err := wb.db.QueryRowContext(ctx, `
		SELECT sc.section_id
		FROM website_section_components sc
		JOIN website_page_sections s ON s.id = sc.section_id
		JOIN website_pages p ON p.id = s.page_id
		JOIN restaurant_websites rw ON rw.id = p.website_id
		WHERE sc.id = ? AND rw.restaurant_id = ?
		LIMIT 1
	`, sectionComponentID, restaurantID).Scan(&sectionID)
	return sectionID, err
}

func (wb *WebsiteBuilder) listSectionComponentsBySection(ctx context.Context, sectionID int) ([]WebsiteSectionComponent, error) {
	rows, err := wb.db.QueryContext(ctx, `
		SELECT
			sc.id,
			sc.section_id,
			sc.component_id,
			sc.position,
			COALESCE(sc.settings, JSON_OBJECT()),
			COALESCE(sc.dynamic_source, ''),
			COALESCE(sc.dynamic_params, JSON_OBJECT()),
			wc.component_type,
			wc.name,
			COALESCE(wc.default_settings, JSON_OBJECT()),
			wc.description,
			COALESCE(wc.icon, ''),
			wc.is_active,
			sc.created_at,
			sc.updated_at
		FROM website_section_components sc
		JOIN website_components wc ON wc.id = sc.component_id
		WHERE sc.section_id = ?
		ORDER BY sc.position ASC, sc.id ASC
	`, sectionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]WebsiteSectionComponent, 0, 16)
	for rows.Next() {
		var item WebsiteSectionComponent
		var component WebsiteComponent
		var createdAt sql.NullTime
		var updatedAt sql.NullTime
		if err := rows.Scan(
			&item.ID,
			&item.SectionID,
			&item.ComponentID,
			&item.Position,
			&item.Settings,
			&item.DynamicSource,
			&item.DynamicParams,
			&item.ComponentType,
			&item.ComponentName,
			&item.DefaultSettings,
			&component.Description,
			&component.Icon,
			&component.IsActive,
			&createdAt,
			&updatedAt,
		); err != nil {
			return nil, err
		}
		component.ID = item.ComponentID
		component.ComponentType = item.ComponentType
		component.Name = item.ComponentName
		component.DefaultSettings = item.DefaultSettings
		item.Component = &component
		if createdAt.Valid {
			item.CreatedAt = &createdAt.Time
		}
		if updatedAt.Valid {
			item.UpdatedAt = &updatedAt.Time
		}
		items = append(items, item)
	}
	return items, nil
}

func (wb *WebsiteBuilder) execVariants(ctx context.Context, variants []boSQLVariant) error {
	_, err := wb.execVariantsResult(ctx, variants)
	return err
}

func (wb *WebsiteBuilder) execVariantsResult(ctx context.Context, variants []boSQLVariant) (sql.Result, error) {
	var lastErr error
	for _, variant := range variants {
		res, err := wb.db.ExecContext(ctx, variant.query, variant.args...)
		if err == nil {
			return res, nil
		}
		if isSQLSchemaError(err) {
			lastErr = err
			continue
		}
		return nil, err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no sql variant succeeded")
	}
	return nil, lastErr
}

func (wb *WebsiteBuilder) seedTemplateSections(ctx context.Context, restaurantID int, websiteID int, templateID int) error {
	if templateID <= 0 {
		return nil
	}
	homepageID, err := wb.ensureHomepageAndGetID(ctx, websiteID)
	if err != nil {
		return err
	}
	var existingCount int
	if err := wb.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM website_page_sections WHERE page_id = ?`, homepageID).Scan(&existingCount); err == nil && existingCount > 0 {
		return nil
	}

	var raw json.RawMessage
	if err := wb.db.QueryRowContext(ctx, `SELECT template_data FROM website_templates WHERE id = ? LIMIT 1`, templateID).Scan(&raw); err != nil {
		return err
	}

	var seed websiteTemplateSeed
	if err := json.Unmarshal(raw, &seed); err != nil {
		return err
	}
	if len(seed.Sections) == 0 {
		return nil
	}

	componentIDs, err := wb.componentIDMap(ctx)
	if err != nil {
		return err
	}
	branding, _ := wb.server.loadRestaurantBranding(ctx, restaurantID)
	brandName := strings.TrimSpace(branding.BrandName)
	if brandName == "" {
		brandName = "Nuestro restaurante"
	}

	sort.Slice(seed.Sections, func(i, j int) bool {
		if seed.Sections[i].Position == seed.Sections[j].Position {
			return seed.Sections[i].Type < seed.Sections[j].Type
		}
		return seed.Sections[i].Position < seed.Sections[j].Position
	})

	for idx, section := range seed.Sections {
		sectionSettings, _ := json.Marshal(section.Settings)
		if len(sectionSettings) == 0 || string(sectionSettings) == "null" {
			sectionSettings = []byte(`{}`)
		}
		res, err := wb.db.ExecContext(ctx, `
			INSERT INTO website_page_sections (page_id, section_type, position, settings, is_visible)
			VALUES (?, ?, ?, ?, 1)
		`, homepageID, strings.TrimSpace(section.Type), idx, sectionSettings)
		if err != nil {
			return err
		}
		sectionID, _ := res.LastInsertId()

		componentType, dynamicSource, componentSettings := defaultTemplateComponentForSection(section.Type, brandName)
		componentID := componentIDs[componentType]
		if componentID == 0 {
			continue
		}
		settingsRaw, _ := json.Marshal(componentSettings)
		if len(settingsRaw) == 0 || string(settingsRaw) == "null" {
			settingsRaw = []byte(`{}`)
		}
		if _, err := wb.db.ExecContext(ctx, `
			INSERT INTO website_section_components (section_id, component_id, position, settings, dynamic_source, dynamic_params)
			VALUES (?, ?, 0, ?, ?, JSON_OBJECT())
		`, sectionID, componentID, settingsRaw, dynamicSource); err != nil {
			return err
		}
	}

	if len(seed.Settings) > 0 {
		if rawSettings, err := json.Marshal(seed.Settings); err == nil {
			_ = wb.execVariants(ctx, []boSQLVariant{{query: `UPDATE restaurant_websites SET settings = ?, updated_at = NOW() WHERE id = ?`, args: []any{rawSettings, websiteID}}})
		}
	}
	return nil
}

func (wb *WebsiteBuilder) ensureHomepageAndGetID(ctx context.Context, websiteID int) (int, error) {
	if err := wb.ensureHomepage(ctx, websiteID); err != nil {
		return 0, err
	}
	var homepageID int
	err := wb.db.QueryRowContext(ctx, `SELECT id FROM website_pages WHERE website_id = ? AND is_homepage = 1 LIMIT 1`, websiteID).Scan(&homepageID)
	return homepageID, err
}

func (wb *WebsiteBuilder) componentIDMap(ctx context.Context) (map[string]int, error) {
	rows, err := wb.db.QueryContext(ctx, `SELECT id, component_type FROM website_components WHERE is_active = 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var id int
		var componentType string
		if err := rows.Scan(&id, &componentType); err != nil {
			return nil, err
		}
		out[strings.TrimSpace(componentType)] = id
	}
	return out, nil
}

func (wb *WebsiteBuilder) markWebsitePublished(ctx context.Context, restaurantID int) error {
	return wb.execVariants(ctx, []boSQLVariant{
		{query: `UPDATE restaurant_websites SET status = 'published', published_at = NOW(), updated_at = NOW() WHERE restaurant_id = ?`, args: []any{restaurantID}},
		{query: `UPDATE restaurant_websites SET is_published = 1, updated_at = NOW() WHERE restaurant_id = ?`, args: []any{restaurantID}},
	})
}

func (wb *WebsiteBuilder) insertPublishHistory(ctx context.Context, websiteID int, userID int64, storagePath string, files []GeneratedFile) (int, error) {
	var version int
	if err := wb.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) + 1 FROM website_publish_history WHERE website_id = ?`, websiteID).Scan(&version); err != nil {
		if !isSQLSchemaError(err) {
			return 0, err
		}
		version = 1
	}
	snapshot := map[string]any{
		"generated_at": time.Now().UTC().Format(time.RFC3339),
		"files":        files,
	}
	raw, _ := json.Marshal(snapshot)
	if _, err := wb.db.ExecContext(ctx, `
		INSERT INTO website_publish_history (website_id, version, snapshot_json, published_by, storage_path)
		VALUES (?, ?, ?, ?, ?)
	`, websiteID, version, raw, userID, storagePath); err != nil {
		return 0, err
	}
	return version, nil
}

func mapWebsiteRow(row map[string]any) Website {
	var settings json.RawMessage
	if raw := firstStringFromMap(row, "settings"); strings.TrimSpace(raw) != "" {
		settings = json.RawMessage(raw)
	}
	templateID := parseWebsiteTemplateID(firstStringFromMap(row, "template_id"))
	website := Website{
		RestaurantID: int(anyToInt64(row["restaurant_id"])),
		TemplateID:   templateID,
		CustomHTML:   firstStringFromMap(row, "custom_html", "html_content"),
		Domain:       firstStringFromMap(row, "domain"),
		Subdomain:    firstStringFromMap(row, "subdomain"),
		Status:       firstNonEmptyString(firstStringFromMap(row, "status"), mapWebsiteStatus(row)),
		Settings:     settings,
	}
	if id, ok := anyToInt64OK(row["id"]); ok {
		website.ID = int(id)
	}
	if t := anyToTimePtr(row["published_at"]); t != nil {
		website.PublishedAt = t
	}
	if t := anyToTimePtr(row["created_at"]); t != nil {
		website.CreatedAt = t
	}
	if t := anyToTimePtr(row["updated_at"]); t != nil {
		website.UpdatedAt = t
	}
	return website
}

func parseWebsiteTemplateID(raw string) *int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return nil
	}
	return &value
}

func mapWebsiteStatus(row map[string]any) string {
	if anyToBool(row["is_published"]) {
		return "published"
	}
	return "draft"
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func anyToInt64(v any) int64 {
	value, _ := anyToInt64OK(v)
	return value
}

func anyToTimePtr(v any) *time.Time {
	switch value := v.(type) {
	case time.Time:
		out := value
		return &out
	case *time.Time:
		return value
	case []byte:
		return parseTimeStringPtr(string(value))
	case string:
		return parseTimeStringPtr(value)
	default:
		return nil
	}
}

func parseTimeStringPtr(raw string) *time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	formats := []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05", "2006-01-02 15:04:05.999999", "2006-01-02"}
	for _, format := range formats {
		if parsed, err := time.Parse(format, raw); err == nil {
			return &parsed
		}
	}
	return nil
}

func defaultTemplateComponentForSection(sectionType string, brandName string) (componentType string, dynamicSource string, settings map[string]any) {
	switch strings.TrimSpace(strings.ToLower(sectionType)) {
	case "header":
		return "text-block", "", map[string]any{"content": fmt.Sprintf("<p data-ui=\"header-brand\">%s</p>", brandName), "align": "center", "maxWidth": "100%"}
	case "hero":
		return "hero-banner", "", map[string]any{"title": brandName, "subtitle": "Descubre nuestra propuesta gastronómica", "ctaText": "Reservar mesa", "ctaLink": "#reservas"}
	case "about":
		return "text-block", "", map[string]any{"content": "<h2 data-ui=\"about-title\">Sobre nosotros</h2><p data-ui=\"about-copy\">Actualiza este contenido desde el builder para contar la historia del restaurante.</p>", "align": "left", "maxWidth": "840px"}
	case "menu":
		return "menu-card", "menus", map[string]any{"title": "Carta y menús"}
	case "wines":
		return "wine-list", "wines", map[string]any{"title": "Carta de vinos"}
	case "hours":
		return "hours-table", "hours", map[string]any{"title": "Horarios"}
	case "gallery":
		return "gallery-grid", "", map[string]any{"columns": 3}
	case "contact":
		return "contact-form", "", map[string]any{"title": "Contacto"}
	case "testimonials":
		return "testimonials", "", map[string]any{"layout": "grid"}
	case "footer":
		return "social-links", "", map[string]any{"layout": "horizontal", "showLabels": true}
	default:
		return "text-block", "", map[string]any{"content": fmt.Sprintf("<p data-ui=\"section-%s\">Contenido de %s</p>", sectionType, sectionType)}
	}
}

func sanitizeWebsiteBuilderFileName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "asset.bin"
	}
	name = strings.ReplaceAll(name, "\\", "-")
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.ReplaceAll(name, "..", "-")
	name = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '.', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, name)
	return strings.Trim(name, "- ")
}

func websiteBuilderBackendBaseURL(r *http.Request) string {
	scheme := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = strings.TrimSpace(r.Host)
	}
	return scheme + "://" + host
}

func websiteBuilderContentType(filePath string) string {
	if strings.HasSuffix(filePath, ".json") {
		return "application/json; charset=utf-8"
	}
	if strings.HasSuffix(filePath, ".css") {
		return "text/css; charset=utf-8"
	}
	if strings.HasSuffix(filePath, ".js") {
		return "application/javascript; charset=utf-8"
	}
	return "text/html; charset=utf-8"
}
