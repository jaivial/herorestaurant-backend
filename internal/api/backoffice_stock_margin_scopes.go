package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"preactvillacarmen/internal/httpx"
)

// marginFloatPtr is the non-test address-of helper for band bounds.
func marginFloatPtr(v float64) *float64 { return &v }

// stockDefaultMarginBands returns the owner-approved food-cost standard as band
// records, so the same stockMarginZone() machinery classifies by configured OR
// default bands. Defaults are never copied into tenant rows.
//
// Bands are diagnostics, not verdicts: PURPLE (margin high) prompts a
// perceived-value check, never an "overpriced" label.
func stockDefaultMarginBands() []stockMarginBand {
	return []stockMarginBand{
		{Zone: "PURPLE", Max: marginFloatPtr(25), SortOrder: 0, IsActive: true},
		{Zone: "GREEN", Min: marginFloatPtr(25), Max: marginFloatPtr(35), SortOrder: 1, IsActive: true},
		{Zone: "AMBER", Min: marginFloatPtr(35), Max: marginFloatPtr(40), SortOrder: 2, IsActive: true},
		{Zone: "RED", Min: marginFloatPtr(40), SortOrder: 3, IsActive: true},
	}
}

// stockMarginScopeSpec locates a scope in the inheritance chain. The chain is
// ordered most-specific first; the caller always appends GLOBAL last.
type stockMarginScopeSpec struct {
	ScopeKind string `json:"scopeKind"`
	ScopeKey  string `json:"scopeKey"`
}

// resolveMarginBands walks the inheritance chain and returns the first scope
// that has at least one configured band. An unresolvable key simply falls
// through to the next level (never an error), so a stale scope_key for a
// deleted category cannot corrupt a calculation.
func resolveMarginBands(chain []stockMarginScopeSpec, scopes map[string][]stockMarginBand) []stockMarginBand {
	for _, spec := range chain {
		if bands, ok := scopes[scopeMapKey(spec.ScopeKind, spec.ScopeKey)]; ok && len(bands) > 0 {
			return bands
		}
	}
	return nil
}

func scopeMapKey(kind, key string) string { return kind + ":" + key }

// marginScopeKeyForCategory builds the type-qualified category scope key.
// Type qualification is mandatory: comida_plato_categories and
// comida_bebida_categories are SEPARATE tables with colliding INT ids, so a
// bare id would silently conflate them.
func marginScopeKeyForCategory(foodType string, categoryID int64) string {
	return foodType + ":" + strconv.FormatInt(categoryID, 10)
}

// stockMarginScopeBandInput is one band of a PUT body. Min/Max may be nil to
// model open bounds (PURPLE low open, RED high open).
type stockMarginScopeBandInput struct {
	Zone string   `json:"zone"`
	Min  *float64 `json:"min"`
	Max  *float64 `json:"max"`
}

var marginZoneOrder = map[string]int{"PURPLE": 0, "GREEN": 1, "AMBER": 2, "RED": 3}

