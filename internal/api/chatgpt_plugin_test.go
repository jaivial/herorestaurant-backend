package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// Verification harness for chatgpt_plugin_v1: exercises the public manifest,
// the authenticated surface and the ACL gate without a database.
func TestVerifyPluginManifestPublic(t *testing.T) {
	s := &Server{}
	r := pluginTestRouter(s)

	for _, path := range []string{"/.well-known/ai-plugin.json", "/openapi.yaml", "/openapi.json", "/chatgpt-plugin-logo.png"} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s: got %d want 200", path, rec.Code)
		}
	}

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/.well-known/ai-plugin.json", nil))
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("manifest not json: %v", err)
	}
	if m["schema_version"] != "v1" {
		t.Errorf("schema_version = %v", m["schema_version"])
	}
	auth := m["auth"].(map[string]any)
	if auth["type"] != "bearer" {
		t.Errorf("auth type = %v", auth["type"])
	}
	apiDoc := m["api"].(map[string]any)
	if !strings.HasSuffix(apiDoc["url"].(string), "/openapi.yaml") {
		t.Errorf("api.url = %v", apiDoc["url"])
	}
	if !strings.HasPrefix(apiDoc["url"].(string), "http") {
		t.Errorf("api.url must be absolute for the connector: %v", apiDoc["url"])
	}
}

func TestVerifyOpenAPISpecCoversEveryTool(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	pluginTestRouter(s).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("openapi.json = %d", rec.Code)
	}
	var spec map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &spec); err != nil {
		t.Fatalf("spec not json: %v", err)
	}
	paths := spec["paths"].(map[string]any)
	// Every registry tool must be reachable from the manifest, otherwise
	// ChatGPT cannot discover the CRUD surface.
	for _, tool := range assistantToolRegistry {
		if _, ok := paths["/tools/"+tool.Name]; !ok {
			t.Errorf("tool %s missing from openapi paths", tool.Name)
		}
	}
	// Security must be declared so the connector knows it is bearer auth.
	sec, ok := spec["components"].(map[string]any)["securitySchemes"].(map[string]any)["bearerAuth"].(map[string]any)
	if !ok || sec["scheme"] != "bearer" {
		t.Errorf("bearerAuth security scheme missing: %v", spec["security"])
	}
	if len(paths) != len(assistantToolRegistry)+1 {
		t.Errorf("paths = %d want %d", len(paths), len(assistantToolRegistry)+1)
	}
}

func TestVerifyOpenAPIYAMLIsWellFormed(t *testing.T) {
	doc := chatgptPluginOpenAPIYAML("https://example.test")
	if !strings.HasPrefix(doc, "openapi: 3.0.3") {
		t.Error("yaml missing openapi header")
	}
	for _, want := range []string{"bearerAuth", "paths:", "operationId:"} {
		if !strings.Contains(doc, want) {
			t.Errorf("yaml missing %q", want)
		}
	}
	// Paths must be sorted for a byte-stable manifest across restarts, and the
	// index endpoint must sort before the per-tool paths ("/tools" < "/tools/").
	iTools := strings.Index(doc, "'/tools':")
	iBookings := strings.Index(doc, "'/tools/bookings_summary':")
	if iTools < 0 || iBookings < 0 || iTools > iBookings {
		t.Errorf("paths are not in stable sorted order (tools=%d bookings=%d)", iTools, iBookings)
	}
	// The document must be deterministic across renders.
	if chatgptPluginOpenAPIYAML("https://example.test") != doc {
		t.Error("openapi.yaml is not byte-stable across renders")
	}
}

func TestVerifyUnauthenticatedIsRejected(t *testing.T) {
	s := &Server{}
	r := pluginTestRouter(s)
	for _, path := range []string{"/api/plugin/tools", "/api/plugin/tools/bookings_list"} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without token: got %d want 401", path, rec.Code)
		}
	}
}

func TestVerifyBearerExtraction(t *testing.T) {
	cases := map[string]string{
		"Bearer abc":   "abc",
		"bearer abc":   "abc",
		"BEARER abc":   "abc",
		"Bearer   abc": "abc",
		"Basic abc":    "",
		"abc":          "",
		"Bearer":       "",
		"":             "",
	}
	for header, want := range cases {
		r := httptest.NewRequest(http.MethodGet, "/api/plugin/tools", nil)
		if header != "" {
			r.Header.Set("Authorization", header)
		}
		if got := chatgptPluginBearerToken(r); got != want {
			t.Errorf("Authorization %q -> %q want %q", header, got, want)
		}
	}
}

func TestVerifyPluginInputCoercion(t *testing.T) {
	// Query params must be typed per the tool schema, otherwise integers arrive
	// as strings and tool handlers silently misbehave.
	raw, err := chatgptPluginInputFromRequest(
		chiRouteWithName(
			httptest.NewRequest(http.MethodGet, "/api/plugin/tools/catalog_list?resource=comida&limit=5&search=arr", nil),
			"catalog_list"))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["resource"] != "comida" {
		t.Errorf("resource = %#v", got["resource"])
	}
	if f, ok := got["limit"].(float64); !ok || f != 5 {
		t.Errorf("limit should be numeric, got %#v", got["limit"])
	}
	if got["search"] != "arr" {
		t.Errorf("search = %#v", got["search"])
	}

	// A boolean-typed parameter must be coerced too.
	rawB, err := chatgptPluginInputFromRequest(
		chiRouteWithName(
			httptest.NewRequest(http.MethodGet, "/api/plugin/tools/menus_list?include_drafts=true", nil),
			"menus_list"))
	if err != nil {
		t.Fatal(err)
	}
	var gotB map[string]any
	if err := json.Unmarshal(rawB, &gotB); err != nil {
		t.Fatal(err)
	}
	if b, ok := gotB["include_drafts"].(bool); !ok || !b {
		t.Errorf("include_drafts should be bool, got %#v", gotB["include_drafts"])
	}
}

func TestVerifyErrorStatusMapping(t *testing.T) {
	if got := chatgptPluginStatusForError(errChatGPTPluginUnauthorized); got != http.StatusUnauthorized {
		t.Errorf("unauthorized mapped to %d", got)
	}
	if got := chatgptPluginStatusForError(errForbiddenFromHandler()); got != http.StatusForbidden {
		t.Errorf("wrapped 403 mapped to %d, want 403", got)
	}
	if got := chatgptPluginStatusForError(errGeneric()); got != http.StatusBadRequest {
		t.Errorf("generic error mapped to %d, want 400", got)
	}
}

// chiRouteWithName injects the {name} chi URL param the dispatcher reads.
func chiRouteWithName(r *http.Request, name string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("name", name)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// pluginTestRouter builds the full public + authenticated plugin surface the
// same way Routes() mounts it, so the verification exercises real mount points.
func pluginTestRouter(s *Server) http.Handler {
	r := chi.NewRouter()
	s.MountChatGPTPlugin(r)
	return r
}

func contains(haystack, needle string) bool { return strings.Contains(haystack, needle) }

func splitLines(s string) []string { return strings.Split(s, "\n") }

func errForbiddenFromHandler() error {
	return errors.New("delete_wine: http 403 Forbidden")
}

func errGeneric() error {
	return errors.New("confirmation_token requerido")
}
