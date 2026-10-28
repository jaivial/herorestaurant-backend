package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	editorialRiceStart = "<!--vc-active-rice-menus:start-->"
	editorialRiceEnd   = "<!--vc-active-rice-menus:end-->"
)

var editorialRicePaths = map[string]bool{
	"/arroces-y-paellas": true, "/en/arroces-y-paellas": true,
	"/guia-arroces-valencianos": true, "/en/guia-arroces-valencianos": true,
}

type editorialRiceLoader func(context.Context) ([]publicMenuItem, error)
type editorialRiceCacheEntry struct {
	body      []byte
	refreshed time.Time
}
type editorialRiceSSR struct {
	staticDir string
	next      http.Handler
	load      editorialRiceLoader
	mu        sync.Mutex
	cache     map[string]editorialRiceCacheEntry
	now       func() time.Time
	ttl       time.Duration
}

func newEditorialRiceSSR(staticDir string, next http.Handler, load editorialRiceLoader) http.Handler {
	return &editorialRiceSSR{staticDir: staticDir, next: next, load: load, cache: map[string]editorialRiceCacheEntry{}, now: time.Now, ttl: time.Minute}
}

func (h *editorialRiceSSR) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if (r.Method != http.MethodGet && r.Method != http.MethodHead) || !editorialRicePaths[r.URL.Path] {
		h.next.ServeHTTP(w, r)
		return
	}
	body, state := h.document(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=60, stale-while-revalidate=300")
	w.Header().Set("X-Villacarmen-Rice-Cache", state)
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

func (h *editorialRiceSSR) document(r *http.Request) ([]byte, string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	path := r.URL.Path
	if entry, ok := h.cache[path]; ok && h.now().Sub(entry.refreshed) < h.ttl {
		return entry.body, "HIT"
	}
	menus, err := h.load(r.Context())
	if err == nil {
		body, renderErr := h.renderDocument(path, menus)
		if renderErr == nil {
			h.cache[path] = editorialRiceCacheEntry{body: body, refreshed: h.now()}
			return body, "MISS"
		}
		err = renderErr
	}
	log.Printf("editorial rice SSR refresh failed for %s: %v", path, err)
	if entry, ok := h.cache[path]; ok {
		return entry.body, "STALE"
	}
	body, readErr := os.ReadFile(h.staticPath(path))
	if readErr == nil {
		return body, "FALLBACK"
	}
	return []byte("Service temporarily unavailable"), "ERROR"
}

func (h *editorialRiceSSR) staticPath(path string) string {
	return filepath.Join(h.staticDir, filepath.FromSlash(strings.Trim(path, "/")), "index.html")
}

func (h *editorialRiceSSR) renderDocument(path string, menus []publicMenuItem) ([]byte, error) {
	body, err := os.ReadFile(h.staticPath(path))
	if err != nil {
		return nil, err
	}
	start := bytes.Index(body, []byte(editorialRiceStart))
	end := bytes.Index(body, []byte(editorialRiceEnd))
	if start < 0 || end < 0 || end <= start {
		return nil, fmt.Errorf("active rice markers not found")
	}
	end += len(editorialRiceEnd)
	lang := "es"
	if strings.HasPrefix(path, "/en/") {
		lang = "en"
	}
	fragment := renderEditorialRiceMenus(menus, lang)
	body = append(append(append([]byte{}, body[:start]...), fragment...), body[end:]...)
	return replaceEditorialBootstrap(body, menus)
}

func replaceEditorialBootstrap(body []byte, menus []publicMenuItem) ([]byte, error) {
	marker := []byte("window.__VILLACARMEN_DATA__=")
	start := bytes.Index(body, marker)
	if start < 0 {
		return nil, fmt.Errorf("prerender bootstrap not found")
	}
	valueStart := start + len(marker)
	endRelative := bytes.Index(body[valueStart:], []byte("</script>"))
	if endRelative < 0 {
		return nil, fmt.Errorf("prerender bootstrap end not found")
	}
	end := valueStart + endRelative
	var data map[string]any
	if err := json.Unmarshal(body[valueStart:end], &data); err != nil {
		return nil, err
	}
	active := map[string]publicMenuItem{}
	for _, menu := range menus {
		if menu.Active && menu.MenuPublicActive {
			active[strconv.FormatInt(menu.ID, 10)] = menu
		}
	}
	data["menus"] = active
	encoded, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	return append(append(append([]byte{}, body[:valueStart]...), encoded...), body[end:]...), nil
}

type publicMenuCapture struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (w *publicMenuCapture) Header() http.Header         { return w.header }
func (w *publicMenuCapture) WriteHeader(status int)      { w.status = status }
func (w *publicMenuCapture) Write(p []byte) (int, error) { return w.body.Write(p) }

func (s *Server) loadEditorialRiceMenus(ctx context.Context) ([]publicMenuItem, error) {
	ids := []int64{41, 42, 1}
	menus := make([]publicMenuItem, 0, len(ids))
	for _, id := range ids {
		capture := &publicMenuCapture{header: http.Header{}, status: http.StatusOK}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("/api/menus/%d", id), nil)
		s.handleFullPublicMenuByID(capture, req, 1, id)
		if capture.status != http.StatusOK {
			if capture.status == http.StatusNotFound {
				continue
			}
			return nil, fmt.Errorf("menu %d returned %d", id, capture.status)
		}
		var payload struct {
			Menu publicMenuItem `json:"menu"`
		}
		if err := json.Unmarshal(capture.body.Bytes(), &payload); err != nil {
			return nil, err
		}
		if payload.Menu.Active && payload.Menu.MenuPublicActive {
			menus = append(menus, payload.Menu)
		}
	}
	return menus, nil
}

