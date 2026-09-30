package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"preactvillacarmen/internal/httpx"
)

// Coordination id: chatgpt_plugin_v1
//
// HTTP surface of the ChatGPT plugin. The manifest endpoints are public (the
// connector must fetch them before it has a credential); everything under
// /api/plugin is authenticated and pinned to a single restaurant.

// ChatGPTPluginRoutes returns the authenticated plugin API only, mounted by the
// server under /api/plugin. Paths are therefore relative to that mount point.
// The public manifest endpoints are registered separately by
// MountChatGPTPlugin, because the plugin contract requires them at the root.
func (s *Server) ChatGPTPluginRoutes() http.Handler {
	r := chi.NewRouter()
	r.Use(s.requireChatGPTPluginAuth)
	r.Use(s.requireChatGPTPluginRateLimit)

	r.Get("/tools", s.handleChatGPTPluginTools)
	r.Get("/tools/{name}", s.handleChatGPTPluginInvoke)
	r.Post("/tools/{name}", s.handleChatGPTPluginInvoke)
	return r
}

// chatgptPluginMaxBodyBytes bounds an optional POST body. The manifest only
// publishes query parameters, so a body is a convenience for richer clients and
// must never be able to grow without limit.
const chatgptPluginMaxBodyBytes = 64 << 10

// chatgptPluginRateLimitBurst caps the per-credential request rate on the
// authenticated surface, so a leaked or runaway token cannot hammer the
// backoffice handlers.
const chatgptPluginRateLimitBurst = 120

// requireChatGPTPluginRateLimit reuses the server-wide scoped token bucket,
// keyed by the token's restaurant, so the plugin can never exceed the same
// per-tenant budget the booking flow already enforces.
func (s *Server) requireChatGPTPluginRateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, ok := boAuthFromContext(r.Context())
		if !ok {
			httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		if !s.checkScopedRateLimit("chatgpt_plugin_v1", clientIP(r), auth.ActiveRestaurantID, chatgptPluginRateLimitBurst) {
			w.Header().Set("Retry-After", "60")
			httpx.WriteError(w, http.StatusTooManyRequests, "Too many requests")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// chatgptPluginCoordinationID is the cross-boundary identifier shared by the
// manifest, the OpenAPI document and the authenticated API, so a request seen
// in the connector logs can be traced to the plugin surface in the backend.
const chatgptPluginCoordinationID = "chatgpt_plugin_v1"

// handleChatGPTPluginManifest serves the mandatory plugin manifest.
func (s *Server) handleChatGPTPluginManifest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Coordination-Id", chatgptPluginCoordinationID)
	httpx.WriteJSON(w, http.StatusOK, chatgptPluginManifest(s.chatgptPluginBaseURL(r)))
}

// handleChatGPTPluginOpenAPIYAML serves the manifest's openapi.yaml reference.
func (s *Server) handleChatGPTPluginOpenAPIYAML(w http.ResponseWriter, r *http.Request) {
	writeChatGPTPluginYAML(w, chatgptPluginOpenAPIYAML(s.chatgptPluginBaseURL(r)))
}

// handleChatGPTPluginOpenAPIJSON serves the same document as JSON for
// debugging and for clients that prefer not to parse YAML.
func (s *Server) handleChatGPTPluginOpenAPIJSON(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, chatgptPluginSpec(s.chatgptPluginBaseURL(r)))
}

// handleChatGPTPluginLogo serves the manifest logo. A 1x1 transparent PNG is
// returned inline so the manifest always resolves without adding a binary
// asset to the repository.
func (s *Server) handleChatGPTPluginLogo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(chatgptPluginLogoPNG)
}

// chatgptPluginBaseURL derives the public origin of the deployment from the
// request, honouring the proxy headers, so the manifest points back at the
// host the connector actually reached.
func (s *Server) chatgptPluginBaseURL(r *http.Request) string {
	scheme := "https"
	if r.TLS == nil && !strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "http"
	}
	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = r.Host
	}
	host = strings.TrimSpace(strings.Split(host, ",")[0])
	if host == "" {
		return ""
	}
	return scheme + "://" + host
}

// handleChatGPTPluginTools lists the operations the calling token may run.
// The list is produced by the same ACL check the executor uses, so ChatGPT is
// never offered a capability the credential cannot perform.
func (s *Server) handleChatGPTPluginTools(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Coordination-Id", chatgptPluginCoordinationID)
	auth, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	tools := []map[string]any{}
	for _, t := range assistantToolRegistry {
		if !assistantToolAllowed(auth, t.Name) {
			continue
		}
		params := chatgptPluginParameters(t.Schema)
		tools = append(tools, map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"section":     t.Section,
			"write":       t.Write,
			"parameters":  params,
		})
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"restaurant_id": auth.ActiveRestaurantID,
		"role":          auth.Role,
		"sections":      auth.User.SectionAccess,
		"tools":         tools,
	})
}

