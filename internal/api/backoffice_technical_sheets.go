package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"preactvillacarmen/internal/httpx"
)

// A technical sheet (ficha tecnica) IS a stock_recipes row (decision D2) plus
// its components, its steps and a derived allergen set. There is deliberately
// no parallel recipe system: production, costing and POS deduction already read
// stock_recipes, so a second table would silently diverge from what the kitchen
// actually produces.

type technicalSheet struct {
	ID             int64    `json:"id"`
	Name           string   `json:"name"`
	Status         string   `json:"status"`
	Portions       int      `json:"portions"`
	OutputItemID   int64    `json:"outputItemId"`
	OutputQtyBase  float64  `json:"outputQtyBase"`
	WastePct       float64  `json:"wastePct"`
	PrepTimeMin    *int     `json:"prepTimeMin,omitempty"`
	Instructions   string   `json:"instructions,omitempty"`
	CopiedFrom     *int64   `json:"copiedFromRecipeId,omitempty"`
	ComponentCount int      `json:"componentCount"`
	StepCount      int      `json:"stepCount"`
	Allergens      []string `json:"allergens"`
}

func sheetIDParam(r *http.Request) int64 {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	return id
}

// loadSheet returns the sheet only when it belongs to the caller's tenant.
// Every query in this file carries restaurant_id for the same reason.
func (s *Server) loadSheet(ctx context.Context, restaurantID int, sheetID int64) (technicalSheet, error) {
	var sheet technicalSheet
	var prep sql.NullInt64
	var instructions sql.NullString
	var copiedFrom sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT r.id, r.name, r.status, r.portions, r.output_item_id, r.output_qty_base,
		       r.waste_pct, r.prep_time_min, r.instructions, r.copied_from_recipe_id,
		       (SELECT COUNT(*) FROM stock_recipe_components c WHERE c.restaurant_id=r.restaurant_id AND c.recipe_id=r.id),
		       (SELECT COUNT(*) FROM stock_recipe_steps st WHERE st.restaurant_id=r.restaurant_id AND st.recipe_id=r.id)
		  FROM stock_recipes r
		 WHERE r.restaurant_id=? AND r.id=?`, restaurantID, sheetID).
		Scan(&sheet.ID, &sheet.Name, &sheet.Status, &sheet.Portions, &sheet.OutputItemID,
			&sheet.OutputQtyBase, &sheet.WastePct, &prep, &instructions, &copiedFrom,
			&sheet.ComponentCount, &sheet.StepCount)
	if err != nil {
		return technicalSheet{}, err
	}
	if prep.Valid {
		v := int(prep.Int64)
		sheet.PrepTimeMin = &v
	}
	sheet.Instructions = instructions.String
	if copiedFrom.Valid {
		sheet.CopiedFrom = &copiedFrom.Int64
	}
	return sheet, nil
}

func (s *Server) handleBOTechnicalSheetCreate(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	var in struct {
		Name        string  `json:"name"`
		Portions    int     `json:"portions"`
		PrepTimeMin *int    `json:"prepTimeMin"`
		ImageURL    string  `json:"imageUrl"`
		WastePct    float64 `json:"wastePct"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid technical sheet")
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		httpx.WriteError(w, http.StatusBadRequest, "El nombre de la ficha es obligatorio")
		return
	}
	if in.Portions <= 0 {
		in.Portions = 1
	}
	if in.WastePct < 0 || in.WastePct >= 100 {
		httpx.WriteError(w, http.StatusBadRequest, "Merma invalida")
		return
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creando ficha tecnica")
		return
	}
	defer tx.Rollback()

	// D4: the output stock item, its base unit and the recipe are created
	// together. A sheet without an output item could never be produced, and an
	// item created outside the transaction would survive a failed create.
	itemRes, err := tx.ExecContext(r.Context(), `
		INSERT INTO stock_items (restaurant_id,name,kind,base_dimension,base_unit,is_tracked,deduction_source,image_url)
		VALUES (?,?,'SEMI_FINISHED','COUNT','ud',1,'SALE',NULLIF(?,''))`, a.ActiveRestaurantID, name, in.ImageURL)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creando el articulo de salida")
		return
	}
	outputItemID, _ := itemRes.LastInsertId()

	if _, err = tx.ExecContext(r.Context(), `
		INSERT INTO stock_item_units (restaurant_id,stock_item_id,code,label,factor_to_base,is_default_display,can_recipe,can_count)
		VALUES (?,?,'ud','ud',1,1,1,1)`, a.ActiveRestaurantID, outputItemID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creando la unidad de salida")
		return
	}

	// New sheets start as DRAFT so an unfinished sheet never reaches the menu,
	// and so the partial-unique output key does not fire while editing.
	recipeRes, err := tx.ExecContext(r.Context(), `
		INSERT INTO stock_recipes (restaurant_id,name,output_item_id,output_qty_base,waste_pct,prep_time_min,portions,status,is_active,draft_owner_user_id)
		VALUES (?,?,?,1,?,?,?, 'DRAFT',1,?)`,
		a.ActiveRestaurantID, name, outputItemID, in.WastePct, in.PrepTimeMin, in.Portions, a.User.ID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creando ficha tecnica")
		return
	}
	sheetID, _ := recipeRes.LastInsertId()

	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creando ficha tecnica")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true, "sheetId": sheetID, "outputItemId": outputItemID,
	})
}

