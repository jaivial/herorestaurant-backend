// Package api provides HTTP handlers for the backend API
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"preactvillacarmen/internal/httpx"
)

// SiteBuilderSites represents a website builder site
type SiteBuilderSites struct {
	ID                 string                  `json:"id"`
	RestaurantID       int                     `json:"restaurant_id"`
	Name               string                  `json:"name"`
	Subdomain          string                  `json:"subdomain"`
	CustomDomain       *string                 `json:"custom_domain"`
	ThemeConfig        *json.RawMessage        `json:"theme_config"`
	Status             string                  `json:"status"`
	PublishedVersionID *string                 `json:"published_version_id"`
	Settings           *json.RawMessage        `json:"settings"`
	CreatedAt          time.Time               `json:"created_at"`
	UpdatedAt          time.Time               `json:"updated_at"`
}

// SiteBuilderPages represents a page within a site
type SiteBuilderPages struct {
	ID                 string           `json:"id"`
	SiteID             string           `json:"site_id"`
	Slug               string           `json:"slug"`
	Name               string           `json:"name"`
	PageType           string           `json:"page_type"`
	Tree               *json.RawMessage `json:"tree"`
	SEOConfig          *json.RawMessage `json:"seo_config"`
	CollectionBinding  *json.RawMessage `json:"collection_binding"`
	IsHome             bool             `json:"is_home"`
	CreatedAt          time.Time        `json:"created_at"`
	UpdatedAt          time.Time        `json:"updated_at"`
}

// SiteBuilderAssets represents an uploaded asset
type SiteBuilderAssets struct {
	ID               string           `json:"id"`
	SiteID           string           `json:"site_id"`
	Type             string           `json:"type"`
	Filename         string           `json:"filename"`
	OriginalFilename *string          `json:"original_filename"`
	URL              string           `json:"url"`
	ThumbnailURL     *string          `json:"thumbnail_url"`
	MimeType         *string          `json:"mime_type"`
	FileSize         *int64           `json:"file_size"`
	Width            *int             `json:"width"`
	Height           *int             `json:"height"`
	AltText          *string          `json:"alt_text"`
	Metadata         *json.RawMessage `json:"metadata"`
	CreatedAt        time.Time        `json:"created_at"`
}

// SiteBuilderVersions represents a version snapshot
type SiteBuilderVersions struct {
	ID               string           `json:"id"`
	SiteID           string           `json:"site_id"`
	VersionNumber    int              `json:"version_number"`
	PagesSnapshot    *json.RawMessage `json:"pages_snapshot"`
	ThemeSnapshot    *json.RawMessage `json:"theme_snapshot"`
	AssetsSnapshot   *json.RawMessage `json:"assets_snapshot"`
	SettingsSnapshot *json.RawMessage `json:"settings_snapshot"`
	Status           string           `json:"status"`
	ChangeSummary    *string          `json:"change_summary"`
	StoragePath      *string          `json:"storage_path"`
	PublishedAt      *time.Time       `json:"published_at"`
	CreatedAt        time.Time        `json:"created_at"`
}

// SiteBuilderComponentRegistry represents a component definition
type SiteBuilderComponentRegistry struct {
	ID            int              `json:"id"`
	Type          string           `json:"type"`
	Category      string           `json:"category"`
	Label         string           `json:"label"`
	Description   *string          `json:"description"`
	PropsSchema   *json.RawMessage `json:"props_schema"`
	StyleSchema   *json.RawMessage `json:"style_schema"`
	BindingsSchema *json.RawMessage `json:"bindings_schema"`
	NestingRules  *json.RawMessage `json:"nesting_rules"`
	Icon          *string          `json:"icon"`
	ThumbnailURL  *string          `json:"thumbnail_url"`
	IsActive      bool             `json:"is_active"`
	SortOrder     int              `json:"sort_order"`
}

// SiteBuilderBindings represents a data binding
type SiteBuilderBindings struct {
	ID           string           `json:"id"`
	SiteID       string           `json:"site_id"`
	PageID       *string          `json:"page_id"`
	NodeID       string           `json:"node_id"`
	ResourceType string           `json:"resource_type"`
	QueryConfig  *json.RawMessage `json:"query_config"`
	RefreshMode  string           `json:"refresh_mode"`
	CacheTTL     *int             `json:"cache_ttl"`
	CreatedAt    time.Time        `json:"created_at"`
}

// SiteBuilderPublishQueue represents a publish job
type SiteBuilderPublishQueue struct {
	ID           string      `json:"id"`
	SiteID       string      `json:"site_id"`
	VersionID    string      `json:"version_id"`
	Action       string      `json:"action"`
	Status       string      `json:"status"`
	Progress     int         `json:"progress"`
	TotalSteps   int         `json:"total_steps"`
	ErrorMessage *string     `json:"error_message"`
	StartedAt    *time.Time  `json:"started_at"`
	CompletedAt  *time.Time  `json:"completed_at"`
	CreatedAt    time.Time   `json:"created_at"`
}

// SiteBuilderDomainMappings represents a custom domain
type SiteBuilderDomainMappings struct {
	ID                string      `json:"id"`
	SiteID            string      `json:"site_id"`
	Domain            string      `json:"domain"`
	IsPrimary         bool        `json:"is_primary"`
	VerificationToken *string     `json:"verification_token"`
	VerificationMethod *string    `json:"verification_method"`
	Status            string      `json:"status"`
	SSLStatus         string      `json:"ssl_status"`
	ErrorMessage      *string     `json:"error_message"`
	VerifiedAt        *time.Time  `json:"verified_at"`
	CreatedAt         time.Time   `json:"created_at"`
	UpdatedAt         time.Time   `json:"updated_at"`
}

