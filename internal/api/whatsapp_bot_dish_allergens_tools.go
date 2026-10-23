package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// Coordination id: wa_bot_dish_allergens_v1
//
// The documented-but-missing staff read (docs/plans/
// whatsapp-bot-conversation-34630762921-fix-plan.json "future work"):
// get_dish_allergens. It answers allergen questions from REAL data — the
// technical sheets and their component tree — instead of the model guessing.
//
// Reuses the whole allergen core (sheetAllergens, resolveSheetAllergens,
// canonicalAllergens): one thin tool over the same engine the panel uses, so
// the kitchen and the group can never disagree.
//
// Boundaries: only in allowed (privileged) contacts/groups of this bot
// version. The customer-facing food-safety handoff (wa_bot_allergen_handoff_v1)
// is untouched: a customer still gets the notice + contact card, never data.

// botToolDishAllergens executes get_dish_allergens: identify the technical
// sheet by id or by name search, return its effective allergens with their
// contributing ingredients.
func (s *Server) botToolDishAllergens(ctx context.Context, restaurantID int, input json.RawMessage) (string, error) {
	var in struct {
		SheetID int64  `json:"sheet_id"`
		Query   string `json:"query"`
	}
	_ = json.Unmarshal(input, &in)

	type sheetRow struct {
		ID   int64
		Name string
	}
	var found []sheetRow
	q := strings.TrimSpace(in.Query)
	switch {
	case in.SheetID > 0:
		rows, err := s.db.QueryContext(ctx, `SELECT id, name FROM stock_recipes WHERE restaurant_id=? AND id=?`, restaurantID, in.SheetID)
		if err != nil {
			return botJSON(map[string]any{"error": "error consultando las fichas"}), nil
		}
		defer rows.Close()
		for rows.Next() {
			var r sheetRow
			if rows.Scan(&r.ID, &r.Name) == nil {
				found = append(found, r)
			}
		}
	case q != "":
		rows, err := s.db.QueryContext(ctx, `SELECT id, name FROM stock_recipes WHERE restaurant_id=? AND name LIKE ? ORDER BY name LIMIT 5`, restaurantID, "%"+q+"%")
		if err != nil {
			return botJSON(map[string]any{"error": "error consultando las fichas"}), nil
		}
		defer rows.Close()
		for rows.Next() {
			var r sheetRow
			if rows.Scan(&r.ID, &r.Name) == nil {
				found = append(found, r)
			}
		}
	default:
		return botJSON(map[string]any{"error": "indica sheet_id o query"}), nil
	}
	if len(found) == 0 {
		// wa_bot_terminal_tool_v1: no ficha means the restaurant never recorded
		// allergens for that dish, so retrying (or searching by another name for
		// the same dish) returns the same empty answer. Terminal: the model must
		// tell the group there is no data instead of burning iterations. In the
		// 2026-10-23 incident three of these lookups ran back-to-back and fed the
		// N+1 exploration that exhausted the turn.
		return botJSON(map[string]any{"error": "no hay fichas técnicas que coincidan (no hay alérgenos registrados para ese plato; dilo al usuario, no lo busques por otra vía)", "catalogue": canonicalAllergens, "terminal": true}), nil
	}

	out := make([]map[string]any, 0, len(found))
	for _, r := range found {
		derived, contributors, manual, err := s.sheetAllergens(ctx, restaurantID, r.ID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return botJSON(map[string]any{"error": "error calculando los alérgenos"}), nil
		}
		if contributors == nil {
			contributors = map[string][]string{}
		}
		out = append(out, map[string]any{
			"sheet_id":     r.ID,
			"name":         r.Name,
			"derived":      normalizeAllergenList(derived),
			"manualAdded":  normalizeAllergenList(manual.Added),
			"effective":    resolveSheetAllergens(derived, manual),
			"contributors": contributors,
		})
	}
	botCheckpoint("wa_bot_dish_allergens_v1", "restaurant_id=%d sheets=%d", restaurantID, len(out))
	return botJSON(map[string]any{"recipes": out, "catalogue": canonicalAllergens}), nil
}
