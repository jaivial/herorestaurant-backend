package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"preactvillacarmen/internal/config"
)

func newTranslateTestServer(t *testing.T, baseURL string) *Server {
	t.Helper()
	return &Server{
		cfg: config.Config{
			MiniMaxAPIKey:               "test-key",
			MiniMaxBaseURL:              baseURL,
			MiniMaxModel:                "MiniMax-M3",
			MiniMaxTranslateTimeout:     5 * time.Second,
			MiniMaxTranslateConcurrency: 4,
		},
	}
}

func TestTranslateToEnglish_Success(t *testing.T) {
	var gotAuth, gotModel, gotSystem, gotUser string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Model    string `json:"model"`
			System   string `json:"system"`
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.Unmarshal(body, &req)
		gotModel = req.Model
		gotSystem = req.System
		if len(req.Messages) > 0 {
			gotUser = req.Messages[0].Content
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{{"type": "text", "text": "EN:" + gotUser}},
		})
	}))
	defer srv.Close()

	s := newTranslateTestServer(t, srv.URL)
	out, err := s.translateToEnglish(context.Background(), "Pan con tomate")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "EN:Pan con tomate" {
		t.Errorf("expected translated text, got %q", out)
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("expected Bearer auth, got %q", gotAuth)
	}
	if gotModel != "MiniMax-M3" {
		t.Errorf("expected model MiniMax-M3, got %q", gotModel)
	}
	if gotSystem != translationSystemPrompt {
		t.Errorf("unexpected system prompt %q", gotSystem)
	}
}

func TestTranslateToEnglish_EmptyInput(t *testing.T) {
	s := newTranslateTestServer(t, "http://127.0.0.1:1")
	out, err := s.translateToEnglish(context.Background(), "   ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "" {
		t.Errorf("expected empty output, got %q", out)
	}
}

func TestTranslateToEnglish_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	s := newTranslateTestServer(t, srv.URL)
	if _, err := s.translateToEnglish(context.Background(), "hola"); err == nil {
		t.Fatal("expected error on HTTP 500")
	}
}

func TestTranslateToEnglish_NoKey(t *testing.T) {
	s := &Server{cfg: config.Config{}}
	if _, err := s.translateToEnglish(context.Background(), "hola"); err == nil {
		t.Fatal("expected error when api key missing")
	}
}

func TestTranslateToEnglish_Concurrency(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.Unmarshal(body, &req)
		txt := ""
		if len(req.Messages) > 0 {
			txt = req.Messages[0].Content
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{{"type": "text", "text": strings.ToUpper(txt)}},
		})
	}))
	defer srv.Close()
	s := newTranslateTestServer(t, srv.URL)
	for _, in := range []string{"uno", "dos", "tres"} {
		out, err := s.translateToEnglish(context.Background(), in)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if out != strings.ToUpper(in) {
			t.Errorf("got %q", out)
		}
	}
	if atomic.LoadInt32(&calls) != 3 {
		t.Errorf("expected 3 calls, got %d", calls)
	}
}

func TestApplyPublicHomeMenuTranslations(t *testing.T) {
	menus := []publicMenuItemHome{{
		ID:           2,
		MenuTitle:    "Menú Mediterráneo",
		MenuSubtitle: []string{"A partir de 7 personas", "Solo con reserva"},
	}}
	applyPublicHomeMenuTranslations(menus, map[int64]map[string]string{
		2: {
			"menu_title":      "Mediterranean Menu",
			"menu_subtitle.0": "From 7 people",
		},
	})

	if menus[0].MenuTitleEnglish != "Mediterranean Menu" {
		t.Fatalf("unexpected English title %q", menus[0].MenuTitleEnglish)
	}
	if got := menus[0].MenuSubtitleEnglish; len(got) != 2 || got[0] != "From 7 people" || got[1] != "" {
		t.Fatalf("unexpected English subtitles %#v", got)
	}
}

func TestHashText_Stable(t *testing.T) {
	a := hashText("Pan con tomate")
	b := hashText("Pan con tomate")
	c := hashText("Pan con tomate ")
	if a != b {
		t.Error("expected same hash for same text")
	}
	if a == c {
		t.Error("expected different hash for different text")
	}
}