// RegisterSiteBuilderRoutes registers all site builder API routes
func RegisterSiteBuilderRoutes(r chi.Router, db *sql.DB) {
	// Sites CRUD
	r.Get("/site-builder/sites", handleListSites(db))
	r.Post("/site-builder/sites", handleCreateSite(db))
	r.Get("/site-builder/sites/{siteId}", handleGetSite(db))
	r.Put("/site-builder/sites/{siteId}", handleUpdateSite(db))
	r.Delete("/site-builder/sites/{siteId}", handleDeleteSite(db))

	// Pages CRUD
	r.Get("/site-builder/sites/{siteId}/pages", handleListPages(db))
	r.Post("/site-builder/sites/{siteId}/pages", handleCreatePage(db))
	r.Get("/site-builder/pages/{pageId}", handleGetPage(db))
	r.Put("/site-builder/pages/{pageId}", handleUpdatePage(db))
	r.Delete("/site-builder/pages/{pageId}", handleDeletePage(db))

	// Assets CRUD
	r.Get("/site-builder/sites/{siteId}/assets", handleListAssets(db))
	r.Post("/site-builder/sites/{siteId}/assets", handleUploadAsset(db))
	r.Get("/site-builder/assets/{assetId}", handleGetAsset(db))
	r.Delete("/site-builder/assets/{assetId}", handleDeleteAsset(db))

	// Versions
	r.Get("/site-builder/sites/{siteId}/versions", handleListVersions(db))
	r.Post("/site-builder/sites/{siteId}/versions", handleCreateVersion(db))
	r.Get("/site-builder/versions/{versionId}", handleGetVersion(db))

	// Component Registry (read-only)
	r.Get("/site-builder/components", handleListComponents(db))
	r.Get("/site-builder/components/{type}", handleGetComponentByType(db))

	// Bindings
	r.Get("/site-builder/sites/{siteId}/bindings", handleListBindings(db))
	r.Post("/site-builder/sites/{siteId}/bindings", handleCreateBinding(db))
	r.Put("/site-builder/bindings/{bindingId}", handleUpdateBinding(db))
	r.Delete("/site-builder/bindings/{bindingId}", handleDeleteBinding(db))

	// Publish
	r.Post("/site-builder/sites/{siteId}/publish", handlePublishSite(db))
	r.Get("/site-builder/sites/{siteId}/publish-status", handleGetPublishStatus(db))

	// Domains
	r.Get("/site-builder/sites/{siteId}/domains", handleListDomains(db))
	r.Post("/site-builder/sites/{siteId}/domains", handleCreateDomain(db))
	r.Delete("/site-builder/domains/{domainId}", handleDeleteDomain(db))
	r.Post("/site-builder/domains/{domainId}/verify", handleVerifyDomain(db))
}

// ============================================================================
// SITES
// ============================================================================

func handleListSites(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		auth, ok := boAuthFromContext(ctx)
		if !ok {
			httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		restaurantID := auth.ActiveRestaurantID

		rows, err := db.QueryContext(ctx, `
			SELECT id, restaurant_id, name, subdomain, custom_domain, theme_config, status, 
				   published_version_id, settings, created_at, updated_at
			FROM site_builder_sites
			WHERE restaurant_id = ?
			ORDER BY updated_at DESC
		`, restaurantID)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to list sites")
			return
		}
		defer rows.Close()

		sites := []SiteBuilderSites{}
		for rows.Next() {
			var s SiteBuilderSites
			err := rows.Scan(
				&s.ID, &s.RestaurantID, &s.Name, &s.Subdomain, &s.CustomDomain,
				&s.ThemeConfig, &s.Status, &s.PublishedVersionID, &s.Settings,
				&s.CreatedAt, &s.UpdatedAt,
			)
			if err != nil {
				httpx.WriteError(w, http.StatusInternalServerError, "Failed to scan site")
				return
			}
			sites = append(sites, s)
		}

		httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"sites":   sites,
		})
	}
}

func handleCreateSite(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		auth, ok := boAuthFromContext(ctx)
		if !ok {
			httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		restaurantID := auth.ActiveRestaurantID

		var input struct {
			Name        string           `json:"name"`
			Subdomain   string           `json:"subdomain"`
			ThemeConfig *json.RawMessage `json:"theme_config"`
			Settings    *json.RawMessage `json:"settings"`
		}

		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Invalid JSON")
			return
		}

		if input.Name == "" || input.Subdomain == "" {
			httpx.WriteError(w, http.StatusBadRequest, "Name and subdomain are required")
			return
		}

		id := uuid.New().String()
		now := time.Now()

		_, err := db.ExecContext(ctx, `
			INSERT INTO site_builder_sites (id, restaurant_id, name, subdomain, theme_config, settings, status, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, 'draft', ?, ?)
		`, id, restaurantID, input.Name, input.Subdomain, input.ThemeConfig, input.Settings, now, now)

		if err != nil {
			if strings.Contains(err.Error(), "uk_subdomain") {
				httpx.WriteError(w, http.StatusConflict, "Subdomain already in use")
				return
			}
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to create site")
			return
		}

		// Create default home page
		homePageID := uuid.New().String()
		defaultTree := json.RawMessage(`{"id":"page_root","type":"page","children":[]}`)
		_, err = db.ExecContext(ctx, `
			INSERT INTO site_builder_pages (id, site_id, slug, name, page_type, tree, is_home, created_at, updated_at)
			VALUES (?, ?, '/', 'Home', 'static', ?, 1, ?, ?)
		`, homePageID, id, defaultTree, now, now)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to create default page")
			return
		}

		httpx.WriteJSON(w, http.StatusCreated, map[string]interface{}{
			"success": true,
			"id":      id,
		})
	}
}

func handleGetSite(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		siteID := chi.URLParam(r, "siteId")

		site, err := getSiteByID(ctx, db, siteID)
		if err != nil {
			if err == sql.ErrNoRows {
				httpx.WriteError(w, http.StatusNotFound, "Site not found")
				return
			}
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to get site")
			return
		}

		// Verify access
		if !canAccessSite(ctx, db, site.RestaurantID) {
			httpx.WriteError(w, http.StatusForbidden, "Access denied")
			return
		}

		httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"site":    site,
		})
	}
}

