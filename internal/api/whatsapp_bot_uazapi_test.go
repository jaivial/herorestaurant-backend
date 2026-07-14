package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBotUazapiSend_BuildsEndpointAndPayload(t *testing.T) {
	var gotPath, gotToken string
	var gotPayload map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotToken = r.URL.Query().Get("token")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotPayload)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"sent":true}`))
	}))
	defer srv.Close()

	err := botUazapiSend(context.Background(), srv.URL, "tok-1", "text", map[string]any{
		"number": "34612345678",
		"text":   "hola",
	})
	if err != nil {
		t.Fatalf("send error: %v", err)
	}
	if gotPath != "/send/text" {
		t.Errorf("path = %q", gotPath)
	}
	if gotToken != "tok-1" {
		t.Errorf("token = %q", gotToken)
	}
	if gotPayload["number"] != "34612345678" || gotPayload["text"] != "hola" {
		t.Errorf("payload = %v", gotPayload)
	}
}

func TestBotUazapiSend_MediaKinds(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	for _, kind := range []string{"image", "document", "location", "contact", "menu"} {
		if err := botUazapiSend(context.Background(), srv.URL, "t", kind, map[string]any{"number": "34600000000"}); err != nil {
			t.Errorf("kind %s: %v", kind, err)
		}
	}
	want := []string{"/send/image", "/send/document", "/send/location", "/send/contact", "/send/menu"}
	if len(paths) != len(want) {
		t.Fatalf("paths = %v", paths)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Errorf("paths[%d] = %q, want %q", i, paths[i], want[i])
		}
	}
}

func TestBotUazapiSend_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	if err := botUazapiSend(context.Background(), srv.URL, "t", "text", map[string]any{}); err == nil {
		t.Fatal("expected error on 401")
	}
}
