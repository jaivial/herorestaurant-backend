package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"preactvillacarmen/internal/config"
)

// fakeMiniMax returns a server that translates by upper-casing with an EN: prefix.
func fakeMiniMax(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		txt := ""
		if len(req.Messages) > 0 {
			txt = req.Messages[0].Content
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{{"type": "text", "text": "EN:" + txt}},
		})
	}))
}

func TestTranslateEntityFields_RoundTrip(t *testing.T) {
	db := testDB(t)
	defer db.Close()

	mm := fakeMiniMax(t)
	defer mm.Close()

	s := NewServer(db, config.Config{
		MiniMaxAPIKey:               "test-key",
		MiniMaxBaseURL:              mm.URL,
		MiniMaxModel:                "MiniMax-M3",
		MiniMaxTranslateTimeout:     5 * time.Second,
		MiniMaxTranslateConcurrency: 4,
	})

	ctx := context.Background()
	const rid = 999999
	const eid = int64(987654321)
	// Clean up any leftovers.
	s.deleteEntityTranslations(ctx, "TEST_ENTITY", rid, eid)
	defer s.deleteEntityTranslations(ctx, "TEST_ENTITY", rid, eid)

	result := s.translateEntityFields(ctx, rid, "TEST_ENTITY", eid, []translationField{
		{Name: "nombre", Text: "Pan con tomate"},
		{Name: "descripcion", Text: "Delicioso"},
		{Name: "empty", Text: "   "},
	})
	if result["nombre"] != "EN:Pan con tomate" {
		t.Errorf("nombre translation wrong: %q", result["nombre"])
	}
	if result["descripcion"] != "EN:Delicioso" {
		t.Errorf("descripcion translation wrong: %q", result["descripcion"])
	}
	if _, ok := result["empty"]; ok {
		t.Errorf("empty field should be skipped")
	}

	// Re-load from DB.
	loaded := s.loadEntityTranslations(ctx, rid, "TEST_ENTITY", eid, "en")
	if loaded["nombre"] != "EN:Pan con tomate" {
		t.Errorf("loaded nombre wrong: %q", loaded["nombre"])
	}

	// Unchanged text should not re-run (verify via hash skip: same output, still present).
	again := s.translateEntityFields(ctx, rid, "TEST_ENTITY", eid, []translationField{
		{Name: "nombre", Text: "Pan con tomate"},
	})
	if again["nombre"] != "EN:Pan con tomate" {
		t.Errorf("unchanged reuse failed: %q", again["nombre"])
	}
}

func TestTranslateEntityFields_FailureDoesNotPanic(t *testing.T) {
	db := testDB(t)
	defer db.Close()

	// Server pointing at a dead endpoint.
	s := NewServer(db, config.Config{
		MiniMaxAPIKey:               "test-key",
		MiniMaxBaseURL:              "http://127.0.0.1:1",
		MiniMaxModel:                "MiniMax-M3",
		MiniMaxTranslateTimeout:     1 * time.Second,
		MiniMaxTranslateConcurrency: 2,
	})
	ctx := context.Background()
	result := s.translateEntityFields(ctx, 999999, "TEST_ENTITY_FAIL", 111, []translationField{
		{Name: "nombre", Text: "Hola"},
	})
	if len(result) != 0 {
		t.Errorf("expected empty result on failure, got %v", result)
	}
}

func TestBuildEnglishArray(t *testing.T) {
	m := map[string]string{
		arrayFieldName("comments", 0): "EN:a",
		arrayFieldName("comments", 2): "EN:c",
	}
	out := buildEnglishArray(m, "comments", 3)
	if out == nil {
		t.Fatal("expected non-nil array")
	}
	if out[0] != "EN:a" || out[1] != "" || out[2] != "EN:c" {
		t.Errorf("unexpected array: %v", out)
	}
	if buildEnglishArray(map[string]string{}, "comments", 3) != nil {
		t.Error("expected nil for empty map")
	}
	_ = strings.TrimSpace("")
}