func handleUpdateSite(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		siteID := chi.URLParam(r, "siteId")

		site, err := getSiteByID(ctx, db, siteID)
		if err != nil {
			if err == sql.ErrNoRows {
				httpx.WriteError(w, http.StatusNotFound, "Site not found")
				return
			}
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to get site")
			return
		}

		if !canAccessSite(ctx, db, site.RestaurantID) {
			httpx.WriteError(w, http.StatusForbidden, "Access denied")
			return
		}

		var input struct {
			Name         *string          `json:"name"`
			Subdomain    *string          `json:"subdomain"`
			CustomDomain *string          `json:"custom_domain"`
			ThemeConfig  *json.RawMessage `json:"theme_config"`
			Settings     *json.RawMessage `json:"settings"`
		}

		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Invalid JSON")
			return
		}

		// Build dynamic update query
		updates := []string{}
		args := []interface{}{}

		if input.Name != nil {
			updates = append(updates, "name = ?")
			args = append(args, *input.Name)
		}
		if input.Subdomain != nil {
			updates = append(updates, "subdomain = ?")
			args = append(args, *input.Subdomain)
		}
		if input.CustomDomain != nil {
			updates = append(updates, "custom_domain = ?")
			args = append(args, *input.CustomDomain)
		}
		if input.ThemeConfig != nil {
			updates = append(updates, "theme_config = ?")
			args = append(args, input.ThemeConfig)
		}
		if input.Settings != nil {
			updates = append(updates, "settings = ?")
			args = append(args, input.Settings)
		}

		if len(updates) == 0 {
			httpx.WriteError(w, http.StatusBadRequest, "No fields to update")
			return
		}

		updates = append(updates, "updated_at = ?")
		args = append(args, time.Now())
		args = append(args, siteID)

		query := "UPDATE site_builder_sites SET " + strings.Join(updates, ", ") + " WHERE id = ?"
		_, err = db.ExecContext(ctx, query, args...)
		if err != nil {
			if strings.Contains(err.Error(), "uk_subdomain") {
				httpx.WriteError(w, http.StatusConflict, "Subdomain already in use")
				return
			}
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to update site")
			return
		}

		httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
		})
	}
}

func handleDeleteSite(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		siteID := chi.URLParam(r, "siteId")

		site, err := getSiteByID(ctx, db, siteID)
		if err != nil {
			if err == sql.ErrNoRows {
				httpx.WriteError(w, http.StatusNotFound, "Site not found")
				return
			}
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to get site")
			return
		}

		if !canAccessSite(ctx, db, site.RestaurantID) {
			httpx.WriteError(w, http.StatusForbidden, "Access denied")
			return
		}

		_, err = db.ExecContext(ctx, "DELETE FROM site_builder_sites WHERE id = ?", siteID)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to delete site")
			return
		}

		httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
		})
	}
}

// ============================================================================
// PAGES
// ============================================================================

func handleListPages(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		siteID := chi.URLParam(r, "siteId")

		if !canAccessSiteBySiteID(ctx, db, siteID) {
			httpx.WriteError(w, http.StatusForbidden, "Access denied")
			return
		}

		rows, err := db.QueryContext(ctx, `
			SELECT id, site_id, slug, name, page_type, tree, seo_config, collection_binding, is_home, created_at, updated_at
			FROM site_builder_pages
			WHERE site_id = ?
			ORDER BY is_home DESC, name ASC
		`, siteID)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to list pages")
			return
		}
		defer rows.Close()

		pages := []SiteBuilderPages{}
		for rows.Next() {
			var p SiteBuilderPages
			err := rows.Scan(
				&p.ID, &p.SiteID, &p.Slug, &p.Name, &p.PageType, &p.Tree,
				&p.SEOConfig, &p.CollectionBinding, &p.IsHome, &p.CreatedAt, &p.UpdatedAt,
			)
			if err != nil {
				httpx.WriteError(w, http.StatusInternalServerError, "Failed to scan page")
				return
			}
			pages = append(pages, p)
		}

		httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"pages":   pages,
		})
	}
}

