package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
)

// ---------------------------------------------------------------------------
// Site-builder WebSocket: all post-load CRUD flows over WS.
//
// The client sends request/response frames: { type, requestId, payload } →
// { type:'ack'|'error', requestId, result }.
//
// ponytail: the hub REUSES the existing REST handler funcs — each frame is
// turned into a synthetic *http.Request carrying the same boAuth context +
// JSON body, then routed through the exact handler the REST endpoint uses.
// Zero duplicated CRUD logic; WS is purely a transport.
//
// Types handled here map 1:1 to REST routes:
//   sites.list/create/get/update/delete
//   pages.list/create/get/update/delete
//   components.list
//   publish.start        → POST /site-builder/sites/{id}/publish
//   publish.status       → GET  /site-builder/sites/{id}/publish-status
//   domains.list/create/delete/verify
//   instatic.ensure/seed/publish/status
// ---------------------------------------------------------------------------

var boSiteBuilderWSUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(*http.Request) bool { return true }, // auth via cookie; origin relaxed like other bo WS
}

type siteBuilderWSClient struct {
	conn *websocket.Conn
	send chan []byte
}

func (c *siteBuilderWSClient) writeJSON(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.conn.WriteMessage(websocket.TextMessage, b)
}

func (c *siteBuilderWSClient) ping() error {
	return c.conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(10*time.Second))
}

func (c *siteBuilderWSClient) close() error {
	return c.conn.Close()
}

type siteBuilderWSHub struct {
	mu      sync.Mutex
	clients map[int]map[*siteBuilderWSClient]struct{} // restaurantID → clients
}

func newSiteBuilderWSHub() *siteBuilderWSHub {
	return &siteBuilderWSHub{clients: map[int]map[*siteBuilderWSClient]struct{}{}}
}

func (h *siteBuilderWSHub) add(restaurantID int, c *siteBuilderWSClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.clients[restaurantID] == nil {
		h.clients[restaurantID] = map[*siteBuilderWSClient]struct{}{}
	}
	h.clients[restaurantID][c] = struct{}{}
}

func (h *siteBuilderWSHub) remove(restaurantID int, c *siteBuilderWSClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if set, ok := h.clients[restaurantID]; ok {
		delete(set, c)
	}
}

// broadcast sends a frame to every client of a restaurant (e.g. publish status
// updates from other editors / long tasks).
func (h *siteBuilderWSHub) broadcast(restaurantID int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients[restaurantID] {
		select {
		case c.send <- b:
		default: // slow client — drop
		}
	}
}

// siteBuilderRoute maps a WS message type to a REST path (and optional body
// mutation). Returns the path + method; "" means unknown.
func siteBuilderRoute(typ string) (method, path string, ok bool) {
	switch typ {
	case "sites.list":
		return http.MethodGet, "/site-builder/sites", true
	case "sites.create":
		return http.MethodPost, "/site-builder/sites", true
	case "sites.get":
		return http.MethodGet, "/site-builder/sites/{siteId}", true
	case "sites.update":
		return http.MethodPut, "/site-builder/sites/{siteId}", true
	case "sites.delete":
		return http.MethodDelete, "/site-builder/sites/{siteId}", true
	case "pages.list":
		return http.MethodGet, "/site-builder/sites/{siteId}/pages", true
	case "pages.create":
		return http.MethodPost, "/site-builder/sites/{siteId}/pages", true
	case "pages.get":
		return http.MethodGet, "/site-builder/pages/{pageId}", true
	case "pages.update":
		return http.MethodPut, "/site-builder/pages/{pageId}", true
	case "pages.delete":
		return http.MethodDelete, "/site-builder/pages/{pageId}", true
	case "components.list":
		return http.MethodGet, "/site-builder/components", true
	case "publish.start":
		return http.MethodPost, "/site-builder/sites/{siteId}/publish", true
	case "publish.status":
		return http.MethodGet, "/site-builder/sites/{siteId}/publish-status", true
	case "domains.list":
		return http.MethodGet, "/site-builder/sites/{siteId}/domains", true
	case "domains.create":
		return http.MethodPost, "/site-builder/sites/{siteId}/domains", true
	case "domains.delete":
		return http.MethodDelete, "/site-builder/domains/{domainId}", true
	case "domains.verify":
		return http.MethodPost, "/site-builder/domains/{domainId}/verify", true
	}
	return "", "", false
}

