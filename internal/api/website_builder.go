package api

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/go-chi/chi/v5"
)

type WebsiteBuilder struct {
	db     *sql.DB
	server *Server
}

func newWebsiteBuilder(s *Server) *WebsiteBuilder {
	return &WebsiteBuilder{db: s.db, server: s}
}

type WebsiteTemplate struct {
	ID           int             `json:"id"`
	Slug         string          `json:"slug"`
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	ThumbnailURL string          `json:"thumbnail_url"`
	TemplateData json.RawMessage `json:"template_data"`
	Category     string          `json:"category"`
	IsActive     bool            `json:"is_active"`
	CreatedAt    *time.Time      `json:"created_at,omitempty"`
	UpdatedAt    *time.Time      `json:"updated_at,omitempty"`
}

type Website struct {
	ID           int             `json:"id"`
	RestaurantID int             `json:"restaurant_id"`
	TemplateID   *int            `json:"template_id"`
	CustomHTML   string          `json:"custom_html,omitempty"`
	Domain       string          `json:"domain"`
	Subdomain    string          `json:"subdomain,omitempty"`
	Status       string          `json:"status,omitempty"`
	Settings     json.RawMessage `json:"settings,omitempty"`
	PublishedAt  *time.Time      `json:"published_at,omitempty"`
	CreatedAt    *time.Time      `json:"created_at,omitempty"`
	UpdatedAt    *time.Time      `json:"updated_at,omitempty"`
}

type WebsitePage struct {
	ID              int        `json:"id"`
	WebsiteID       int        `json:"website_id"`
	Slug            string     `json:"slug"`
	Title           string     `json:"title"`
	MetaDescription string     `json:"meta_description"`
	MetaKeywords    string     `json:"meta_keywords"`
	IsHomepage      bool       `json:"is_homepage"`
	Status          string     `json:"status"`
	CreatedAt       *time.Time `json:"created_at,omitempty"`
	UpdatedAt       *time.Time `json:"updated_at,omitempty"`
}

type WebsitePageSection struct {
	ID          int             `json:"id"`
	PageID      int             `json:"page_id"`
	SectionType string          `json:"section_type"`
	Position    int             `json:"position"`
	Settings    json.RawMessage `json:"settings"`
	IsVisible   bool            `json:"is_visible"`
	CreatedAt   *time.Time      `json:"created_at,omitempty"`
	UpdatedAt   *time.Time      `json:"updated_at,omitempty"`
}

type WebsiteComponent struct {
	ID              int             `json:"id"`
	ComponentType   string          `json:"component_type"`
	Name            string          `json:"name"`
	Description     string          `json:"description"`
	Icon            string          `json:"icon"`
	DefaultSettings json.RawMessage `json:"default_settings"`
	SchemaJSON      json.RawMessage `json:"schema_json"`
	IsActive        bool            `json:"is_active"`
	CreatedAt       *time.Time      `json:"created_at,omitempty"`
	UpdatedAt       *time.Time      `json:"updated_at,omitempty"`
}

type WebsiteSectionComponent struct {
	ID              int               `json:"id"`
	SectionID       int               `json:"section_id"`
	ComponentID     int               `json:"component_id"`
	Position        int               `json:"position"`
	Settings        json.RawMessage   `json:"settings"`
	DynamicSource   string            `json:"dynamic_source"`
	DynamicParams   json.RawMessage   `json:"dynamic_params"`
	ComponentType   string            `json:"component_type,omitempty"`
	ComponentName   string            `json:"component_name,omitempty"`
	DefaultSettings json.RawMessage   `json:"default_settings,omitempty"`
	Component       *WebsiteComponent `json:"component,omitempty"`
	CreatedAt       *time.Time        `json:"created_at,omitempty"`
	UpdatedAt       *time.Time        `json:"updated_at,omitempty"`
}

type WebsiteAsset struct {
	ID               int        `json:"id"`
	WebsiteID        int        `json:"website_id"`
	AssetType        string     `json:"asset_type"`
	OriginalFilename string     `json:"original_filename"`
	StoragePath      string     `json:"storage_path"`
	PublicURL        string     `json:"public_url"`
	MimeType         string     `json:"mime_type"`
	FileSize         int        `json:"file_size"`
	Width            int        `json:"width"`
	Height           int        `json:"height"`
	AltText          string     `json:"alt_text"`
	CreatedAt        *time.Time `json:"created_at,omitempty"`
}

type WebsitePublishHistoryEntry struct {
	ID          int             `json:"id"`
	WebsiteID   int             `json:"website_id"`
	Version     int             `json:"version"`
	Snapshot    json.RawMessage `json:"snapshot_json,omitempty"`
	PublishedBy *int64          `json:"published_by,omitempty"`
	PublishedAt *time.Time      `json:"published_at,omitempty"`
	StoragePath string          `json:"storage_path"`
}

func (wb *WebsiteBuilder) RegisterRoutes(r chi.Router) {
	// Legacy routes (section-based model)
	r.Get("/website-builder/templates", wb.ListTemplates)
	r.Get("/website-builder/templates/{id}", wb.GetTemplate)

	r.Get("/website-builder/website", wb.GetWebsite)
	r.Post("/website-builder/website", wb.CreateWebsite)
	r.Put("/website-builder/website", wb.UpdateWebsite)
	r.Delete("/website-builder/website", wb.DeleteWebsite)

	r.Get("/website-builder/pages", wb.ListPages)
	r.Post("/website-builder/pages", wb.CreatePage)
	r.Put("/website-builder/pages/{id}", wb.UpdatePage)
	r.Delete("/website-builder/pages/{id}", wb.DeletePage)

	r.Get("/website-builder/pages/{pageId}/sections", wb.ListSections)
	r.Post("/website-builder/pages/{pageId}/sections", wb.CreateSection)
	r.Put("/website-builder/sections/{id}", wb.UpdateSection)
	r.Delete("/website-builder/sections/{id}", wb.DeleteSection)
	r.Put("/website-builder/sections/reorder", wb.ReorderSections)

	r.Get("/website-builder/components", wb.ListComponents)
	r.Get("/website-builder/components/{id}", wb.GetComponent)

	r.Get("/website-builder/sections/{sectionId}/components", wb.ListSectionComponents)
	r.Post("/website-builder/sections/{sectionId}/components", wb.CreateSectionComponent)
	r.Put("/website-builder/section-components/{id}", wb.UpdateSectionComponent)
	r.Delete("/website-builder/section-components/{id}", wb.DeleteSectionComponent)
	r.Put("/website-builder/section-components/reorder", wb.ReorderSectionComponents)

	r.Get("/website-builder/assets", wb.ListAssets)
	r.Post("/website-builder/assets", wb.UploadAsset)
	r.Delete("/website-builder/assets/{id}", wb.DeleteAsset)

	r.Post("/website-builder/publish", wb.PublishWebsite)
	r.Get("/website-builder/preview", wb.PreviewWebsite)
	r.Get("/website-builder/history", wb.PublishHistory)

	// New site builder routes (JSON tree model)
	RegisterSiteBuilderRoutes(r, wb.db)
}
