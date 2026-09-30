package api

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"
)

// A POST body must be bounded: an oversized body is ignored rather than
// buffered, so the plugin cannot be used to exhaust server memory.
func TestVerifyPluginBodyIsBounded(t *testing.T) {
	huge := `{"name":"` + strings.Repeat("A", chatgptPluginMaxBodyBytes*2) + `"}`
	r := chiRouteWithName(
		httptest.NewRequest("POST", "/api/plugin/tools/create_booking", bytes.NewBufferString(huge)),
		"create_booking")
	raw, err := chatgptPluginInputFromRequest(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > chatgptPluginMaxBodyBytes {
		t.Errorf("body not bounded: %d bytes", len(raw))
	}
	// A normal body must still be accepted.
	ok := chiRouteWithName(
		httptest.NewRequest("POST", "/api/plugin/tools/create_booking",
			bytes.NewBufferString(`{"name":"Ana","people":4}`)),
		"create_booking")
	rawOK, err := chatgptPluginInputFromRequest(ok)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rawOK), `"name":"Ana"`) {
		t.Errorf("normal body dropped: %s", rawOK)
	}
}