// validateMarginScopeBands enforces the cross-row rules the database cannot:
// all four zones present exactly once, and the four bands form a contiguous,
// non-overlapping partition of [0,100]. Returns bands sorted PURPLE..RED.
//
// Partial updates are exactly what would let gaps slip in, so PUT always
// replaces all four in one transaction.
func validateMarginScopeBands(in []stockMarginScopeBandInput) ([]stockMarginScopeBandInput, error) {
	if len(in) != 4 {
		return nil, fmt.Errorf("se requieren exactamente cuatro zonas")
	}
	seen := map[string]bool{}
	for i := range in {
		z := strings.ToUpper(strings.TrimSpace(in[i].Zone))
		if _, ok := marginZoneOrder[z]; !ok {
			return nil, fmt.Errorf("zona inválida %q", in[i].Zone)
		}
		if seen[z] {
			return nil, fmt.Errorf("zona duplicada %s", z)
		}
		seen[z] = true
		in[i].Zone = z
		if in[i].Min != nil && (*in[i].Min < 0 || *in[i].Min > 100) {
			return nil, fmt.Errorf("zona %s: mínimo fuera de rango", z)
		}
		if in[i].Max != nil && (*in[i].Max < 0 || *in[i].Max > 100) {
			return nil, fmt.Errorf("zona %s: máximo fuera de rango", z)
		}
		if in[i].Min != nil && in[i].Max != nil && *in[i].Min >= *in[i].Max {
			return nil, fmt.Errorf("zona %s: mínimo >= máximo", z)
		}
	}
	sort.SliceStable(in, func(i, j int) bool { return marginZoneOrder[in[i].Zone] < marginZoneOrder[in[j].Zone] })
	// Contiguity: each lower zone's upper bound equals the next zone's lower bound.
	for i := 0; i < 3; i++ {
		if in[i].Max == nil || in[i+1].Min == nil {
			return nil, fmt.Errorf("las zonas deben formar una partición contigua")
		}
		if *in[i].Max != *in[i+1].Min {
			return nil, fmt.Errorf("zonas %s y %s no son contiguas", in[i].Zone, in[i+1].Zone)
		}
	}
	return in, nil
}

func validMarginScopeKind(k string) bool {
	switch k {
	case "GLOBAL", "COMIDA_TYPE", "COMIDA_CATEGORY", "STOCK_CATEGORY":
		return true
	}
	return false
}

type stockMarginScopePutBody struct {
	ScopeKind        string                      `json:"scopeKind"`
	ScopeKey         string                      `json:"scopeKey"`
	Label            string                      `json:"label"`
	TargetFoodCostPct *float64                   `json:"targetFoodCostPct"`
	Bands            []stockMarginScopeBandInput `json:"bands"`
}

// marginScopeView is the JSON shape returned by GET endpoints.
type marginScopeView struct {
	ScopeID          int64                       `json:"scopeId"`
	ScopeKind        string                      `json:"scopeKind"`
	ScopeKey         string                      `json:"scopeKey"`
	Label            string                      `json:"label"`
	TargetFoodCostPct *float64                   `json:"targetFoodCostPct"`
	Bands            []stockMarginBand           `json:"bands"`
}

// PUT /api/admin/stock/margin-scopes
// Atomically upserts a scope and its four bands in one transaction.
func (s *Server) handleBOStockMarginScopePut(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	var in stockMarginScopePutBody
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Cuerpo de petición inválido")
		return
	}
	in.ScopeKind = strings.ToUpper(strings.TrimSpace(in.ScopeKind))
	in.ScopeKey = strings.TrimSpace(in.ScopeKey)
	in.Label = strings.TrimSpace(in.Label)
	if !validMarginScopeKind(in.ScopeKind) {
		httpx.WriteError(w, http.StatusBadRequest, "scopeKind inválido")
		return
	}
	if in.ScopeKind == "GLOBAL" {
		in.ScopeKey = "*"
	}
	if in.ScopeKey == "" || in.Label == "" {
		httpx.WriteError(w, http.StatusBadRequest, "scopeKey y label son obligatorios")
		return
	}
	if len(in.ScopeKey) > 64 {
		httpx.WriteError(w, http.StatusBadRequest, "scopeKey demasiado largo")
		return
	}
	// A scope whose key points at nothing can never resolve, so it would sit in
	// the settings looking configured while being silently ignored.
	if message := s.validateMarginScopeKey(a.ActiveRestaurantID, in.ScopeKind, in.ScopeKey); message != "" {
		httpx.WriteError(w, http.StatusBadRequest, message)
		return
	}
	if in.TargetFoodCostPct != nil && (*in.TargetFoodCostPct <= 0 || *in.TargetFoodCostPct >= 100) {
		httpx.WriteError(w, http.StatusBadRequest, "targetFoodCostPct debe estar entre 0 y 100")
		return
	}
	bands, err := validateMarginScopeBands(in.Bands)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx := r.Context()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error al guardar el scope")
		return
	}
	defer tx.Rollback()

	var scopeID int64
	row := tx.QueryRowContext(ctx,
		`SELECT id FROM stock_margin_scopes WHERE restaurant_id=? AND scope_kind=? AND scope_key=?`,
		a.ActiveRestaurantID, in.ScopeKind, in.ScopeKey)
	if err := row.Scan(&scopeID); err != nil {
		if err != sql.ErrNoRows {
			httpx.WriteError(w, http.StatusInternalServerError, "Error al leer el scope")
			return
		}
		res, err := tx.ExecContext(ctx,
			`INSERT INTO stock_margin_scopes (restaurant_id,scope_kind,scope_key,label,target_food_cost_pct)
			 VALUES (?,?,?,?,?)`,
			a.ActiveRestaurantID, in.ScopeKind, in.ScopeKey, in.Label, in.TargetFoodCostPct)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "No se pudo crear el scope")
			return
		}
		scopeID, _ = res.LastInsertId()
	} else {
		if _, err := tx.ExecContext(ctx,
			`UPDATE stock_margin_scopes SET label=?, target_food_cost_pct=? WHERE id=?`,
			in.Label, in.TargetFoodCostPct, scopeID); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "No se pudo actualizar el scope")
			return
		}
	}

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM stock_margin_scope_bands WHERE restaurant_id=? AND scope_id=?`,
		a.ActiveRestaurantID, scopeID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error al reemplazar bandas")
		return
	}
	for i, b := range bands {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO stock_margin_scope_bands (restaurant_id,scope_id,zone,min_food_cost_pct,max_food_cost_pct,sort_order)
			 VALUES (?,?,?,?,?,?)`,
			a.ActiveRestaurantID, scopeID, b.Zone, b.Min, b.Max, i); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "No se pudo guardar una banda")
			return
		}
	}
	if err := tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error al confirmar el scope")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "scopeId": scopeID})
}

