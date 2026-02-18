package api

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"preactvillacarmen/internal/httpx"
)

type restaurantIDCtxKey int

const restaurantIDKey restaurantIDCtxKey = 1

func withRestaurantID(ctx context.Context, restaurantID int) context.Context {
	return context.WithValue(ctx, restaurantIDKey, restaurantID)
}

func restaurantIDFromContext(ctx context.Context) (int, bool) {
	v := ctx.Value(restaurantIDKey)
	if v == nil {
		return 0, false
	}
	id, ok := v.(int)
	return id, ok && id > 0
}

type tenantDomainCacheEntry struct {
	restaurantID int
	expiresAt    time.Time
}

type tenantDomainCache struct {
	mu     sync.RWMutex
	byHost map[string]tenantDomainCacheEntry
}

func (c *tenantDomainCache) get(host string, now time.Time) (int, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.byHost == nil {
		return 0, false
	}
	ent, ok := c.byHost[host]
	if !ok {
		return 0, false
	}
	if now.After(ent.expiresAt) {
		return 0, false
	}
	return ent.restaurantID, ent.restaurantID > 0
}

func (c *tenantDomainCache) set(host string, restaurantID int, now time.Time, ttl time.Duration) {
	if host == "" || restaurantID <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.byHost == nil {
		c.byHost = make(map[string]tenantDomainCacheEntry)
	}
	c.byHost[host] = tenantDomainCacheEntry{
		restaurantID: restaurantID,
		expiresAt:    now.Add(ttl),
	}
}

func firstForwardedHost(r *http.Request) string {
	raw := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if raw == "" {
		raw = strings.TrimSpace(r.Host)
	}
	if raw == "" {
		return ""
	}
	// X-Forwarded-Host can be a comma-separated list.
	if strings.Contains(raw, ",") {
		raw = strings.TrimSpace(strings.Split(raw, ",")[0])
	}
	return raw
}

func stripPort(hostport string) string {
	hostport = strings.TrimSpace(hostport)
	if hostport == "" {
		return ""
	}
	// IPv6 bracket form: [::1]:8080
	if strings.HasPrefix(hostport, "[") {
		if h, _, err := net.SplitHostPort(hostport); err == nil {
			return strings.Trim(h, "[]")
		}
		return strings.Trim(hostport, "[]")
	}
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	// Best-effort fallback for "example.com:8080".
	if strings.Count(hostport, ":") == 1 {
		if h, _, ok := strings.Cut(hostport, ":"); ok {
			return h
		}
	}
	return hostport
}

func normalizedTenantHost(r *http.Request) string {
	host := strings.ToLower(stripPort(firstForwardedHost(r)))
	host = strings.TrimSpace(host)
	return host
}

func (s *Server) lookupRestaurantIDByDomain(ctx context.Context, host string) (int, error) {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return 0, nil
	}

	var restaurantID int
	err := s.db.QueryRowContext(ctx, "SELECT restaurant_id FROM restaurant_domains WHERE domain = ? LIMIT 1", host).Scan(&restaurantID)
	if err == nil {
		return restaurantID, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		// Common fallback: if host is www.* try without it.
		if strings.HasPrefix(host, "www.") {
			return s.lookupRestaurantIDByDomain(ctx, strings.TrimPrefix(host, "www."))
		}
		return 0, nil
	}
	return 0, err
}

func (s *Server) restaurantFromRequest(r *http.Request) (int, bool, string) {
	// Optional override for trusted callers (internal automation / admin token).
	// Never honor this for unauthenticated public traffic.
	if raw := strings.TrimSpace(r.Header.Get("X-Restaurant-Id")); raw != "" {
		if id, err := strconv.Atoi(raw); err == nil && id > 0 {
			// Admin token present and valid?
			if strings.TrimSpace(s.cfg.AdminToken) != "" {
				adminToken := strings.TrimSpace(r.Header.Get("X-Admin-Token"))
				if adminToken == "" {
					authz := strings.TrimSpace(r.Header.Get("Authorization"))
					if strings.HasPrefix(strings.ToLower(authz), "bearer ") {
						adminToken = strings.TrimSpace(authz[len("bearer "):])
					}
				}
				if adminToken != "" && adminToken == strings.TrimSpace(s.cfg.AdminToken) {
					return id, true, "header:admin"
				}
			}
			// Internal token present and valid?
			if validateInternalAPIToken(r) {
				return id, true, "header:internal"
			}
		}
	}

	host := normalizedTenantHost(r)
	if host == "" {
		if def := strings.TrimSpace(os.Getenv("DEFAULT_RESTAURANT_ID")); def != "" {
			if id, err := strconv.Atoi(def); err == nil && id > 0 {
				return id, true, "env:default"
			}
		}
		return 0, false, "host:empty"
	}

	now := time.Now()
	if id, ok := s.tenantCache.get(host, now); ok {
		return id, true, "cache"
	}

	id, err := s.lookupRestaurantIDByDomain(r.Context(), host)
	if err != nil {
		return 0, false, "db:error"
	}
	if id <= 0 {
		if def := strings.TrimSpace(os.Getenv("DEFAULT_RESTAURANT_ID")); def != "" {
			if defID, err := strconv.Atoi(def); err == nil && defID > 0 {
				return defID, true, "env:default"
			}
		}
		return 0, false, "host:unknown"
	}

	s.tenantCache.set(host, id, now, 5*time.Minute)
	return id, true, "db"
}

func (s *Server) withRestaurant(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		restaurantID, ok, _ := s.restaurantFromRequest(r)
		if !ok || restaurantID <= 0 {
			httpx.WriteError(w, http.StatusNotFound, "Unknown restaurant")
			return
		}
		next.ServeHTTP(w, r.WithContext(withRestaurantID(r.Context(), restaurantID)))
	})
}

