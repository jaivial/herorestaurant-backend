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

// Component rows are the ingredients of a technical sheet. Quantity is stored
// twice on purpose: as entered (so the UI can show "0.5 kg") and converted to
// the item's base unit (so cost, production and allergen code never have to
// re-derive which unit a row was typed in).

// sheetComponentWouldCycle reports whether making parentRecipeID depend on
// childRecipeID closes a loop. The existing bulk recipe save only rejected a
// direct self-reference, which misses A -> B -> A. A cycle here would make the
// allergen walk and cost expansion non-terminating.
func (s *Server) sheetComponentWouldCycle(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, restaurantID int, parentRecipeID, childRecipeID int64) (bool, error) {
	if parentRecipeID == childRecipeID {
		return true, nil
	}
	rows, err := q.QueryContext(ctx,
		`SELECT recipe_id, sub_recipe_id FROM stock_recipe_components
		  WHERE restaurant_id=? AND sub_recipe_id IS NOT NULL`, restaurantID)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	edges := map[int64][]int64{}
	for rows.Next() {
		var recipeID, subRecipeID int64
		if err = rows.Scan(&recipeID, &subRecipeID); err != nil {
			return false, err
		}
		edges[recipeID] = append(edges[recipeID], subRecipeID)
	}
	if err = rows.Err(); err != nil {
		return false, err
	}
	// Walk down from the prospective child. If the parent is reachable, adding
	// the edge parent -> child would close a loop.
	seen := map[int64]bool{}
	stack := []int64{childRecipeID}
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if current == parentRecipeID {
			return true, nil
		}
		if seen[current] {
			continue
		}
		seen[current] = true
		stack = append(stack, edges[current]...)
	}
	return false, nil
}

// sheetOwnedByTenant resolves the sheet's output item and confirms ownership.
func (s *Server) sheetOwnedByTenant(ctx context.Context, restaurantID int, sheetID int64) (outputItemID int64, err error) {
	err = s.db.QueryRowContext(ctx,
		`SELECT output_item_id FROM stock_recipes WHERE restaurant_id=? AND id=?`, restaurantID, sheetID).
		Scan(&outputItemID)
	return outputItemID, err
}

type sheetComponentInput struct {
	StockItemID int64   `json:"stockItemId"`
	UnitID      int64   `json:"unitId"`
	Quantity    float64 `json:"quantity"`
	SubRecipeID *int64  `json:"subRecipeId"`
	WastePct    float64 `json:"wastePct"`
	IsOptional  bool    `json:"isOptional"`
	Notes       *string `json:"notes"`
}