func (s *Server) handleBOTechnicalSheetGet(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	sheet, err := s.loadSheet(r.Context(), a.ActiveRestaurantID, sheetIDParam(r))
	if err == sql.ErrNoRows {
		httpx.WriteError(w, http.StatusNotFound, "Ficha tecnica no encontrada")
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error cargando ficha tecnica")
		return
	}
	derived, _, manual, err := s.sheetAllergens(r.Context(), a.ActiveRestaurantID, sheet.ID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error calculando alergenos")
		return
	}
	sheet.Allergens = resolveSheetAllergens(derived, manual)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "sheet": sheet})
}

func (s *Server) handleBOTechnicalSheetPatch(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	sheetID := sheetIDParam(r)
	var in struct {
		Name         *string  `json:"name"`
		Portions     *int     `json:"portions"`
		PrepTimeMin  *int     `json:"prepTimeMin"`
		WastePct     *float64 `json:"wastePct"`
		Instructions *string  `json:"instructions"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Datos invalidos")
		return
	}
	sets := []string{}
	args := []any{}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			httpx.WriteError(w, http.StatusBadRequest, "El nombre no puede estar vacio")
			return
		}
		sets = append(sets, "name=?")
		args = append(args, name)
	}
	if in.Portions != nil {
		if *in.Portions <= 0 {
			httpx.WriteError(w, http.StatusBadRequest, "Las porciones deben ser mayores que cero")
			return
		}
		sets = append(sets, "portions=?")
		args = append(args, *in.Portions)
	}
	if in.PrepTimeMin != nil {
		if *in.PrepTimeMin < 0 {
			httpx.WriteError(w, http.StatusBadRequest, "Tiempo de preparacion invalido")
			return
		}
		sets = append(sets, "prep_time_min=?")
		args = append(args, *in.PrepTimeMin)
	}
	if in.WastePct != nil {
		if *in.WastePct < 0 || *in.WastePct >= 100 {
			httpx.WriteError(w, http.StatusBadRequest, "Merma invalida")
			return
		}
		sets = append(sets, "waste_pct=?")
		args = append(args, *in.WastePct)
	}
	if in.Instructions != nil {
		sets = append(sets, "instructions=?")
		args = append(args, *in.Instructions)
	}
	if len(sets) == 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Nada que actualizar")
		return
	}
	args = append(args, a.ActiveRestaurantID, sheetID)
	res, err := s.db.ExecContext(r.Context(),
		`UPDATE stock_recipes SET `+strings.Join(sets, ",")+` WHERE restaurant_id=? AND id=?`, args...)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando ficha tecnica")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Either the sheet does not exist or it belongs to another tenant. Both
		// answer 404 so the endpoint cannot be used to probe for foreign ids.
		var exists int
		s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM stock_recipes WHERE restaurant_id=? AND id=?`,
			a.ActiveRestaurantID, sheetID).Scan(&exists)
		if exists == 0 {
			httpx.WriteError(w, http.StatusNotFound, "Ficha tecnica no encontrada")
			return
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleBOTechnicalSheetPublish(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	sheetID := sheetIDParam(r)

	var components int
	err := s.db.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM stock_recipe_components WHERE restaurant_id=? AND recipe_id=?`,
		a.ActiveRestaurantID, sheetID).Scan(&components)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error validando ficha tecnica")
		return
	}
	// A sheet with no ingredients would cost nothing and declare no allergens.
	if components == 0 {
		httpx.WriteError(w, http.StatusBadRequest, "La ficha necesita al menos un ingrediente para publicarse")
		return
	}

	res, err := s.db.ExecContext(r.Context(),
		`UPDATE stock_recipes SET status='ACTIVE', is_active=1, draft_owner_user_id=NULL, draft_expires_at=NULL
		  WHERE restaurant_id=? AND id=? AND status='DRAFT'`, a.ActiveRestaurantID, sheetID)
	if err != nil {
		// uq_stock_recipe_output rejects a second ACTIVE recipe for one output
		// item; surface that as a conflict rather than a generic failure.
		httpx.WriteError(w, http.StatusConflict, "Ya existe una ficha activa para este articulo de salida")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		httpx.WriteError(w, http.StatusNotFound, "Ficha tecnica en borrador no encontrada")
		return
	}
	if err := s.refreshSheetDerivedAllergens(r.Context(), a.ActiveRestaurantID, sheetID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error calculando alergenos")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

// sheetAllergens loads the component tree once, derives the read-only allergen
// set from it, and returns it alongside the stored manual layer.
func (s *Server) sheetAllergens(ctx context.Context, restaurantID int, sheetID int64) (
	derived []string, contributors map[string][]string, manual manualAllergens, err error,
) {
	// Load every component row for the tenant once. Recipes are small and this
	// avoids issuing one query per nesting level inside the walk.
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.recipe_id, c.stock_item_id, c.sub_recipe_id, i.name, i.allergens_json
		  FROM stock_recipe_components c
		  JOIN stock_items i ON i.restaurant_id=c.restaurant_id AND i.id=c.stock_item_id
		 WHERE c.restaurant_id=?`, restaurantID)
	if err != nil {
		return nil, nil, manual, err
	}
	defer rows.Close()

	tree := map[int64][]allergenTreeNode{}
	itemAllergens := map[int64][]string{}
	for rows.Next() {
		var recipeID, itemID int64
		var subRecipeID sql.NullInt64
		var itemName string
		var allergensJSON sql.NullString
		if err = rows.Scan(&recipeID, &itemID, &subRecipeID, &itemName, &allergensJSON); err != nil {
			return nil, nil, manual, err
		}
		node := allergenTreeNode{ItemID: itemID, ItemName: itemName}
		if subRecipeID.Valid {
			id := subRecipeID.Int64
			node.SubRecipeID = &id
		}
		tree[recipeID] = append(tree[recipeID], node)
		if allergensJSON.Valid && allergensJSON.String != "" {
			var list []string
			if json.Unmarshal([]byte(allergensJSON.String), &list) == nil {
				itemAllergens[itemID] = list
			}
		}
	}
	if err = rows.Err(); err != nil {
		return nil, nil, manual, err
	}

	derived, contributors, err = deriveAllergensFromTree(sheetID, tree, itemAllergens)
	if err != nil {
		return nil, nil, manual, err
	}

	var manualJSON sql.NullString
	if err = s.db.QueryRowContext(ctx,
		`SELECT manual_allergens_json FROM stock_recipes WHERE restaurant_id=? AND id=?`,
		restaurantID, sheetID).Scan(&manualJSON); err != nil {
		return nil, nil, manual, err
	}
	if manualJSON.Valid && manualJSON.String != "" {
		_ = json.Unmarshal([]byte(manualJSON.String), &manual)
	}
	return derived, contributors, manual, nil
}

func (s *Server) refreshSheetDerivedAllergens(ctx context.Context, restaurantID int, sheetID int64) error {
	derived, _, _, err := s.sheetAllergens(ctx, restaurantID, sheetID)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(derived)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE stock_recipes SET derived_allergens_json=?, derived_allergens_at=NOW() WHERE restaurant_id=? AND id=?`,
		string(encoded), restaurantID, sheetID)
	return err
}

func (s *Server) handleBOTechnicalSheetAllergensGet(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	derived, contributors, manual, err := s.sheetAllergens(r.Context(), a.ActiveRestaurantID, sheetIDParam(r))
	if err == sql.ErrNoRows {
		httpx.WriteError(w, http.StatusNotFound, "Ficha tecnica no encontrada")
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error calculando alergenos")
		return
	}
	if contributors == nil {
		contributors = map[string][]string{}
	}
	// derived/manualDisabled are normalised to empty slices, never null: the
	// client calls .includes() on them and null would throw.
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":        true,
		"derived":        normalizeAllergenList(derived),
		"manualAdded":    normalizeAllergenList(manual.Added),
		"manualDisabled": normalizeAllergenList(manual.Disabled),
		"effective":      resolveSheetAllergens(derived, manual),
		"final":          resolveSheetAllergens(derived, manual),
		"contributors":   contributors,
		"catalogue":      canonicalAllergens,
	})
}

func (s *Server) handleBOTechnicalSheetAllergensPatch(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	sheetID := sheetIDParam(r)
	var in manualAllergens
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Alergenos invalidos")
		return
	}
	// Unknown strings are dropped rather than stored, so the manual layer can
	// only ever contain real allergens.
	clean := manualAllergens{
		Added:    normalizeAllergenList(in.Added),
		Disabled: normalizeAllergenList(in.Disabled),
	}
	encoded, err := json.Marshal(clean)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Alergenos invalidos")
		return
	}
	res, err := s.db.ExecContext(r.Context(),
		`UPDATE stock_recipes SET manual_allergens_json=? WHERE restaurant_id=? AND id=?`,
		string(encoded), a.ActiveRestaurantID, sheetID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error guardando alergenos")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var exists int
		s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM stock_recipes WHERE restaurant_id=? AND id=?`,
			a.ActiveRestaurantID, sheetID).Scan(&exists)
		if exists == 0 {
			httpx.WriteError(w, http.StatusNotFound, "Ficha tecnica no encontrada")
			return
		}
	}
	if err := s.refreshSheetDerivedAllergens(r.Context(), a.ActiveRestaurantID, sheetID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error calculando alergenos")
		return
	}
	// The response reports the effective set, so a client that tried to disable
	// a derived allergen immediately sees that it is still present.
	derived, contributors, manual, err := s.sheetAllergens(r.Context(), a.ActiveRestaurantID, sheetID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error calculando alergenos")
		return
	}
	if contributors == nil {
		contributors = map[string][]string{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":        true,
		"derived":        normalizeAllergenList(derived),
		"manualAdded":    normalizeAllergenList(manual.Added),
		"manualDisabled": normalizeAllergenList(manual.Disabled),
		"effective":      resolveSheetAllergens(derived, manual),
		"final":          resolveSheetAllergens(derived, manual),
		"contributors":   contributors,
		"catalogue":      canonicalAllergens,
	})
}
