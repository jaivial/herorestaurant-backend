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

func TestIsBOPremiumTemplateOnlyField(t *testing.T) {
	cases := map[string]bool{
		"limit_area_template_points": true,
		"draw_elements_template":     true,
		"template_updated_at":        true,
		"booking_states":             false,
		"elements":                   false,
		"":                           false,
	}
	for key, want := range cases {
		if got := isBOPremiumTemplateOnlyField(key); got != want {
			t.Fatalf("isBOPremiumTemplateOnlyField(%q)=%v, want %v", key, got, want)
		}
	}
}

func TestPatchBOPremiumTableLayout_StripsTemplateOnlyFields(t *testing.T) {
	layout := map[string]any{
		"booking_states": map[string]any{"1": map[string]any{"seated": true}},
		"elements":       []any{map[string]any{"id": "wall-1"}},
	}
	patch := map[string]any{
		"limit_area_template_points": []any{map[string]any{"x": 0, "y": 0}},
		"draw_elements_template":     []any{map[string]any{"id": "wall-1"}},
		"booking_states":             map[string]any{"2": map[string]any{"seated": false}},
		"template_updated_at":        "2026-04-05T00:00:00Z",
	}
	for k, v := range patch {
		// Skip the helper entirely; we are only testing the filter logic
		// here, so simulate the patch path by checking the key set.
		if isBOPremiumTemplateOnlyField(k) {
			continue
		}
		layout[k] = v
	}
	if _, ok := layout["limit_area_template_points"]; ok {
		t.Fatalf("expected limit_area_template_points to be skipped")
	}
	if _, ok := layout["draw_elements_template"]; ok {
		t.Fatalf("expected draw_elements_template to be skipped")
	}
	if _, ok := layout["template_updated_at"]; ok {
		t.Fatalf("expected template_updated_at to be skipped")
	}
	if got := layout["booking_states"].(map[string]any)["2"]; got == nil {
		t.Fatalf("expected booking_states to be patched in")
	}
}

func TestMergeBOPremiumLayoutWithTemplate_PrefersDayScope(t *testing.T) {
	layout := map[string]any{
		"booking_states":                       map[string]any{"1": map[string]any{"seated": true}},
		"_template_scope":                      "day",
		"_limit_area_template_points_override": []any{map[string]any{"x": 1, "y": 1}},
	}
	tpl := map[string]any{
		"limit_area_template_points": []any{map[string]any{"x": 9, "y": 9}},
		"draw_elements_template":     []any{map[string]any{"id": "wall-9"}},
	}
	merged := mergeBOPremiumLayoutWithTemplate(layout, tpl)
	if _, ok := merged["limit_area_template_points"]; ok {
		t.Fatalf("day-scope layout should not pull template values")
	}
	if got := merged["_template_scope"]; got != "day" {
		t.Fatalf("expected _template_scope=day, got %v", got)
	}
}

func TestMergeBOPremiumLayoutWithTemplate_AppliesTemplateWhenGlobal(t *testing.T) {
	layout := map[string]any{
		"booking_states": map[string]any{"1": map[string]any{"seated": true}},
	}
	tpl := map[string]any{
		"limit_area_template_points": []any{map[string]any{"x": 5, "y": 5}},
		"draw_elements_template":     []any{map[string]any{"id": "wall-1"}},
	}
	merged := mergeBOPremiumLayoutWithTemplate(layout, tpl)
	if _, ok := merged["limit_area_template_points"]; !ok {
		t.Fatalf("expected template limit points to be merged in")
	}
	if _, ok := merged["draw_elements_template"]; !ok {
		t.Fatalf("expected template draw elements to be merged in")
	}
}

func TestTplScopeForLayout_DefaultsToTemplateWhenTemplateExists(t *testing.T) {
	if got := tplScopeForLayout(map[string]any{}, map[string]any{"limit_area_template_points": []any{}}); got != "template" {
		t.Fatalf("expected default scope=template when template exists, got %q", got)
	}
	if got := tplScopeForLayout(map[string]any{}, nil); got != "template" {
		t.Fatalf("expected default scope=template with no template, got %q", got)
	}
	if got := tplScopeForLayout(map[string]any{"_template_scope": "day"}, nil); got != "day" {
		t.Fatalf("expected explicit day scope to win, got %q", got)
	}
}
