package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"preactvillacarmen/internal/httpx"
)

func stockRecipeIngredientCost(recipeID int64, recipes map[int64]stockBOMRecipe, unitCosts map[int64]float64) (float64, error) {
	recipe, ok := recipes[recipeID]
	if !ok || recipe.OutputQty <= 0 {
		return 0, errors.New("recipe not found")
	}
	requirements, err := expandStockRecipeRequirements(recipeID, 1, recipes)
	if err != nil {
		return 0, err
	}
	cost := 0.0
	for itemID, quantity := range requirements {
		cost += quantity * unitCosts[itemID]
	}
	return cost / recipe.OutputQty, nil
}

func stockCostMetrics(grossPrice, vatRate, cost float64) (net, foodCostPct, margin float64) {
	net = math.Round(grossPrice/(1+vatRate/100)*100) / 100
	if net > 0 {
		foodCostPct = cost / net * 100
	}
	margin = net - cost
	return
}

// stockDefaultMarginZone classifies a food-cost percentage using the
// owner-approved standard (RED >40%, AMBER 35-40%, GREEN 25-35%, PURPLE <25%).
//
// Interval convention is half-open [min,max) so it matches the configurable
// band machinery exactly: a boundary value belongs to the higher band (40 -> RED,
// not AMBER). A real food-cost percentage is never exactly on a boundary, so the
// tie-break is invisible; what matters is that the default and any configured
// scope share one convention.
//
// These are diagnostics, not verdicts: PURPLE means the margin is high and the
// perceived value should be validated, not that the dish is overpriced.
//
// Used only when a tenant has not configured its own bands for the applicable
// scope; defaults are never copied into tenant rows.
func stockDefaultMarginZone(pct float64) string {
	switch {
	case pct >= 40:
		return "RED"
	case pct >= 35:
		return "AMBER"
	case pct >= 25:
		return "GREEN"
	default:
		return "PURPLE"
	}
}

type stockMarginBand struct {
	ID        int64    `json:"id"`
	Name      string   `json:"name"`
	Zone      string   `json:"zone"`
	Min       *float64 `json:"minFoodCostPct"`
	Max       *float64 `json:"maxFoodCostPct"`
	SortOrder int      `json:"sortOrder"`
	IsActive  bool     `json:"isActive"`
}

func protectStockSignatureDishRecommendations(result map[string]any, costs []map[string]any) {
	protected := map[string]bool{}
	for _, cost := range costs {
		if value, _ := cost["isProtected"].(bool); value {
			if name, _ := cost["name"].(string); name != "" {
				protected[strings.ToLower(strings.TrimSpace(name))] = true
			}
		}
	}
	recommendations, ok := result["recommendations"].([]any)
	if !ok {
		return
	}
	for _, raw := range recommendations {
		recommendation, _ := raw.(map[string]any)
		item, _ := recommendation["item"].(string)
		kind, _ := recommendation["type"].(string)
		if strings.EqualFold(kind, "REMOVE_DISH") && protected[strings.ToLower(strings.TrimSpace(item))] {
			recommendation["type"] = "MONITOR"
			recommendation["suggestedAction"] = "Protected signature dish: monitor or improve economics; do not remove."
		}
	}
}

func stockMarginZone(pct float64, bands []stockMarginBand) string {
	for _, band := range bands {
		if band.IsActive && (band.Min == nil || pct >= *band.Min) && (band.Max == nil || pct < *band.Max) {
			return band.Zone
		}
	}
	return stockDefaultMarginZone(pct)
}

const (
	stockAIGlobalFeatureKey = "ai_pack"
	stockAIFeatureKey       = "stock_ai_pack"
)

