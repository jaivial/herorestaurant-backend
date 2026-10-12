// Package cache provides a Redis-backed cache-aside layer for hot
// backoffice GET endpoints (coordination id: bo_redis_cache_v1).
//
// Design: the middleware caches JSON GET responses for selected /admin
// prefixes, keyed by session so per-user/per-restaurant data never leaks
// across sessions. Any mutating request (POST/PUT/PATCH/DELETE) under
// /admin flushes every cached entry, so writes never serve stale reads
// beyond the short TTL.
package cache

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	keyPrefix  = "bocache:v1:"
	defaultTTL = 15 * time.Second
)

// Cache wraps a Redis client. A nil *Cache is valid and disables caching.
type Cache struct {
	cli *redis.Client
	ttl time.Duration
}

// New connects to Redis at addr (host:port). It returns nil, nil when addr
// is empty, and nil, err when Redis does not answer within 500ms: callers
// treat both as "caching disabled".
func New(addr string) (*Cache, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil, nil
	}
	cli := redis.NewClient(&redis.Options{
		Addr:         addr,
		DialTimeout:  500 * time.Millisecond,
		ReadTimeout:  300 * time.Millisecond,
		WriteTimeout: 300 * time.Millisecond,
		PoolSize:     8,
		MaxRetries:   0,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if err := cli.Ping(ctx).Err(); err != nil {
		_ = cli.Close()
		return nil, err
	}
	return &Cache{cli: cli, ttl: defaultTTL}, nil
}

// cacheablePrefixes are the /admin path prefixes whose GET responses are
// cached. File downloads, exports and websocket upgrades are excluded.
var cacheablePrefixes = []string{
	"/admin/dashboard",
	"/admin/calendar",
	"/admin/bookings",
	"/admin/menus",
	"/admin/comida",
}

func cacheablePath(path string) bool {
	if strings.Contains(path, "/ws") || strings.Contains(path, ".pdf") || strings.Contains(path, "/export") {
		return false
	}
	for _, p := range cacheablePrefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// sessionKey scopes entries to the caller's session token so cached data
// never crosses users or restaurants. Falls back to client IP when no
// session cookie is present (pre-auth routes are not cached anyway).
func sessionKey(r *http.Request, cookieName string) string {
	seed := r.RemoteAddr
	if c, err := r.Cookie(cookieName); err == nil && c.Value != "" {
		seed = c.Value
	}
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:8])
}

// Middleware caches GET responses for cacheablePath prefixes and flushes
// all cached entries after any mutating /admin request completes.
func (c *Cache) Middleware(cookieName string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if c == nil || !strings.HasPrefix(r.URL.Path, "/admin/") {
				next.ServeHTTP(w, r)
				return
			}

			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				next.ServeHTTP(w, r)
				c.flush()
				return
			}

			if !cacheablePath(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			key := keyPrefix + sessionKey(r, cookieName) + ":" + r.URL.RequestURI()
			ctx, cancel := context.WithTimeout(r.Context(), 400*time.Millisecond)
			defer cancel()

			if cached, err := c.cli.HGetAll(ctx, key).Result(); err == nil && len(cached) > 0 {
				if ct := cached["content-type"]; ct != "" {
					w.Header().Set("Content-Type", ct)
				}
				w.Header().Set("X-BO-Cache", "HIT")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(cached["body"]))
				return
			}

			rec := &responseRecorder{status: http.StatusOK, header: http.Header{}}
			next.ServeHTTP(rec, r)

			ct := rec.header.Get("Content-Type")
			if rec.status == http.StatusOK && strings.Contains(ct, "application/json") && rec.header.Get("Set-Cookie") == "" {
				stored := map[string]string{
					"content-type": ct,
					"body":         rec.body.String(),
				}
				if err := c.cli.HSet(ctx, key, stored).Err(); err == nil {
					c.cli.Expire(ctx, key, c.ttl)
				}
			}

			copyHeader(w.Header(), rec.header)
			w.Header().Set("X-BO-Cache", "MISS")
			w.WriteHeader(rec.status)
			_, _ = w.Write([]byte(rec.body.String()))
		})
	}
}

// flush deletes every cached entry. Blunt by design: any admin write
// invalidates all cached admin reads. Errors are logged, never fatal.
func (c *Cache) flush() {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	var cursor uint64
	for {
		keys, next, err := c.cli.Scan(ctx, cursor, keyPrefix+"*", 500).Result()
		if err != nil {
			if !errors.Is(err, redis.Nil) {
				log.Printf("[cache] flush scan error: %v", err)
			}
			return
		}
		if len(keys) > 0 {
			if err := c.cli.Del(ctx, keys...).Err(); err != nil {
				log.Printf("[cache] flush del error: %v", err)
			}
		}
		if next == 0 {
			return
		}
		cursor = next
	}
}

// responseRecorder captures the handler's response so it can be replayed
// upstream (through chi's compressor) and optionally stored.
type responseRecorder struct {
	status int
	header http.Header
	body   strings.Builder
}

func (r *responseRecorder) WriteHeader(status int) { r.status = status }

func (r *responseRecorder) Write(b []byte) (int, error) {
	r.body.Write(b)
	return len(b), nil
}

func (r *responseRecorder) Header() http.Header { return r.header }

// Flush lets streaming handlers run without breaking; cached prefixes
// exclude SSE/WS routes, so this is only a safety valve.
func (r *responseRecorder) Flush() {}

// Hijack rejects connection hijacking (websockets) under the recorder.
func (r *responseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errors.New("cache recorder does not support hijacking")
}

func copyHeader(dst, src http.Header) {
	for k := range dst {
		delete(dst, k)
	}
	for k, vs := range src {
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}
