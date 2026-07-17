package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// boRouter wires a backoffice route with a fixed boAuth (restaurant scope).
func boSliderRouter(t *testing.T, srv *Server, restaurantID int) *chi.Mux {
	t.Helper()
	r := chi.NewMux()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := withBOAuth(req.Context(), boAuth{ActiveRestaurantID: restaurantID})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Get("/group-menus-v2/{id}/slider", srv.handleBOGroupMenusV2GetSlider)
	r.Patch("/group-menus-v2/{id}/slider", srv.handleBOGroupMenusV2PatchSlider)
	r.Post("/group-menus-v2/{id}/slider/images/ai", srv.handleBOGroupMenusV2GenerateSliderAIImage)
	return r
}

func TestSliderGetReturnsDefaults(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	srv := newTestServer(t, db)
	r := boSliderRouter(t, srv, 1)

	req := httptest.NewRequest(http.MethodGet, "/group-menus-v2/1/slider", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	body := decodeJSON(t, rec.Body.Bytes())
	if body["success"] != true {
		t.Fatalf("expected success, got %v", body)
	}
	slider, _ := body["slider"].(map[string]any)
	imgs, _ := slider["images"].([]any)
	if len(imgs) == 0 {
		t.Fatalf("expected seeded default images, got none")
	}
	first, _ := imgs[0].(map[string]any)
	if first["is_default"] != true {
		t.Errorf("expected first image is_default=true, got %v", first["is_default"])
	}
	// Default URLs are absolute — must not be double-prefixed with the bunny base.
	url, _ := first["image_url"].(string)
	if strings.Contains(url, "example.b-cdn.net") {
		t.Errorf("default URL was double-prefixed: %s", url)
	}
	if !strings.HasPrefix(url, "https://villacarmenmedia") {
		t.Errorf("unexpected default URL: %s", url)
	}
}

func TestSliderPatchModeValidation(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	srv := newTestServer(t, db)
	r := boSliderRouter(t, srv, 1)

	patch := func(mode string) map[string]any {
		req := httptest.NewRequest(http.MethodPatch, "/group-menus-v2/1/slider",
			strings.NewReader(`{"mode":"`+mode+`"}`))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return decodeJSON(t, rec.Body.Bytes())
	}

	if got := patch("garbage"); got["success"] != false {
		t.Errorf("garbage mode should be rejected, got %v", got)
	}
	for _, m := range []string{"default", "custom", "both", "hidden"} {
		if got := patch(m); got["success"] != true {
			t.Errorf("mode %q should be accepted, got %v", m, got)
		}
	}
	// leave it back at default
	patch("default")
}

func TestSliderAIGatedWithoutSubscription(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	srv := newTestServer(t, db)
	// Restaurant 999 has no ai_image_pack; the entitlement gate runs before menu ownership.
	r := boSliderRouter(t, srv, 999)

	req := httptest.NewRequest(http.MethodPost, "/group-menus-v2/1/slider/images/ai", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	body := decodeJSON(t, rec.Body.Bytes())
	// restaurant 1 has no ai_image_pack recurring feature in the test DB.
	if body["success"] != false || body["code"] != "NEEDS_SUBSCRIPTION" {
		t.Errorf("expected NEEDS_SUBSCRIPTION, got %v", body)
	}
}

func TestPublicSliderModeFilter(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	srv := newTestServer(t, db)
	bo := boSliderRouter(t, srv, 1)
	pub := testRouter(t, srv, 1)

	setMode := func(mode string) {
		req := httptest.NewRequest(http.MethodPatch, "/group-menus-v2/1/slider",
			strings.NewReader(`{"mode":"`+mode+`"}`))
		bo.ServeHTTP(httptest.NewRecorder(), req)
	}
	sliderImages := func() []any {
		rec := doGET(t, pub, "/menus/1")
		body := decodeJSON(t, rec.Body.Bytes())
		menu, _ := body["menu"].(map[string]any)
		imgs, _ := menu["slider_images"].([]any)
		return imgs
	}

	setMode("default")
	if got := sliderImages(); len(got) == 0 {
		t.Errorf("default mode should serve seeded defaults, got %d", len(got))
	}
	setMode("hidden")
	if got := sliderImages(); len(got) != 0 {
		t.Errorf("hidden mode should serve no images, got %d", len(got))
	}
	setMode("custom")
	if got := sliderImages(); len(got) != 0 {
		t.Errorf("custom mode with no custom images should be empty, got %d", len(got))
	}
	setMode("default") // restore
}
