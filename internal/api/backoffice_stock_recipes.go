package api

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"preactvillacarmen/internal/httpx"
)

type stockRecipeInput struct {
	Name              string   `json:"name"`
	OutputItemID      int64    `json:"outputItemId"`
	OutputQuantity    float64  `json:"outputQuantity"`
	OutputUnitID      int64    `json:"outputUnitId"`
	WastePct          float64  `json:"wastePct"`
	PrepTimeMin       *int     `json:"prepTimeMin"`
	Instructions      string   `json:"instructions"`
	SellingPriceGross *float64 `json:"sellingPriceGross"`
	VATRateID         *int64   `json:"vatRateId"`
	OverheadPct       float64  `json:"overheadPct"`
	IsProtected       bool     `json:"isProtected"`
	Labour            []struct {
		MemberID        int     `json:"memberId"`
		MinutesPerBatch float64 `json:"minutesPerBatch"`
		Notes           string  `json:"notes"`
	} `json:"labour"`
	Components []struct {
		StockItemID int64   `json:"stockItemId"`
		SubRecipeID *int64  `json:"subRecipeId"`
		Quantity    float64 `json:"quantity"`
		UnitID      int64   `json:"unitId"`
		WastePct    float64 `json:"wastePct"`
		IsOptional  bool    `json:"isOptional"`
		Notes       string  `json:"notes"`
	} `json:"components"`
}

func stockProductionRequirement(componentBase, batches, wastePct float64) (float64, error) {
	if componentBase <= 0 || batches <= 0 || wastePct < 0 || wastePct >= 100 {
		return 0, errors.New("invalid production requirement")
	}
	return componentBase * batches / (1 - wastePct/100), nil
}

type stockBOMComponent struct {
	ItemID      int64
	SubRecipeID *int64
	QtyBase     float64
	WastePct    float64
}

type stockBOMRecipe struct {
	OutputQty  float64
	Components []stockBOMComponent
}

func expandStockRecipeRequirements(recipeID int64, batches float64, recipes map[int64]stockBOMRecipe) (map[int64]float64, error) {
	out := map[int64]float64{}
	visiting := map[int64]bool{}
	var expand func(int64, float64) error
	expand = func(id int64, multiplier float64) error {
		if visiting[id] {
			return errors.New("recipe cycle detected")
		}
		recipe, ok := recipes[id]
		if !ok || recipe.OutputQty <= 0 || multiplier <= 0 {
			return errors.New("invalid nested recipe")
		}
		visiting[id] = true
		defer delete(visiting, id)
		for _, component := range recipe.Components {
			needed, err := stockProductionRequirement(component.QtyBase, multiplier, component.WastePct)
			if err != nil {
				return err
			}
			if component.SubRecipeID == nil {
				out[component.ItemID] += needed
				continue
			}
			sub, ok := recipes[*component.SubRecipeID]
			if !ok || sub.OutputQty <= 0 {
				return errors.New("nested recipe not found")
			}
			if err := expand(*component.SubRecipeID, needed/sub.OutputQty); err != nil {
				return err
			}
		}
		return nil
	}
	if err := expand(recipeID, batches); err != nil {
		return nil, err
	}
	return out, nil
}

