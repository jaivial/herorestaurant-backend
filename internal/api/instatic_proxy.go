package api

// Restaurant website virtual host: serves the restaurant's published static
// bundle (baked by instatic publish to uploads/published/current) directly from
// disk. No per-restaurant Bun process is involved in serving — instances run
// on-demand only for editing/publishing (see instatic_manager.go).
//
// nginx routes every `*.app_base_url` subdomain (and bought custom domains)
// here (one upstream); this handler resolves restaurant → data dir from the DB
// and serves the static files, keeping the edge config static.

import (
	"io/fs"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// instaticEditorHost is the dedicated origin that serves the on-demand instatic
// editor. It's a single-label subdomain (covered by CF Universal SSL for
// *.menustudioai.com) and matches the wildcard nginx block → :8085. Override
// with INSTATIC_EDITOR_HOST.
func instaticEditorHost() string {
	if v := strings.TrimSpace(os.Getenv("INSTATIC_EDITOR_HOST")); v != "" {
		return strings.ToLower(v)
	}
	return "editor-dev.menustudioai.com"
}

// serveInstaticEditorIfHost reverse-proxies the whole origin to the restaurant's
// on-demand instatic instance (root path — so instatic's absolute /assets & /admin
// paths resolve). The instance session cookie is injected server-side, so the
// iframe is authenticated without a browser login. X-Frame-Options / CSP
// frame-ancestors are stripped so the backoffice can embed it.
// Returns true if it handled the request.
func (s *Server) serveInstaticEditorIfHost(w http.ResponseWriter, r *http.Request) bool {
	host := strings.ToLower(strings.TrimSpace(r.Host))
	host = strings.TrimSuffix(strings.TrimSuffix(host, ":80"), ":443")
	if host != instaticEditorHost() {
		return false
	}

	// ponytail: rid cookie threads multi-tenant; defaults to 1 (only active restaurant).
	rid := 1
	if q := strings.TrimSpace(r.URL.Query().Get("rid")); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 {
			rid = n
		}
		http.SetCookie(w, &http.Cookie{Name: "editor_rid", Value: strconv.Itoa(rid), Path: "/", Secure: true, SameSite: http.SameSiteLaxMode})
		// Strip ?rid on the top document so the SPA's later absolute requests
		// (which drop the query) still find the cookie.
		if r.Method == http.MethodGet && strings.Contains(r.Header.Get("Accept"), "text/html") {
			u := *r.URL
			u.RawQuery = ""
			http.Redirect(w, r, u.RequestURI(), http.StatusFound)
			return true
		}
	} else if c, err := r.Cookie("editor_rid"); err == nil {
		if n, err := strconv.Atoi(c.Value); err == nil && n > 0 {
			rid = n
		}
	}

	base, err := s.instatic.EnsureRunning(r.Context(), rid)
	if err != nil {
		http.Error(w, "editor start: "+err.Error(), http.StatusBadGateway)
		return true
	}
	_ = s.instatic.ensureBootstrapped(r.Context(), rid, base)
	token := s.instatic.sessionToken(rid)

	target, err := url.Parse(base)
	if err != nil {
		http.Error(w, "editor target: "+err.Error(), http.StatusBadGateway)
		return true
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	origDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		origDirector(req)
		req.Host = target.Host
		if token != "" {
			req.Header.Set("Cookie", mergeCookie(req.Header.Get("Cookie"), "instatic_admin_session", token))
		}
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		resp.Header.Del("X-Frame-Options")
		if csp := resp.Header.Get("Content-Security-Policy"); csp != "" {
			resp.Header.Set("Content-Security-Policy", stripFrameAncestors(csp))
		}
		return nil
	}
	proxy.ServeHTTP(w, r)
	return true
}

// mergeCookie returns the Cookie header value with name=val set (replacing any
// existing pair of that name).
func mergeCookie(existing, name, val string) string {
	out := name + "=" + val
	for _, part := range strings.Split(existing, ";") {
		part = strings.TrimSpace(part)
		if part == "" || strings.HasPrefix(part, name+"=") {
			continue
		}
		out += "; " + part
	}
	return out
}