func handleCreatePage(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		siteID := chi.URLParam(r, "siteId")

		if !canAccessSiteBySiteID(ctx, db, siteID) {
			httpx.WriteError(w, http.StatusForbidden, "Access denied")
			return
		}

		var input struct {
			Slug              string           `json:"slug"`
			Name              string           `json:"name"`
			PageType          string           `json:"page_type"`
			Tree              *json.RawMessage `json:"tree"`
			SEOConfig         *json.RawMessage `json:"seo_config"`
			CollectionBinding *json.RawMessage `json:"collection_binding"`
			IsHome            bool             `json:"is_home"`
		}

		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Invalid JSON")
			return
		}

		if input.Slug == "" || input.Name == "" {
			httpx.WriteError(w, http.StatusBadRequest, "Slug and name are required")
			return
		}

		if input.PageType == "" {
			input.PageType = "static"
		}

		if input.Tree == nil {
			defaultTree := json.RawMessage(`{"id":"page_root","type":"page","children":[]}`)
			input.Tree = &defaultTree
		}

		id := uuid.New().String()
		now := time.Now()

		_, err := db.ExecContext(ctx, `
			INSERT INTO site_builder_pages (id, site_id, slug, name, page_type, tree, seo_config, collection_binding, is_home, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, id, siteID, input.Slug, input.Name, input.PageType, input.Tree, input.SEOConfig, input.CollectionBinding, input.IsHome, now, now)

		if err != nil {
			if strings.Contains(err.Error(), "uk_site_slug") {
				httpx.WriteError(w, http.StatusConflict, "Page slug already exists")
				return
			}
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to create page")
			return
		}

		httpx.WriteJSON(w, http.StatusCreated, map[string]interface{}{
			"success": true,
			"id":      id,
		})
	}
}

func handleGetPage(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		pageID := chi.URLParam(r, "pageId")

		var p SiteBuilderPages
		var siteID string
		err := db.QueryRowContext(ctx, `
			SELECT id, site_id, slug, name, page_type, tree, seo_config, collection_binding, is_home, created_at, updated_at
			FROM site_builder_pages
			WHERE id = ?
		`, pageID).Scan(
			&p.ID, &siteID, &p.Slug, &p.Name, &p.PageType, &p.Tree,
			&p.SEOConfig, &p.CollectionBinding, &p.IsHome, &p.CreatedAt, &p.UpdatedAt,
		)

		if err == sql.ErrNoRows {
			httpx.WriteError(w, http.StatusNotFound, "Page not found")
			return
		}
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to get page")
			return
		}

		if !canAccessSiteBySiteID(ctx, db, siteID) {
			httpx.WriteError(w, http.StatusForbidden, "Access denied")
			return
		}

		httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"page":    p,
		})
	}
}

func handleUpdatePage(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		pageID := chi.URLParam(r, "pageId")

		var siteID string
		err := db.QueryRowContext(ctx, "SELECT site_id FROM site_builder_pages WHERE id = ?", pageID).Scan(&siteID)
		if err == sql.ErrNoRows {
			httpx.WriteError(w, http.StatusNotFound, "Page not found")
			return
		}
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to get page")
			return
		}

		if !canAccessSiteBySiteID(ctx, db, siteID) {
			httpx.WriteError(w, http.StatusForbidden, "Access denied")
			return
		}

		var input struct {
			Slug              *string          `json:"slug"`
			Name              *string          `json:"name"`
			PageType          *string          `json:"page_type"`
			Tree              *json.RawMessage `json:"tree"`
			SEOConfig         *json.RawMessage `json:"seo_config"`
			CollectionBinding *json.RawMessage `json:"collection_binding"`
			IsHome            *bool            `json:"is_home"`
		}

		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Invalid JSON")
			return
		}

		updates := []string{}
		args := []interface{}{}

		if input.Slug != nil {
			updates = append(updates, "slug = ?")
			args = append(args, *input.Slug)
		}
		if input.Name != nil {
			updates = append(updates, "name = ?")
			args = append(args, *input.Name)
		}
		if input.PageType != nil {
			updates = append(updates, "page_type = ?")
			args = append(args, *input.PageType)
		}
		if input.Tree != nil {
			updates = append(updates, "tree = ?")
			args = append(args, input.Tree)
		}
		if input.SEOConfig != nil {
			updates = append(updates, "seo_config = ?")
			args = append(args, input.SEOConfig)
		}
		if input.CollectionBinding != nil {
			updates = append(updates, "collection_binding = ?")
			args = append(args, input.CollectionBinding)
		}
		if input.IsHome != nil {
			updates = append(updates, "is_home = ?")
			args = append(args, *input.IsHome)
		}

		if len(updates) == 0 {
			httpx.WriteError(w, http.StatusBadRequest, "No fields to update")
			return
		}

		updates = append(updates, "updated_at = ?")
		args = append(args, time.Now())
		args = append(args, pageID)

		query := "UPDATE site_builder_pages SET " + strings.Join(updates, ", ") + " WHERE id = ?"
		_, err = db.ExecContext(ctx, query, args...)
		if err != nil {
			if strings.Contains(err.Error(), "uk_site_slug") {
				httpx.WriteError(w, http.StatusConflict, "Page slug already exists")
				return
			}
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to update page")
			return
		}

		httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
		})
	}
}

func handleDeletePage(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		pageID := chi.URLParam(r, "pageId")

		var siteID string
		var isHome bool
		err := db.QueryRowContext(ctx, "SELECT site_id, is_home FROM site_builder_pages WHERE id = ?", pageID).Scan(&siteID, &isHome)
		if err == sql.ErrNoRows {
			httpx.WriteError(w, http.StatusNotFound, "Page not found")
			return
		}
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to get page")
			return
		}

		if !canAccessSiteBySiteID(ctx, db, siteID) {
			httpx.WriteError(w, http.StatusForbidden, "Access denied")
			return
		}

		if isHome {
			httpx.WriteError(w, http.StatusBadRequest, "Cannot delete home page")
			return
		}

		_, err = db.ExecContext(ctx, "DELETE FROM site_builder_pages WHERE id = ?", pageID)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to delete page")
			return
		}

		httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
		})
	}
}

// ============================================================================
// ASSETS
// ============================================================================

func handleListAssets(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		siteID := chi.URLParam(r, "siteId")

		if !canAccessSiteBySiteID(ctx, db, siteID) {
			httpx.WriteError(w, http.StatusForbidden, "Access denied")
			return
		}

		assetType := r.URL.Query().Get("type")

		query := `
			SELECT id, site_id, type, filename, original_filename, url, thumbnail_url,
				   mime_type, file_size, width, height, alt_text, metadata, created_at
			FROM site_builder_assets
			WHERE site_id = ?
		`
		args := []interface{}{siteID}

		if assetType != "" {
			query += " AND type = ?"
			args = append(args, assetType)
		}

		query += " ORDER BY created_at DESC"

		rows, err := db.QueryContext(ctx, query, args...)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to list assets")
			return
		}
		defer rows.Close()

		assets := []SiteBuilderAssets{}
		for rows.Next() {
			var a SiteBuilderAssets
			err := rows.Scan(
				&a.ID, &a.SiteID, &a.Type, &a.Filename, &a.OriginalFilename,
				&a.URL, &a.ThumbnailURL, &a.MimeType, &a.FileSize, &a.Width,
				&a.Height, &a.AltText, &a.Metadata, &a.CreatedAt,
			)
			if err != nil {
				httpx.WriteError(w, http.StatusInternalServerError, "Failed to scan asset")
				return
			}
			assets = append(assets, a)
		}

		httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"assets":  assets,
		})
	}
}

func handleUploadAsset(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		siteID := chi.URLParam(r, "siteId")

		if !canAccessSiteBySiteID(ctx, db, siteID) {
			httpx.WriteError(w, http.StatusForbidden, "Access denied")
			return
		}

		// TODO: Implement file upload to storage (BunnyCDN or local)
		// For now, accept URL-based assets
		var input struct {
			Type             string           `json:"type"`
			Filename         string           `json:"filename"`
			OriginalFilename *string          `json:"original_filename"`
			URL              string           `json:"url"`
			ThumbnailURL     *string          `json:"thumbnail_url"`
			MimeType         *string          `json:"mime_type"`
			FileSize         *int64           `json:"file_size"`
			Width            *int             `json:"width"`
			Height           *int             `json:"height"`
			AltText          *string          `json:"alt_text"`
			Metadata         *json.RawMessage `json:"metadata"`
		}

		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Invalid JSON")
			return
		}

		if input.URL == "" {
			httpx.WriteError(w, http.StatusBadRequest, "URL is required")
			return
		}

		if input.Type == "" {
			input.Type = "image"
		}

		id := uuid.New().String()
		now := time.Now()

		_, err := db.ExecContext(ctx, `
			INSERT INTO site_builder_assets (id, site_id, type, filename, original_filename, url, thumbnail_url, mime_type, file_size, width, height, alt_text, metadata, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, id, siteID, input.Type, input.Filename, input.OriginalFilename, input.URL, input.ThumbnailURL, input.MimeType, input.FileSize, input.Width, input.Height, input.AltText, input.Metadata, now)

		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to create asset")
			return
		}

		httpx.WriteJSON(w, http.StatusCreated, map[string]interface{}{
			"success": true,
			"id":      id,
		})
	}
}

