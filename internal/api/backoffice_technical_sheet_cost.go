package api

import (
	"context"
	"database/sql"
	"net/http"

	"preactvillacarmen/internal/httpx"
)

// Sheet costing follows decision D8: proportional, computed from base units, and
// honest about gaps. Rounding happens only at display, never on intermediates,
// so a long ingredient list does not accumulate drift.

type sheetCostLine struct {
	StockItemID  int64   `json:"stockItemId"`
	Name         string  `json:"name"`
	ImageURL     string  `json:"imageUrl,omitempty"`
	QtyBase      float64 `json:"qtyBase"`
	BaseUnit     string  `json:"baseUnit"`
	EnteredQty   float64 `json:"enteredQty"`
	UnitLabel    string  `json:"unitLabel"`
	WastePct     float64 `json:"wastePct"`
	UnitCostBase float64 `json:"unitCostBase"`
	LineCost     float64 `json:"lineCost"`
	PriceMissing bool    `json:"priceMissing"`
}

type sheetCostContext struct {
	Portions           int
	GrossPrice         float64
	VatRate            float64
	LabourCost         float64
	DirectVariableCost float64
	Bands              []stockMarginBand
	TargetFoodCostPct  float64
}

type sheetCost struct {
	Lines              []sheetCostLine `json:"lines"`
	IngredientCost     float64         `json:"ingredientCost"`
	LabourCost         float64         `json:"labourCost"`
	DirectVariableCost float64         `json:"directVariableCost"`
	TotalCost          float64         `json:"totalCost"`
	CostPerPortion     float64         `json:"costPerPortion"`
	GrossPrice         float64         `json:"grossPrice"`
	NetPrice           float64         `json:"netPrice"`
	VatRate            float64         `json:"vatRate"`
	FoodCostPct        float64         `json:"foodCostPct"`
	GrossMargin        float64         `json:"grossMargin"`
	Zone               string          `json:"zone,omitempty"`
	TargetFoodCostPct  float64         `json:"targetFoodCostPct,omitempty"`
	CostComplete       bool            `json:"costComplete"`
	MissingPrices      []string        `json:"missingPrices"`
}

// computeSheetCost is pure so the arithmetic can be pinned by unit tests
// without a database.
func computeSheetCost(lines []sheetCostLine, ctx sheetCostContext) sheetCost {
	out := sheetCost{
		Lines:              make([]sheetCostLine, 0, len(lines)),
		LabourCost:         ctx.LabourCost,
		DirectVariableCost: ctx.DirectVariableCost,
		GrossPrice:         ctx.GrossPrice,
		VatRate:            ctx.VatRate,
		TargetFoodCostPct:  ctx.TargetFoodCostPct,
		CostComplete:       true,
		MissingPrices:      []string{},
	}

	for _, line := range lines {
		if line.PriceMissing {
			// An unpriced ingredient is unknown, not free. Reporting it as zero
			// would make the dish look more profitable than it really is, which
			// is the same rule the labour costing already applies.
			out.CostComplete = false
			out.MissingPrices = append(out.MissingPrices, line.Name)
			line.LineCost = 0
			out.Lines = append(out.Lines, line)
			continue
		}
		cost := line.QtyBase * line.UnitCostBase
		// Waste is a yield loss: to plate QtyBase you must buy more than
		// QtyBase, so the cost is divided by the yield, not multiplied by it.
		if line.WastePct > 0 && line.WastePct < 100 {
			cost /= 1 - line.WastePct/100
		}
		line.LineCost = cost
		out.IngredientCost += cost
		out.Lines = append(out.Lines, line)
	}

	out.TotalCost = out.IngredientCost + ctx.LabourCost + ctx.DirectVariableCost

	portions := ctx.Portions
	if portions < 1 {
		// A sheet always yields at least one portion; guarding here keeps the
		// response free of Inf/NaN, which would break the JSON encoder.
		portions = 1
	}
	out.CostPerPortion = out.TotalCost / float64(portions)

	if ctx.GrossPrice > 0 {
		// Food cost % is deliberately ingredient-only (owner standard §8.1):
		// blending labour in would make the number incomparable with the
		// industry benchmarks the owner uses.
		net, foodCostPct, margin := stockCostMetrics(ctx.GrossPrice, ctx.VatRate, out.IngredientCost)
		out.NetPrice = net
		out.FoodCostPct = foodCostPct
		out.GrossMargin = margin
		if out.CostComplete {
			// A zone computed from a partial cost would look authoritative
			// while being wrong, so it is withheld until every price is known.
			out.Zone = stockMarginZone(foodCostPct, ctx.Bands)
		}
	}
	return out
}

// maxSheetCostDepth bounds sub-recipe recursion. Cycles are already rejected on
// write, but a cap keeps a corrupted row from hanging a request thread.
const maxSheetCostDepth = 12

type sheetCostComponent struct {
	StockItemID int64
	Name        string
	ImageURL    string
	QtyBase     float64
	BaseUnit    string
	EnteredQty  float64
	UnitLabel   string
	WastePct    float64
	SubRecipeID *int64
	UnitCost    *float64
}