// DELETE /api/admin/stock/margin-scopes/{id}
func (s *Server) handleBOStockMarginScopeDelete(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	res, err := s.db.ExecContext(r.Context(),
		`DELETE FROM stock_margin_scopes WHERE restaurant_id=? AND id=?`,
		a.ActiveRestaurantID, id)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error al borrar el scope")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		httpx.WriteError(w, http.StatusNotFound, "Scope no encontrado")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

// loadAllMarginScopes loads every configured scope+bands for a tenant into a
// map keyed by "KIND:KEY" for O(1) resolution. Used by costing + resolve.
func (s *Server) loadAllMarginScopes(restaurantID int) (map[string][]stockMarginBand, error) {
	rows, err := s.db.Query(
		`SELECT sc.scope_kind, sc.scope_key, b.zone, b.min_food_cost_pct, b.max_food_cost_pct, b.sort_order, sc.is_active
		 FROM stock_margin_scopes sc
		 JOIN stock_margin_scope_bands b ON b.scope_id=sc.id AND b.restaurant_id=sc.restaurant_id
		 WHERE sc.restaurant_id=?
		 ORDER BY sc.scope_kind, sc.scope_key, b.sort_order`, restaurantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]stockMarginBand{}
	for rows.Next() {
		var kind, key, zone string
		var minN, maxN sql.NullFloat64
		var order, active int
		if err := rows.Scan(&kind, &key, &zone, &minN, &maxN, &order, &active); err != nil {
			return nil, err
		}
		var band stockMarginBand
		band.Zone = zone
		if minN.Valid {
			band.Min = &minN.Float64
		}
		if maxN.Valid {
			band.Max = &maxN.Float64
		}
		band.SortOrder = order
		band.IsActive = active != 0
		k := scopeMapKey(kind, key)
		out[k] = append(out[k], band)
	}
	return out, rows.Err()
}

// GET /api/admin/stock/margin-scopes
// Lists all configured scopes with their bands (for the management panel).
func (s *Server) handleBOStockMarginScopesList(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	rows, err := s.db.QueryContext(r.Context(),
		`SELECT id, scope_kind, scope_key, label, target_food_cost_pct
		 FROM stock_margin_scopes WHERE restaurant_id=?
		 ORDER BY FIELD(scope_kind,'GLOBAL','COMIDA_TYPE','COMIDA_CATEGORY','STOCK_CATEGORY'), label`, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error al cargar scopes")
		return
	}
	defer rows.Close()
	var views []marginScopeView
	for rows.Next() {
		var v marginScopeView
		var tgt sql.NullFloat64
		if err := rows.Scan(&v.ScopeID, &v.ScopeKind, &v.ScopeKey, &v.Label, &tgt); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error al leer scopes")
			return
		}
		if tgt.Valid {
			v.TargetFoodCostPct = &tgt.Float64
		}
		v.Bands = []stockMarginBand{}
		views = append(views, v)
	}
	if views == nil {
		views = []marginScopeView{}
	}
	// Attach bands per scope.
	for i := range views {
		brows, err := s.db.QueryContext(r.Context(),
			`SELECT zone, min_food_cost_pct, max_food_cost_pct, sort_order
			 FROM stock_margin_scope_bands WHERE restaurant_id=? AND scope_id=?
			 ORDER BY sort_order`, a.ActiveRestaurantID, views[i].ScopeID)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error al cargar bandas")
			return
		}
		for brows.Next() {
			var b stockMarginBand
			var minN, maxN sql.NullFloat64
			if err := brows.Scan(&b.Zone, &minN, &maxN, &b.SortOrder); err != nil {
				brows.Close()
				httpx.WriteError(w, http.StatusInternalServerError, "Error al leer bandas")
				return
			}
			if minN.Valid {
				b.Min = &minN.Float64
			}
			if maxN.Valid {
				b.Max = &maxN.Float64
			}
			b.IsActive = true
			views[i].Bands = append(views[i].Bands, b)
		}
		brows.Close()
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "scopes": views, "defaults": stockDefaultMarginBands()})
}