func handleGetAsset(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		assetID := chi.URLParam(r, "assetId")

		var a SiteBuilderAssets
		var siteID string
		err := db.QueryRowContext(ctx, `
			SELECT id, site_id, type, filename, original_filename, url, thumbnail_url,
				   mime_type, file_size, width, height, alt_text, metadata, created_at
			FROM site_builder_assets
			WHERE id = ?
		`, assetID).Scan(
			&a.ID, &siteID, &a.Type, &a.Filename, &a.OriginalFilename,
			&a.URL, &a.ThumbnailURL, &a.MimeType, &a.FileSize, &a.Width,
			&a.Height, &a.AltText, &a.Metadata, &a.CreatedAt,
		)

		if err == sql.ErrNoRows {
			httpx.WriteError(w, http.StatusNotFound, "Asset not found")
			return
		}
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to get asset")
			return
		}

		if !canAccessSiteBySiteID(ctx, db, siteID) {
			httpx.WriteError(w, http.StatusForbidden, "Access denied")
			return
		}

		httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"asset":   a,
		})
	}
}

func handleDeleteAsset(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		assetID := chi.URLParam(r, "assetId")

		var siteID string
		err := db.QueryRowContext(ctx, "SELECT site_id FROM site_builder_assets WHERE id = ?", assetID).Scan(&siteID)
		if err == sql.ErrNoRows {
			httpx.WriteError(w, http.StatusNotFound, "Asset not found")
			return
		}
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to get asset")
			return
		}

		if !canAccessSiteBySiteID(ctx, db, siteID) {
			httpx.WriteError(w, http.StatusForbidden, "Access denied")
			return
		}

		_, err = db.ExecContext(ctx, "DELETE FROM site_builder_assets WHERE id = ?", assetID)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to delete asset")
			return
		}

		httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
		})
	}
}

// ============================================================================
// VERSIONS
// ============================================================================

func handleListVersions(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		siteID := chi.URLParam(r, "siteId")

		if !canAccessSiteBySiteID(ctx, db, siteID) {
			httpx.WriteError(w, http.StatusForbidden, "Access denied")
			return
		}

		rows, err := db.QueryContext(ctx, `
			SELECT id, site_id, version_number, pages_snapshot, theme_snapshot, assets_snapshot,
				   settings_snapshot, status, change_summary, storage_path, published_at, created_at
			FROM site_builder_versions
			WHERE site_id = ?
			ORDER BY version_number DESC
			LIMIT 50
		`, siteID)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to list versions")
			return
		}
		defer rows.Close()

		versions := []SiteBuilderVersions{}
		for rows.Next() {
			var v SiteBuilderVersions
			err := rows.Scan(
				&v.ID, &v.SiteID, &v.VersionNumber, &v.PagesSnapshot, &v.ThemeSnapshot,
				&v.AssetsSnapshot, &v.SettingsSnapshot, &v.Status, &v.ChangeSummary,
				&v.StoragePath, &v.PublishedAt, &v.CreatedAt,
			)
			if err != nil {
				httpx.WriteError(w, http.StatusInternalServerError, "Failed to scan version")
				return
			}
			versions = append(versions, v)
		}

		httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"success":  true,
			"versions": versions,
		})
	}
}

func handleCreateVersion(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		siteID := chi.URLParam(r, "siteId")

		if !canAccessSiteBySiteID(ctx, db, siteID) {
			httpx.WriteError(w, http.StatusForbidden, "Access denied")
			return
		}

		var input struct {
			ChangeSummary *string `json:"change_summary"`
		}
		json.NewDecoder(r.Body).Decode(&input)

		// Get next version number
		var maxVersion int
		err := db.QueryRowContext(ctx, "SELECT COALESCE(MAX(version_number), 0) FROM site_builder_versions WHERE site_id = ?", siteID).Scan(&maxVersion)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to get version number")
			return
		}

		// Gather snapshots
		var themeConfig, settings *json.RawMessage
		err = db.QueryRowContext(ctx, "SELECT theme_config, settings FROM site_builder_sites WHERE id = ?", siteID).Scan(&themeConfig, &settings)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to get site data")
			return
		}

		// Get pages
		rows, err := db.QueryContext(ctx, "SELECT id, site_id, slug, name, page_type, tree, seo_config, collection_binding, is_home, created_at, updated_at FROM site_builder_pages WHERE site_id = ?", siteID)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to get pages")
			return
		}
		defer rows.Close()

		pages := []SiteBuilderPages{}
		for rows.Next() {
			var p SiteBuilderPages
			rows.Scan(&p.ID, &p.SiteID, &p.Slug, &p.Name, &p.PageType, &p.Tree, &p.SEOConfig, &p.CollectionBinding, &p.IsHome, &p.CreatedAt, &p.UpdatedAt)
			pages = append(pages, p)
		}
		pagesJSON, _ := json.Marshal(pages)
		pagesSnapshot := json.RawMessage(pagesJSON)

		// Get assets
		assetRows, err := db.QueryContext(ctx, "SELECT id, site_id, type, filename, original_filename, url, thumbnail_url, mime_type, file_size, width, height, alt_text, metadata, created_at FROM site_builder_assets WHERE site_id = ?", siteID)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to get assets")
			return
		}
		defer assetRows.Close()

		assets := []SiteBuilderAssets{}
		for assetRows.Next() {
			var a SiteBuilderAssets
			assetRows.Scan(&a.ID, &a.SiteID, &a.Type, &a.Filename, &a.OriginalFilename, &a.URL, &a.ThumbnailURL, &a.MimeType, &a.FileSize, &a.Width, &a.Height, &a.AltText, &a.Metadata, &a.CreatedAt)
			assets = append(assets, a)
		}
		assetsJSON, _ := json.Marshal(assets)
		assetsSnapshot := json.RawMessage(assetsJSON)

		id := uuid.New().String()
		now := time.Now()

		_, err = db.ExecContext(ctx, `
			INSERT INTO site_builder_versions (id, site_id, version_number, pages_snapshot, theme_snapshot, assets_snapshot, settings_snapshot, status, change_summary, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, 'draft', ?, ?)
		`, id, siteID, maxVersion+1, pagesSnapshot, themeConfig, assetsSnapshot, settings, input.ChangeSummary, now)

		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to create version")
			return
		}

		httpx.WriteJSON(w, http.StatusCreated, map[string]interface{}{
			"success":       true,
			"id":            id,
			"version_number": maxVersion + 1,
		})
	}
}