func loadStockBOM(ctx context.Context, tx *sql.Tx, restaurantID int) (map[int64]stockBOMRecipe, error) {
	rows, err := tx.QueryContext(ctx, `SELECT r.id,r.output_qty_base,c.stock_item_id,c.sub_recipe_id,c.qty_base,c.waste_pct FROM stock_recipes r LEFT JOIN stock_recipe_components c ON c.restaurant_id=r.restaurant_id AND c.recipe_id=r.id WHERE r.restaurant_id=? AND r.is_active=1 ORDER BY r.id,c.sort_order,c.id`, restaurantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	recipes := map[int64]stockBOMRecipe{}
	for rows.Next() {
		var recipeID int64
		var outputQty float64
		var itemID, subRecipeID sql.NullInt64
		var qty, waste sql.NullFloat64
		if err := rows.Scan(&recipeID, &outputQty, &itemID, &subRecipeID, &qty, &waste); err != nil {
			return nil, err
		}
		recipe := recipes[recipeID]
		recipe.OutputQty = outputQty
		if itemID.Valid {
			var sub *int64
			if subRecipeID.Valid {
				value := subRecipeID.Int64
				sub = &value
			}
			recipe.Components = append(recipe.Components, stockBOMComponent{ItemID: itemID.Int64, SubRecipeID: sub, QtyBase: qty.Float64, WastePct: waste.Float64})
		}
		recipes[recipeID] = recipe
	}
	return recipes, rows.Err()
}

func (s *Server) handleBOStockRecipesList(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT r.id,r.name,r.output_item_id,i.name,r.output_qty_base,r.waste_pct,r.prep_time_min,r.version,r.source,r.is_active,r.selling_price_gross,r.vat_rate_id,r.overhead_pct,r.is_protected FROM stock_recipes r JOIN stock_items i ON i.restaurant_id=r.restaurant_id AND i.id=r.output_item_id WHERE r.restaurant_id=? AND r.is_active=1 ORDER BY r.name`, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading recipes")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, itemID int64
		var name, itemName, source string
		var output, waste float64
		var prep sql.NullInt64
		var version, active, protected int
		var sellingPrice sql.NullFloat64
		var vatRateID sql.NullInt64
		var overhead float64
		if err = rows.Scan(&id, &name, &itemID, &itemName, &output, &waste, &prep, &version, &source, &active, &sellingPrice, &vatRateID, &overhead, &protected); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error reading recipes")
			return
		}
		out = append(out, map[string]any{"id": id, "name": name, "outputItemId": itemID, "outputItemName": itemName, "outputQuantityBase": output, "wastePct": waste, "prepTimeMin": stockNullableDBInt(prep), "version": version, "source": source, "isActive": active != 0, "sellingPriceGross": nullableFloat(sellingPrice), "vatRateId": stockNullableDBInt(vatRateID), "overheadPct": overhead, "isProtected": protected != 0})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "recipes": out})
}

func (s *Server) handleBOStockRecipeGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	recipeID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var recipe struct {
		ID               int64
		Name             string
		OutputItemID     int64
		OutputItemName   string
		OutputQty, Waste float64
		Prep             sql.NullInt64
		Instructions     string
		Version          int
		Source           string
		SellingPrice     sql.NullFloat64
		VATRateID        sql.NullInt64
		Overhead         float64
		Protected        int
	}
	err := s.db.QueryRowContext(r.Context(), `SELECT r.id,r.name,r.output_item_id,i.name,r.output_qty_base,r.waste_pct,r.prep_time_min,COALESCE(r.instructions,''),r.version,r.source,r.selling_price_gross,r.vat_rate_id,r.overhead_pct,r.is_protected FROM stock_recipes r JOIN stock_items i ON i.restaurant_id=r.restaurant_id AND i.id=r.output_item_id WHERE r.id=? AND r.restaurant_id=? AND r.is_active=1`, recipeID, a.ActiveRestaurantID).Scan(&recipe.ID, &recipe.Name, &recipe.OutputItemID, &recipe.OutputItemName, &recipe.OutputQty, &recipe.Waste, &recipe.Prep, &recipe.Instructions, &recipe.Version, &recipe.Source, &recipe.SellingPrice, &recipe.VATRateID, &recipe.Overhead, &recipe.Protected)
	if err != nil {
		if err == sql.ErrNoRows {
			httpx.WriteError(w, http.StatusNotFound, "Recipe not found")
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading recipe")
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT c.id,c.stock_item_id,i.name,c.sub_recipe_id,c.entered_qty,c.entered_unit_id,u.label,c.qty_base,c.waste_pct,c.is_optional,COALESCE(c.notes,'') FROM stock_recipe_components c JOIN stock_items i ON i.restaurant_id=c.restaurant_id AND i.id=c.stock_item_id JOIN stock_item_units u ON u.restaurant_id=c.restaurant_id AND u.id=c.entered_unit_id WHERE c.restaurant_id=? AND c.recipe_id=? ORDER BY c.sort_order,c.id`, a.ActiveRestaurantID, recipeID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading components")
		return
	}
	defer rows.Close()
	components := []map[string]any{}
	for rows.Next() {
		var id, itemID, unitID int64
		var sub sql.NullInt64
		var itemName, unitLabel, notes string
		var entered, base, waste float64
		var optional int
		if err = rows.Scan(&id, &itemID, &itemName, &sub, &entered, &unitID, &unitLabel, &base, &waste, &optional, &notes); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error reading components")
			return
		}
		components = append(components, map[string]any{"id": id, "stockItemId": itemID, "stockItemName": itemName, "subRecipeId": stockNullableDBInt(sub), "enteredQuantity": entered, "unitId": unitID, "unitLabel": unitLabel, "quantityBase": base, "wastePct": waste, "isOptional": optional != 0, "notes": notes})
	}
	labourRows, err := s.db.QueryContext(r.Context(), `SELECT l.restaurant_member_id,CONCAT(m.first_name,' ',m.last_name),l.minutes_per_batch,COALESCE(l.notes,'') FROM stock_recipe_labour l JOIN restaurant_members m ON m.restaurant_id=l.restaurant_id AND m.id=l.restaurant_member_id WHERE l.restaurant_id=? AND l.recipe_id=? ORDER BY l.sort_order,l.id`, a.ActiveRestaurantID, recipeID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading recipe labour")
		return
	}
	defer labourRows.Close()
	labour := []map[string]any{}
	for labourRows.Next() {
		var memberID int
		var name, notes string
		var minutes float64
		if err = labourRows.Scan(&memberID, &name, &minutes, &notes); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error reading recipe labour")
			return
		}
		labour = append(labour, map[string]any{"memberId": memberID, "memberName": name, "minutesPerBatch": minutes, "notes": notes})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "recipe": map[string]any{"id": recipe.ID, "name": recipe.Name, "outputItemId": recipe.OutputItemID, "outputItemName": recipe.OutputItemName, "outputQuantityBase": recipe.OutputQty, "wastePct": recipe.Waste, "prepTimeMin": stockNullableDBInt(recipe.Prep), "instructions": recipe.Instructions, "version": recipe.Version, "source": recipe.Source, "sellingPriceGross": nullableFloat(recipe.SellingPrice), "vatRateId": stockNullableDBInt(recipe.VATRateID), "overheadPct": recipe.Overhead, "isProtected": recipe.Protected != 0, "components": components, "labour": labour}})
}

