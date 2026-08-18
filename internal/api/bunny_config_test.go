package api

import (
	"testing"
	"time"
)

func TestMergeBunnyCredentials(t *testing.T) {
	env := bunnyCredentials{
		StorageZone:        "env-zone",
		StorageKey:         "env-key",
		PullBaseURL:        "https://env.b-cdn.net",
		MemberStorageZone:  "env-members",
		MemberStorageKey:   "env-members-key",
		MemberPullBaseURL:  "https://env-members.b-cdn.net",
		PrivateStorageZone: "env-private",
		PrivateStorageKey:  "env-private-key",
	}

	t.Run("empty db row keeps every env value", func(t *testing.T) {
		got := mergeBunnyCredentials(env, bunnyCredentials{})
		if got != env {
			t.Fatalf("expected env passthrough, got %+v", got)
		}
	})

	t.Run("db overrides only the public zone", func(t *testing.T) {
		got := mergeBunnyCredentials(env, bunnyCredentials{
			StorageZone: "db-zone",
			StorageKey:  "db-key",
			PullBaseURL: "https://db.b-cdn.net",
		})
		if got.StorageZone != "db-zone" || got.StorageKey != "db-key" || got.PullBaseURL != "https://db.b-cdn.net" {
			t.Fatalf("public zone not overridden: %+v", got)
		}
		// Members/private are env-only now, so they must survive untouched.
		if got.MemberStorageZone != "env-members" || got.PrivateStorageKey != "env-private-key" {
			t.Fatalf("unset zones should fall back to env: %+v", got)
		}
	})

	t.Run("whitespace-only db values do not clobber env", func(t *testing.T) {
		got := mergeBunnyCredentials(env, bunnyCredentials{StorageZone: "   ", StorageKey: "\t"})
		if got.StorageZone != "env-zone" || got.StorageKey != "env-key" {
			t.Fatalf("blank db values should be ignored: %+v", got)
		}
	})
}

func TestValidateBunnyPullBaseURL(t *testing.T) {
	valid := []string{"", "   ", "https://zone.b-cdn.net", "http://localhost:8080", "https://cdn.example.com/prefix"}
	for _, in := range valid {
		if err := validateBunnyPullBaseURL(in); err != nil {
			t.Fatalf("validateBunnyPullBaseURL(%q) = %v; want nil", in, err)
		}
	}

	invalid := []string{"zone.b-cdn.net", "ftp://zone.example.com", "https://", "://nope"}
	for _, in := range invalid {
		if err := validateBunnyPullBaseURL(in); err == nil {
			t.Fatalf("validateBunnyPullBaseURL(%q) = nil; want error", in)
		}
	}
}

func TestValidateBunnyStorageZone(t *testing.T) {
	// Empty means "fall back to env"; a bare zone name is what Bunny expects.
	for _, in := range []string{"", "   ", "villacarmen", "mi-zona-123"} {
		if err := validateBunnyStorageZone(in); err != nil {
			t.Fatalf("validateBunnyStorageZone(%q) = %v; want nil", in, err)
		}
	}
	// Bunny shows the zone as https://storage.bunnycdn.com/<zone>; pasting the
	// whole endpoint is the easy mistake and must be rejected.
	for _, in := range []string{
		"https://storage.bunnycdn.com/villacarmen",
		"storage.bunnycdn.com/villacarmen",
		"villacarmen/subdir",
	} {
		if err := validateBunnyStorageZone(in); err == nil {
			t.Fatalf("validateBunnyStorageZone(%q) = nil; want error", in)
		}
	}
}

func TestBunnyCredentialsCache(t *testing.T) {
	c := newBunnyCredentialsCache()
	now := time.Now()
	creds := bunnyCredentials{StorageZone: "zone-1"}

	if _, ok := c.get(1, now); ok {
		t.Fatal("empty cache should miss")
	}

	c.set(1, creds, now)
	got, ok := c.get(1, now)
	if !ok || got.StorageZone != "zone-1" {
		t.Fatalf("expected hit with zone-1, got %+v ok=%v", got, ok)
	}

	// Entries must not leak across restaurants.
	if _, ok := c.get(2, now); ok {
		t.Fatal("restaurant 2 should not read restaurant 1 credentials")
	}

	if _, ok := c.get(1, now.Add(bunnyCredentialsTTL+time.Second)); ok {
		t.Fatal("entry should expire after the TTL")
	}

	c.set(1, creds, now)
	c.invalidate(1)
	if _, ok := c.get(1, now); ok {
		t.Fatal("invalidate should drop the entry so a save takes effect immediately")
	}
}

func TestBunnyCredsFallsBackToEnvWithoutRestaurant(t *testing.T) {
	s := &Server{bunnyCredsCache: newBunnyCredentialsCache()}
	s.cfg.BunnyStorageZone = "env-zone"
	s.cfg.BunnyStorageKey = "env-key"
	s.cfg.BunnyPullBaseURL = "https://env.b-cdn.net"

	// restaurantID 0 and a nil DB must not panic; both return the env config.
	got := s.bunnyCreds(t.Context(), 0)
	if got.StorageZone != "env-zone" || got.PullBaseURL != "https://env.b-cdn.net" {
		t.Fatalf("expected env credentials, got %+v", got)
	}
	if got := s.bunnyCreds(t.Context(), 7); got.StorageZone != "env-zone" {
		t.Fatalf("nil db should fall back to env, got %+v", got)
	}
}