// handleChatGPTPluginInvoke runs one tool. Arguments arrive as query parameters
// (the form the OpenAPI document declares) and are re-encoded to the JSON
// input the tool registry expects, so there is a single execution path shared
// with the rest of the assistant.
//
// The restaurant is never taken from the caller: it comes from the token, so a
// crafted request cannot read or mutate another restaurant.
func (s *Server) handleChatGPTPluginInvoke(w http.ResponseWriter, r *http.Request) {
	auth, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	name := strings.TrimSpace(chi.URLParam(r, "name"))
	if name == "" {
		httpx.WriteError(w, http.StatusBadRequest, "Tool name is required")
		return
	}

	// Authorize before any work: the registry owns the section/write policy.
	if !assistantToolAllowed(auth, name) {
		if _, exists := assistantToolLookup(name); !exists {
			httpx.WriteError(w, http.StatusNotFound, "Unknown operation")
			return
		}
		httpx.WriteError(w, http.StatusForbidden, "Forbidden")
		return
	}

	input, err := chatgptPluginInputFromRequest(r)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid arguments: "+err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), chatgptPluginIdleExpiry)
	defer cancel()
	ctx = withBOAuth(ctx, auth)
	ctx = withRestaurantID(ctx, auth.ActiveRestaurantID)

	out, err := s.assistantExecuteTool(ctx, auth.ActiveRestaurantID, name, input)
	if err != nil {
		httpx.WriteError(w, chatgptPluginStatusForError(err), err.Error())
		return
	}

	// Tools return their result as a JSON string; pass it through decoded so
	// the plugin response is a real JSON document, not a quoted string.
	var payload any
	if json.Unmarshal([]byte(out), &payload) != nil {
		payload = map[string]any{"result": out}
	}
	httpx.WriteJSON(w, http.StatusOK, payload)
}

// chatgptPluginInputFromRequest builds the tool input from the query string,
// typed according to the tool's own schema so numeric and boolean fields
// survive the JSON encoding.
func chatgptPluginInputFromRequest(r *http.Request) (json.RawMessage, error) {
	tool, ok := assistantToolLookup(chi.URLParam(r, "name"))
	if !ok {
		return nil, errors.New("unknown operation")
	}
	var schema struct {
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
	}
	_ = json.Unmarshal(tool.Schema, &schema)

	values := r.URL.Query()
	input := map[string]any{}
	for key := range values {
		raw := values.Get(key)
		prop, known := schema.Properties[key]
		if !known {
			// Unknown keys are forwarded untouched: a tool may accept fields
			// its published schema does not enumerate yet.
			input[key] = raw
			continue
		}
		input[key] = chatgptPluginCoerce(raw, prop.Type)
	}

	// A POST body (when present) wins over query parameters, so clients that
	// send richer payloads are not truncated by the flat manifest schema. The
	// reader is bounded so a large or endless body cannot exhaust memory.
	if r.Method == http.MethodPost && r.Body != nil {
		var body map[string]any
		if err := json.NewDecoder(io.LimitReader(r.Body, chatgptPluginMaxBodyBytes)).Decode(&body); err == nil {
			for k, v := range body {
				input[k] = v
			}
		}
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

// chatgptPluginCoerce converts a query string to the JSON type declared by the
// tool schema, defaulting to string for anything unrecognised.
func chatgptPluginCoerce(raw, kind string) any {
	switch kind {
	case "integer":
		if n, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil {
			return n
		}
	case "number":
		if f, err := strconv.ParseFloat(strings.TrimSpace(raw), 64); err == nil {
			return f
		}
	case "boolean":
		if b, err := strconv.ParseBool(strings.TrimSpace(raw)); err == nil {
			return b
		}
	}
	return raw
}

// chatgptPluginStatusForError maps a tool failure to an HTTP status. The tool
// errors carry an "http <code>" prefix when they wrap a domain handler
// rejection, so those are propagated instead of being flattened to 500.
func chatgptPluginStatusForError(err error) int {
	if err == nil {
		return http.StatusOK
	}
	if errors.Is(err, errChatGPTPluginUnauthorized) {
		return http.StatusUnauthorized
	}
	status := http.StatusBadRequest
	// Domain handlers wrap rejections as "http <code> message", so the status
	// is recovered from anywhere in the message rather than only at its start.
	// An out-of-range code falls back to 400 instead of writing an invalid
	// status line.
	if _, err := fmt.Sscanf(httpStatusIn(err.Error()), "%d", &status); err == nil && status >= 100 && status <= 599 {
		return status
	}
	return http.StatusBadRequest
}

// httpStatusIn extracts the "http <code>" marker a wrapped handler error
// carries, returning the code itself or an empty string when absent.
func httpStatusIn(msg string) string {
	const marker = "http "
	idx := strings.Index(msg, marker)
	if idx < 0 {
		return ""
	}
	rest := msg[idx+len(marker):]
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	return rest[:end]
}

// MountChatGPTPlugin registers the whole plugin surface on the main router:
// the public manifest endpoints at the root, and the authenticated API under
// /api/plugin. It is a method (not an inline mount) so the registration order
// in Routes() stays obvious and chi's "Use after routes" rule is respected.
func (s *Server) MountChatGPTPlugin(r chi.Router) {
	r.Mount("/api/plugin", s.ChatGPTPluginRoutes())

	wellKnown := chi.NewRouter()
	wellKnown.Get("/ai-plugin.json", s.handleChatGPTPluginManifest)
	r.Mount("/.well-known", wellKnown)

	r.Get("/openapi.yaml", s.handleChatGPTPluginOpenAPIYAML)
	r.Get("/openapi.json", s.handleChatGPTPluginOpenAPIJSON)
	r.Get("/chatgpt-plugin-logo.png", s.handleChatGPTPluginLogo)
}
