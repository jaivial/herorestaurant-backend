package api

// Restaurant website virtual host: dispatches `Host: <slug>.<app_base_url>`
// to that restaurant's instatic instance port via reverse proxy.
//
// nginx routes every `*.app_base_url` subdomain here (one upstream), and this
// handler resolves restaurant → instatic port from instatic_instances. This
// keeps the edge config static while restaurant→port mapping lives in the DB.

import (
	"context"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// serveRestaurantSiteIfHost serves the restaurant website when the Host header
// matches `<slug>.<app_base_url>`; otherwise it falls through to next (normal
// routing for the admin API / health / etc).
func (s *Server) serveRestaurantSiteIfHost(w http.ResponseWriter, r *http.Request, next http.Handler) {
	base := s.instatic.appBaseURL()
	host := strings.ToLower(strings.TrimSpace(r.Host))
	// Match `<slug>.<base>` exactly (one label before the base).
	if !strings.HasSuffix(host, "."+base) {
		next.ServeHTTP(w, r)
		return
	}
	slug := strings.TrimSuffix(host, "."+base)
	if slug == "" || strings.Contains(slug, ".") {
		next.ServeHTTP(w, r)
		return
	}

	var restaurantID int
	err := s.db.QueryRowContext(r.Context(), `SELECT id FROM restaurants WHERE slug=?`, slug).Scan(&restaurantID)
	if err != nil {
		next.ServeHTTP(w, r)
		return
	}

	// Ensure the instance is running (best-effort; proxy handles a miss).
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	_, _ = s.instatic.EnsureRunning(ctx, restaurantID)

	port := s.instatic.portFor(restaurantID)
	target, err := url.Parse("http://127.0.0.1:" + strconv.Itoa(port))
	if err != nil {
		http.Error(w, "bad upstream", http.StatusInternalServerError)
		return
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("[restaurant-proxy] %s → %s: %v", host, target, err)
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
	}
	proxy.ServeHTTP(w, r)
}
