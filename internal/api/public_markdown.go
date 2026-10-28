package api

import (
	"bytes"
	"fmt"
	"html"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	activeRiceMarkdownStart = "<!--vc-active-rice-markdown:start-->"
	activeRiceMarkdownEnd   = "<!--vc-active-rice-markdown:end-->"
)

type publicMarkdownCacheEntry struct {
	body      []byte
	refreshed time.Time
}

type publicMarkdownHandler struct {
	staticDir string
	next      http.Handler
	load      editorialRiceLoader
	mu        sync.Mutex
	cache     map[string]publicMarkdownCacheEntry
	now       func() time.Time
	ttl       time.Duration
}

func newPublicMarkdownHandler(staticDir string, next http.Handler, load editorialRiceLoader) http.Handler {
	return &publicMarkdownHandler{
		staticDir: staticDir, next: next, load: load,
		cache: make(map[string]publicMarkdownCacheEntry), now: time.Now, ttl: time.Minute,
	}
}

func (h *publicMarkdownHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		h.next.ServeHTTP(w, r)
		return
	}
	path := r.URL.Path
	if path == "/sitemap.xml" {
		h.serveSitemap(w, r)
		return
	}
	if !strings.HasSuffix(path, ".md") {
		h.next.ServeHTTP(w, r)
		return
	}
	w.Header().Set("X-Robots-Tag", "noindex, follow")
	body := []byte(nil)
	state := ""
	if editorialRicePaths[strings.TrimSuffix(path, "/index.md")] {
		body, state = h.document(w, r)
		if state == "ERROR" {
			return
		}
		w.Header().Set("X-Villacarmen-Markdown-Cache", state)
	} else {
		var err error
		body, err = h.staticBody(path)
		if err != nil {
			h.next.ServeHTTP(w, r)
			return
		}
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.Header().Set("Cache-Control", "public, max-age=60, stale-while-revalidate=300")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

func (h *publicMarkdownHandler) filePath(path string) string {
	return filepath.Join(h.staticDir, filepath.FromSlash(strings.TrimPrefix(path, "/")))
}

func (h *publicMarkdownHandler) staticBody(path string) ([]byte, error) {
	clean := filepath.Clean("/" + path)
	if clean != "/"+strings.TrimPrefix(path, "/") || strings.Contains(path, "\\") {
		return nil, fmt.Errorf("invalid public Markdown path")
	}
	return os.ReadFile(h.filePath(path))
}

func (h *publicMarkdownHandler) document(w http.ResponseWriter, r *http.Request) ([]byte, string) {
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
			h.cache[path] = publicMarkdownCacheEntry{body: body, refreshed: h.now()}
			return body, "MISS"
		}
		err = renderErr
	}
	log.Printf("public Markdown refresh failed for %s: %v", path, err)
	if entry, ok := h.cache[path]; ok {
		w.Header().Set("Warning", `199 VillaCarmen "Serving stale public Markdown"`)
		return entry.body, "STALE"
	}
	body, readErr := h.staticBody(path)
	if readErr == nil {
		w.Header().Set("Warning", `199 VillaCarmen "Serving build-time public Markdown"`)
		return body, "FALLBACK"
	}
	h.next.ServeHTTP(w, r)
	return nil, "ERROR"
}

func (h *publicMarkdownHandler) renderDocument(path string, menus []publicMenuItem) ([]byte, error) {
	body, err := h.staticBody(path)
	if err != nil {
		return nil, err
	}
	start := bytes.Index(body, []byte(activeRiceMarkdownStart))
	end := bytes.Index(body, []byte(activeRiceMarkdownEnd))
	if start < 0 || end < 0 || end <= start {
		return nil, fmt.Errorf("active rice Markdown markers not found")
	}
	end += len(activeRiceMarkdownEnd)
	lang := "es"
	if strings.HasPrefix(path, "/en/") {
		lang = "en"
	}
	fragment := renderEditorialRiceMarkdown(menus, lang)
	body = append(append(append([]byte{}, body[:start]...), fragment...), body[end:]...)
	return replacePublicMarkdownLastmod(body, latestEditorialMenuDate(menus, body)), nil
}

func latestEditorialMenuDate(menus []publicMenuItem, body []byte) string {
	latest := ""
	if match := regexp.MustCompile(`(?m)^lastmod: (\d{4}-\d{2}-\d{2})$`).FindSubmatch(body); len(match) == 2 {
		latest = string(match[1])
	}
	for _, menu := range menus {
		if len(menu.ModifiedAt) >= 10 && menu.ModifiedAt[:10] > latest {
			latest = menu.ModifiedAt[:10]
		}
	}
	return latest
}

