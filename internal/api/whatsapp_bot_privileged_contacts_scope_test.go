package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"preactvillacarmen/internal/config"
)

// La selección de conversaciones privilegiadas decide quién recibe el
// catálogo root, así que va atada al restaurante de la sesión. Un root que
// administra varios restaurantes no puede escribir la selección de otro
// tenant poniendo su id en la ruta.
//
// Coordination id: wa_bot_privileged_contacts_v1
func TestPrivilegedContactsRoutesStayOnTheSessionRestaurant(t *testing.T) {
	cases := []struct {
		name     string
		pathRid  string
		sessRid  int
		wantCode int
	}{
		{"the session's own restaurant is accepted", "1", 1, http.StatusOK},
		{"another restaurant in the path is refused", "2", 1, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := testDB(t) // skips when TEST_DB_DSN is not set
			s := &Server{cfg: config.Config{}, db: db}
			auth := boAuth{Role: "root", User: boUser{Role: "root"}, ActiveRestaurantID: tc.sessRid}
			ctx := withBOAuth(context.Background(), auth)

			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("restaurantId", tc.pathRid)
			req := httptest.NewRequest(http.MethodGet, "/api/admin/bot/ai/"+tc.pathRid+"/privileged-contacts", nil)
			req = req.WithContext(ctx)
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

			rec := httptest.NewRecorder()
			s.handleBOBotPrivilegedContactsGet(rec, req)
			if rec.Code != tc.wantCode {
				t.Errorf("status = %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String()[:120])
			}
		})
	}
}