func (s *Server) handleBOSiteBuilderWS(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpxWriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	conn, err := boSiteBuilderWSUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	client := &siteBuilderWSClient{conn: conn, send: make(chan []byte, 16)}
	s.siteBuilderHub.add(a.ActiveRestaurantID, client)
	defer func() {
		s.siteBuilderHub.remove(a.ActiveRestaurantID, client)
		_ = client.close()
	}()

	// writer goroutine
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-done:
				return
			default:
			}
			select {
			case b := <-client.send:
				if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
					return
				}
			case <-time.After(25 * time.Second):
				if err := client.ping(); err != nil {
					return
				}
			}
		}
	}()

	conn.SetReadLimit(4 << 20)
	_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(90 * time.Second)) })

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if len(raw) == 0 {
			continue
		}
		var msg struct {
			Type      string          `json:"type"`
			RequestID string          `json:"requestId"`
			Payload   json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}
		typ := strings.ToLower(strings.TrimSpace(msg.Type))
		method, path, ok := siteBuilderRoute(typ)
		if !ok {
			_ = client.writeJSON(map[string]any{
				"type": "error", "requestId": msg.RequestID,
				"result": map[string]any{"success": false, "message": "unknown message type: " + typ},
			})
			continue
		}
		// Publish status / other long tasks may stream progress later; for now
		// handle synchronously and reply.
		result := s.dispatchSiteBuilderFrame(r.Context(), a, method, path, msg.Payload)
		_ = client.writeJSON(map[string]any{
			"type": "ack", "requestId": msg.RequestID, "result": result,
		})
	}
}

