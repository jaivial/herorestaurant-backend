package api

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/json"
	"fmt"
	"html"
	"path"
	"strings"
	"time"
)

type HTMXGenerator struct {
	server         *Server
	backendBaseURL string
}

type GeneratedFile struct {
	Path     string `json:"path"`
	Content  string `json:"content,omitempty"`
	Size     int    `json:"size"`
	Checksum string `json:"checksum"`
}

type PageData struct {
	Website             *Website
	Pages               []*WebsitePage
	SectionsByPage      map[int][]*WebsitePageSection
	ComponentsBySection map[int][]*WebsiteSectionComponent
	AssetsByType        map[string][]*WebsiteAsset
	Branding            restaurantBrandingCfg
}

type WebsiteSettings struct {
	PrimaryColor string `json:"primaryColor"`
	AccentColor  string `json:"accentColor"`
	FontFamily   string `json:"fontFamily"`
	FontBody     string `json:"fontBody"`
	CustomCSS    string `json:"customCSS"`
	LogoURL      string `json:"logoUrl"`
	FaviconURL   string `json:"faviconUrl"`
}

func (g *HTMXGenerator) GenerateWebsite(ctx context.Context, websiteID int) ([]GeneratedFile, error) {
	data, err := g.loadPageData(ctx, websiteID)
	if err != nil {
		return nil, err
	}

	files := make([]GeneratedFile, 0, len(data.Pages)+1)
	for _, page := range data.Pages {
		file, err := g.generatePage(data, page)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	files = append(files, g.generateManifest(data))
	return files, nil
}

func (g *HTMXGenerator) loadPageData(ctx context.Context, websiteID int) (*PageData, error) {
	row, found, err := g.server.queryOneAsMap(ctx, `SELECT * FROM restaurant_websites WHERE id = ? LIMIT 1`, websiteID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("website not found")
	}
	website := mapWebsiteRow(row)
	if website.ID == 0 {
		website.ID = websiteID
	}

	pageRows, err := g.server.db.QueryContext(ctx, `
		SELECT id, website_id, slug, title, meta_description, meta_keywords, is_homepage, status, created_at, updated_at
		FROM website_pages
		WHERE website_id = ?
		ORDER BY is_homepage DESC, slug ASC, id ASC
	`, websiteID)
	if err != nil {
		return nil, err
	}
	defer pageRows.Close()

	pages := make([]*WebsitePage, 0, 8)
	for pageRows.Next() {
		item := &WebsitePage{}
		var createdAt sql.NullTime
		var updatedAt sql.NullTime
		if err := pageRows.Scan(&item.ID, &item.WebsiteID, &item.Slug, &item.Title, &item.MetaDescription, &item.MetaKeywords, &item.IsHomepage, &item.Status, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		if createdAt.Valid {
			item.CreatedAt = &createdAt.Time
		}
		if updatedAt.Valid {
			item.UpdatedAt = &updatedAt.Time
		}
		pages = append(pages, item)
	}
	if len(pages) == 0 {
		pages = append(pages, &WebsitePage{ID: 0, WebsiteID: websiteID, Slug: "", Title: "Inicio", IsHomepage: true, Status: "draft"})
	}

	sectionsByPage := map[int][]*WebsitePageSection{}
	for _, page := range pages {
		rows, err := g.server.db.QueryContext(ctx, `
			SELECT id, page_id, section_type, position, COALESCE(settings, JSON_OBJECT()), is_visible, created_at, updated_at
			FROM website_page_sections
			WHERE page_id = ?
			ORDER BY position ASC, id ASC
		`, page.ID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			item := &WebsitePageSection{}
			var createdAt sql.NullTime
			var updatedAt sql.NullTime
			if err := rows.Scan(&item.ID, &item.PageID, &item.SectionType, &item.Position, &item.Settings, &item.IsVisible, &createdAt, &updatedAt); err != nil {
				rows.Close()
				return nil, err
			}
			if createdAt.Valid {
				item.CreatedAt = &createdAt.Time
			}
			if updatedAt.Valid {
				item.UpdatedAt = &updatedAt.Time
			}
			sectionsByPage[item.PageID] = append(sectionsByPage[item.PageID], item)
		}
		rows.Close()
	}

	componentsBySection := map[int][]*WebsiteSectionComponent{}
	for _, sections := range sectionsByPage {
		for _, section := range sections {
			rows, err := g.server.db.QueryContext(ctx, `
				SELECT sc.id, sc.section_id, sc.component_id, sc.position, COALESCE(sc.settings, JSON_OBJECT()), COALESCE(sc.dynamic_source, ''), COALESCE(sc.dynamic_params, JSON_OBJECT()), wc.component_type, wc.name, COALESCE(wc.default_settings, JSON_OBJECT())
				FROM website_section_components sc
				JOIN website_components wc ON wc.id = sc.component_id
				WHERE sc.section_id = ?
				ORDER BY sc.position ASC, sc.id ASC
			`, section.ID)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				item := &WebsiteSectionComponent{}
				if err := rows.Scan(&item.ID, &item.SectionID, &item.ComponentID, &item.Position, &item.Settings, &item.DynamicSource, &item.DynamicParams, &item.ComponentType, &item.ComponentName, &item.DefaultSettings); err != nil {
					rows.Close()
					return nil, err
				}
				componentsBySection[item.SectionID] = append(componentsBySection[item.SectionID], item)
			}
			rows.Close()
		}
	}

	assetsByType := map[string][]*WebsiteAsset{}
	assetRows, err := g.server.db.QueryContext(ctx, `
		SELECT id, website_id, asset_type, original_filename, storage_path, public_url, mime_type, file_size, width, height, alt_text, created_at
		FROM website_assets
		WHERE website_id = ?
		ORDER BY id DESC
	`, websiteID)
	if err == nil {
		defer assetRows.Close()
		for assetRows.Next() {
			item := &WebsiteAsset{}
			var createdAt sql.NullTime
			if err := assetRows.Scan(&item.ID, &item.WebsiteID, &item.AssetType, &item.OriginalFilename, &item.StoragePath, &item.PublicURL, &item.MimeType, &item.FileSize, &item.Width, &item.Height, &item.AltText, &createdAt); err != nil {
				return nil, err
			}
			if createdAt.Valid {
				item.CreatedAt = &createdAt.Time
			}
			assetsByType[item.AssetType] = append(assetsByType[item.AssetType], item)
		}
	}

	branding, _ := g.server.loadRestaurantBranding(ctx, website.RestaurantID)
	return &PageData{
		Website:             &website,
		Pages:               pages,
		SectionsByPage:      sectionsByPage,
		ComponentsBySection: componentsBySection,
		AssetsByType:        assetsByType,
		Branding:            branding,
	}, nil
}

func (g *HTMXGenerator) generatePage(data *PageData, page *WebsitePage) (GeneratedFile, error) {
	var out strings.Builder
	out.WriteString("<!DOCTYPE html>\n")
	out.WriteString("<html lang=\"es\" data-ui=\"website-document\">\n")
	out.WriteString("<head>\n")
	out.WriteString(g.generateHead(data, page))
	out.WriteString("</head>\n")
	out.WriteString("<body data-ui=\"website-body\">\n")
	out.WriteString(g.generateNavigation(data, page))
	out.WriteString("<main data-ui=\"website-main\">\n")

	sections := data.SectionsByPage[page.ID]
	if len(sections) == 0 && page.IsHomepage && strings.TrimSpace(data.Website.CustomHTML) != "" {
		out.WriteString(data.Website.CustomHTML)
	} else {
		for _, section := range sections {
			out.WriteString(g.generateSection(data, section))
		}
	}

	out.WriteString("</main>\n")
	out.WriteString("</body>\n")
	out.WriteString("</html>\n")

	relativePath := "index.html"
	if !page.IsHomepage && strings.TrimSpace(page.Slug) != "" {
		relativePath = path.Join(strings.Trim(page.Slug, "/"), "index.html")
	}
	content := out.String()
	return GeneratedFile{Path: relativePath, Content: content, Size: len(content), Checksum: md5Hash(content)}, nil
}

func (g *HTMXGenerator) generateHead(data *PageData, page *WebsitePage) string {
	settings := parseSettings(data.Website.Settings)
	title := strings.TrimSpace(page.Title)
	if title == "" {
		title = strings.TrimSpace(data.Branding.BrandName)
	}
	if title == "" {
		title = "Restaurante"
	}

	var out strings.Builder
	out.WriteString(`<meta charset="utf-8">` + "\n")
	out.WriteString(`<meta name="viewport" content="width=device-width, initial-scale=1">` + "\n")
	out.WriteString(`<title>` + html.EscapeString(title) + `</title>` + "\n")
	if desc := strings.TrimSpace(page.MetaDescription); desc != "" {
		out.WriteString(`<meta name="description" content="` + html.EscapeString(desc) + `">` + "\n")
	}
	if keywords := strings.TrimSpace(page.MetaKeywords); keywords != "" {
		out.WriteString(`<meta name="keywords" content="` + html.EscapeString(keywords) + `">` + "\n")
	}
	if settings != nil && strings.TrimSpace(settings.FaviconURL) != "" {
		out.WriteString(`<link rel="icon" href="` + html.EscapeString(settings.FaviconURL) + `">` + "\n")
	}
	out.WriteString(`<script src="https://unpkg.com/htmx.org@1.9.12"></script>` + "\n")
	out.WriteString(`<style>` + g.generateCSSVariables(settings) + g.generateBaseCSS() + `</style>` + "\n")
	if settings != nil && strings.TrimSpace(settings.CustomCSS) != "" {
		out.WriteString(`<style>` + settings.CustomCSS + `</style>` + "\n")
	}
	return out.String()
}

func (g *HTMXGenerator) generateNavigation(data *PageData, page *WebsitePage) string {
	if len(data.Pages) <= 1 {
		return ""
	}
	var out strings.Builder
	out.WriteString(`<nav data-ui="website-nav"><div data-ui="website-nav-inner">`)
	brand := strings.TrimSpace(data.Branding.BrandName)
	if brand == "" {
		brand = "Restaurante"
	}
	out.WriteString(`<a data-ui="website-brand" href="/">` + html.EscapeString(brand) + `</a>`)
	out.WriteString(`<div data-ui="website-nav-links">`)
	for _, item := range data.Pages {
		href := "/"
		if !item.IsHomepage && strings.TrimSpace(item.Slug) != "" {
			href = "/" + strings.Trim(item.Slug, "/") + "/"
		}
		label := strings.TrimSpace(item.Title)
		if label == "" {
			label = "Página"
		}
		current := "false"
		if item.ID == page.ID {
			current = "page"
		}
		out.WriteString(`<a data-ui="website-nav-link" aria-current="` + current + `" href="` + html.EscapeString(href) + `">` + html.EscapeString(label) + `</a>`)
	}
	out.WriteString(`</div></div></nav>` + "\n")
	return out.String()
}

func (g *HTMXGenerator) generateSection(data *PageData, section *WebsitePageSection) string {
	if !section.IsVisible {
		return ""
	}
	settings := mergeSettings(json.RawMessage(`{}`), section.Settings)
	var out strings.Builder
	out.WriteString(`<section data-ui="website-section" data-section-id="` + fmt.Sprintf("%d", section.ID) + `" data-section-type="` + html.EscapeString(section.SectionType) + `"`)
	if bg := strings.TrimSpace(getString(settings, "background", "")); bg != "" {
		out.WriteString(` style="background:` + html.EscapeString(bg) + `"`)
	}
	out.WriteString(`>` + "\n")
	for _, component := range data.ComponentsBySection[section.ID] {
		out.WriteString(g.generateComponent(data, component))
	}
	out.WriteString(`</section>` + "\n")
	return out.String()
}

func (g *HTMXGenerator) generateComponent(data *PageData, comp *WebsiteSectionComponent) string {
	settings := mergeSettings(comp.DefaultSettings, comp.Settings)
	kind := componentRenderKind(comp)
	if kind != "" {
		return g.generateDynamicComponent(data, comp, kind)
	}

	switch comp.ComponentType {
	case "hero-banner":
		return g.generateHeroBanner(settings)
	case "text-block":
		return g.generateTextBlock(settings)
	case "image-block":
		return g.generateImageBlock(settings)
	case "cta-button":
		return g.generateCTAButton(settings)
	case "gallery-grid":
		return g.generateGallery(data, settings)
	case "social-links":
		return g.generateSocialLinks(settings)
	case "contact-form":
		return g.generateContactBlock(settings)
	case "reservation-form":
		return g.generateReservationBlock(settings)
	case "testimonials":
		return `<div data-ui="testimonials-placeholder"><p data-ui="testimonials-copy">Añade testimonios desde el builder.</p></div>` + "\n"
	case "divider":
		return `<hr data-ui="divider">` + "\n"
	case "spacer":
		return `<div data-ui="spacer" style="height:` + html.EscapeString(getString(settings, "height", "32px")) + `"></div>` + "\n"
	default:
		return `<div data-ui="component-placeholder" data-component-type="` + html.EscapeString(comp.ComponentType) + `"></div>` + "\n"
	}
}

func (g *HTMXGenerator) generateDynamicComponent(data *PageData, comp *WebsiteSectionComponent, kind string) string {
	url := strings.TrimRight(g.backendBaseURL, "/") + `/api/public/website-builder/render/` + kind + `?website_id=` + fmt.Sprintf("%d", data.Website.ID)
	return `<div data-ui="dynamic-component" data-component-id="` + fmt.Sprintf("%d", comp.ID) + `" data-component-type="` + html.EscapeString(comp.ComponentType) + `" hx-get="` + html.EscapeString(url) + `" hx-trigger="load" hx-swap="innerHTML"><div data-ui="dynamic-loading">Cargando...</div></div>` + "\n"
}

func (g *HTMXGenerator) generateHeroBanner(settings map[string]any) string {
	title := html.EscapeString(getString(settings, "title", "Bienvenidos"))
	subtitle := html.EscapeString(getString(settings, "subtitle", "Descubre nuestra cocina"))
	ctaText := html.EscapeString(getString(settings, "ctaText", "Reservar mesa"))
	ctaLink := html.EscapeString(getString(settings, "ctaLink", "#reservas"))
	imageURL := html.EscapeString(getString(settings, "imageURL", getString(settings, "src", "")))
	var out strings.Builder
	out.WriteString(`<section data-ui="hero-banner">`)
	if imageURL != "" {
		out.WriteString(`<img data-ui="hero-image" src="` + imageURL + `" alt="" loading="eager">`)
	}
	out.WriteString(`<div data-ui="hero-copy"><h1 data-ui="hero-title">` + title + `</h1><p data-ui="hero-subtitle">` + subtitle + `</p><a data-ui="hero-cta" href="` + ctaLink + `">` + ctaText + `</a></div></section>` + "\n")
	return out.String()
}

func (g *HTMXGenerator) generateTextBlock(settings map[string]any) string {
	align := html.EscapeString(getString(settings, "align", "left"))
	maxWidth := html.EscapeString(getString(settings, "maxWidth", "860px"))
	content := getString(settings, "content", "")
	return `<div data-ui="text-block" style="text-align:` + align + `;max-width:` + maxWidth + `">` + content + `</div>` + "\n"
}

func (g *HTMXGenerator) generateImageBlock(settings map[string]any) string {
	src := html.EscapeString(getString(settings, "src", ""))
	alt := html.EscapeString(getString(settings, "alt", ""))
	if src == "" {
		return ""
	}
	return `<figure data-ui="image-block"><img data-ui="image-block-image" src="` + src + `" alt="` + alt + `" loading="lazy"></figure>` + "\n"
}

func (g *HTMXGenerator) generateCTAButton(settings map[string]any) string {
	text := html.EscapeString(getString(settings, "text", "Continuar"))
	link := html.EscapeString(getString(settings, "link", "#"))
	return `<div data-ui="cta-wrapper"><a data-ui="cta-button" href="` + link + `">` + text + `</a></div>` + "\n"
}

func (g *HTMXGenerator) generateGallery(data *PageData, settings map[string]any) string {
	images := data.AssetsByType["image"]
	if len(images) == 0 {
		return `<div data-ui="gallery-empty"><p data-ui="gallery-empty-copy">Sube imágenes desde el builder para completar esta galería.</p></div>` + "\n"
	}
	columns := html.EscapeString(getString(settings, "columns", "3"))
	var out strings.Builder
	out.WriteString(`<div data-ui="gallery-grid" style="grid-template-columns:repeat(` + columns + `, minmax(0,1fr))">`)
	for _, asset := range images {
		out.WriteString(`<img data-ui="gallery-image" src="` + html.EscapeString(asset.PublicURL) + `" alt="` + html.EscapeString(asset.AltText) + `" loading="lazy">`)
	}
	out.WriteString(`</div>` + "\n")
	return out.String()
}

func (g *HTMXGenerator) generateSocialLinks(settings map[string]any) string {
	platforms := getStringSlice(settings, "platforms", []string{"instagram", "facebook"})
	var out strings.Builder
	out.WriteString(`<div data-ui="social-links">`)
	for _, platform := range platforms {
		url := strings.TrimSpace(getString(settings, platform+"_url", ""))
		if url == "" {
			continue
		}
		out.WriteString(`<a data-ui="social-link" href="` + html.EscapeString(url) + `" target="_blank" rel="noreferrer">` + html.EscapeString(strings.Title(platform)) + `</a>`)
	}
	out.WriteString(`</div>` + "\n")
	return out.String()
}

func (g *HTMXGenerator) generateContactBlock(settings map[string]any) string {
	title := html.EscapeString(getString(settings, "title", "Contacto"))
	copy := html.EscapeString(getString(settings, "copy", "Actualiza este bloque desde el builder con dirección, teléfono o email."))
	return `<section data-ui="contact-block"><h2 data-ui="contact-title">` + title + `</h2><p data-ui="contact-copy">` + copy + `</p></section>` + "\n"
}

func (g *HTMXGenerator) generateReservationBlock(settings map[string]any) string {
	text := html.EscapeString(getString(settings, "ctaText", "Solicitar reserva"))
	link := html.EscapeString(getString(settings, "ctaLink", "#reservas"))
	return `<section data-ui="reservation-block" id="reservas"><a data-ui="reservation-link" href="` + link + `">` + text + `</a></section>` + "\n"
}

func (g *HTMXGenerator) generateManifest(data *PageData) GeneratedFile {
	pages := make([]string, 0, len(data.Pages))
	for _, page := range data.Pages {
		if page.IsHomepage || strings.TrimSpace(page.Slug) == "" {
			pages = append(pages, "/")
		} else {
			pages = append(pages, "/"+strings.Trim(page.Slug, "/")+"/")
		}
	}
	raw := mustMarshal(map[string]any{
		"website_id":   data.Website.ID,
		"generated_at": time.Now().UTC().Format(time.RFC3339),
		"pages":        pages,
	})
	return GeneratedFile{Path: "manifest.json", Content: raw, Size: len(raw), Checksum: md5Hash(raw)}
}

func (g *HTMXGenerator) generateCSSVariables(settings *WebsiteSettings) string {
	if settings == nil {
		return ""
	}
	var out strings.Builder
	out.WriteString(`:root{`)
	if v := strings.TrimSpace(settings.PrimaryColor); v != "" {
		out.WriteString(`--wb-primary:` + v + `;`)
	}
	if v := strings.TrimSpace(settings.AccentColor); v != "" {
		out.WriteString(`--wb-accent:` + v + `;`)
	}
	if v := strings.TrimSpace(settings.FontFamily); v != "" {
		out.WriteString(`--wb-font-heading:"` + strings.ReplaceAll(v, `"`, ``) + `";`)
	}
	if v := strings.TrimSpace(settings.FontBody); v != "" {
		out.WriteString(`--wb-font-body:"` + strings.ReplaceAll(v, `"`, ``) + `";`)
	}
	out.WriteString(`}`)
	return out.String()
}

func (g *HTMXGenerator) generateBaseCSS() string {
	return `
*{box-sizing:border-box}
html,body{margin:0;padding:0}
body{font-family:var(--wb-font-body,system-ui,sans-serif);line-height:1.5;color:var(--wb-primary,#1f2937);background:#fff}
img{display:block;max-width:100%}
a{color:inherit}
[data-ui="website-nav"]{border-bottom:1px solid rgba(0,0,0,.08)}
[data-ui="website-nav-inner"]{max-width:1100px;margin:0 auto;padding:16px;display:flex;gap:16px;align-items:center;justify-content:space-between}
[data-ui="website-nav-links"]{display:flex;gap:12px;flex-wrap:wrap}
[data-ui="website-main"]{display:block}
[data-ui="website-section"]{padding:48px 16px}
[data-ui="website-section"]>*{max-width:1100px;margin:0 auto}
[data-ui="hero-banner"]{position:relative;min-height:420px;display:grid;place-items:center;overflow:hidden;background:#111827;color:#fff}
[data-ui="hero-image"]{position:absolute;inset:0;width:100%;height:100%;object-fit:cover;opacity:.45}
[data-ui="hero-copy"]{position:relative;z-index:1;max-width:720px;padding:48px 16px;text-align:center}
[data-ui="hero-title"]{margin:0 0 12px;font-family:var(--wb-font-heading,inherit);font-size:clamp(2rem,4vw,4rem)}
[data-ui="hero-subtitle"]{margin:0 0 20px}
[data-ui="hero-cta"],[data-ui="cta-button"],[data-ui="reservation-link"]{display:inline-block;padding:12px 20px;border-radius:999px;text-decoration:none;background:var(--wb-accent,#111827);color:#fff}
[data-ui="text-block"]{padding:16px}
[data-ui="gallery-grid"]{display:grid;gap:12px;padding:16px}
[data-ui="social-links"]{display:flex;gap:12px;flex-wrap:wrap;padding:16px}
[data-ui="dynamic-component"]{padding:16px}
[data-ui="dynamic-loading"]{padding:12px;border:1px dashed rgba(0,0,0,.16)}
@media (max-width:768px){[data-ui="website-section"]{padding:32px 12px}}
`
}

func parseSettings(raw json.RawMessage) *WebsiteSettings {
	if len(raw) == 0 {
		return nil
	}
	var settings WebsiteSettings
	if err := json.Unmarshal(raw, &settings); err != nil {
		return nil
	}
	return &settings
}

func mergeSettings(defaults json.RawMessage, custom json.RawMessage) map[string]any {
	out := map[string]any{}
	if len(defaults) > 0 {
		_ = json.Unmarshal(defaults, &out)
	}
	if len(custom) > 0 {
		var override map[string]any
		_ = json.Unmarshal(custom, &override)
		for key, value := range override {
			out[key] = value
		}
	}
	return out
}

func componentRenderKind(comp *WebsiteSectionComponent) string {
	if source := strings.TrimSpace(comp.DynamicSource); source != "" {
		return source
	}
	switch comp.ComponentType {
	case "menu-card":
		return "menus"
	case "wine-list":
		return "wines"
	case "hours-table":
		return "hours"
	default:
		return ""
	}
}

func getString(m map[string]any, key string, fallback string) string {
	if value, ok := m[key]; ok {
		switch typed := value.(type) {
		case string:
			if strings.TrimSpace(typed) != "" {
				return typed
			}
		case float64:
			return fmt.Sprintf("%g", typed)
		case int:
			return fmt.Sprintf("%d", typed)
		}
	}
	return fallback
}

func getStringSlice(m map[string]any, key string, fallback []string) []string {
	value, ok := m[key]
	if !ok {
		return fallback
	}
	slice, ok := value.([]any)
	if !ok {
		return fallback
	}
	out := make([]string, 0, len(slice))
	for _, item := range slice {
		out = append(out, fmt.Sprintf("%v", item))
	}
	if len(out) == 0 {
		return fallback
	}
	return out
}

func md5Hash(s string) string {
	sum := md5.Sum([]byte(s))
	return fmt.Sprintf("%x", sum)
}

func mustMarshal(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return `{}`
	}
	return string(raw)
}