func replacePublicMarkdownLastmod(body []byte, date string) []byte {
	if len(date) != 10 {
		return body
	}
	lines := strings.Split(string(body), "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "lastmod: ") {
			lines[i] = "lastmod: " + date
			return []byte(strings.Join(lines, "\n"))
		}
		if line == "---" && i > 0 {
			break
		}
	}
	return body
}

var (
	markdownHeading = regexp.MustCompile(`(?i)<h([1-6])\b[^>]*>(.*?)</h[1-6]>`)
	markdownLink    = regexp.MustCompile(`(?i)<a\b[^>]*href="([^"]*)"[^>]*>(.*?)</a>`)
	markdownLiOpen  = regexp.MustCompile(`(?i)<li\b[^>]*>`)
	markdownLiClose = regexp.MustCompile(`(?i)</li>`)
	markdownBlock   = regexp.MustCompile(`(?i)</(?:p|div|section|article|main|ul|ol|nav)>`)
	markdownTag     = regexp.MustCompile(`<[^>]+>`)
)

func markdownText(value string) string {
	return strings.Join(strings.Fields(html.UnescapeString(markdownTag.ReplaceAllString(value, " "))), " ")
}

func renderEditorialRiceMarkdown(menus []publicMenuItem, lang string) []byte {
	// Reuse the exact shared-dish HTML renderer so both representations expose
	// the same active dishes, prices, supplements, descriptions and menu links.
	body := string(renderEditorialRiceMenus(menus, lang))
	body = markdownHeading.ReplaceAllStringFunc(body, func(tag string) string {
		parts := markdownHeading.FindStringSubmatch(tag)
		return "\n\n" + strings.Repeat("#", int(parts[1][0]-'0')) + " " + markdownText(parts[2]) + "\n\n"
	})
	body = markdownLink.ReplaceAllStringFunc(body, func(tag string) string {
		parts := markdownLink.FindStringSubmatch(tag)
		label := markdownText(parts[2])
		if label == "" || !strings.HasPrefix(parts[1], "/") {
			return label
		}
		return "[" + label + "](https://www.alqueriavillacarmen.com" + parts[1] + ") "
	})
	body = regexp.MustCompile(`(?i)<img\b[^>]*alt="([^"]*)"[^>]*>`).ReplaceAllString(body, " $1 ")
	body = markdownLiOpen.ReplaceAllString(body, "\n- ")
	body = markdownLiClose.ReplaceAllString(body, "\n")
	body = markdownBlock.ReplaceAllString(body, "\n\n")
	body = markdownTag.ReplaceAllString(body, " ")
	body = strings.ReplaceAll(body, editorialRiceStart, "")
	body = strings.ReplaceAll(body, editorialRiceEnd, "")
	var output []string
	for _, line := range strings.Split(html.UnescapeString(body), "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			output = append(output, line)
		}
	}
	return []byte(activeRiceMarkdownStart + "\n" + strings.Join(output, "\n") + "\n" + activeRiceMarkdownEnd)
}

func (h *publicMarkdownHandler) serveSitemap(w http.ResponseWriter, r *http.Request) {
	body, err := os.ReadFile(filepath.Join(h.staticDir, "sitemap.xml"))
	if err != nil {
		h.next.ServeHTTP(w, r)
		return
	}
	menus, loadErr := h.load(r.Context())
	if loadErr == nil {
		body = refreshSitemapMenuDates(body, menus)
	} else {
		log.Printf("sitemap menu lastmod refresh failed: %v", loadErr)
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=60, stale-while-revalidate=300")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

func refreshSitemapMenuDates(body []byte, menus []publicMenuItem) []byte {
	dates := map[string]string{}
	for _, menu := range menus {
		if len(menu.ModifiedAt) < 10 {
			continue
		}
		path := editorialMenuPath(menu)
		dates["https://www.alqueriavillacarmen.com"+path] = menu.ModifiedAt[:10]
		dates["https://www.alqueriavillacarmen.com/en"+path] = menu.ModifiedAt[:10]
	}
	if latest := latestEditorialMenuDate(menus, nil); latest != "" {
		for _, path := range []string{"/arroces-y-paellas", "/guia-arroces-valencianos"} {
			dates["https://www.alqueriavillacarmen.com"+path] = latest
			dates["https://www.alqueriavillacarmen.com/en"+path] = latest
		}
	}
	parts := strings.Split(string(body), "<url>")
	for i, part := range parts {
		for url, date := range dates {
			if !strings.Contains(part, "<loc>"+url+"</loc>") {
				continue
			}
			start := strings.Index(part, "<lastmod>")
			end := strings.Index(part, "</lastmod>")
			if start >= 0 && end > start {
				start += len("<lastmod>")
				if date > part[start:end] {
					part = part[:start] + date + part[end:]
				}
			}
			break
		}
		parts[i] = part
	}
	return []byte(strings.Join(parts, "<url>"))
}
