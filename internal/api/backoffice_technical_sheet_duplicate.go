package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"preactvillacarmen/internal/httpx"
)

// Decision #5: a technical sheet is never shared between two products. Linking
// an existing sheet to a second product deep-copies it, so changing the paella
// recipe for the lunch menu can never silently change the a-la-carte one.
// Everything is copied in one transaction; a partial copy would leave an
// orphaned output item that pollutes the stock catalogue.
func (s *Server) handleBOTechnicalSheetDuplicate(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	sourceID := sheetIDParam(r)

	var in struct {
		Name string `json:"name"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Datos invalidos")
		return
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error duplicando la ficha")
		return
	}
	defer tx.Rollback()

	var sourceName, imageURL string
	var portions int
	var wastePct float64
	var prepTime, vatRateID, overheadPct any
	if err := tx.QueryRowContext(r.Context(), `
		SELECT r.name, COALESCE(i.image_url,''), COALESCE(r.portions,1), r.waste_pct,
		       r.prep_time_min, r.vat_rate_id, r.overhead_pct
		  FROM stock_recipes r
		  JOIN stock_items i ON i.restaurant_id=r.restaurant_id AND i.id=r.output_item_id
		 WHERE r.restaurant_id=? AND r.id=?`,
		a.ActiveRestaurantID, sourceID).
		Scan(&sourceName, &imageURL, &portions, &wastePct, &prepTime, &vatRateID, &overheadPct); err != nil {
		httpx.WriteError(w, http.StatusNotFound, "Ficha tecnica no encontrada")
		return
	}

	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = sourceName + " (copia)"
	}

	// The copy gets its own output item: sharing one would make both sheets
	// write production into the same stock line.
	itemRes, err := tx.ExecContext(r.Context(), `
		INSERT INTO stock_items (restaurant_id,name,kind,base_dimension,base_unit,is_tracked,deduction_source,image_url)
		VALUES (?,?,'SEMI_FINISHED','COUNT','ud',1,'SALE',NULLIF(?,''))`,
		a.ActiveRestaurantID, name, imageURL)
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

	// The copy starts as a DRAFT owned by whoever duplicated it, so an
	// unfinished copy never reaches a menu and the published-output unique key
	// does not fire while it is being edited.
	recipeRes, err := tx.ExecContext(r.Context(), `
		INSERT INTO stock_recipes
			(restaurant_id,name,output_item_id,output_qty_base,waste_pct,prep_time_min,portions,
			 vat_rate_id,overhead_pct,status,is_active,draft_owner_user_id,copied_from_recipe_id)
		VALUES (?,?,?,1,?,?,?,?,?,'DRAFT',1,?,?)`,
		a.ActiveRestaurantID, name, outputItemID, wastePct, prepTime, portions,
		vatRateID, overheadPct, a.User.ID, sourceID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error duplicando la ficha")
		return
	}
	copyID, _ := recipeRes.LastInsertId()

	// Components keep pointing at the same ingredients and sub-recipes: only
	// this sheet's own rows are duplicated, not the whole ingredient tree.
	if _, err = tx.ExecContext(r.Context(), `
		INSERT INTO stock_recipe_components
			(restaurant_id,recipe_id,stock_item_id,sub_recipe_id,entered_qty,entered_unit_id,qty_base,waste_pct,is_optional,notes,sort_order)
		SELECT restaurant_id,?,stock_item_id,sub_recipe_id,entered_qty,entered_unit_id,qty_base,waste_pct,is_optional,notes,sort_order
		  FROM stock_recipe_components WHERE restaurant_id=? AND recipe_id=?`,
		copyID, a.ActiveRestaurantID, sourceID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error copiando los ingredientes")
		return
	}

	// Step images are referenced by URL, not re-uploaded: the CDN object is
	// immutable and shared safely, while the sweep registry keeps both
	// referencing rows so neither copy can orphan the other's image.
	if _, err = tx.ExecContext(r.Context(), `
		INSERT INTO stock_recipe_steps
			(restaurant_id,recipe_id,step_no,title,description,image_url,image_object_path,generation_status,generation_mode)
		SELECT restaurant_id,?,step_no,title,description,image_url,image_object_path,
		       CASE WHEN generation_status='READY' THEN 'READY' ELSE 'NONE' END, generation_mode
		  FROM stock_recipe_steps WHERE restaurant_id=? AND recipe_id=?`,
		copyID, a.ActiveRestaurantID, sourceID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error copiando los pasos")
		return
	}

	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error duplicando la ficha")
		return
	}

	// Derived allergens are recomputed rather than copied so the new sheet can
	// never inherit a stale cache from the original.
	if err := s.refreshSheetDerivedAllergens(r.Context(), a.ActiveRestaurantID, copyID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error recalculando alergenos")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true, "sheetId": copyID, "outputItemId": outputItemID,
	})
}
