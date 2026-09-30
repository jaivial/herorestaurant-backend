package api

import (
	"net/http/httptest"
	"testing"
)

// The /api/plugin exemption must not become a way to reach the admin API.
func TestVerifyPluginExemptionCannotReachAdmin(t *testing.T) {
	s := &Server{}
	r := pluginTestRouter(s)
	for _, p := range []string{
		"/api/plugin/../admin/bookings",
		"/api/plugin/../../admin/pos",
		"/api/plugin/tools/../../admin/x",
	} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		if rec.Code == 200 {
			t.Errorf("%s reached an unauthenticated handler: %s", p, rec.Body.String()[:min(80, len(rec.Body.String()))])
		}
	}
}