func (s *Server) handleBOTechnicalSheetComponentCreate(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	sheetID := sheetIDParam(r)
	var in sheetComponentInput
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Ingrediente invalido")
		return
	}
	outputItemID, err := s.sheetOwnedByTenant(r.Context(), a.ActiveRestaurantID, sheetID)
	if err == sql.ErrNoRows {
		httpx.WriteError(w, http.StatusNotFound, "Ficha tecnica no encontrada")
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error cargando ficha tecnica")
		return
	}
	if in.StockItemID <= 0 || in.UnitID <= 0 || in.Quantity <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Cantidad, articulo y unidad son obligatorios")
		return
	}
	// A sheet consuming its own output is a one-step loop.
	if in.StockItemID == outputItemID {
		httpx.WriteError(w, http.StatusBadRequest, "Una ficha no puede consumir su propio articulo de salida")
		return
	}
	if in.WastePct < 0 || in.WastePct >= 100 {
		httpx.WriteError(w, http.StatusBadRequest, "Merma invalida")
		return
	}

	// The unit must belong to the item, otherwise the conversion factor would
	// silently produce a wrong base quantity.
	var factor float64
	if err := s.db.QueryRowContext(r.Context(),
		`SELECT factor_to_base FROM stock_item_units WHERE restaurant_id=? AND id=? AND stock_item_id=?`,
		a.ActiveRestaurantID, in.UnitID, in.StockItemID).Scan(&factor); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "La unidad no pertenece a este articulo")
		return
	}

	if in.SubRecipeID != nil {
		var subOutputItemID int64
		if err := s.db.QueryRowContext(r.Context(),
			`SELECT output_item_id FROM stock_recipes WHERE restaurant_id=? AND id=?`,
			a.ActiveRestaurantID, *in.SubRecipeID).Scan(&subOutputItemID); err != nil || subOutputItemID != in.StockItemID {
			httpx.WriteError(w, http.StatusBadRequest, "La sub-receta no produce el articulo seleccionado")
			return
		}
		cycles, err := s.sheetComponentWouldCycle(r.Context(), s.db, a.ActiveRestaurantID, sheetID, *in.SubRecipeID)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error validando sub-recetas")
			return
		}
		if cycles {
			httpx.WriteError(w, http.StatusBadRequest, "Ciclo de recetas detectado")
			return
		}
	}

	var nextSort int
	s.db.QueryRowContext(r.Context(),
		`SELECT COALESCE(MAX(sort_order)+1,0) FROM stock_recipe_components WHERE restaurant_id=? AND recipe_id=?`,
		a.ActiveRestaurantID, sheetID).Scan(&nextSort)

	res, err := s.db.ExecContext(r.Context(), `
		INSERT INTO stock_recipe_components
			(restaurant_id,recipe_id,stock_item_id,sub_recipe_id,entered_qty,entered_unit_id,qty_base,waste_pct,is_optional,notes,sort_order)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		a.ActiveRestaurantID, sheetID, in.StockItemID, in.SubRecipeID, in.Quantity, in.UnitID,
		in.Quantity*factor, in.WastePct, stockBoolInt(in.IsOptional), stockNullableString(derefString(in.Notes)), nextSort)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "No se pudo guardar el ingrediente")
		return
	}
	componentID, _ := res.LastInsertId()

	// What the dish contains just changed, so the cached allergen set is stale.
	if err := s.refreshSheetDerivedAllergens(r.Context(), a.ActiveRestaurantID, sheetID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error recalculando alergenos")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "componentId": componentID})
}

func (s *Server) handleBOTechnicalSheetComponentPatch(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	sheetID := sheetIDParam(r)
	componentID, _ := strconv.ParseInt(chi.URLParam(r, "componentId"), 10, 64)

	var in struct {
		Quantity   *float64 `json:"quantity"`
		UnitID     *int64   `json:"unitId"`
		WastePct   *float64 `json:"wastePct"`
		IsOptional *bool    `json:"isOptional"`
		Notes      *string  `json:"notes"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Datos invalidos")
		return
	}

	var stockItemID, currentUnitID int64
	var currentQty float64
	if err := s.db.QueryRowContext(r.Context(),
		`SELECT stock_item_id,entered_unit_id,entered_qty FROM stock_recipe_components
		  WHERE restaurant_id=? AND id=? AND recipe_id=?`,
		a.ActiveRestaurantID, componentID, sheetID).Scan(&stockItemID, &currentUnitID, &currentQty); err != nil {
		httpx.WriteError(w, http.StatusNotFound, "Ingrediente no encontrado")
		return
	}

	unitID := currentUnitID
	if in.UnitID != nil {
		unitID = *in.UnitID
	}
	quantity := currentQty
	if in.Quantity != nil {
		quantity = *in.Quantity
	}
	if quantity <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "La cantidad debe ser mayor que cero")
		return
	}
	var factor float64
	if err := s.db.QueryRowContext(r.Context(),
		`SELECT factor_to_base FROM stock_item_units WHERE restaurant_id=? AND id=? AND stock_item_id=?`,
		a.ActiveRestaurantID, unitID, stockItemID).Scan(&factor); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "La unidad no pertenece a este articulo")
		return
	}

	sets := []string{"entered_qty=?", "entered_unit_id=?", "qty_base=?"}
	args := []any{quantity, unitID, quantity * factor}
	if in.WastePct != nil {
		if *in.WastePct < 0 || *in.WastePct >= 100 {
			httpx.WriteError(w, http.StatusBadRequest, "Merma invalida")
			return
		}
		sets = append(sets, "waste_pct=?")
		args = append(args, *in.WastePct)
	}
	if in.IsOptional != nil {
		sets = append(sets, "is_optional=?")
		args = append(args, stockBoolInt(*in.IsOptional))
	}
	if in.Notes != nil {
		sets = append(sets, "notes=?")
		args = append(args, stockNullableString(derefString(in.Notes)))
	}
	args = append(args, a.ActiveRestaurantID, componentID, sheetID)

	if _, err := s.db.ExecContext(r.Context(),
		`UPDATE stock_recipe_components SET `+strings.Join(sets, ",")+
			` WHERE restaurant_id=? AND id=? AND recipe_id=?`, args...); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando el ingrediente")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleBOTechnicalSheetComponentDelete(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	sheetID := sheetIDParam(r)
	componentID, _ := strconv.ParseInt(chi.URLParam(r, "componentId"), 10, 64)

	res, err := s.db.ExecContext(r.Context(),
		`DELETE FROM stock_recipe_components WHERE restaurant_id=? AND id=? AND recipe_id=?`,
		a.ActiveRestaurantID, componentID, sheetID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error eliminando el ingrediente")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		httpx.WriteError(w, http.StatusNotFound, "Ingrediente no encontrado")
		return
	}
	// Removing an ingredient can remove an allergen, so the cache must follow.
	if err := s.refreshSheetDerivedAllergens(r.Context(), a.ActiveRestaurantID, sheetID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error recalculando alergenos")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleBOTechnicalSheetComponentsList(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	sheetID := sheetIDParam(r)
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT c.id, c.stock_item_id, i.name, c.sub_recipe_id, c.entered_qty, c.entered_unit_id,
		       u.code, c.qty_base, c.waste_pct, c.is_optional, c.notes, c.sort_order, i.base_unit,
		       COALESCE(i.image_url,'')
		  FROM stock_recipe_components c
		  JOIN stock_items i ON i.restaurant_id=c.restaurant_id AND i.id=c.stock_item_id
		  JOIN stock_item_units u ON u.restaurant_id=c.restaurant_id AND u.id=c.entered_unit_id
		 WHERE c.restaurant_id=? AND c.recipe_id=?
		 ORDER BY c.sort_order, c.id`, a.ActiveRestaurantID, sheetID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error cargando ingredientes")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, stockItemID, unitID int64
		var subRecipeID sql.NullInt64
		var name, unitCode, baseUnit, imageURL string
		var notes sql.NullString
		var qty, qtyBase, waste float64
		var optional int
		var sortOrder int
		if err := rows.Scan(&id, &stockItemID, &name, &subRecipeID, &qty, &unitID, &unitCode,
			&qtyBase, &waste, &optional, &notes, &sortOrder, &baseUnit, &imageURL); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo ingredientes")
			return
		}
		row := map[string]any{
			"id": id, "stockItemId": stockItemID, "name": name, "quantity": qty,
			"unitId": unitID, "unitCode": unitCode, "qtyBase": qtyBase, "baseUnit": baseUnit,
			"wastePct": waste, "isOptional": optional != 0, "sortOrder": sortOrder,
			"imageUrl": imageURL,
		}
		if subRecipeID.Valid {
			row["subRecipeId"] = subRecipeID.Int64
		}
		if notes.Valid {
			row["notes"] = notes.String
		}
		items = append(items, row)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "components": items})
}