func handleGetVersion(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		versionID := chi.URLParam(r, "versionId")

		var v SiteBuilderVersions
		var siteID string
		err := db.QueryRowContext(ctx, `
			SELECT id, site_id, version_number, pages_snapshot, theme_snapshot, assets_snapshot,
				   settings_snapshot, status, change_summary, storage_path, published_at, created_at
			FROM site_builder_versions
			WHERE id = ?
		`, versionID).Scan(
			&v.ID, &siteID, &v.VersionNumber, &v.PagesSnapshot, &v.ThemeSnapshot,
			&v.AssetsSnapshot, &v.SettingsSnapshot, &v.Status, &v.ChangeSummary,
			&v.StoragePath, &v.PublishedAt, &v.CreatedAt,
		)

		if err == sql.ErrNoRows {
			httpx.WriteError(w, http.StatusNotFound, "Version not found")
			return
		}
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to get version")
			return
		}

		if !canAccessSiteBySiteID(ctx, db, siteID) {
			httpx.WriteError(w, http.StatusForbidden, "Access denied")
			return
		}

		httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"version": v,
		})
	}
}

// ============================================================================
// COMPONENT REGISTRY
// ============================================================================

func handleListComponents(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		category := r.URL.Query().Get("category")

		query := `
			SELECT id, type, category, label, description, props_schema, style_schema, bindings_schema, nesting_rules, icon, thumbnail_url, is_active, sort_order
			FROM site_builder_component_registry
			WHERE is_active = 1
		`
		args := []interface{}{}

		if category != "" {
			query += " AND category = ?"
			args = append(args, category)
		}

		query += " ORDER BY sort_order ASC, label ASC"

		rows, err := db.QueryContext(ctx, query, args...)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to list components")
			return
		}
		defer rows.Close()

		components := []SiteBuilderComponentRegistry{}
		for rows.Next() {
			var c SiteBuilderComponentRegistry
			err := rows.Scan(
				&c.ID, &c.Type, &c.Category, &c.Label, &c.Description,
				&c.PropsSchema, &c.StyleSchema, &c.BindingsSchema, &c.NestingRules,
				&c.Icon, &c.ThumbnailURL, &c.IsActive, &c.SortOrder,
			)
			if err != nil {
				httpx.WriteError(w, http.StatusInternalServerError, "Failed to scan component")
				return
			}
			components = append(components, c)
		}

		httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"success":    true,
			"components": components,
		})
	}
}

func handleGetComponentByType(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		componentType := chi.URLParam(r, "type")

		var c SiteBuilderComponentRegistry
		err := db.QueryRowContext(ctx, `
			SELECT id, type, category, label, description, props_schema, style_schema, bindings_schema, nesting_rules, icon, thumbnail_url, is_active, sort_order
			FROM site_builder_component_registry
			WHERE type = ? AND is_active = 1
		`, componentType).Scan(
			&c.ID, &c.Type, &c.Category, &c.Label, &c.Description,
			&c.PropsSchema, &c.StyleSchema, &c.BindingsSchema, &c.NestingRules,
			&c.Icon, &c.ThumbnailURL, &c.IsActive, &c.SortOrder,
		)

		if err == sql.ErrNoRows {
			httpx.WriteError(w, http.StatusNotFound, "Component not found")
			return
		}
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to get component")
			return
		}

		httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"success":   true,
			"component": c,
		})
	}
}

// ============================================================================
// BINDINGS
// ============================================================================

func handleListBindings(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		siteID := chi.URLParam(r, "siteId")

		if !canAccessSiteBySiteID(ctx, db, siteID) {
			httpx.WriteError(w, http.StatusForbidden, "Access denied")
			return
		}

		rows, err := db.QueryContext(ctx, `
			SELECT id, site_id, page_id, node_id, resource_type, query_config, refresh_mode, cache_ttl, created_at
			FROM site_builder_bindings
			WHERE site_id = ?
			ORDER BY created_at DESC
		`, siteID)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to list bindings")
			return
		}
		defer rows.Close()

		bindings := []SiteBuilderBindings{}
		for rows.Next() {
			var b SiteBuilderBindings
			err := rows.Scan(
				&b.ID, &b.SiteID, &b.PageID, &b.NodeID, &b.ResourceType,
				&b.QueryConfig, &b.RefreshMode, &b.CacheTTL, &b.CreatedAt,
			)
			if err != nil {
				httpx.WriteError(w, http.StatusInternalServerError, "Failed to scan binding")
				return
			}
			bindings = append(bindings, b)
		}

		httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"success":  true,
			"bindings": bindings,
		})
	}
}

