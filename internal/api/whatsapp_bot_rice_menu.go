package api

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

// Rice menu tool: resolves the day/weekend menu for a reservation date and
// returns ONLY the active rices of that menu, with the house rules, so the
// model can match the customer's request exactly instead of inventing a dish.
// Coordination id: bot_rice_menu_by_date_v1

var botRiceSupplementRe = regexp.MustCompile(`\(([+]?\s*\d+)\s*€\)`)

// botRiceMenuForDate maps a date to the menu it belongs to: the day menu runs
// Monday-Friday, the weekend menu Saturday-Sunday.
func botRiceMenuForDate(dateISO string) (key, label, weekday string) {
	t, err := time.Parse("2006-01-02", dateISO)
	if err != nil {
		return "finde", "Menú de fin de semana", ""
	}
	weekday = botSpanishDays[int(t.Weekday())]
	switch t.Weekday() {
	case time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday:
		return "dia", "Menú del día", weekday
	default:
		return "finde", "Menú de fin de semana", weekday
	}
}

// botRiceKind classifies a rice name so the model can reason about it.
func botRiceKind(name string) string {
	l := strings.ToLower(name)
	switch {
	case strings.Contains(l, "fideu"):
		return "fideua"
	case strings.Contains(l, "meloso"):
		return "meloso"
	case strings.Contains(l, "seco"):
		return "seco"
	case strings.Contains(l, "paella"):
		return "paella"
	default:
		return "arroz"
	}
}

// botRiceSupplement extracts the "+N€" supplement from a rice name.
func botRiceSupplement(name string) string {
	m := botRiceSupplementRe.FindStringSubmatch(name)
	if len(m) < 2 {
		return ""
	}
	return "+" + strings.ReplaceAll(strings.TrimSpace(m[1]), " ", "") + "€"
}

// botRiceNeedsAdvanceOrder reports whether the rice must be ordered ahead.
func botRiceNeedsAdvanceOrder(name string) bool {
	l := strings.ToLower(name)
	return strings.Contains(l, "encargo") || strings.Contains(l, "anticipad")
}

// botToolRiceMenu returns the rices of the menu that applies to the booking
// date (or today when no date is given).
func (s *Server) botToolRiceMenu(ctx context.Context, restaurantID int, input json.RawMessage) (string, error) {
	var in struct {
		Date string `json:"date"`
	}
	_ = json.Unmarshal(input, &in)

	dateISO := botTodayISO()
	if raw := strings.TrimSpace(in.Date); raw != "" {
		if parsed, err := parseBotDate(raw); err == nil {
			dateISO = parsed
		}
	}
	return s.botAvailableRices(ctx, restaurantID, dateISO)
}

// botAvailableRices builds the tool payload: the applicable menu, its active
// rices (name, kind, supplement, advance order) and the house rules.
func (s *Server) botAvailableRices(ctx context.Context, restaurantID int, dateISO string) (string, error) {
	menuKey, menuLabel, weekday := botRiceMenuForDate(dateISO)
	rices, english, err := s.loadRiceTypesForMenu(ctx, restaurantID, menuKey)
	if err != nil {
		return botJSON(map[string]any{"error": "error consultando arroces"}), nil
	}

	options := make([]map[string]any, 0, len(rices))
	for i, name := range rices {
		opt := map[string]any{
			"name":          name,
			"kind":          botRiceKind(name),
			"supplement":    botRiceSupplement(name),
			"advance_order": botRiceNeedsAdvanceOrder(name),
		}
		if i < len(english) && strings.TrimSpace(english[i]) != "" {
			opt["name_english"] = english[i]
		}
		options = append(options, opt)
	}

	return botJSON(map[string]any{
		"menu":               menuKey,
		"menu_label":         menuLabel,
		"date":               dateISO,
		"weekday":            weekday,
		"rice_types":         rices,
		"rice_types_english": english,
		"rices":              options,
		"rules": map[string]any{
			"only_closed_menus":                    true,
			"menu_includes":                        "1 entrante por persona + 1 principal por persona; el principal puede ser un plato principal o una ración de arroz",
			"min_rice_servings_per_rice":           2,
			"min_people_with_rice":                 2,
			"max_rice_varieties_per_table_under_8": 1,
			"note":                                 "El restaurante solo trabaja con menú cerrado. Usa SOLO los arroces de esta lista: no inventes ni confirmes ninguno que no aparezca aquí.",
		},
	}), nil
}
