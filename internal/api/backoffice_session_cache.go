package api

import (
	"sync"
	"time"
)

// boSessionCacheEntry holds everything requireBOSession needs to authorize a
// request without touching the DB: the resolved boAuth plus the session's
// last_seen / expiry timestamps used for the moving-expiration header.
type boSessionCacheEntry struct {
	auth       boAuth
	lastSeenAt time.Time
	expiresAt  time.Time
	cachedAt   time.Time
}

// boSessionCache is a small TTL cache for session+roles lookups, keyed by the
// token's SHA-256. Every backoffice request currently pays 2-3 DB queries
// (session JOIN + role permissions) before its handler runs; the reservas SSR
// fans out 5 parallel requests, multiplying that cost. A short TTL amortizes it.
//
// ponytail: single-instance in-memory cache. The session row is still the
// source of truth; a 30s TTL bounds staleness after a role/permission change,
// and logout / restaurant-switch invalidate eagerly. If you run multiple
// backend replicas, move this to Redis and key on the session token.
type boSessionCache struct {
	mu    sync.Mutex
	ttl   time.Duration
	items map[string]boSessionCacheEntry
}

func newBOSessionCache(ttl time.Duration) *boSessionCache {
	return &boSessionCache{
		ttl:   ttl,
		items: make(map[string]boSessionCacheEntry),
	}
}

func (c *boSessionCache) get(tokenSHA string, now time.Time) (boSessionCacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[tokenSHA]
	if !ok {
		return boSessionCacheEntry{}, false
	}
	if now.Sub(e.cachedAt) > c.ttl || !now.Before(e.expiresAt) {
		delete(c.items, tokenSHA)
		return boSessionCacheEntry{}, false
	}
	return e, true
}

func (c *boSessionCache) set(tokenSHA string, e boSessionCacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[tokenSHA] = e
}

func (c *boSessionCache) invalidate(tokenSHA string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.items, tokenSHA)
}