func handleCreateBinding(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		siteID := chi.URLParam(r, "siteId")

		if !canAccessSiteBySiteID(ctx, db, siteID) {
			httpx.WriteError(w, http.StatusForbidden, "Access denied")
			return
		}

		var input struct {
			PageID       *string          `json:"page_id"`
			NodeID       string           `json:"node_id"`
			ResourceType string           `json:"resource_type"`
			QueryConfig  *json.RawMessage `json:"query_config"`
			RefreshMode  string           `json:"refresh_mode"`
			CacheTTL     *int             `json:"cache_ttl"`
		}

		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Invalid JSON")
			return
		}

		if input.NodeID == "" || input.ResourceType == "" {
			httpx.WriteError(w, http.StatusBadRequest, "Node ID and resource type are required")
			return
		}

		if input.RefreshMode == "" {
			input.RefreshMode = "publish"
		}

		id := uuid.New().String()
		now := time.Now()

		_, err := db.ExecContext(ctx, `
			INSERT INTO site_builder_bindings (id, site_id, page_id, node_id, resource_type, query_config, refresh_mode, cache_ttl, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, id, siteID, input.PageID, input.NodeID, input.ResourceType, input.QueryConfig, input.RefreshMode, input.CacheTTL, now)

		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to create binding")
			return
		}

		httpx.WriteJSON(w, http.StatusCreated, map[string]interface{}{
			"success": true,
			"id":      id,
		})
	}
}

func handleUpdateBinding(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		bindingID := chi.URLParam(r, "bindingId")

		var siteID string
		err := db.QueryRowContext(ctx, "SELECT site_id FROM site_builder_bindings WHERE id = ?", bindingID).Scan(&siteID)
		if err == sql.ErrNoRows {
			httpx.WriteError(w, http.StatusNotFound, "Binding not found")
			return
		}
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to get binding")
			return
		}

		if !canAccessSiteBySiteID(ctx, db, siteID) {
			httpx.WriteError(w, http.StatusForbidden, "Access denied")
			return
		}

		var input struct {
			QueryConfig  *json.RawMessage `json:"query_config"`
			RefreshMode  *string          `json:"refresh_mode"`
			CacheTTL     *int             `json:"cache_ttl"`
		}

		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Invalid JSON")
			return
		}

		updates := []string{}
		args := []interface{}{}

		if input.QueryConfig != nil {
			updates = append(updates, "query_config = ?")
			args = append(args, input.QueryConfig)
		}
		if input.RefreshMode != nil {
			updates = append(updates, "refresh_mode = ?")
			args = append(args, *input.RefreshMode)
		}
		if input.CacheTTL != nil {
			updates = append(updates, "cache_ttl = ?")
			args = append(args, *input.CacheTTL)
		}

		if len(updates) == 0 {
			httpx.WriteError(w, http.StatusBadRequest, "No fields to update")
			return
		}

		args = append(args, bindingID)
		query := "UPDATE site_builder_bindings SET " + strings.Join(updates, ", ") + " WHERE id = ?"
		_, err = db.ExecContext(ctx, query, args...)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to update binding")
			return
		}

		httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
		})
	}
}

func handleDeleteBinding(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		bindingID := chi.URLParam(r, "bindingId")

		var siteID string
		err := db.QueryRowContext(ctx, "SELECT site_id FROM site_builder_bindings WHERE id = ?", bindingID).Scan(&siteID)
		if err == sql.ErrNoRows {
			httpx.WriteError(w, http.StatusNotFound, "Binding not found")
			return
		}
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to get binding")
			return
		}

		if !canAccessSiteBySiteID(ctx, db, siteID) {
			httpx.WriteError(w, http.StatusForbidden, "Access denied")
			return
		}

		_, err = db.ExecContext(ctx, "DELETE FROM site_builder_bindings WHERE id = ?", bindingID)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to delete binding")
			return
		}

		httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
		})
	}
}

// ============================================================================
// PUBLISH
// ============================================================================

func handlePublishSite(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		siteID := chi.URLParam(r, "siteId")

		if !canAccessSiteBySiteID(ctx, db, siteID) {
			httpx.WriteError(w, http.StatusForbidden, "Access denied")
			return
		}

		// Create a new version snapshot
		var maxVersion int
		err := db.QueryRowContext(ctx, "SELECT COALESCE(MAX(version_number), 0) FROM site_builder_versions WHERE site_id = ?", siteID).Scan(&maxVersion)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to get version number")
			return
		}

		// Gather snapshots
		var themeConfig, settings *json.RawMessage
		err = db.QueryRowContext(ctx, "SELECT theme_config, settings FROM site_builder_sites WHERE id = ?", siteID).Scan(&themeConfig, &settings)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to get site data")
			return
		}

		// Get pages
		rows, err := db.QueryContext(ctx, "SELECT id, site_id, slug, name, page_type, tree, seo_config, collection_binding, is_home, created_at, updated_at FROM site_builder_pages WHERE site_id = ?", siteID)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to get pages")
			return
		}
		defer rows.Close()

		pages := []SiteBuilderPages{}
		for rows.Next() {
			var p SiteBuilderPages
			rows.Scan(&p.ID, &p.SiteID, &p.Slug, &p.Name, &p.PageType, &p.Tree, &p.SEOConfig, &p.CollectionBinding, &p.IsHome, &p.CreatedAt, &p.UpdatedAt)
			pages = append(pages, p)
		}
		pagesJSON, _ := json.Marshal(pages)
		pagesSnapshot := json.RawMessage(pagesJSON)

		// Get assets
		assetRows, _ := db.QueryContext(ctx, "SELECT id, site_id, type, filename, original_filename, url, thumbnail_url, mime_type, file_size, width, height, alt_text, metadata, created_at FROM site_builder_assets WHERE site_id = ?", siteID)
		defer assetRows.Close()
		assets := []SiteBuilderAssets{}
		for assetRows.Next() {
			var a SiteBuilderAssets
			assetRows.Scan(&a.ID, &a.SiteID, &a.Type, &a.Filename, &a.OriginalFilename, &a.URL, &a.ThumbnailURL, &a.MimeType, &a.FileSize, &a.Width, &a.Height, &a.AltText, &a.Metadata, &a.CreatedAt)
			assets = append(assets, a)
		}
		assetsJSON, _ := json.Marshal(assets)
		assetsSnapshot := json.RawMessage(assetsJSON)

		versionID := uuid.New().String()
		now := time.Now()

		// Create version as published
		_, err = db.ExecContext(ctx, `
			INSERT INTO site_builder_versions (id, site_id, version_number, pages_snapshot, theme_snapshot, assets_snapshot, settings_snapshot, status, published_at, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, 'published', ?, ?)
		`, versionID, siteID, maxVersion+1, pagesSnapshot, themeConfig, assetsSnapshot, settings, now, now)

		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to create version")
			return
		}

		// Update site with published version
		_, err = db.ExecContext(ctx, `
			UPDATE site_builder_sites SET published_version_id = ?, status = 'published', updated_at = ? WHERE id = ?
		`, versionID, now, siteID)

		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to update site")
			return
		}

		// TODO: Trigger actual publish to BunnyCDN
		// This would be done via a background job/queue

		httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"success":       true,
			"version_id":    versionID,
			"version_number": maxVersion + 1,
			"message":       "Site published successfully",
		})
	}
}

func handleGetPublishStatus(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		siteID := chi.URLParam(r, "siteId")

		if !canAccessSiteBySiteID(ctx, db, siteID) {
			httpx.WriteError(w, http.StatusForbidden, "Access denied")
			return
		}

		// Check for active publish jobs
		var job SiteBuilderPublishQueue
		err := db.QueryRowContext(ctx, `
			SELECT id, site_id, version_id, action, status, progress, total_steps, error_message, started_at, completed_at, created_at
			FROM site_builder_publish_queue
			WHERE site_id = ? AND status IN ('pending', 'processing')
			ORDER BY created_at DESC
			LIMIT 1
		`, siteID).Scan(
			&job.ID, &job.SiteID, &job.VersionID, &job.Action, &job.Status,
			&job.Progress, &job.TotalSteps, &job.ErrorMessage, &job.StartedAt,
			&job.CompletedAt, &job.CreatedAt,
		)

		if err == sql.ErrNoRows {
			httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{
				"success": true,
				"status":  "idle",
			})
			return
		}
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to get publish status")
			return
		}

		httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"status":  job.Status,
			"job":     job,
		})
	}
}

// ============================================================================
// DOMAINS
// ============================================================================

func handleListDomains(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		siteID := chi.URLParam(r, "siteId")

		if !canAccessSiteBySiteID(ctx, db, siteID) {
			httpx.WriteError(w, http.StatusForbidden, "Access denied")
			return
		}

		rows, err := db.QueryContext(ctx, `
			SELECT id, site_id, domain, is_primary, verification_token, verification_method, status, ssl_status, error_message, verified_at, created_at, updated_at
			FROM site_builder_domain_mappings
			WHERE site_id = ?
			ORDER BY is_primary DESC, created_at ASC
		`, siteID)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to list domains")
			return
		}
		defer rows.Close()

		domains := []SiteBuilderDomainMappings{}
		for rows.Next() {
			var d SiteBuilderDomainMappings
			err := rows.Scan(
				&d.ID, &d.SiteID, &d.Domain, &d.IsPrimary, &d.VerificationToken,
				&d.VerificationMethod, &d.Status, &d.SSLStatus, &d.ErrorMessage,
				&d.VerifiedAt, &d.CreatedAt, &d.UpdatedAt,
			)
			if err != nil {
				httpx.WriteError(w, http.StatusInternalServerError, "Failed to scan domain")
				return
			}
			domains = append(domains, d)
		}

		httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"domains": domains,
		})
	}
}

func handleCreateDomain(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		siteID := chi.URLParam(r, "siteId")

		if !canAccessSiteBySiteID(ctx, db, siteID) {
			httpx.WriteError(w, http.StatusForbidden, "Access denied")
			return
		}

		var input struct {
			Domain    string `json:"domain"`
			IsPrimary bool   `json:"is_primary"`
		}

		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Invalid JSON")
			return
		}

		if input.Domain == "" {
			httpx.WriteError(w, http.StatusBadRequest, "Domain is required")
			return
		}

		id := uuid.New().String()
		verificationToken := uuid.New().String()
		now := time.Now()

		_, err := db.ExecContext(ctx, `
			INSERT INTO site_builder_domain_mappings (id, site_id, domain, is_primary, verification_token, verification_method, status, ssl_status, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, 'dns', 'pending', 'none', ?, ?)
		`, id, siteID, input.Domain, input.IsPrimary, verificationToken, now, now)

		if err != nil {
			if strings.Contains(err.Error(), "uk_domain") {
				httpx.WriteError(w, http.StatusConflict, "Domain already in use")
				return
			}
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to create domain")
			return
		}

		httpx.WriteJSON(w, http.StatusCreated, map[string]interface{}{
			"success":           true,
			"id":                id,
			"verification_token": verificationToken,
			"verification_instructions": map[string]string{
				"type":  "TXT",
				"name":  "_verify",
				"value": verificationToken,
			},
		})
	}
}

func handleDeleteDomain(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		domainID := chi.URLParam(r, "domainId")

		var siteID string
		err := db.QueryRowContext(ctx, "SELECT site_id FROM site_builder_domain_mappings WHERE id = ?", domainID).Scan(&siteID)
		if err == sql.ErrNoRows {
			httpx.WriteError(w, http.StatusNotFound, "Domain not found")
			return
		}
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to get domain")
			return
		}

		if !canAccessSiteBySiteID(ctx, db, siteID) {
			httpx.WriteError(w, http.StatusForbidden, "Access denied")
			return
		}

		_, err = db.ExecContext(ctx, "DELETE FROM site_builder_domain_mappings WHERE id = ?", domainID)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to delete domain")
			return
		}

		httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
		})
	}
}

func handleVerifyDomain(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		domainID := chi.URLParam(r, "domainId")

		var d SiteBuilderDomainMappings
		err := db.QueryRowContext(ctx, `
			SELECT id, site_id, domain, is_primary, verification_token, verification_method, status, ssl_status, error_message, verified_at, created_at, updated_at
			FROM site_builder_domain_mappings
			WHERE id = ?
		`, domainID).Scan(
			&d.ID, &d.SiteID, &d.Domain, &d.IsPrimary, &d.VerificationToken,
			&d.VerificationMethod, &d.Status, &d.SSLStatus, &d.ErrorMessage,
			&d.VerifiedAt, &d.CreatedAt, &d.UpdatedAt,
		)

		if err == sql.ErrNoRows {
			httpx.WriteError(w, http.StatusNotFound, "Domain not found")
			return
		}
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to get domain")
			return
		}

		if !canAccessSiteBySiteID(ctx, db, d.SiteID) {
			httpx.WriteError(w, http.StatusForbidden, "Access denied")
			return
		}

		// TODO: Implement actual DNS verification
		// For now, just mark as verified
		now := time.Now()
		_, err = db.ExecContext(ctx, `
			UPDATE site_builder_domain_mappings
			SET status = 'verified', verified_at = ?, updated_at = ?
			WHERE id = ?
		`, now, now, domainID)

		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Failed to verify domain")
			return
		}

		httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"status":  "verified",
		})
	}
}

// ============================================================================
// HELPERS
// ============================================================================

func getSiteByID(ctx context.Context, db *sql.DB, siteID string) (*SiteBuilderSites, error) {
	var s SiteBuilderSites
	err := db.QueryRowContext(ctx, `
		SELECT id, restaurant_id, name, subdomain, custom_domain, theme_config, status, 
			   published_version_id, settings, created_at, updated_at
		FROM site_builder_sites
		WHERE id = ?
	`, siteID).Scan(
		&s.ID, &s.RestaurantID, &s.Name, &s.Subdomain, &s.CustomDomain,
		&s.ThemeConfig, &s.Status, &s.PublishedVersionID, &s.Settings,
		&s.CreatedAt, &s.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func canAccessSite(ctx context.Context, db *sql.DB, restaurantID int) bool {
	auth, ok := boAuthFromContext(ctx)
	if !ok {
		return false
	}
	return auth.ActiveRestaurantID == restaurantID
}

func canAccessSiteBySiteID(ctx context.Context, db *sql.DB, siteID string) bool {
	var restaurantID int
	err := db.QueryRowContext(ctx, "SELECT restaurant_id FROM site_builder_sites WHERE id = ?", siteID).Scan(&restaurantID)
	if err != nil {
		return false
	}
	return canAccessSite(ctx, db, restaurantID)
}
