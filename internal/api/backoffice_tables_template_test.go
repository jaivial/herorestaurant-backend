package api

import (
	"testing"
)

func posX(t *testing.T, pos map[string]any, tableID string) int64 {
	t.Helper()
	entry, _ := asStringAnyMap(pos[tableID])
	x, _ := anyToInt64OK(entry["x_pos"])
	return x
}

func TestMergeBOPremiumLayoutWithTemplateTablePositions(t *testing.T) {
	layout := map[string]any{
		"booking_states": map[string]any{"1": map[string]any{"seated": true}},
		"table_positions": map[string]any{
			"2": map[string]any{"x_pos": 20, "y_pos": 20},
			"3": map[string]any{"x_pos": 30, "y_pos": 30},
		},
	}
	tpl := map[string]any{
		"limit_area_template_points": []map[string]any{{"x": 0.0, "y": 0.0}},
		"table_positions": map[string]any{
			"1": map[string]any{"x_pos": 100, "y_pos": 100},
			"2": map[string]any{"x_pos": 200, "y_pos": 200},
		},
	}
	merged := mergeBOPremiumLayoutWithTemplate(layout, tpl)

	// Template wins per id; per-day fills the ids the template does not own.
	pos, _ := asStringAnyMap(merged["table_positions"])
	if got := posX(t, pos, "1"); got != 100 {
		t.Fatalf("table 1: template position expected 100, got %v", got)
	}
	if got := posX(t, pos, "2"); got != 200 {
		t.Fatalf("table 2: template position expected 200, got %v", got)
	}
	if got := posX(t, pos, "3"); got != 30 {
		t.Fatalf("table 3: per-day position expected 30, got %v", got)
	}
	// Limit area template fields still merge.
	if _, ok := merged["limit_area_template_points"]; !ok {
		t.Fatal("limit_area_template_points must merge from the template")
	}
	// Booking states are per-day and untouched.
	states, _ := asStringAnyMap(merged["booking_states"])
	if len(states) != 1 {
		t.Fatalf("booking_states must stay per-day, got %v", states)
	}
}

func TestMergeBOPremiumLayoutWithTemplateDayScopeWinsWholesale(t *testing.T) {
	layout := map[string]any{
		"_template_scope": "day",
		"table_positions": map[string]any{"1": map[string]any{"x_pos": 5, "y_pos": 5}},
		"elements":        []any{},
	}
	tpl := map[string]any{
		"table_positions":            map[string]any{"1": map[string]any{"x_pos": 999, "y_pos": 999}},
		"limit_area_template_points": []map[string]any{{"x": 0.0, "y": 0.0}},
	}
	merged := mergeBOPremiumLayoutWithTemplate(layout, tpl)
	pos, _ := asStringAnyMap(merged["table_positions"])
	if got := posX(t, pos, "1"); got != 5 {
		t.Fatalf("day scope must keep per-day position 5, got %v", got)
	}
	if _, ok := merged["limit_area_template_points"]; ok {
		t.Fatal("day scope must not merge template limit points")
	}
}

func TestMergeBOPremiumLayoutWithTemplateOverrideMarkerBlocksPositions(t *testing.T) {
	layout := map[string]any{
		"_table_positions_override": map[string]any{"1": map[string]any{"x_pos": 5, "y_pos": 5}},
		"table_positions":           map[string]any{"1": map[string]any{"x_pos": 5, "y_pos": 5}},
	}
	tpl := map[string]any{
		"table_positions": map[string]any{"1": map[string]any{"x_pos": 999, "y_pos": 999}},
	}
	merged := mergeBOPremiumLayoutWithTemplate(layout, tpl)
	pos, _ := asStringAnyMap(merged["table_positions"])
	if got := posX(t, pos, "1"); got != 5 {
		t.Fatalf("override marker must keep per-day position 5, got %v", got)
	}
}

func TestTplScopeForLayoutWithoutTemplateDefaultsDay(t *testing.T) {
	if got := tplScopeForLayout(map[string]any{}, nil); got != "day" {
		t.Fatalf("no template must default to day scope, got %q", got)
	}
	tpl := map[string]any{"limit_area_template_points": []map[string]any{{"x": 0.0, "y": 0.0}}}
	if got := tplScopeForLayout(map[string]any{}, tpl); got != "template" {
		t.Fatalf("existing template must default to template scope, got %q", got)
	}
	if got := tplScopeForLayout(map[string]any{"_template_scope": "day"}, tpl); got != "day" {
		t.Fatalf("day marker must win, got %q", got)
	}
}

func TestMergeBOPremiumLayoutWithTemplateEmptyInputs(t *testing.T) {
	if got := mergeBOPremiumLayoutWithTemplate(nil, nil); len(got) != 0 {
		t.Fatalf("nil inputs must return empty layout, got %v", got)
	}
	layout := map[string]any{"booking_states": map[string]any{}}
	if got := mergeBOPremiumLayoutWithTemplate(layout, nil); got["booking_states"] == nil {
		t.Fatal("nil template must leave the layout untouched")
	}
}

func TestHasBOPremiumTemplateContent(t *testing.T) {
	cases := []struct {
		name string
		tpl  map[string]any
		want bool
	}{
		{"nil template", nil, false},
		{"empty template", map[string]any{}, false},
		{"only timestamp", map[string]any{"template_updated_at": "2024-01-01T00:00:00Z"}, false},
		{"empty arrays", map[string]any{"limit_area_template_points": []any{}, "draw_elements_template": []any{}}, false},
		{"limit points present", map[string]any{"limit_area_template_points": []map[string]any{{"x": 0.0, "y": 0.0}}}, true},
		{"draw elements present", map[string]any{"draw_elements_template": []any{map[string]any{"id": "wall-1"}}}, true},
		{"positions-only template", map[string]any{"table_positions": map[string]any{"7": map[string]any{"x_pos": 10, "y_pos": 20}}}, true},
		{"empty positions", map[string]any{"table_positions": map[string]any{}}, false},
	}
	for _, tc := range cases {
		if got := hasBOPremiumTemplateContent(tc.tpl); got != tc.want {
			t.Fatalf("%s: expected %v, got %v", tc.name, tc.want, got)
		}
	}
}
