package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
)

// assistantHandlerInput describes how an existing backoffice HTTP handler is
// invoked as a Forky tool: HTTP method, query params, JSON body (POST), form
// body (application/x-www-form-urlencoded) and optional chi URL params.
type assistantHandlerInput struct {
	Method   string
	Query    map[string]string
	URLParam map[string]string
	Body     map[string]any
	Form     map[string]string
}

// assistantCallHandler runs an existing backoffice handler through the tool
// registry with the authenticated session context, so tools reuse the full
// domain logic (business rules, ownership checks) without duplicating it. The
// handler's body bytes and status code are returned for the tool to shape.
func (s *Server) assistantCallHandler(ctx context.Context, handler http.HandlerFunc, in assistantHandlerInput) ([]byte, int, error) {
	method := in.Method
	if method == "" {
		method = http.MethodGet
	}
	vals := url.Values{}
	for k, v := range in.Query {
		vals.Set(k, v)
	}
	var body io.Reader
	switch {
	case in.Body != nil:
		b, err := json.Marshal(in.Body)
		if err != nil {
			return nil, 0, err
		}
		body = bytes.NewReader(b)
	case in.Form != nil:
		fv := url.Values{}
		for k, v := range in.Form {
			fv.Set(k, v)
		}
		body = strings.NewReader(fv.Encode())
	}
	req := httptest.NewRequest(method, "/tool?"+vals.Encode(), body)
	switch {
	case in.Body != nil:
		req.Header.Set("Content-Type", "application/json")
	case in.Form != nil:
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	auth, ok := boAuthFromContext(ctx)
	if !ok {
		return nil, 0, fmt.Errorf("autenticación requerida")
	}
	auth.ActiveRestaurantID = ridFromCtxOrAuth(ctx, auth)
	req = req.WithContext(withBOAuth(req.Context(), auth))
	// Legacy handlers resolve the tenant via the restaurantID context key.
	req = req.WithContext(withRestaurantID(req.Context(), auth.ActiveRestaurantID))
	if len(in.URLParam) > 0 {
		rctx := chi.NewRouteContext()
		for k, v := range in.URLParam {
			rctx.URLParams.Add(k, v)
		}
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	}
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec.Body.Bytes(), rec.Code, nil
}

// ridFromCtxOrAuth returns the active restaurant id from the tool context when
// present (the auth carried in ctx), falling back to the auth value.
func ridFromCtxOrAuth(ctx context.Context, auth boAuth) int {
	if a, ok := boAuthFromContext(ctx); ok && a.ActiveRestaurantID > 0 {
		return a.ActiveRestaurantID
	}
	return auth.ActiveRestaurantID
}

// botHandlerResponse wraps a handler JSON body with the tool name and returns
// it as the tool_result string.
func botHandlerResponse(tool string, body []byte) (string, error) {
	var v map[string]any
	if err := json.Unmarshal(body, &v); err != nil {
		return "", err
	}
	v["tool"] = tool
	return botJSON(v), nil
}

// assistantHandlerError converts a non-2xx handler response into a tool error.
func assistantHandlerError(prefix string, body []byte, code int) error {
	msg := strings.TrimSpace(string(body))
	if len(msg) > 300 {
		msg = msg[:300]
	}
	return fmt.Errorf("%s: http %d %s", prefix, code, msg)
}
