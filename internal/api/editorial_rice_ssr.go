package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log"
	"net/http"
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

func editorialPrice(raw string) string {
	price, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || price <= 0 {
		return ""
	}
	return strings.TrimSuffix(strconv.FormatFloat(price, 'f', 2, 64), ".00")
}

func renderEditorialRiceMenus(menus []publicMenuItem, lang string) []byte {
	var visible []publicMenuItem
	for _, menu := range menus {
		if !menu.Active || !menu.MenuPublicActive {
			continue
		}
		for _, section := range menu.Sections {
			if editorialRiceSection(section) && len(section.Dishes) > 0 {
				visible = append(visible, menu)
				break
			}
		}
	}
	var b strings.Builder
	b.WriteString(editorialRiceStart)
	if len(visible) == 0 {
		b.WriteString(editorialRiceEnd)
		return []byte(b.String())
	}
	title := editorialLocalized("Arroces de nuestros menús", "Rice dishes from our menus", lang)
	intro := editorialLocalized("Descubre los arroces disponibles en los menús actualmente activos. La selección y los suplementos se actualizan desde el restaurante.", "Explore the rice dishes available on our currently active menus. The selection and supplements are updated directly by the restaurant.", lang)
	b.WriteString(`<div class="container editorial-menu-list" data-testid="active-rice-menus"><header class="editorial-menu-heading"><h2>` + html.EscapeString(title) + `</h2><p>` + html.EscapeString(intro) + `</p></header>`)
	for _, menu := range visible {
		fmt.Fprintf(&b, `<section class="editorial-menu" data-menu-id="%d" aria-labelledby="rice-menu-%d"><h3 id="rice-menu-%d">%s</h3><p class="editorial-menu-price">`, menu.ID, menu.ID, menu.ID, html.EscapeString(editorialLocalized(menu.MenuTitle, menu.MenuTitleEnglish, lang)))
		if price := editorialPrice(menu.Price); price != "" {
			fmt.Fprintf(&b, `<strong>%s € %s</strong>`, price, editorialLocalized("por persona", "per person", lang))
		}
		if menu.Settings.MinPartySize > 1 {
			fmt.Fprintf(&b, `<span>%s %d %s</span>`, editorialLocalized("Mínimo", "Minimum", lang), menu.Settings.MinPartySize, editorialLocalized("personas", "people", lang))
		}
		b.WriteString(`</p>`)
		for _, section := range menu.Sections {
			if !editorialRiceSection(section) {
				continue
			}
			dishes := section.Dishes
			if len(dishes) == 0 {
				continue
			}
			sectionTitle := section.DisplayTitle
			if sectionTitle == "" {
				sectionTitle = section.Title
			}
			sectionEnglish := section.DisplayTitleEnglish
			if sectionEnglish == "" {
				sectionEnglish = section.TitleEnglish
			}
			fmt.Fprintf(&b, `<div class="editorial-rice-section"><h4>%s</h4><ul class="editorial-dishes">`, html.EscapeString(editorialLocalized(sectionTitle, sectionEnglish, lang)))
			for _, dish := range dishes {
				fmt.Fprintf(&b, `<li><h5>%s</h5>`, html.EscapeString(editorialLocalized(dish.Title, dish.TitleEnglish, lang)))
				description := publicMenuDescription(editorialLocalized(dish.Description, dish.DescriptionEnglish, lang), dish.DescriptionEnabled)
				if description != "" {
					b.WriteString(`<p>` + html.EscapeString(description) + `</p>`)
				}
				if dish.SupplementEnabled && dish.SupplementPrice != nil && *dish.SupplementPrice != 0 {
					fmt.Fprintf(&b, `<span class="editorial-dish-supplement">%s +%s €</span>`, editorialLocalized("Suplemento", "Supplement", lang), strconv.FormatFloat(*dish.SupplementPrice, 'f', -1, 64))
				}
				b.WriteString(`</li>`)
			}
			b.WriteString(`</ul></div>`)
		}
		fmt.Fprintf(&b, `<a href="%s" class="btn primary">%s</a>`, html.EscapeString(editorialMenuPath(menu)), editorialLocalized("Ver el menú completo", "View the full menu", lang))
		conditions := menu.Settings.Comments
		if lang == "en" && len(menu.Settings.CommentsEnglish) == len(conditions) {
			conditions = menu.Settings.CommentsEnglish
		}
		if len(conditions) > 0 {
			b.WriteString(`<h4>` + editorialLocalized("Condiciones publicadas", "Published conditions", lang) + `</h4><ul class="editorial-menu-conditions">`)
			for _, condition := range conditions {
				b.WriteString(`<li>` + html.EscapeString(condition) + `</li>`)
			}
			b.WriteString(`</ul>`)
		}
		b.WriteString(`</section>`)
	}
	b.WriteString(`</div>` + editorialRiceEnd)
	return []byte(b.String())
}