// GET /api/admin/stock/margin-scopes/resolve?scopeKind=&scopeKey=&foodType=&categoryId=
// Returns the effective bands for a scope following the inheritance chain,
// plus which level they came from.
func (s *Server) handleBOStockMarginScopeResolve(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	kind := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("scopeKind")))
	key := strings.TrimSpace(r.URL.Query().Get("scopeKey"))
	foodType := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("foodType")))
	catStr := r.URL.Query().Get("categoryId")

	// Build the inheritance chain from the most specific available level.
	var chain []stockMarginScopeSpec
	if kind == "COMIDA_CATEGORY" && foodType != "" && catStr != "" {
		if catID, err := strconv.ParseInt(catStr, 10, 64); err == nil {
			chain = append(chain, stockMarginScopeSpec{ScopeKind: "COMIDA_CATEGORY", ScopeKey: marginScopeKeyForCategory(foodType, catID)})
		}
	} else if kind != "" && key != "" {
		chain = append(chain, stockMarginScopeSpec{ScopeKind: kind, ScopeKey: key})
	}
	if foodType != "" {
		chain = append(chain, stockMarginScopeSpec{ScopeKind: "COMIDA_TYPE", ScopeKey: foodType})
	}
	chain = append(chain, stockMarginScopeSpec{ScopeKind: "GLOBAL", ScopeKey: "*"})

	scopes, err := s.loadAllMarginScopes(a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error al cargar scopes")
		return
	}
	resolved := resolveMarginBands(chain, scopes)
	source := "default"
	if resolved != nil {
		source = "configured"
	} else {
		resolved = stockDefaultMarginBands()
	}
	// Identify which chain level actually answered, for the inheritance UI.
	level := ""
	for _, spec := range chain {
		if _, ok := scopes[scopeMapKey(spec.ScopeKind, spec.ScopeKey)]; ok {
			level = spec.ScopeKind
			break
		}
	}
	if level == "" {
		level = "default"
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"bands":   resolved,
		"source":  source,
		"level":   level,
		"chain":   chain,
	})
}

// GET /api/admin/stock/margin-scopes/defaults
func (s *Server) handleBOStockMarginScopeDefaults(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "bands": stockDefaultMarginBands()})
}
