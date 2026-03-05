package api

import "testing"

func TestNormalizeBOPremiumDrawElementDisplayMode(t *testing.T) {
	if got := normalizeBOPremiumDrawElementDisplayMode("asset"); got != "asset" {
		t.Fatalf("expected asset, got %q", got)
	}
	if got := normalizeBOPremiumDrawElementDisplayMode("text"); got != "text" {
		t.Fatalf("expected text, got %q", got)
	}
	if got := normalizeBOPremiumDrawElementDisplayMode("both"); got != "both" {
		t.Fatalf("expected both, got %q", got)
	}
	if got := normalizeBOPremiumDrawElementDisplayMode("invalid"); got != "both" {
		t.Fatalf("expected fallback both, got %q", got)
	}
	if got := normalizeBOPremiumDrawElementDisplayMode(nil); got != "both" {
		t.Fatalf("expected fallback both for nil, got %q", got)
	}
}

func TestNormalizeBOPremiumTableLayoutMap_DisplayModeDefaulting(t *testing.T) {
	layout := map[string]any{
		"elements": []any{
			map[string]any{"id": "draw-1", "preset": "plant", "display_mode": "asset"},
			map[string]any{"id": "draw-2", "preset": "column"},
			map[string]any{"id": "draw-3", "preset": "lamp", "display_mode": "weird"},
			"invalid",
		},
	}

	normalized := normalizeBOPremiumTableLayoutMap(layout)
	elements, ok := normalized["elements"].([]any)
	if !ok {
		t.Fatalf("expected elements array")
	}
	if len(elements) != 4 {
		t.Fatalf("expected 4 elements, got %d", len(elements))
	}

	first, ok := elements[0].(map[string]any)
	if !ok || first["display_mode"] != "asset" {
		t.Fatalf("expected first element display_mode=asset, got %#v", elements[0])
	}
	second, ok := elements[1].(map[string]any)
	if !ok || second["display_mode"] != "both" {
		t.Fatalf("expected second element display_mode=both, got %#v", elements[1])
	}
	third, ok := elements[2].(map[string]any)
	if !ok || third["display_mode"] != "both" {
		t.Fatalf("expected third element display_mode normalized to both, got %#v", elements[2])
	}
}
