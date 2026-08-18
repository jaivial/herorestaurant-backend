package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	"preactvillacarmen/internal/config"
)

// Exercises the real GET/POST handlers against MySQL: secrets must never leave
// the server, blank keys must preserve stored values, and a save must be
// visible to bunnyCreds immediately (cache invalidation).
func TestBunnyStorageConfigEndpointsIntegration(t *testing.T) {
	dsn := os.Getenv("BUNNY_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("BUNNY_TEST_MYSQL_DSN not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`DELETE FROM bunny_storage_config`); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	s := NewServer(db, config.Config{
		BunnyStorageZone: "env-zone",
		BunnyStorageKey:  "env-key",
		BunnyPullBaseURL: "https://env.b-cdn.net",
	})

	post := func(t *testing.T, body string) map[string]any {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/admin/config/bunny-storage", strings.NewReader(body))
		req = req.WithContext(withBOAuth(context.Background(), boAuth{ActiveRestaurantID: 1, Role: "root", User: boUser{ID: 7}}))
		rec := httptest.NewRecorder()
		s.handleBOBunnyStorageConfigSet(rec, req)
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v (%s)", err, rec.Body.String())
		}
		return out
	}

	t.Run("rejects activation without the full public trio", func(t *testing.T) {
		out := post(t, `{"storageZone":"z","isActive":true}`)
		if out["success"] != false {
			t.Fatalf("expected refusal, got %v", out)
		}
	})

	t.Run("rejects a malformed pull url", func(t *testing.T) {
		out := post(t, `{"pullBaseUrl":"not-a-url"}`)
		if out["success"] != false {
			t.Fatalf("expected validation failure, got %v", out)
		}
	})

	t.Run("rejects the full storage endpoint url as the zone", func(t *testing.T) {
		out := post(t, `{"storageZone":"https://storage.bunnycdn.com/villacarmen"}`)
		if out["success"] != false {
			t.Fatalf("expected the full URL to be rejected, got %v", out)
		}
	})

	t.Run("saves and never returns the secret", func(t *testing.T) {
		out := post(t, `{"storageZone":"tenant-zone","storageKey":"super-secret-key","pullBaseUrl":"https://tenant.b-cdn.net","isActive":true}`)
		if out["success"] != true {
			t.Fatalf("save failed: %v", out)
		}
		raw, _ := json.Marshal(out)
		if strings.Contains(string(raw), "super-secret-key") {
			t.Fatalf("response leaked the access key: %s", raw)
		}
		cfg := out["config"].(map[string]any)
		if cfg["hasStorageKey"] != true {
			t.Fatalf("hasStorageKey should be true: %v", cfg)
		}
		if cfg["usingEnvFallback"] != false {
			t.Fatalf("active row should stop env fallback: %v", cfg)
		}
	})

	t.Run("saved credentials take effect immediately", func(t *testing.T) {
		got := s.bunnyCreds(context.Background(), 1)
		if got.StorageZone != "tenant-zone" || got.StorageKey != "super-secret-key" {
			t.Fatalf("bunnyCreds did not pick up the save: %+v", got)
		}
		// Members/private are env-only and were not set on this Server.
		if got.MemberStorageZone != "" {
			t.Fatalf("unset member zone should stay empty, got %q", got.MemberStorageZone)
		}
	})

	t.Run("blank key keeps the stored secret", func(t *testing.T) {
		out := post(t, `{"storageZone":"renamed-zone","storageKey":"","pullBaseUrl":"https://tenant.b-cdn.net","isActive":true}`)
		if out["success"] != true {
			t.Fatalf("save failed: %v", out)
		}
		got := s.bunnyCreds(context.Background(), 1)
		if got.StorageKey != "super-secret-key" {
			t.Fatalf("empty key should preserve the stored one, got %q", got.StorageKey)
		}
		if got.StorageZone != "renamed-zone" {
			t.Fatalf("zone should have been renamed, got %q", got.StorageZone)
		}
	})

	t.Run("deactivating reverts to env credentials", func(t *testing.T) {
		if out := post(t, `{"isActive":false}`); out["success"] != true {
			t.Fatalf("save failed: %v", out)
		}
		got := s.bunnyCreds(context.Background(), 1)
		if got.StorageZone != "env-zone" || got.StorageKey != "env-key" {
			t.Fatalf("inactive row should fall back to env, got %+v", got)
		}
	})

	t.Run("other restaurants are unaffected", func(t *testing.T) {
		got := s.bunnyCreds(context.Background(), 999)
		if got.StorageZone != "env-zone" {
			t.Fatalf("restaurant without a row should use env, got %+v", got)
		}
	})

	t.Run("get masks the key", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/config/bunny-storage", nil)
		req = req.WithContext(withBOAuth(context.Background(), boAuth{ActiveRestaurantID: 1, Role: "root", User: boUser{ID: 7}}))
		rec := httptest.NewRecorder()
		s.handleBOBunnyStorageConfigGet(rec, req)
		if strings.Contains(rec.Body.String(), "super-secret-key") {
			t.Fatalf("GET leaked the access key: %s", rec.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		cfg := out["config"].(map[string]any)
		if mask, _ := cfg["storageKeyMask"].(string); !strings.HasSuffix(mask, "-key") {
			t.Fatalf("expected a masked preview, got %q", mask)
		}
	})
}