// loadSheetCostComponents reads one tenant's whole component graph in a single
// query. Prices are left NULL when absent so the caller can tell "free" from
// "unknown"; the older costing query coalesced them to zero, which is exactly
// the silent-zero trap this feature must avoid.
func (s *Server) loadSheetCostComponents(ctx context.Context, restaurantID int) (map[int64][]sheetCostComponent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.recipe_id, c.stock_item_id, i.name, COALESCE(i.image_url,''), c.qty_base,
		       i.base_unit, c.entered_qty, u.code, c.waste_pct, c.sub_recipe_id,
		       (SELECT p.unit_cost_base FROM stock_item_prices p
		         WHERE p.restaurant_id=i.restaurant_id AND p.stock_item_id=i.id
		           AND p.effective_at<=NOW()
		         ORDER BY p.effective_at DESC, p.id DESC LIMIT 1) AS unit_cost
		  FROM stock_recipe_components c
		  JOIN stock_items i ON i.restaurant_id=c.restaurant_id AND i.id=c.stock_item_id
		  JOIN stock_item_units u ON u.restaurant_id=c.restaurant_id AND u.id=c.entered_unit_id
		 WHERE c.restaurant_id=?
		 ORDER BY c.recipe_id, c.sort_order, c.id`, restaurantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	graph := map[int64][]sheetCostComponent{}
	for rows.Next() {
		var recipeID int64
		var comp sheetCostComponent
		var subRecipeID sql.NullInt64
		var unitCost sql.NullFloat64
		if err := rows.Scan(&recipeID, &comp.StockItemID, &comp.Name, &comp.ImageURL, &comp.QtyBase,
			&comp.BaseUnit, &comp.EnteredQty, &comp.UnitLabel, &comp.WastePct, &subRecipeID, &unitCost); err != nil {
			return nil, err
		}
		if subRecipeID.Valid {
			comp.SubRecipeID = &subRecipeID.Int64
		}
		if unitCost.Valid {
			comp.UnitCost = &unitCost.Float64
		}
		graph[recipeID] = append(graph[recipeID], comp)
	}
	return graph, rows.Err()
}

// sheetCostLinesFor turns one recipe's components into cost lines, resolving a
// sub-recipe's unit cost from its own ingredients when the semi-finished item
// has no purchase price of its own.
func sheetCostLinesFor(recipeID int64, graph map[int64][]sheetCostComponent,
	yields map[int64]float64, depth int) []sheetCostLine {
	lines := make([]sheetCostLine, 0, len(graph[recipeID]))
	for _, comp := range graph[recipeID] {
		line := sheetCostLine{
			StockItemID: comp.StockItemID, Name: comp.Name, ImageURL: comp.ImageURL,
			QtyBase: comp.QtyBase, BaseUnit: comp.BaseUnit, EnteredQty: comp.EnteredQty,
			UnitLabel: comp.UnitLabel, WastePct: comp.WastePct,
		}
		switch {
		case comp.UnitCost != nil:
			line.UnitCostBase = *comp.UnitCost
		case comp.SubRecipeID != nil && depth < maxSheetCostDepth:
			// A semi-finished item is worth what it costs to make, spread over
			// the batch it yields.
			sub := computeSheetCost(
				sheetCostLinesFor(*comp.SubRecipeID, graph, yields, depth+1),
				sheetCostContext{Portions: 1})
			yield := yields[*comp.SubRecipeID]
			if !sub.CostComplete || yield <= 0 {
				line.PriceMissing = true
			} else {
				line.UnitCostBase = sub.IngredientCost / yield
			}
		default:
			line.PriceMissing = true
		}
		lines = append(lines, line)
	}
	return lines
}

func (s *Server) handleBOTechnicalSheetCost(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	sheetID := sheetIDParam(r)

	var portions int
	var grossPrice, vatRate sql.NullFloat64
	// The sheet stores a VAT rate id, so the percentage is joined in; a sheet
	// without a rate simply has no net price to reason about.
	if err := s.db.QueryRowContext(r.Context(), `
		SELECT COALESCE(r.portions,1), r.selling_price_gross, v.rate
		  FROM stock_recipes r
		  LEFT JOIN stock_vat_rates v
		    ON v.restaurant_id=r.restaurant_id AND v.id=r.vat_rate_id
		 WHERE r.restaurant_id=? AND r.id=?`,
		a.ActiveRestaurantID, sheetID).Scan(&portions, &grossPrice, &vatRate); err != nil {
		httpx.WriteError(w, http.StatusNotFound, "Ficha tecnica no encontrada")
		return
	}

	graph, err := s.loadSheetCostComponents(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error cargando costes")
		return
	}
	yields, err := s.loadSheetYields(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error cargando rendimientos")
		return
	}

	scopes, err := s.loadAllMarginScopes(a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error cargando bandas de margen")
		return
	}
	bands := resolveMarginBands([]stockMarginScopeSpec{{ScopeKind: "GLOBAL", ScopeKey: "GLOBAL"}}, scopes)
	if len(bands) == 0 {
		bands = stockDefaultMarginBands()
	}

	cost := computeSheetCost(
		sheetCostLinesFor(sheetID, graph, yields, 0),
		sheetCostContext{
			Portions:   portions,
			GrossPrice: grossPrice.Float64,
			VatRate:    vatRate.Float64,
			Bands:      bands,
		})
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "cost": cost})
}

// loadSheetYields maps each recipe to the base-unit quantity one batch produces,
// used to price a sub-recipe per base unit.
func (s *Server) loadSheetYields(ctx context.Context, restaurantID int) (map[int64]float64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, COALESCE(output_qty_base,0) FROM stock_recipes WHERE restaurant_id=?`, restaurantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	yields := map[int64]float64{}
	for rows.Next() {
		var id int64
		var yield float64
		if err := rows.Scan(&id, &yield); err != nil {
			return nil, err
		}
		yields[id] = yield
	}
	return yields, rows.Err()
}