func (s *Server) requireBOStockAI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a, ok := boAuthFromContext(r.Context())
		if !ok {
			httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		var global, module, limit, used int
		var month sql.NullString
		err := s.db.QueryRowContext(r.Context(), `SELECT global_ai_enabled,stock_ai_enabled,monthly_call_limit,calls_used,usage_month FROM stock_ai_entitlements WHERE restaurant_id=?`, a.ActiveRestaurantID).Scan(&global, &module, &limit, &used, &month)
		if err != nil && err != sql.ErrNoRows {
			httpx.WriteError(w, http.StatusInternalServerError, "Error validating AI plan")
			return
		}
		if err == sql.ErrNoRows || global == 0 && module == 0 {
			globalPlan, globalErr := s.hasActiveRecurringFeature(r.Context(), a.ActiveRestaurantID, stockAIGlobalFeatureKey)
			stockPlan, stockErr := s.hasActiveRecurringFeature(r.Context(), a.ActiveRestaurantID, stockAIFeatureKey)
			if globalErr != nil || stockErr != nil {
				httpx.WriteError(w, http.StatusInternalServerError, "Error validating AI plan")
				return
			}
			if !globalPlan && !stockPlan {
				httpx.WriteError(w, http.StatusPaymentRequired, "Stock AI plan required")
				return
			}
			limit = 100
			_, err = s.db.ExecContext(r.Context(), `INSERT INTO stock_ai_entitlements (restaurant_id,global_ai_enabled,stock_ai_enabled,monthly_call_limit,usage_month) VALUES (?,?,?,?,?) ON DUPLICATE KEY UPDATE global_ai_enabled=VALUES(global_ai_enabled),stock_ai_enabled=VALUES(stock_ai_enabled)`, a.ActiveRestaurantID, stockBoolInt(globalPlan), stockBoolInt(stockPlan), limit, time.Now().Format("2006-01"))
			if err != nil {
				httpx.WriteError(w, http.StatusInternalServerError, "Error preparing AI usage")
				return
			}
		}
		current := time.Now().Format("2006-01")
		res, err := s.db.ExecContext(r.Context(), `UPDATE stock_ai_entitlements SET calls_used=CASE WHEN usage_month=? THEN calls_used+1 ELSE 1 END,usage_month=? WHERE restaurant_id=? AND (monthly_call_limit=0 OR usage_month<>? OR calls_used<monthly_call_limit)`, current, current, a.ActiveRestaurantID, current)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error recording AI usage")
			return
		}
		affected, _ := res.RowsAffected()
		if affected == 0 {
			httpx.WriteError(w, http.StatusTooManyRequests, "Monthly AI limit reached")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleBOStockSeasonalityClassify(w http.ResponseWriter, r *http.Request) {
	var in struct {
		BusinessProfile string `json:"businessProfile"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || strings.TrimSpace(in.BusinessProfile) == "" {
		httpx.WriteError(w, http.StatusBadRequest, "Business profile is required")
		return
	}
	var profile map[string]any
	system := "Classify restaurant seasonality. Never invent exact dates or facts not provided. Return strict JSON only."
	prompt := `Return {"venueType":"","servicePatterns":[],"peakMonths":[],"lowMonths":[],"weatherSensitive":false,"tourismSensitive":false,"terraceSensitive":false,"notes":[]}. Profile: ` + in.BusinessProfile
	if err := s.minimaxJSON(stockAIFeatureContext(r.Context(), "seasonality"), system, prompt, &profile); err != nil {
		httpx.WriteError(w, http.StatusBadGateway, "AI classification failed")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "seasonalityProfile": profile})
}

func (s *Server) handleBOStockAffluenceUpsert(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	var in struct {
		Date        string `json:"date"`
		ServiceType string `json:"serviceType"`
		Covers      int    `json:"covers"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || in.Covers < 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid affluence data")
		return
	}
	if _, err := time.Parse("2006-01-02", in.Date); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid date")
		return
	}
	switch in.ServiceType {
	case "LUNCH", "DINNER", "OTHER":
	default:
		httpx.WriteError(w, http.StatusBadRequest, "Invalid service type")
		return
	}
	var posOwned int
	_ = s.db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM pos_settings p JOIN stock_affluence_daily a ON a.restaurant_id=p.restaurant_id WHERE p.restaurant_id=? AND p.covers_mode='LIVE' AND a.service_date=? AND a.service_type=? AND a.source='POS')`, a.ActiveRestaurantID, in.Date, in.ServiceType).Scan(&posOwned)
	if posOwned != 0 {
		httpx.WriteJSON(w, http.StatusConflict, map[string]any{"success": false, "message": "POS covers are authoritative for this service", "code": "POS_COVERS_AUTHORITATIVE"})
		return
	}
	_, err := s.db.ExecContext(r.Context(), `INSERT INTO stock_affluence_daily (restaurant_id,service_date,service_type,covers) VALUES (?,?,?,?) ON DUPLICATE KEY UPDATE covers=VALUES(covers),source='MANUAL'`, a.ActiveRestaurantID, in.Date, in.ServiceType, in.Covers)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error saving affluence")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleBOStockForecast(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	scenario := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("scenario")))
	if scenario == "" {
		scenario = "MEDIUM"
	}
	multiplier := 1.0
	switch scenario {
	case "LIGHT":
		multiplier = .75
	case "HIGH":
		multiplier = 1.35
	case "MEDIUM":
	default:
		httpx.WriteError(w, http.StatusBadRequest, "Invalid scenario")
		return
	}
	horizon := stockQueryInt(r, "horizonDays", 7, 1, 30)
	var historyDays, totalCovers, affluenceDays int
	_ = s.db.QueryRowContext(r.Context(), `SELECT COALESCE(DATEDIFF(MAX(occurred_at),MIN(occurred_at))+1,0) FROM stock_movements WHERE restaurant_id=? AND type IN ('SALE','PRODUCTION_OUT','WASTE')`, a.ActiveRestaurantID).Scan(&historyDays)
	_ = s.db.QueryRowContext(r.Context(), `SELECT COALESCE(SUM(covers),0),COUNT(DISTINCT service_date) FROM stock_affluence_daily WHERE restaurant_id=? AND service_date>=DATE_SUB(CURDATE(),INTERVAL 8 WEEK)`, a.ActiveRestaurantID).Scan(&totalCovers, &affluenceDays)
	rows, err := s.db.QueryContext(r.Context(), `SELECT i.id,i.name,COALESCE(SUM(l.qty_base),0),COALESCE(item_usage.avg_daily,0),COALESCE(item_usage.total_usage,0),u.factor_to_base,u.label FROM stock_items i JOIN stock_item_units u ON u.restaurant_id=i.restaurant_id AND u.stock_item_id=i.id AND u.is_default_display=1 LEFT JOIN stock_levels l ON l.restaurant_id=i.restaurant_id AND l.stock_item_id=i.id LEFT JOIN (SELECT stock_item_id,SUM(-qty_base) total_usage,SUM(-qty_base)/GREATEST(DATEDIFF(MAX(occurred_at),MIN(occurred_at))+1,1) avg_daily FROM stock_movements WHERE restaurant_id=? AND type IN ('SALE','PRODUCTION_OUT','WASTE') AND occurred_at>=DATE_SUB(NOW(),INTERVAL 8 WEEK) GROUP BY stock_item_id) item_usage ON item_usage.stock_item_id=i.id WHERE i.restaurant_id=? AND i.is_tracked=1 AND i.is_active=1 AND i.deleted_at IS NULL GROUP BY i.id,u.id ORDER BY i.name`, a.ActiveRestaurantID, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading forecast")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id int64
		var name, label string
		var onHand, avgDaily, totalUsage, factor float64
		if err = rows.Scan(&id, &name, &onHand, &avgDaily, &totalUsage, &factor, &label); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error reading forecast")
			return
		}
		forecast := avgDaily * float64(horizon) * multiplier
		if totalCovers > 0 && affluenceDays > 0 {
			usagePerCover := totalUsage / float64(totalCovers)
			expectedCoversPerDay := float64(totalCovers) / float64(affluenceDays)
			forecast = usagePerCover * expectedCoversPerDay * float64(horizon) * multiplier
		}
		safety := avgDaily * 2
		recommended := forecast + safety
		order := math.Max(0, recommended-onHand)
		items = append(items, map[string]any{"itemId": id, "name": name, "onHand": onHand / factor, "averageDailyUsage": avgDaily / factor, "forecast": forecast / factor, "recommended": recommended / factor, "toOrder": order / factor, "unit": label})
	}
	confidence := "FULL"
	if historyDays < 14 {
		confidence = "COLLECTING"
	} else if historyDays < 28 {
		confidence = "LOW"
	} else if historyDays < 56 {
		confidence = "MEDIUM"
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "scenario": scenario, "horizonDays": horizon, "historyDays": historyDays, "requiredHistoryDays": 56, "affluenceDays": affluenceDays, "confidence": confidence, "items": items})
}

func (s *Server) handleBOStockVATList(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,name,rate,is_default,is_active FROM stock_vat_rates WHERE restaurant_id=? ORDER BY is_default DESC,name`, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading VAT rates")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var name string
		var rate float64
		var def, active int
		if err = rows.Scan(&id, &name, &rate, &def, &active); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error reading VAT rates")
			return
		}
		out = append(out, map[string]any{"id": id, "name": name, "rate": rate, "isDefault": def != 0, "isActive": active != 0})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "vatRates": out})
}
func (s *Server) handleBOStockVATCreate(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	var in struct {
		Name      string  `json:"name"`
		Rate      float64 `json:"rate"`
		IsDefault bool    `json:"isDefault"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || strings.TrimSpace(in.Name) == "" || in.Rate < 0 || in.Rate > 100 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid VAT rate")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creating VAT rate")
		return
	}
	defer tx.Rollback()
	if in.IsDefault {
		_, _ = tx.ExecContext(r.Context(), `UPDATE stock_vat_rates SET is_default=0 WHERE restaurant_id=?`, a.ActiveRestaurantID)
	}
	res, err := tx.ExecContext(r.Context(), `INSERT INTO stock_vat_rates (restaurant_id,name,rate,is_default) VALUES (?,?,?,?)`, a.ActiveRestaurantID, strings.TrimSpace(in.Name), in.Rate, stockBoolInt(in.IsDefault))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "VAT rate could not be created")
		return
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creating VAT rate")
		return
	}
	id, _ := res.LastInsertId()
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"success": true, "id": id})
}
func (s *Server) handleBOStockVATPatch(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var in struct {
		Name      string  `json:"name"`
		Rate      float64 `json:"rate"`
		IsDefault bool    `json:"isDefault"`
		IsActive  bool    `json:"isActive"`
	}
	if id <= 0 || json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || strings.TrimSpace(in.Name) == "" || in.Rate < 0 || in.Rate > 100 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid VAT rate")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error updating VAT rate")
		return
	}
	defer tx.Rollback()
	if in.IsDefault {
		_, _ = tx.ExecContext(r.Context(), `UPDATE stock_vat_rates SET is_default=0 WHERE restaurant_id=?`, a.ActiveRestaurantID)
	}
	res, err := tx.ExecContext(r.Context(), `UPDATE stock_vat_rates SET name=?,rate=?,is_default=?,is_active=? WHERE id=? AND restaurant_id=?`, strings.TrimSpace(in.Name), in.Rate, stockBoolInt(in.IsDefault), stockBoolInt(in.IsActive), id, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "VAT rate could not be updated")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		httpx.WriteError(w, http.StatusNotFound, "VAT rate not found")
		return
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error updating VAT rate")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}
func (s *Server) handleBOStockVATDelete(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var used int
	if err := s.db.QueryRowContext(r.Context(), `SELECT (EXISTS(SELECT 1 FROM stock_recipes WHERE restaurant_id=? AND vat_rate_id=? AND is_active=1) OR EXISTS(SELECT 1 FROM pos_products WHERE restaurant_id=? AND vat_rate_id=?))`, a.ActiveRestaurantID, id, a.ActiveRestaurantID, id).Scan(&used); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error deleting VAT rate")
		return
	}
	if used != 0 {
		httpx.WriteError(w, http.StatusConflict, "VAT rate is in use")
		return
	}
	res, err := s.db.ExecContext(r.Context(), `DELETE FROM stock_vat_rates WHERE id=? AND restaurant_id=? AND is_default=0`, id, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error deleting VAT rate")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		httpx.WriteError(w, http.StatusConflict, "Default VAT rate cannot be deleted")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleBOStockItemPriceCreate(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	itemID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var in struct {
		UnitCostBase float64 `json:"unitCostBase"`
		SupplierName string  `json:"supplierName"`
	}
	if itemID <= 0 || json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || in.UnitCostBase < 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid price")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error saving price")
		return
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(r.Context(), `INSERT INTO stock_item_prices (restaurant_id,stock_item_id,supplier_name,unit_cost_base) VALUES (?,?,?,?)`, a.ActiveRestaurantID, itemID, stockNullableString(in.SupplierName), in.UnitCostBase)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Price could not be saved")
		return
	}
	_, err = tx.ExecContext(r.Context(), `UPDATE stock_levels SET avg_unit_cost=? WHERE restaurant_id=? AND stock_item_id=?`, in.UnitCostBase, a.ActiveRestaurantID, itemID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error saving price")
		return
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error saving price")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"success": true})
}

func (s *Server) handleBOStockCosting(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	// Aggregate costing classifies every item by the GLOBAL scope (the tenant-wide
	// default); per-item comida-category resolution belongs in the technical-sheet
	// cost view, not this summary. Fall back to code defaults when unconfigured.
	scopes, err := s.loadAllMarginScopes(a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading margin scopes")
		return
	}
	bands := resolveMarginBands([]stockMarginScopeSpec{{ScopeKind: "GLOBAL", ScopeKey: "*"}}, scopes)
	if bands == nil {
		bands = stockDefaultMarginBands()
	}
	tx, err := s.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading costs")
		return
	}
	defer tx.Rollback()
	bom, err := loadStockBOM(r.Context(), tx, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading recipe costs")
		return
	}
	priceRows, err := tx.QueryContext(r.Context(), `SELECT i.id,COALESCE((SELECT p.unit_cost_base FROM stock_item_prices p WHERE p.restaurant_id=i.restaurant_id AND p.stock_item_id=i.id AND p.effective_at<=NOW() ORDER BY p.effective_at DESC,p.id DESC LIMIT 1),0) FROM stock_items i WHERE i.restaurant_id=?`, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading item prices")
		return
	}
	unitCosts := map[int64]float64{}
	for priceRows.Next() {
		var itemID int64
		var cost float64
		if err = priceRows.Scan(&itemID, &cost); err != nil {
			priceRows.Close()
			httpx.WriteError(w, http.StatusInternalServerError, "Error reading item prices")
			return
		}
		unitCosts[itemID] = cost
	}
	priceRows.Close()
	var labourEnabled int
	_ = tx.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT labour_cost_enabled FROM stock_settings WHERE restaurant_id=?),0)`, a.ActiveRestaurantID).Scan(&labourEnabled)
	directLabour, missingLabour, err := loadStockRecipeLabourCosts(r.Context(), tx, a.ActiveRestaurantID, boTodayDate())
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading labour costs")
		return
	}
	rows, err := tx.QueryContext(r.Context(), `SELECT r.id,r.name,r.selling_price_gross,COALESCE(v.rate,0),r.overhead_pct,r.is_protected FROM stock_recipes r LEFT JOIN stock_vat_rates v ON v.restaurant_id=r.restaurant_id AND v.id=r.vat_rate_id WHERE r.restaurant_id=? AND r.is_active=1 ORDER BY r.name`, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading costs")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var name string
		var gross sql.NullFloat64
		var vat, overheadPct float64
		var protected int
		if err = rows.Scan(&id, &name, &gross, &vat, &overheadPct, &protected); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error reading costs")
			return
		}
		ingredientCost, costErr := stockRecipeIngredientCost(id, bom, unitCosts)
		if costErr != nil {
			httpx.WriteError(w, http.StatusBadRequest, costErr.Error())
			return
		}
		labourCost, missingMembers, labourErr := stockRecipeLabourCost(id, bom, directLabour, missingLabour)
		if labourErr != nil {
			httpx.WriteError(w, http.StatusBadRequest, labourErr.Error())
			return
		}
		if labourEnabled == 0 {
			labourCost = 0
		}
		overheadCost := (ingredientCost + labourCost) * overheadPct / 100
		totalCost := ingredientCost + labourCost + overheadCost
		net, pct, margin := stockCostMetrics(gross.Float64, vat, totalCost)
		out = append(out, map[string]any{"recipeId": id, "name": name, "grossPrice": gross.Float64, "netPrice": net, "vatRate": vat, "ingredientCost": ingredientCost, "labourCost": labourCost, "overheadCost": overheadCost, "totalCost": totalCost, "foodCostPct": pct, "grossMargin": margin, "zone": stockMarginZone(pct, bands), "isProtected": protected != 0, "labourCostEnabled": labourEnabled != 0, "labourCostAvailable": len(missingMembers) == 0, "missingLabourMembers": missingMembers})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "items": out})
}