// stripFrameAncestors drops the frame-ancestors directive so the editor can be
// embedded cross-origin by the backoffice.
func stripFrameAncestors(csp string) string {
	parts := strings.Split(csp, ";")
	kept := parts[:0]
	for _, p := range parts {
		if strings.HasPrefix(strings.TrimSpace(strings.ToLower(p)), "frame-ancestors") {
			continue
		}
		kept = append(kept, p)
	}
	return strings.Join(kept, ";")
}

// serveRestaurantSiteIfHost serves the restaurant website when the Host header
// matches `<slug>.<app_base_url>` OR a restaurant's custom domain (from
// restaurant_domains / site_builder_domain_mappings); otherwise falls through.
func (s *Server) serveRestaurantSiteIfHost(w http.ResponseWriter, r *http.Request, next http.Handler) {
	if s.serveInstaticEditorIfHost(w, r) {
		return
	}
	base := s.instatic.appBaseURL()
	host := strings.ToLower(strings.TrimSpace(r.Host))
	host = strings.TrimSuffix(host, ":80")
	host = strings.TrimSuffix(host, ":443")

	var restaurantID int
	resolved := false

	// Custom domain lookup first (a restaurant may have bought its own domain).
	err := s.db.QueryRowContext(r.Context(), `SELECT restaurant_id FROM restaurant_domains WHERE domain=? LIMIT 1`, host).Scan(&restaurantID)
	if err == nil && restaurantID > 0 {
		resolved = true
	}
	if !resolved {
		err = s.db.QueryRowContext(r.Context(), `SELECT restaurant_id FROM site_builder_domain_mappings WHERE domain=? LIMIT 1`, host).Scan(&restaurantID)
		if err == nil && restaurantID > 0 {
			resolved = true
		}
	}

	// Subdomain of app base: `<slug>.<base>`.
	if !resolved && strings.HasSuffix(host, "."+base) {
		slug := strings.TrimSuffix(host, "."+base)
		if slug != "" && !strings.Contains(slug, ".") {
			err = s.db.QueryRowContext(r.Context(), `SELECT id FROM restaurants WHERE slug=?`, slug).Scan(&restaurantID)
			if err == nil {
				resolved = true
			}
		}
	}

	if !resolved {
		next.ServeHTTP(w, r)
		return
	}

	// Booking widget API is served by the backend itself (same-origin so the
	// published page's CSP needs no cross-origin connect-src). Widget JS passes
	// ?restaurant_id=X, so no rewrite needed — just don't treat it as a static file.
	if strings.HasPrefix(r.URL.Path, "/widget/") {
		next.ServeHTTP(w, r)
		return
	}

	// Serve the published static bundle from disk. `current` is a symlink to the
	// active slot (a|b); os.DirFS follows it.
	root := filepath.Join(s.instatic.instanceDataDir(restaurantID), "uploads", "published", "current")
	fsys := os.DirFS(root)

	rel := staticRelPath(r.URL.Path)
	if fsFileExists(fsys, rel) {
		http.ServeFileFS(w, r, fsys, rel)
		return
	}
	// Baked 404 page (instatic bakes 404.html per slot).
	if b, rerr := fs.ReadFile(fsys, "404.html"); rerr == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write(b)
		return
	}
	// Never published (no bundle yet) → fall through to normal routing.
	next.ServeHTTP(w, r)
}

// staticRelPath maps a request URL path to a bundle-relative file path,
// mirroring instatic's urlToDiskRelPath (staticArtefact.ts): `/` → index.html,
// a page route → `<route>.html`, and a real asset (has an extension, e.g.
// /_instatic/css/x.css) → served verbatim. Returns an fs-valid path (no leading
// slash, cleaned) so os.DirFS rejects traversal.
func staticRelPath(urlPath string) string {
	p := strings.TrimPrefix(path.Clean("/"+urlPath), "/")
	if p == "" {
		return "index.html"
	}
	// ponytail: single-page restaurant templates — an extension means a literal
	// asset, anything else is a page → `.html`. Add trailing-slash→index.html if
	// dir-index page routes ever appear.
	if path.Ext(p) != "" {
		return p
	}
	return p + ".html"
}

func fsFileExists(fsys fs.FS, name string) bool {
	st, err := fs.Stat(fsys, name)
	return err == nil && !st.IsDir()
}