func (s *Server) saveBOStockRecipe(w http.ResponseWriter, r *http.Request, recipeID int64) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	var in stockRecipeInput
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&in) != nil || strings.TrimSpace(in.Name) == "" || in.OutputItemID <= 0 || in.OutputUnitID <= 0 || in.OutputQuantity <= 0 || len(in.Components) == 0 || in.WastePct < 0 || in.WastePct >= 100 || in.OverheadPct < 0 || in.OverheadPct > 100 || (in.SellingPriceGross != nil && *in.SellingPriceGross < 0) {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid recipe")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error saving recipe")
		return
	}
	defer tx.Rollback()
	var outputFactor float64
	if err = tx.QueryRowContext(r.Context(), `SELECT factor_to_base FROM stock_item_units WHERE id=? AND stock_item_id=? AND restaurant_id=?`, in.OutputUnitID, in.OutputItemID, a.ActiveRestaurantID).Scan(&outputFactor); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid output unit")
		return
	}
	outputBase := in.OutputQuantity * outputFactor
	if recipeID == 0 {
		res, err := tx.ExecContext(r.Context(), `INSERT INTO stock_recipes (restaurant_id,name,output_item_id,output_qty_base,waste_pct,prep_time_min,instructions,selling_price_gross,vat_rate_id,overhead_pct,is_protected) VALUES (?,?,?,?,?,?,?,?,?,?,?)`, a.ActiveRestaurantID, strings.TrimSpace(in.Name), in.OutputItemID, outputBase, in.WastePct, in.PrepTimeMin, stockNullableString(in.Instructions), in.SellingPriceGross, in.VATRateID, in.OverheadPct, stockBoolInt(in.IsProtected))
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Recipe could not be created")
			return
		}
		recipeID, _ = res.LastInsertId()
	} else {
		res, err := tx.ExecContext(r.Context(), `UPDATE stock_recipes SET name=?,output_item_id=?,output_qty_base=?,waste_pct=?,prep_time_min=?,instructions=?,selling_price_gross=?,vat_rate_id=?,overhead_pct=?,is_protected=?,version=version+1 WHERE id=? AND restaurant_id=? AND is_active=1`, strings.TrimSpace(in.Name), in.OutputItemID, outputBase, in.WastePct, in.PrepTimeMin, stockNullableString(in.Instructions), in.SellingPriceGross, in.VATRateID, in.OverheadPct, stockBoolInt(in.IsProtected), recipeID, a.ActiveRestaurantID)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Recipe could not be updated")
			return
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			httpx.WriteError(w, http.StatusNotFound, "Recipe not found")
			return
		}
		if _, err = tx.ExecContext(r.Context(), `DELETE FROM stock_recipe_components WHERE recipe_id=? AND restaurant_id=?`, recipeID, a.ActiveRestaurantID); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error saving recipe")
			return
		}
		if _, err = tx.ExecContext(r.Context(), `DELETE FROM stock_recipe_labour WHERE recipe_id=? AND restaurant_id=?`, recipeID, a.ActiveRestaurantID); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error saving recipe labour")
			return
		}
	}
	for index, component := range in.Components {
		if component.StockItemID <= 0 || component.UnitID <= 0 || component.Quantity <= 0 || component.StockItemID == in.OutputItemID || component.WastePct < 0 || component.WastePct >= 100 {
			httpx.WriteError(w, http.StatusBadRequest, "Invalid recipe component")
			return
		}
		var factor float64
		if err = tx.QueryRowContext(r.Context(), `SELECT factor_to_base FROM stock_item_units WHERE id=? AND stock_item_id=? AND restaurant_id=?`, component.UnitID, component.StockItemID, a.ActiveRestaurantID).Scan(&factor); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Invalid component unit")
			return
		}
		if component.SubRecipeID != nil {
			if *component.SubRecipeID == recipeID {
				httpx.WriteError(w, http.StatusBadRequest, "Recipe cycle detected")
				return
			}
			var subOutputItemID int64
			if err = tx.QueryRowContext(r.Context(), `SELECT output_item_id FROM stock_recipes WHERE restaurant_id=? AND id=? AND is_active=1`, a.ActiveRestaurantID, *component.SubRecipeID).Scan(&subOutputItemID); err != nil || subOutputItemID != component.StockItemID {
				httpx.WriteError(w, http.StatusBadRequest, "Sub-recipe must produce selected component item")
				return
			}
		}
		_, err = tx.ExecContext(r.Context(), `INSERT INTO stock_recipe_components (restaurant_id,recipe_id,stock_item_id,sub_recipe_id,entered_qty,entered_unit_id,qty_base,waste_pct,is_optional,notes,sort_order) VALUES (?,?,?,?,?,?,?,?,?,?,?)`, a.ActiveRestaurantID, recipeID, component.StockItemID, component.SubRecipeID, component.Quantity, component.UnitID, component.Quantity*factor, component.WastePct, stockBoolInt(component.IsOptional), stockNullableString(component.Notes), index)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Recipe component could not be saved")
			return
		}
	}
	for index, labour := range in.Labour {
		if labour.MemberID <= 0 || labour.MinutesPerBatch <= 0 {
			httpx.WriteError(w, http.StatusBadRequest, "Invalid recipe labour")
			return
		}
		res, err := tx.ExecContext(r.Context(), `INSERT INTO stock_recipe_labour (restaurant_id,recipe_id,restaurant_member_id,minutes_per_batch,notes,sort_order) SELECT ?,?,?,?,?,? FROM restaurant_members WHERE restaurant_id=? AND id=? AND is_active=1`, a.ActiveRestaurantID, recipeID, labour.MemberID, labour.MinutesPerBatch, stockNullableString(labour.Notes), index, a.ActiveRestaurantID, labour.MemberID)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Recipe labour could not be saved")
			return
		}
		if affected, _ := res.RowsAffected(); affected == 0 {
			httpx.WriteError(w, http.StatusBadRequest, "Invalid recipe labour member")
			return
		}
	}
	bom, err := loadStockBOM(r.Context(), tx, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error validating recipe")
		return
	}
	if _, err = expandStockRecipeRequirements(recipeID, 1, bom); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error saving recipe")
		return
	}
	status := http.StatusOK
	if r.Method == http.MethodPost {
		status = http.StatusCreated
	}
	httpx.WriteJSON(w, status, map[string]any{"success": true, "id": recipeID})
}

