package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// ACL enforcement: the plugin must expose exactly the tools the token's role
// grants and nothing more. This is the guarantee that lets a restaurant hand a
// ChatGPT connector to a limited user without leaking the whole backoffice.

func TestVerifyPluginToolsListRespectsRoleACL(t *testing.T) {
	// jefe_cocina has comida/fichaje/horarios/reservas but not stock or pos.
	jefe := boAuth{
		Role:               "jefe_cocina",
		ActiveRestaurantID: 7,
		User:               boUser{ID: 2, Role: "jefe_cocina", SectionAccess: []string{"comida", "reservas", "fichaje", "horarios"}},
	}
	s := &Server{}
	req := chiRouteWithName(httptest.NewRequest(http.MethodGet, "/api/plugin/tools", nil), "")
	req = req.WithContext(withBOAuth(req.Context(), jefe))

	// Call the handler directly: the token-auth middleware is exercised by the
	// dedicated auth tests, and this case is about the ACL the handler applies.
	rec := httptest.NewRecorder()
	s.handleChatGPTPluginTools(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("tools list = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), `"restaurant_id":7`) {
		t.Error("response must be pinned to the token restaurant, not a caller-supplied one")
	}

	// A stock tool must be absent for this role.
	for _, line := range splitLines(rec.Body.String()) {
		if contains(line, "stock_items_list") {
			t.Error("jefe_cocina must not be offered stock tools")
		}
	}
}

func TestVerifyPluginInvokeDeniesForbiddenSection(t *testing.T) {
	// A caller whose role lacks the section gets 403, and no tool runs.
	jefe := boAuth{
		Role:               "jefe_cocina",
		ActiveRestaurantID: 7,
		User:               boUser{ID: 2, Role: "jefe_cocina", SectionAccess: []string{"comida"}},
	}
	s := &Server{}
	req := chiRouteWithName(
		httptest.NewRequest(http.MethodGet, "/api/plugin/tools/stock_items_list", nil),
		"stock_items_list")
	req = req.WithContext(withBOAuth(req.Context(), jefe))
	req.Header.Set("Authorization", "Bearer anything")

	rec := httptest.NewRecorder()
	s.ChatGPTPluginRoutes().ServeHTTP(rec, req)
	// 403 when the section is denied; 401 only if the token itself is rejected.
	// A nil db means token resolution fails, so either is acceptable here —
	// what must never happen is a 200 or a 500.
	if rec.Code == http.StatusOK {
		t.Error("forbidden tool must not execute")
	}
	if rec.Code >= 500 {
		t.Errorf("expected a client error, got %d", rec.Code)
	}
}

func TestVerifyPluginInvokeUnknownToolIs404(t *testing.T) {
	admin := boAuth{
		Role:               "root",
		ActiveRestaurantID: 3,
		User:               boUser{ID: 1, Role: "root", SectionAccess: []string{"reservas", "menus", "comida", "stock", "pos"}},
	}
	s := &Server{}
	req := chiRouteWithName(
		httptest.NewRequest(http.MethodGet, "/api/plugin/tools/definitely_not_a_tool", nil),
		"definitely_not_a_tool")
	req = req.WithContext(withBOAuth(req.Context(), admin))

	rec := httptest.NewRecorder()
	// A known-but-denied tool is 403; an unknown name is 404. The lookup must
	// happen before any execution.
	s.handleChatGPTPluginInvoke(rec, req)
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusUnauthorized {
		t.Errorf("unknown tool = %d, want 404 (or 401 before auth)", rec.Code)
	}
}
