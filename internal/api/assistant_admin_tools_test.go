package api

import (
	"strings"
	"testing"
)

// [FORKY-ADMIN-TOOLS-S01] the pure parts of the admin trio.

func TestForkyRenderPathFillsAndValidates(t *testing.T) {
	got, err := forkyRenderPath("/bookings/{id}/qr", map[string]any{"id": 42})
	if err != nil || got != "/bookings/42/qr" {
		t.Fatalf("got %q err %v", got, err)
	}
	for _, bad := range []any{"../x", "1/2", "", "a b"} {
		if _, err := forkyRenderPath("/bookings/{id}", map[string]any{"id": bad}); err == nil {
			t.Errorf("value %q must be refused", bad)
		}
	}
	if _, err := forkyRenderPath("/bookings/{id}", nil); err == nil || !strings.Contains(err.Error(), "id") {
		t.Errorf("missing param must be named: %v", err)
	}
}

func TestForkyAdminMapIsValidAndSafe(t *testing.T) {
	loadForkyAdminOps()
	seen := map[string]bool{}
	for _, op := range forkyAdminOps {
		if op.Name == "" || seen[op.Name] {
			t.Errorf("empty or duplicated name %q", op.Name)
		}
		seen[op.Name] = true
		if !strings.HasPrefix(op.Path, "/") || strings.Contains(op.Path, "/assistant/") || strings.HasSuffix(op.Path, "/ws") {
			t.Errorf("operation %s has a path the trio must not reach: %s", op.Name, op.Path)
		}
		if op.Path == "/login" || op.Path == "/logout" {
			t.Errorf("auth route in the map: %s", op.Path)
		}
		// wa_customtools_get_writes_v1: a non-GET is always a write, and a GET
		// may be flagged write ONLY when the audit marked it (the generator
		// appends "GET con escritura: <reason>" to its description). A GET that
		// writes must stay behind the approval chain, so the flag is what the
		// safety nets key on — never "method == GET" alone.
		if op.Method != "GET" && !op.Write {
			t.Errorf("operation %s writes by method %s but is not flagged write", op.Name, op.Method)
		}
		if op.Method == "GET" && op.Write && !strings.Contains(op.Description, "GET con escritura") {
			t.Errorf("GET %s is flagged write without the audited description (wa_customtools_get_writes_v1)", op.Name)
		}
	}
}

func TestForkyAdminTrioRegistered(t *testing.T) {
	for _, n := range []string{"admin_catalog", "admin_describe", "admin_call"} {
		tool, ok := assistantToolLookup(n)
		if !ok || !tool.BackofficeOnly || tool.Section != assistantSessionSection {
			t.Errorf("%s not registered as a backoffice-only session tool", n)
		}
	}
}