func (s *Server) handleBOStockAIRecommendations(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	var in struct {
		Forecast []map[string]any `json:"forecast"`
		Costs    []map[string]any `json:"costs"`
		Scenario string           `json:"scenario"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&in) != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid recommendation context")
		return
	}
	profileRaw := json.RawMessage(`{}`)
	_ = s.db.QueryRowContext(r.Context(), `SELECT COALESCE(seasonality_profile,JSON_OBJECT()) FROM stock_settings WHERE restaurant_id=?`, a.ActiveRestaurantID).Scan(&profileRaw)
	contextPayload, _ := json.Marshal(map[string]any{"scenario": in.Scenario, "forecast": in.Forecast, "costs": in.Costs, "seasonality": json.RawMessage(profileRaw)})
	system := "You advise a restaurant using deterministic stock and cost metrics. Do not recalculate or change numbers. Give cautious, actionable advice. Protected/signature dishes may never receive REMOVE_DISH. Return strict JSON only."
	prompt := `Return {"summary":"","recommendations":[{"type":"ORDER|REDUCE_ORDER|RAISE_PRICE|REDUCE_PORTION|RENEGOTIATE_SUPPLIER|REMOVE_DISH|MONITOR","priority":"HIGH|MEDIUM|LOW","item":"","reason":"","suggestedAction":""}]}. Maximum 10 recommendations. Context: ` + string(contextPayload)
	var result map[string]any
	if err := s.minimaxJSON(stockAIFeatureContext(r.Context(), "recommendations"), system, prompt, &result); err != nil {
		httpx.WriteError(w, http.StatusBadGateway, "AI recommendations failed")
		return
	}
	protectStockSignatureDishRecommendations(result, in.Costs)
	reportRaw, _ := json.Marshal(result)
	inputRaw, _ := json.Marshal(map[string]any{"scenario": in.Scenario, "forecast": in.Forecast, "costs": in.Costs, "seasonality": json.RawMessage(profileRaw)})
	reportID := int64(0)
	if res, saveErr := s.db.ExecContext(r.Context(), `INSERT INTO stock_ai_reports (restaurant_id,report_type,model,input_snapshot,report,created_by) VALUES (?,'COMBINED',?,?,?,?)`, a.ActiveRestaurantID, s.cfg.MiniMaxModel, inputRaw, reportRaw, a.User.ID); saveErr == nil {
		reportID, _ = res.LastInsertId()
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "id": reportID, "report": result, "model": s.cfg.MiniMaxModel})
}

func minimaxDocumentContent(mediaType string, payload []byte, prompt string) []map[string]any {
	blockType := "image"
	if mediaType == "application/pdf" {
		blockType = "document"
	}
	return []map[string]any{
		{"type": blockType, "source": map[string]any{"type": "base64", "media_type": mediaType, "data": base64.StdEncoding.EncodeToString(payload)}},
		{"type": "text", "text": prompt},
	}
}

func stockAIFeatureContext(ctx context.Context, feature string) context.Context {
	return context.WithValue(ctx, stockAIFeatureContextKey{}, feature)
}

type stockAIFeatureContextKey struct{}

func (s *Server) minimaxJSON(ctx context.Context, system, user string, target any) error {
	return s.minimaxJSONContent(ctx, system, user, target)
}

func (s *Server) minimaxJSONContent(ctx context.Context, system string, content any, target any) error {
	apiKey := strings.TrimSpace(s.cfg.MiniMaxAPIKey)
	if apiKey == "" {
		return fmt.Errorf("minimax not configured")
	}
	raw, _ := json.Marshal(map[string]any{"model": s.cfg.MiniMaxModel, "max_tokens": 4096, "system": system, "messages": []map[string]any{{"role": "user", "content": content}}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.cfg.MiniMaxBaseURL, "/")+"/v1/messages", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	res, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	if err != nil {
		return err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("minimax http %d", res.StatusCode)
	}
	var envelope minimaxMessagesResponse
	if err = json.Unmarshal(body, &envelope); err != nil {
		return err
	}
	if a, ok := boAuthFromContext(ctx); ok {
		feature, _ := ctx.Value(stockAIFeatureContextKey{}).(string)
		if feature == "" {
			feature = "stock"
		}
		_, _ = s.db.ExecContext(ctx, `INSERT INTO stock_ai_usage (restaurant_id,feature,model,input_tokens,output_tokens) VALUES (?,?,?,?,?)`, a.ActiveRestaurantID, feature, s.cfg.MiniMaxModel, envelope.Usage.InputTokens, envelope.Usage.OutputTokens)
	}
	text := ""
	for _, block := range envelope.Content {
		if block.Type == "text" {
			text += block.Text
		}
	}
	text = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(text), "```json"), "```"))
	return json.Unmarshal([]byte(text), target)
}

func (s *Server) handleBOStockDocumentTextExtract(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	var in struct {
		DocumentType string `json:"documentType"`
		Text         string `json:"text"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&in) != nil || strings.TrimSpace(in.Text) == "" {
		httpx.WriteError(w, http.StatusBadRequest, "Text is required")
		return
	}
	if in.DocumentType != "INVOICE" && in.DocumentType != "RECIPE" {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid document type")
		return
	}
	textHash := hashText(strings.TrimSpace(in.Text))
	var existingID int64
	if err := s.db.QueryRowContext(r.Context(), `SELECT id FROM stock_document_scans WHERE restaurant_id=? AND file_hash=? LIMIT 1`, a.ActiveRestaurantID, textHash).Scan(&existingID); err == nil {
		httpx.WriteJSON(w, http.StatusConflict, map[string]any{"success": false, "message": "Document already extracted", "id": existingID})
		return
	} else if err != sql.ErrNoRows {
		httpx.WriteError(w, http.StatusInternalServerError, "Error checking document")
		return
	}
	system, prompt := stockDocumentPrompt(in.DocumentType)
	var extraction stockDocumentExtraction
	if err := s.minimaxJSON(stockAIFeatureContext(r.Context(), "ocr_text"), system, prompt+"\n\nDocument text:\n"+in.Text, &extraction); err != nil {
		httpx.WriteError(w, http.StatusBadGateway, "AI extraction failed")
		return
	}
	id, err := s.saveBOStockDocumentExtraction(r, a.ActiveRestaurantID, a.User.ID, in.DocumentType, "PASTE", textHash, in.Text, extraction)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error saving extraction")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"success": true, "id": id, "extraction": extraction, "needsReview": true})
}