func (s *Server) handleBOStockRecipeCreate(w http.ResponseWriter, r *http.Request) {
	s.saveBOStockRecipe(w, r, 0)
}
func (s *Server) handleBOStockRecipePatch(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if id <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid recipe")
		return
	}
	s.saveBOStockRecipe(w, r, id)
}
func (s *Server) handleBOStockRecipePricingPatch(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var in struct {
		SellingPriceGross *float64 `json:"sellingPriceGross"`
		VATRateID         *int64   `json:"vatRateId"`
		OverheadPct       float64  `json:"overheadPct"`
		IsProtected       bool     `json:"isProtected"`
	}
	if id <= 0 || json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || in.OverheadPct < 0 || in.OverheadPct > 100 || in.SellingPriceGross != nil && *in.SellingPriceGross < 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid recipe pricing")
		return
	}
	if in.VATRateID != nil {
		var exists int
		if err := s.db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM stock_vat_rates WHERE restaurant_id=? AND id=? AND is_active=1)`, a.ActiveRestaurantID, *in.VATRateID).Scan(&exists); err != nil || exists == 0 {
			httpx.WriteError(w, http.StatusBadRequest, "Invalid VAT rate")
			return
		}
	}
	res, err := s.db.ExecContext(r.Context(), `UPDATE stock_recipes SET selling_price_gross=?,vat_rate_id=?,overhead_pct=?,is_protected=?,version=version+1 WHERE restaurant_id=? AND id=? AND is_active=1`, in.SellingPriceGross, in.VATRateID, in.OverheadPct, stockBoolInt(in.IsProtected), a.ActiveRestaurantID, id)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Recipe pricing could not be saved")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		httpx.WriteError(w, http.StatusNotFound, "Recipe not found")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleBOStockRecipeDelete(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	res, err := s.db.ExecContext(r.Context(), `UPDATE stock_recipes SET is_active=0,version=version+1 WHERE id=? AND restaurant_id=?`, id, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error deleting recipe")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		httpx.WriteError(w, http.StatusNotFound, "Recipe not found")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleBOStockProductionPreview(w http.ResponseWriter, r *http.Request) {
	s.handleBOStockProduction(w, r, true)
}
func (s *Server) handleBOStockProductionCreate(w http.ResponseWriter, r *http.Request) {
	s.handleBOStockProduction(w, r, false)
}
func (s *Server) handleBOStockProduction(w http.ResponseWriter, r *http.Request, preview bool) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	recipeID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var in struct {
		WarehouseID    int64   `json:"warehouseId"`
		Batches        float64 `json:"batches"`
		IdempotencyKey string  `json:"idempotencyKey"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || recipeID <= 0 || in.WarehouseID <= 0 || in.Batches <= 0 || (!preview && strings.TrimSpace(in.IdempotencyKey) == "") {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid production request")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error preparing production")
		return
	}
	defer tx.Rollback()
	var outputItemID int64
	var outputQty float64
	var version int
	if err = tx.QueryRowContext(r.Context(), `SELECT output_item_id,output_qty_base,version FROM stock_recipes WHERE id=? AND restaurant_id=? AND is_active=1`, recipeID, a.ActiveRestaurantID).Scan(&outputItemID, &outputQty, &version); err != nil {
		httpx.WriteError(w, http.StatusNotFound, "Recipe not found")
		return
	}
	bom, err := loadStockBOM(r.Context(), tx, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading recipe tree")
		return
	}
	requirements, err := expandStockRecipeRequirements(recipeID, in.Batches, bom)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	type line struct {
		ItemID, UnitID                int64
		Name                          string
		Needed, Available, UnitFactor float64
		Tracked                       bool
	}
	directLabour, missingLabour, err := loadStockRecipeLabourCosts(r.Context(), tx, a.ActiveRestaurantID, boTodayDate())
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading labour cost")
		return
	}
	labourPerOutput, missingMembers, err := stockRecipeLabourCost(recipeID, bom, directLabour, missingLabour)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	standardLabourCost := labourPerOutput * outputQty * in.Batches
	lines := make([]line, 0, len(requirements))
	for itemID, needed := range requirements {
		var x line
		var tracked int
		x.ItemID, x.Needed = itemID, needed
		if err = tx.QueryRowContext(r.Context(), `SELECT i.name,i.is_tracked,COALESCE(l.qty_base,0),u.id,u.factor_to_base FROM stock_items i JOIN stock_item_units u ON u.restaurant_id=i.restaurant_id AND u.stock_item_id=i.id AND u.is_default_display=1 LEFT JOIN stock_levels l ON l.restaurant_id=i.restaurant_id AND l.stock_item_id=i.id AND l.warehouse_id=? WHERE i.restaurant_id=? AND i.id=? AND i.deleted_at IS NULL`, in.WarehouseID, a.ActiveRestaurantID, itemID).Scan(&x.Name, &tracked, &x.Available, &x.UnitID, &x.UnitFactor); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Recipe component is unavailable")
			return
		}
		x.Tracked = tracked != 0
		lines = append(lines, x)
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i].Name < lines[j].Name })
	payload := make([]map[string]any, 0, len(lines))
	for _, x := range lines {
		payload = append(payload, map[string]any{"stockItemId": x.ItemID, "name": x.Name, "neededQuantityBase": x.Needed, "availableQuantityBase": x.Available, "afterQuantityBase": x.Available - x.Needed, "isTracked": x.Tracked, "shortage": x.Tracked && x.Available < x.Needed})
	}
	if preview {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "outputItemId": outputItemID, "outputQuantityBase": outputQty * in.Batches, "standardLabourCost": round2(standardLabourCost), "missingLabourMembers": missingMembers, "components": payload})
		return
	}
	res, err := tx.ExecContext(r.Context(), `INSERT INTO stock_production_orders (restaurant_id,recipe_id,recipe_version,warehouse_id,batches,qty_produced_base,standard_labour_cost,labour_cost_complete,idempotency_key,actor_user_id) VALUES (?,?,?,?,?,?,?,?,?,?)`, a.ActiveRestaurantID, recipeID, version, in.WarehouseID, in.Batches, outputQty*in.Batches, standardLabourCost, stockBoolInt(len(missingMembers) == 0), in.IdempotencyKey, a.User.ID)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "duplicate": true})
			return
		}
		httpx.WriteError(w, http.StatusBadRequest, "Production could not be created")
		return
	}
	orderID, _ := res.LastInsertId()
	var allowNegative int
	_ = tx.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT allow_negative_stock FROM stock_settings WHERE restaurant_id=?),1)`, a.ActiveRestaurantID).Scan(&allowNegative)
	for index, x := range lines {
		_, err = tx.ExecContext(r.Context(), `INSERT INTO stock_production_order_lines (restaurant_id,production_order_id,stock_item_id,planned_qty_base,actual_qty_base,was_untracked) VALUES (?,?,?,?,?,?)`, a.ActiveRestaurantID, orderID, x.ItemID, x.Needed, x.Needed, stockBoolInt(!x.Tracked))
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error saving production")
			return
		}
		if !x.Tracked {
			continue
		}
		if allowNegative == 0 && x.Available < x.Needed {
			httpx.WriteError(w, http.StatusConflict, "Insufficient component stock")
			return
		}
		_, err = tx.ExecContext(r.Context(), `INSERT INTO stock_levels (restaurant_id,stock_item_id,warehouse_id,qty_base) VALUES (?,?,?,0) ON DUPLICATE KEY UPDATE stock_item_id=VALUES(stock_item_id)`, a.ActiveRestaurantID, x.ItemID, in.WarehouseID)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error saving production")
			return
		}
		_, err = tx.ExecContext(r.Context(), `INSERT INTO stock_movements (restaurant_id,stock_item_id,warehouse_id,qty_base,type,entered_qty,entered_unit_id,ref_type,ref_id,idempotency_key,actor_user_id) VALUES (?,?,?,?,?,?,?,?,?,?,?)`, a.ActiveRestaurantID, x.ItemID, in.WarehouseID, -x.Needed, "PRODUCTION_OUT", x.Needed/x.UnitFactor, x.UnitID, "production_order", orderID, in.IdempotencyKey+"-component-"+strconv.Itoa(index), a.User.ID)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error saving production")
			return
		}
		_, err = tx.ExecContext(r.Context(), `UPDATE stock_levels SET qty_base=qty_base-?,version=version+1 WHERE restaurant_id=? AND stock_item_id=? AND warehouse_id=?`, x.Needed, a.ActiveRestaurantID, x.ItemID, in.WarehouseID)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error saving production")
			return
		}
	}
	var outputUnitID int64
	var outputUnitFactor float64
	if err = tx.QueryRowContext(r.Context(), `SELECT id,factor_to_base FROM stock_item_units WHERE restaurant_id=? AND stock_item_id=? AND is_default_display=1`, a.ActiveRestaurantID, outputItemID).Scan(&outputUnitID, &outputUnitFactor); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Output item needs a default unit")
		return
	}
	_, err = tx.ExecContext(r.Context(), `INSERT INTO stock_levels (restaurant_id,stock_item_id,warehouse_id,qty_base) VALUES (?,?,?,0) ON DUPLICATE KEY UPDATE stock_item_id=VALUES(stock_item_id)`, a.ActiveRestaurantID, outputItemID, in.WarehouseID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error saving production")
		return
	}
	_, err = tx.ExecContext(r.Context(), `INSERT INTO stock_movements (restaurant_id,stock_item_id,warehouse_id,qty_base,type,entered_qty,entered_unit_id,ref_type,ref_id,idempotency_key,actor_user_id) VALUES (?,?,?,?,?,?,?,?,?,?,?)`, a.ActiveRestaurantID, outputItemID, in.WarehouseID, outputQty*in.Batches, "PRODUCTION_IN", outputQty*in.Batches/outputUnitFactor, outputUnitID, "production_order", orderID, in.IdempotencyKey+"-output", a.User.ID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error saving production")
		return
	}
	_, err = tx.ExecContext(r.Context(), `UPDATE stock_levels SET qty_base=qty_base+?,version=version+1 WHERE restaurant_id=? AND stock_item_id=? AND warehouse_id=?`, outputQty*in.Batches, a.ActiveRestaurantID, outputItemID, in.WarehouseID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error saving production")
		return
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error saving production")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"success": true, "id": orderID, "components": payload})
}

func stockNullableDBInt(v driver.Valuer) any {
	value, err := v.Value()
	if err != nil || value == nil {
		return nil
	}
	return value
}
