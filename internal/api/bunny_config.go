package api

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"time"
)

// bunnyCredentials holds the resolved BunnyCDN settings for one restaurant.
// Only the public zone (zone/key/pull URL) is configurable per restaurant; the
// members and private zones are still process-wide env values.
type bunnyCredentials struct {
	StorageZone        string
	StorageKey         string
	PullBaseURL        string
	MemberStorageZone  string
	MemberStorageKey   string
	MemberPullBaseURL  string
	PrivateStorageZone string
	PrivateStorageKey  string
}

// bunnyCredentialsTTL bounds how long a saved credential change takes to reach
// in-flight workers. Saves invalidate eagerly, so this only covers other replicas.
const bunnyCredentialsTTL = 60 * time.Second

// ponytail: single-instance in-memory cache. The bunny_storage_config row is the
// source of truth; saves invalidate the entry for this process. With multiple
// backend replicas a stale entry can survive up to bunnyCredentialsTTL.
type bunnyCredentialsCache struct {
	mu    sync.RWMutex
	items map[int]bunnyCredentialsCacheEntry
}

type bunnyCredentialsCacheEntry struct {
	creds    bunnyCredentials
	cachedAt time.Time
}

func newBunnyCredentialsCache() *bunnyCredentialsCache {
	return &bunnyCredentialsCache{items: make(map[int]bunnyCredentialsCacheEntry)}
}

func (c *bunnyCredentialsCache) get(restaurantID int, now time.Time) (bunnyCredentials, bool) {
	c.mu.RLock()
	e, ok := c.items[restaurantID]
	c.mu.RUnlock()
	if !ok || now.Sub(e.cachedAt) > bunnyCredentialsTTL {
		return bunnyCredentials{}, false
	}
	return e.creds, true
}

func (c *bunnyCredentialsCache) set(restaurantID int, creds bunnyCredentials, now time.Time) {
	c.mu.Lock()
	c.items[restaurantID] = bunnyCredentialsCacheEntry{creds: creds, cachedAt: now}
	c.mu.Unlock()
}

func (c *bunnyCredentialsCache) invalidate(restaurantID int) {
	c.mu.Lock()
	delete(c.items, restaurantID)
	c.mu.Unlock()
}

// envBunnyCredentials returns the process-wide fallback used when a restaurant
// has no row, or leaves individual fields blank.
func (s *Server) envBunnyCredentials() bunnyCredentials {
	return bunnyCredentials{
		StorageZone:        strings.TrimSpace(s.cfg.BunnyStorageZone),
		StorageKey:         strings.TrimSpace(s.cfg.BunnyStorageKey),
		PullBaseURL:        strings.TrimSpace(s.cfg.BunnyPullBaseURL),
		MemberStorageZone:  strings.TrimSpace(s.cfg.BunnyMemberStorageZone),
		MemberStorageKey:   strings.TrimSpace(s.cfg.BunnyMemberStorageKey),
		MemberPullBaseURL:  strings.TrimSpace(s.cfg.BunnyMemberPullBaseURL),
		PrivateStorageZone: strings.TrimSpace(s.cfg.BunnyPrivateStorageZone),
		PrivateStorageKey:  strings.TrimSpace(s.cfg.BunnyPrivateStorageKey),
	}
}

// bunnyCreds resolves the credentials for a restaurant: the stored public zone
// wins field by field, anything left blank falls back to the env config. A row
// with is_active = 0 is ignored entirely so an operator can disable it without
// clearing the stored key.
func (s *Server) bunnyCreds(ctx context.Context, restaurantID int) bunnyCredentials {
	env := s.envBunnyCredentials()
	if restaurantID <= 0 || s.db == nil {
		return env
	}
	now := time.Now()
	if cached, ok := s.bunnyCredsCache.get(restaurantID, now); ok {
		return cached
	}

	stored, found, err := s.loadBunnyStorageRow(ctx, restaurantID)
	if err != nil {
		// Missing table or transient DB error: keep serving with env values
		// rather than breaking every upload and media URL.
		return env
	}
	creds := env
	if found && stored.IsActive {
		creds = mergeBunnyCredentials(env, stored.creds)
	}
	s.bunnyCredsCache.set(restaurantID, creds, now)
	return creds
}

func mergeBunnyCredentials(env, db bunnyCredentials) bunnyCredentials {
	out := env
	if v := strings.TrimSpace(db.StorageZone); v != "" {
		out.StorageZone = v
	}
	if v := strings.TrimSpace(db.StorageKey); v != "" {
		out.StorageKey = v
	}
	if v := strings.TrimSpace(db.PullBaseURL); v != "" {
		out.PullBaseURL = v
	}
	return out
}

type bunnyStorageRow struct {
	creds    bunnyCredentials
	IsActive bool
}

func (s *Server) loadBunnyStorageRow(ctx context.Context, restaurantID int) (bunnyStorageRow, bool, error) {
	var (
		out             bunnyStorageRow
		zone, key, pull sql.NullString
		isActiveInt     int
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT storage_zone, storage_access_key, pull_base_url, is_active
		FROM bunny_storage_config
		WHERE restaurant_id = ?
		LIMIT 1
	`, restaurantID).Scan(&zone, &key, &pull, &isActiveInt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, false, nil
		}
		return out, false, err
	}
	out.creds = bunnyCredentials{
		StorageZone: strings.TrimSpace(zone.String),
		StorageKey:  strings.TrimSpace(key.String),
		PullBaseURL: strings.TrimSpace(pull.String),
	}
	out.IsActive = isActiveInt != 0
	return out, true, nil
}
