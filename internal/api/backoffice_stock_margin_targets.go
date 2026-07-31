package api

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"strings"

	"preactvillacarmen/internal/httpx"
)

// Margin scopes point at real things: a food type, a comida category or a stock
// category. A scope whose key does not resolve is dead configuration - it can
// never match a dish, and the user gets no feedback about why their target is
// being ignored. So keys are validated at write time.

// marginScopeFoodTypes mirrors FOOD_TYPE_ORDER in the frontend's foodTypes.ts.
var marginScopeFoodTypes = map[string]bool{
	"vinos": true, "cafes": true, "postres": true, "platos": true, "bebidas": true,
}

// comidaCategoryTableForFoodType maps a food type to the table that actually
// holds its categories. Platos and bebidas live in SEPARATE tables with
// colliding ids, which is exactly why scope keys are type-qualified.
func comidaCategoryTableForFoodType(foodType string) string {
	switch foodType {
	case "platos":
		return "comida_plato_categories"
	case "bebidas":
		return "comida_bebida_categories"
	}
	return ""
}

// splitCategoryScopeKey parses "platos:12". A bare id is rejected: with two
// category tables sharing an id space it is genuinely ambiguous.
func splitCategoryScopeKey(key string) (foodType string, categoryID int64, ok bool) {
	parts := strings.SplitN(key, ":", 2)
	if len(parts) != 2 {
		return "", 0, false
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || id <= 0 {
		return "", 0, false
	}
	return strings.ToLower(strings.TrimSpace(parts[0])), id, true
}

// validateMarginScopeKey checks that the scope points at something real for
// this tenant. Returns a user-facing message when it does not.
func (s *Server) validateMarginScopeKey(restaurantID int, kind, key string) string {
	switch kind {
	case "GLOBAL":
		return ""
	case "COMIDA_TYPE":
		if !marginScopeFoodTypes[strings.ToLower(key)] {
			return "Tipo de carta desconocido"
		}
		return ""
	case "COMIDA_CATEGORY":
		foodType, categoryID, ok := splitCategoryScopeKey(key)
		if !ok {
			return "La categoria debe indicarse como tipo:id (por ejemplo platos:12)"
		}
		table := comidaCategoryTableForFoodType(foodType)
		if table == "" {
			// Only platos and bebidas have their own category tables.
			return "Ese tipo de carta no tiene categorias propias"
		}
		var exists int
		// The table name comes from a fixed internal mapping, never from user
		// input, so it cannot be used for injection.
		if err := s.db.QueryRow(
			`SELECT COUNT(*) FROM `+table+` WHERE restaurant_id=? AND id=?`,
			restaurantID, categoryID).Scan(&exists); err != nil || exists == 0 {
			return "La categoria indicada no existe"
		}
		return ""
	case "STOCK_CATEGORY":
		categoryID, err := strconv.ParseInt(key, 10, 64)
		if err != nil || categoryID <= 0 {
			return "Categoria de stock invalida"
		}
		var exists int
		if err := s.db.QueryRow(
			`SELECT COUNT(*) FROM stock_categories WHERE restaurant_id=? AND id=?`,
			restaurantID, categoryID).Scan(&exists); err != nil || exists == 0 {
			return "La categoria de stock indicada no existe"
		}
		return ""
	}
	return "scopeKind invalido"
}

// marginScopeChain builds the inheritance chain, most specific first. The
// caller always ends at GLOBAL, so there is a single well-defined answer.
func marginScopeChain(kind, key, foodType string, categoryID int64) []stockMarginScopeSpec {
	var chain []stockMarginScopeSpec
	if kind == "COMIDA_CATEGORY" && foodType != "" && categoryID > 0 {
		chain = append(chain, stockMarginScopeSpec{
			ScopeKind: "COMIDA_CATEGORY",
			ScopeKey:  marginScopeKeyForCategory(foodType, categoryID),
		})
	} else if kind != "" && kind != "GLOBAL" && key != "" {
		chain = append(chain, stockMarginScopeSpec{ScopeKind: kind, ScopeKey: key})
	}
	if foodType != "" {
		chain = append(chain, stockMarginScopeSpec{ScopeKind: "COMIDA_TYPE", ScopeKey: foodType})
	}
	return append(chain, stockMarginScopeSpec{ScopeKind: "GLOBAL", ScopeKey: "*"})
}

// handleBOStockMarginTargets answers "what food cost should this dish aim for".
// The target is reported together with the level it came from, so the UI can
// say "heredado de Global" instead of showing an unexplained number.
func (s *Server) handleBOStockMarginTargets(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	query := r.URL.Query()
	kind := strings.ToUpper(strings.TrimSpace(query.Get("scopeKind")))
	key := strings.TrimSpace(query.Get("scopeKey"))
	foodType := strings.ToLower(strings.TrimSpace(query.Get("foodType")))
	categoryID, _ := strconv.ParseInt(query.Get("categoryId"), 10, 64)

	targets, err := s.loadMarginScopeTargets(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error al cargar objetivos")
		return
	}

	var target *float64
	source := ""
	for _, spec := range marginScopeChain(kind, key, foodType, categoryID) {
		if value, found := targets[scopeMapKey(spec.ScopeKind, spec.ScopeKey)]; found && value != nil {
			target = value
			source = spec.ScopeKind
			break
		}
	}
	// A missing target stays null: inventing a default would present a guess as
	// the owner's decision.
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true, "targetFoodCostPct": target, "source": source,
	})
}

func (s *Server) loadMarginScopeTargets(ctx context.Context, restaurantID int) (map[string]*float64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT scope_kind, scope_key, target_food_cost_pct
		   FROM stock_margin_scopes WHERE restaurant_id=? AND is_active=1`, restaurantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]*float64{}
	for rows.Next() {
		var kind, key string
		var target sql.NullFloat64
		if err := rows.Scan(&kind, &key, &target); err != nil {
			return nil, err
		}
		if target.Valid {
			value := target.Float64
			out[scopeMapKey(kind, key)] = &value
		} else {
			out[scopeMapKey(kind, key)] = nil
		}
	}
	return out, rows.Err()
}