func editorialLocalized(es, en, lang string) string {
	if lang == "en" && strings.TrimSpace(en) != "" {
		return en
	}
	return es
}

func editorialRiceSection(section publicMenuSection) bool {
	title := strings.ToLower(section.Title + " " + section.DisplayTitle)
	return section.Kind == "arroces" || strings.Contains(title, "arroz") || strings.Contains(title, "paella")
}

func editorialMenuPath(menu publicMenuItem) string {
	if slug := strings.TrimSpace(menu.SEOSlug); slug != "" {
		return "/menu/" + slug
	}
	path := "/menu/" + strconv.FormatInt(menu.ID, 10)
	if menu.Slug != "" {
		path += "/" + menu.Slug
	}
	return path
}

func editorialEuro(value float64) string {
	value = math.Round(value*100) / 100
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func editorialFold(value string) string {
	value = strings.ToLower(strings.Join(strings.Fields(value), " "))
	return strings.NewReplacer("á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", "ü", "u", "ñ", "n").Replace(value)
}

func editorialWeekendDishes(menu publicMenuItem) []publicMenuDish {
	seen := map[string]bool{}
	var dishes []publicMenuDish
	for _, section := range menu.Sections {
		if !editorialRiceSection(section) {
			continue
		}
		for _, dish := range section.Dishes {
			key := editorialFold(dish.Title)
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			dishes = append(dishes, dish)
		}
	}
	return dishes
}

type editorialAllergen struct{ es, en, file string }

var editorialAllergens = map[string]editorialAllergen{
	"Gluten": {"Gluten", "Gluten", "gluten.png"}, "Crustaceos": {"Crustáceos", "Crustaceans", "crustaceos.png"},
	"Huevos": {"Huevos", "Eggs", "huevos.png"}, "Pescado": {"Pescado", "Fish", "pescado.png"},
	"Cacahuetes": {"Cacahuetes", "Peanuts", "cacahuetes.png"}, "Soja": {"Soja", "Soy", "soja.png"},
	"Leche": {"Leche", "Milk", "leche.png"}, "Frutos de cascara": {"Frutos de cáscara", "Tree nuts", "frutoscascara.png"},
	"Apio": {"Apio", "Celery", "apio.png"}, "Mostaza": {"Mostaza", "Mustard", "mostaza.png"},
	"Sesamo": {"Sésamo", "Sesame", "sesamo.png"}, "Sulfitos": {"Sulfitos", "Sulfites", "sulfitos.png"},
	"Altramuces": {"Altramuces", "Lupin", "altramuces.png"}, "Moluscos": {"Moluscos", "Molluscs", "moluscos.png"},
}

func editorialMediaSrc(raw string) string {
	raw = strings.TrimSpace(raw)
	lower := strings.ToLower(raw)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "data:") || strings.HasPrefix(lower, "blob:") {
		return raw
	}
	if strings.HasPrefix(raw, "//") {
		return "https:" + raw
	}
	parts := strings.Split(strings.TrimLeft(raw, "/"), "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return "https://villacarmenmedia.b-cdn.net/" + strings.Join(parts, "/")
}

func editorialDishBody(dish publicMenuDish, lang string) string {
	var b strings.Builder
	title := editorialLocalized(dish.Title, dish.TitleEnglish, lang)
	b.WriteString(`<div class="dishDescription">` + html.EscapeString(title) + `</div>`)
	description := publicMenuDescription(editorialLocalized(dish.Description, dish.DescriptionEnglish, lang), dish.DescriptionEnabled)
	if description != "" {
		b.WriteString(`<div class="dishDescriptionExtra">` + html.EscapeString(description) + `</div>`)
	}
	if dish.SupplementEnabled && dish.SupplementPrice != nil && *dish.SupplementPrice > 0 && !math.IsNaN(*dish.SupplementPrice) && !math.IsInf(*dish.SupplementPrice, 0) {
		b.WriteString(`<div class="dishSupplementInfo" data-testid="dish-supplement" data-coordination-id="dish-supplement-` + strconv.FormatInt(dish.ID, 10) + `">` + editorialLocalized("Suplemento", "Supplement", lang) + ` +` + editorialEuro(*dish.SupplementPrice) + `€</div>`)
	}
	if dish.Price != nil && !math.IsNaN(*dish.Price) && !math.IsInf(*dish.Price, 0) {
		b.WriteString(`<div class="dishCardPrice">+` + editorialEuro(*dish.Price) + `€</div>`)
	}
	return b.String()
}

func editorialDishAllergens(dish publicMenuDish, lang string) string {
	seen := map[string]bool{}
	var icons strings.Builder
	for _, raw := range dish.Allergens {
		key := strings.TrimSpace(raw)
		allergen, ok := editorialAllergens[key]
		if !ok || seen[key] {
			continue
		}
		seen[key] = true
		label := editorialLocalized(allergen.es, allergen.en, lang)
		icons.WriteString(`<img src="/images/` + allergen.file + `" class="allergenIcon" alt="` + html.EscapeString(label) + `" title="` + html.EscapeString(label) + `" loading="lazy" decoding="async"/>`)
	}
	if icons.Len() == 0 {
		return ""
	}
	return `<div class="dishAllergenRow" aria-label="` + editorialLocalized("Alérgenos", "Allergens", lang) + `">` + icons.String() + `</div>`
}

func renderEditorialDish(dish publicMenuDish, showImage bool, lang string) string {
	className := "dishCard is-revealed"
	if showImage {
		className += " dishCard--withImage"
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<li class="%s" data-dish-id="%d">`, className, dish.ID)
	if !showImage {
		b.WriteString(`<div class="dishCardBody">` + editorialDishBody(dish, lang) + editorialDishAllergens(dish, lang) + `</div></li>`)
		return b.String()
	}
	title := editorialLocalized(dish.Title, dish.TitleEnglish, lang)
	rawImage := strings.TrimSpace(dish.FotoURL)
	b.WriteString(`<div class="dishCardMain"><div class="dishCardMedia`)
	if rawImage == "" {
		b.WriteString(` is-fallback`)
	}
	b.WriteString(`">`)
	if rawImage != "" {
		b.WriteString(`<button type="button" class="dishCardMediaBtn" aria-label="Ver imagen de ` + html.EscapeString(title) + `"><img class="dishCardMediaImage" src="` + html.EscapeString(editorialMediaSrc(rawImage)) + `" alt="` + html.EscapeString(title) + `" loading="lazy" decoding="async"/></button>`)
	} else {
		b.WriteString(`<div class="dishCardMediaFallback" aria-label="` + editorialLocalized("Sin imagen disponible", "Image not available", lang) + `"><svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="lucide lucide-chef-hat-icon lucide-chef-hat" aria-hidden="true"><path d="M17 21a1 1 0 0 0 1-1v-5.35c0-.457.316-.844.727-1.041a4 4 0 0 0-2.134-7.589 5 5 0 0 0-9.186 0 4 4 0 0 0-2.134 7.588c.411.198.727.585.727 1.041V20a1 1 0 0 0 1 1Z"/><path d="M6 17h12"/></svg></div>`)
	}
	b.WriteString(`</div><div class="dishCardInfo"><div class="dishCardBody">` + editorialDishBody(dish, lang) + `</div>` + editorialDishAllergens(dish, lang) + `</div></div></li>`)
	return b.String()
}

func renderEditorialRiceMenus(menus []publicMenuItem, lang string) []byte {
	var links []publicMenuItem
	var weekend *publicMenuItem
	for i := range menus {
		menu := &menus[i]
		if !menu.Active || !menu.MenuPublicActive {
			continue
		}
		if menu.ID == 41 || menu.ID == 42 || menu.ID == 1 {
			links = append(links, *menu)
		}
		if menu.ID == 42 {
			weekend = menu
		}
	}
	var dishes []publicMenuDish
	if weekend != nil {
		dishes = editorialWeekendDishes(*weekend)
	}
	var b strings.Builder
	b.WriteString(editorialRiceStart)
	if len(dishes) == 0 && len(links) == 0 {
		b.WriteString(editorialRiceEnd)
		return []byte(b.String())
	}
	b.WriteString(`<div class="container editorial-menu-list" data-testid="active-rice-menus">`)
	if len(dishes) > 0 {
		b.WriteString(`<section class="menuSection"><h2 class="menuSectionHeading">` + editorialLocalized("Nuestros arroces", "Our rice dishes", lang) + `</h2><ul class="dishGrid" role="list">`)
		for _, dish := range dishes {
			b.WriteString(renderEditorialDish(dish, weekend.ShowDishImages, lang))
		}
		b.WriteString(`</ul></section>`)
	}
	if len(links) > 0 {
		b.WriteString(`<nav class="editorial-rice-links" aria-label="` + editorialLocalized("Menús con arroces", "Menus with rice dishes", lang) + `">`)
		for _, menu := range links {
			path := editorialMenuPath(menu)
			if lang == "en" {
				path = "/en" + path
			}
			b.WriteString(`<a href="` + html.EscapeString(path) + `" class="editorial-rice-link"><span>` + html.EscapeString(editorialLocalized(menu.MenuTitle, menu.MenuTitleEnglish, lang)) + `</span><span aria-hidden="true">→</span></a>`)
		}
		b.WriteString(`</nav>`)
	}
	b.WriteString(`</div>` + editorialRiceEnd)
	return []byte(b.String())
}