// dispatchSiteBuilderFrame builds a synthetic request with boAuth context and
// routes it through the existing site-builder REST handler (via a chi context
// holding path params). The handler writes JSON to a buffer we return.
func (s *Server) dispatchSiteBuilderFrame(ctx context.Context, a boAuth, method, path string, rawBody json.RawMessage) any {
	if rawBody == nil || string(rawBody) == "null" {
		rawBody = []byte("{}")
	}

	// Path params for {siteId}, {pageId}, {domainId} — extract from payload and
	// substitute into the path so chi matches the concrete route.
	var params map[string]string
	_ = json.Unmarshal(rawBody, &params)
	concrete := path
	for _, key := range []string{"siteId", "pageId", "domainId"} {
		concrete = strings.ReplaceAll(concrete, "{"+key+"}", params[key])
	}

	// Build a request carrying the auth context + body. RegisterSiteBuilderRoutes
	// mounts at /site-builder/... so the synthetic path is stripped of /admin.
	req, err := http.NewRequestWithContext(ctx, method, concrete, bytes.NewReader(rawBody))
	if err != nil {
		return map[string]any{"success": false, "message": err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	ctx2 := withBOAuth(req.Context(), a)
	// Inject chi route params so handlers using chi.URLParam read them even
	// though we call the handlers directly (no router pass).
	paramVals := map[string]string{}
	extractChiParams(concrete, paramVals)
	if len(paramVals) > 0 {
		rctx := chi.NewRouteContext()
		for k, v := range paramVals {
			rctx.URLParams.Add(k, v)
		}
		ctx2 = context.WithValue(ctx2, chi.RouteCtxKey, rctx)
	}
	req = req.WithContext(ctx2)
	log.Printf("[site-builder-ws] dispatch %s %s", method, concrete)

	// Response recorder.
	rec := &wsResponseRecorder{header: http.Header{}}

	// Route to existing handler.
	s.dispatchSiteBuilderREST(rec, req, method, path)
	if rec.status >= 400 {
		return map[string]any{"success": false, "message": rec.bodyString()}
	}
	var out any
	if err := json.Unmarshal(rec.body, &out); err != nil {
		return map[string]any{"success": true, "data": rec.bodyString()}
	}
	return out
}

// dispatchSiteBuilderREST routes the synthetic request to the same handler
// funcs the REST router uses.
func (s *Server) dispatchSiteBuilderREST(w http.ResponseWriter, r *http.Request, method, path string) {
	// Call the concrete handler directly (they are http.HandlerFuncs returned
	// by the same funcs RegisterSiteBuilderRoutes uses) — no second chi router
	// per frame, and exactly the handlers REST runs.
	h, ok := siteBuilderHandlerFor(method, path, s.db)
	if !ok {
		http.NotFound(w, r)
		return
	}
	h(w, r)
}

// siteBuilderHandlerFor returns the concrete handler for a (method, path) pair.
// Keep in sync with RegisterSiteBuilderRoutes.
func siteBuilderHandlerFor(method, path string, db *sql.DB) (http.HandlerFunc, bool) {
	switch {
	case method == http.MethodGet && path == "/site-builder/sites":
		return handleListSites(db), true
	case method == http.MethodPost && path == "/site-builder/sites":
		return handleCreateSite(db), true
	case method == http.MethodGet && isSingleParam(path, "/site-builder/sites/", "siteId"):
		return handleGetSite(db), true
	case method == http.MethodPut && isSingleParam(path, "/site-builder/sites/", "siteId"):
		return handleUpdateSite(db), true
	case method == http.MethodDelete && isSingleParam(path, "/site-builder/sites/", "siteId"):
		return handleDeleteSite(db), true
	case method == http.MethodGet && isChild(path, "/site-builder/sites/", "/pages"):
		return handleListPages(db), true
	case method == http.MethodPost && isChild(path, "/site-builder/sites/", "/pages"):
		return handleCreatePage(db), true
	case method == http.MethodGet && isSingleParam(path, "/site-builder/pages/", "pageId"):
		return handleGetPage(db), true
	case method == http.MethodPut && isSingleParam(path, "/site-builder/pages/", "pageId"):
		return handleUpdatePage(db), true
	case method == http.MethodDelete && isSingleParam(path, "/site-builder/pages/", "pageId"):
		return handleDeletePage(db), true
	case method == http.MethodGet && path == "/site-builder/components":
		return handleListComponents(db), true
	case method == http.MethodPost && isChild(path, "/site-builder/sites/", "/publish"):
		return handlePublishSite(db), true
	case method == http.MethodGet && isChild(path, "/site-builder/sites/", "/publish-status"):
		return handleGetPublishStatus(db), true
	case method == http.MethodGet && isChild(path, "/site-builder/sites/", "/domains"):
		return handleListDomains(db), true
	case method == http.MethodPost && isChild(path, "/site-builder/sites/", "/domains"):
		return handleCreateDomain(db), true
	case method == http.MethodDelete && isSingleParam(path, "/site-builder/domains/", "domainId"):
		return handleDeleteDomain(db), true
	case method == http.MethodPost && isChild(path, "/site-builder/domains/", "/verify"):
		return handleVerifyDomain(db), true
	}
	return nil, false
}

// isSingleParam matches prefix<value> where <value> has no '/'.
func isSingleParam(path, prefix, _ string) bool {
	return strings.HasPrefix(path, prefix) && len(path) > len(prefix) &&
		!strings.Contains(path[len(prefix):], "/")
}

// isChild matches prefix<value>/<suffix> (e.g. sites/{id}/pages).
func isChild(path, prefix, suffix string) bool {
	return strings.HasPrefix(path, prefix) && strings.HasSuffix(path, suffix) &&
		len(path) > len(prefix)+len(suffix) &&
		!strings.Contains(path[len(prefix):len(path)-len(suffix)], "/")
}

// extractChiParams pulls {siteId}/{pageId}/{domainId} values out of a concrete
// path into the given map, for handlers that read them via chi.URLParam.
func extractChiParams(path string, out map[string]string) {
	segs := strings.Split(path, "/")
	for i, s := range segs {
		if s == "" {
			continue
		}
		if i > 0 && segs[i-1] == "sites" && i+1 < len(segs) && segs[i+1] == "pages" {
			out["siteId"] = s
		}
		if i > 0 && segs[i-1] == "sites" && (i+1 == len(segs) || strings.HasPrefix(strings.Join(segs[i+1:], "/"), "pages/") || i+1 < len(segs) && (segs[i+1] == "assets" || segs[i+1] == "versions" || segs[i+1] == "bindings" || segs[i+1] == "domains" || segs[i+1] == "publish" || segs[i+1] == "publish-status")) {
			out["siteId"] = s
		}
		if i > 0 && segs[i-1] == "pages" {
			out["pageId"] = s
		}
		if i > 0 && segs[i-1] == "domains" && (i+1 == len(segs) || segs[i+1] == "verify") {
			out["domainId"] = s
		}
	}
}

// wsResponseRecorder captures handler output.
type wsResponseRecorder struct {
	header http.Header
	status int
	body   []byte
}

func (r *wsResponseRecorder) Header() http.Header  { return r.header }
func (r *wsResponseRecorder) WriteHeader(code int) { r.status = code }
func (r *wsResponseRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = 200
	}
	r.body = append(r.body, b...)
	return len(b), nil
}
func (r *wsResponseRecorder) bodyString() string { return string(r.body) }

// keep io import used in build
var _ = io.Discard
