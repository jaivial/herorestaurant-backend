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

	var databaseName string
	if err := db.QueryRow(`SELECT DATABASE()`).Scan(&databaseName); err != nil {
		db.Close()
		t.Fatalf("read database name: %v", err)
	}
	name := strings.ToLower(databaseName)
	isTestDatabase := strings.Contains(name, "test") || strings.Contains(name, "sandbox")
	if !isTestDatabase && os.Getenv("BUNNY_TEST_ALLOW_NON_TEST_DB") != "1" {
		db.Close()
		t.Fatalf("refusing destructive test against database %q; use a test/sandbox database or set BUNNY_TEST_ALLOW_NON_TEST_DB=1", databaseName)
	}

	// The handler writes through Server.db, so the test cannot run inside a
	// transaction. Use a dedicated restaurant and restore its original row on
	// cleanup to keep an explicit non-test run reversible.
	const testRestaurantID = 987654321
	type storedConfigRow struct {
		id               int
		restaurantID     int
		storageZone      sql.NullString
		storageAccessKey sql.NullString
		pullBaseURL      sql.NullString
		isActive         int
		updatedAt        sql.NullTime
		updatedByUserID  sql.NullInt64
	}
	var previousRows []storedConfigRow
	rows, err := db.Query(`
		SELECT id, restaurant_id, storage_zone, storage_access_key,
		       pull_base_url, is_active, updated_at, updated_by_user_id
		FROM bunny_storage_config
		WHERE restaurant_id = ?
	`, testRestaurantID)
	if err != nil {
		db.Close()
		t.Fatalf("snapshot existing config: %v", err)
	}
	for rows.Next() {
		var row storedConfigRow
		if err := rows.Scan(
			&row.id,
			&row.restaurantID,
			&row.storageZone,
			&row.storageAccessKey,
			&row.pullBaseURL,
			&row.isActive,
			&row.updatedAt,
			&row.updatedByUserID,
		); err != nil {
			rows.Close()
			db.Close()
			t.Fatalf("read existing config snapshot: %v", err)
		}
		previousRows = append(previousRows, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		db.Close()
		t.Fatalf("iterate existing config snapshot: %v", err)
	}
	if err := rows.Close(); err != nil {
		db.Close()
		t.Fatalf("close existing config snapshot: %v", err)
	}

	t.Cleanup(func() {
		tx, err := db.BeginTx(context.Background(), nil)
		if err != nil {
			t.Errorf("restore config cleanup: %v", err)
			db.Close()
			return
		}
		if _, err := tx.Exec(`DELETE FROM bunny_storage_config WHERE restaurant_id = ?`, testRestaurantID); err != nil {
			tx.Rollback()
			t.Errorf("delete test config: %v", err)
			db.Close()
			return
		}
		for _, row := range previousRows {
			_, err := tx.Exec(`
				INSERT INTO bunny_storage_config
					(id, restaurant_id, storage_zone, storage_access_key,
					 pull_base_url, is_active, updated_at, updated_by_user_id)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			`, row.id, row.restaurantID, row.storageZone, row.storageAccessKey,
				row.pullBaseURL, row.isActive, row.updatedAt, row.updatedByUserID)
			if err != nil {
				tx.Rollback()
				t.Errorf("restore config row: %v", err)
				db.Close()
				return
			}
		}
		if err := tx.Commit(); err != nil {
			t.Errorf("commit restored config: %v", err)
		}
		db.Close()
	})

	if _, err := db.Exec(`DELETE FROM bunny_storage_config WHERE restaurant_id = ?`, testRestaurantID); err != nil {
		t.Fatalf("cleanup test restaurant: %v", err)
	}

	s := NewServer(db, config.Config{
		BunnyStorageZone: "env-zone",
		BunnyStorageKey:  "env-key",
		BunnyPullBaseURL: "https://env.b-cdn.net",
	})

	post := func(t *testing.T, body string) map[string]any {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/admin/config/bunny-storage", strings.NewReader(body))
		req = req.WithContext(withBOAuth(context.Background(), boAuth{ActiveRestaurantID: testRestaurantID, Role: "root", User: boUser{ID: 7}}))
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
		got := s.bunnyCreds(context.Background(), testRestaurantID)
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
		got := s.bunnyCreds(context.Background(), testRestaurantID)
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
		got := s.bunnyCreds(context.Background(), testRestaurantID)
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
		req = req.WithContext(withBOAuth(context.Background(), boAuth{ActiveRestaurantID: testRestaurantID, Role: "root", User: boUser{ID: 7}}))
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
