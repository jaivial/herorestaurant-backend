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

func setupMiniMaxTestServer(t *testing.T) (*Server, *sql.DB) {
	t.Helper()
	dsn := os.Getenv("BUNNY_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("BUNNY_TEST_MYSQL_DSN not set (skipping integration)")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM restaurant_minimax_config`); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	s := NewServer(db, config.Config{
		VaultToken:    "integration-test-vault-token-123456",
		MiniMaxAPIKey: "env-fallback-key",
		MiniMaxModel:  "MiniMax-M3",
	})
	return s, db
}

func minimaxReq(t *testing.T, h func(w http.ResponseWriter, r *http.Request), method, body string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(method, "/admin/config/minimax", strings.NewReader(body))
	req = req.WithContext(withBOAuth(context.Background(), boAuth{ActiveRestaurantID: 1, Role: "root", User: boUser{ID: 7}}))
	rec := httptest.NewRecorder()
	h(rec, req)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	return out
}

func TestMiniMaxConfigEndpointsIntegration(t *testing.T) {
	s, db := setupMiniMaxTestServer(t)
	defer db.Close()

	t.Run("get returns env fallback state before save", func(t *testing.T) {
		out := minimaxReq(t, s.handleBOMiniMaxConfigGet, http.MethodGet, "")
		if out["success"] != true {
			t.Fatalf("expected success, got %v", out)
		}
		cfg, _ := out["config"].(map[string]any)
		if cfg == nil || cfg["has_api_key"] != false {
			t.Fatalf("expected has_api_key=false before save, got %v", out)
		}
	})

	t.Run("save with key encrypts and never returns plaintext", func(t *testing.T) {
		out := minimaxReq(t, s.handleBOMiniMaxConfigSet, http.MethodPost, `{"api_key":"sk-super-secret-999","model":"MiniMax-M2"}`)
		if out["success"] != true {
			t.Fatalf("save failed: %v", out)
		}
		raw, _ := json.Marshal(out)
		if strings.Contains(string(raw), "sk-super-secret-999") {
			t.Fatalf("plaintext api key leaked in response: %s", raw)
		}

		// Row must hold the encrypted form only.
		var enc string
		if err := db.QueryRow(`SELECT api_key_encrypted FROM restaurant_minimax_config WHERE restaurant_id = 1`).Scan(&enc); err != nil {
			t.Fatalf("row missing: %v", err)
		}
		if strings.Contains(enc, "sk-super-secret-999") {
			t.Fatalf("api key stored in plaintext in DB!")
		}

		// Cache must reflect the just-saved config.
		got, ok := s.minimaxStoreCache.get(1)
		if !ok || got.APIKey != "sk-super-secret-999" || got.Model != "MiniMax-M2" {
			t.Fatalf("cache did not update after save: %+v (found=%v)", got, ok)
		}
	})

	t.Run("get shows has_api_key=true and the model", func(t *testing.T) {
		out := minimaxReq(t, s.handleBOMiniMaxConfigGet, http.MethodGet, "")
		cfg, _ := out["config"].(map[string]any)
		if cfg == nil || cfg["has_api_key"] != true || cfg["model"] != "MiniMax-M2" {
			t.Fatalf("unexpected get after save: %v", out)
		}
	})

	t.Run("blank key preserves the stored key (model-only update)", func(t *testing.T) {
		out := minimaxReq(t, s.handleBOMiniMaxConfigSet, http.MethodPost, `{"api_key":"","model":"MiniMax-M3"}`)
		if out["success"] != true {
			t.Fatalf("model-only update failed: %v", out)
		}
		got, _ := s.minimaxStoreCache.get(1)
		if got.APIKey != "sk-super-secret-999" {
			t.Fatalf("blank key should preserve stored key, got %+v", got)
		}
	})

	t.Run("key-only update preserves the stored model", func(t *testing.T) {
		out := minimaxReq(t, s.handleBOMiniMaxConfigSet, http.MethodPost, `{"api_key":"sk-new-key-456"}`)
		if out["success"] != true {
			t.Fatalf("key-only update failed: %v", out)
		}
		cfg, _ := out["config"].(map[string]any)
		if cfg["model"] != "MiniMax-M3" {
			t.Fatalf("key-only update must keep stored model, got %v", out)
		}
		var enc string
		var model string
		if err := db.QueryRow(`SELECT api_key_encrypted, model FROM restaurant_minimax_config WHERE restaurant_id = 1`).Scan(&enc, &model); err != nil {
			t.Fatalf("row missing: %v", err)
		}
		if strings.Contains(enc, "sk-new-key-456") {
			t.Fatalf("new key stored in plaintext!")
		}
		if model != "MiniMax-M3" {
			t.Fatalf("stored model was wiped by key-only update: %q", model)
		}
		got, _ := s.minimaxStoreCache.get(1)
		if got.APIKey != "sk-new-key-456" || got.Model != "MiniMax-M3" {
			t.Fatalf("cache mismatch after key-only update: %+v", got)
		}
	})

	t.Run("decrypt resolves through resolver (restaurant=1)", func(t *testing.T) {
		key := s.resolveMiniMaxKey(context.Background(), 1)
		model := s.resolveMiniMaxModel(context.Background(), 1, "")
		if key != "sk-super-secret-999" {
			t.Fatalf("resolver returned wrong key: %q", key)
		}
		if model != "MiniMax-M3" {
			t.Fatalf("resolver returned wrong model: %q", model)
		}
	})

	t.Run("resolver falls back to env for unknown restaurant", func(t *testing.T) {
		key := s.resolveMiniMaxKey(context.Background(), 999999)
		model := s.resolveMiniMaxModel(context.Background(), 999999, "")
		if key != "env-fallback-key" {
			t.Fatalf("expected env fallback key, got %q", key)
		}
		if model != "MiniMax-M3" {
			t.Fatalf("expected env fallback model, got %q", model)
		}
	})
}
