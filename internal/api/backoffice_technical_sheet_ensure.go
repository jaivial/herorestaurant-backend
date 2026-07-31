package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"preactvillacarmen/internal/httpx"
)

// handleBOTechnicalSheetEnsureForProduct returns the technical sheet for a
// product, creating and linking it if there is none.
//
// The UI creates a sheet the moment a product is switched to "Preparado", and
// React may run that effect more than once (StrictMode, a remount, an impatient
// double click). A guard in the component cannot survive a remount, so the
// guarantee of ONE sheet per product has to live here: the row lock below makes
// concurrent callers agree on a single sheet instead of racing and leaving
// orphans behind.
func (s *Server) handleBOTechnicalSheetEnsureForProduct(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())

	var in struct {
		ItemID int64  `json:"itemId"`
		Name   string `json:"name"`
		Source string `json:"source"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || in.ItemID <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Producto invalido")
		return
	}

	source := strings.ToLower(strings.TrimSpace(in.Source))
	table, idColumn := "comida_items", "id"
	switch source {
	case "vinos":
		table, idColumn = "VINOS", "num"
	case "postres":
		table, idColumn = "POSTRES", "NUM"
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error preparando la ficha tecnica")
		return
	}
	defer tx.Rollback()

	// Locking the product row is what serialises concurrent callers: the second
	// one waits, then sees the link the first one wrote.
	// Table and column come from the fixed mapping above, never from user input.
	var existing sql.NullInt64
	if err := tx.QueryRowContext(r.Context(),
		fmt.Sprintf(`SELECT stock_recipe_id FROM %s WHERE restaurant_id=? AND %s=? FOR UPDATE`, table, idColumn),
		a.ActiveRestaurantID, in.ItemID).Scan(&existing); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteError(w, http.StatusNotFound, "Producto no encontrado")
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo el producto")
		return
	}

	if existing.Valid && existing.Int64 > 0 {
		// Already has one; reuse rather than creating a second.
		if err := tx.Commit(); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error preparando la ficha tecnica")
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": true, "sheetId": existing.Int64, "reused": true,
		})
		return
	}

	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = "Ficha tecnica"
	}
	// stock_items.name is varchar(180) and catalogue names can be far longer.
	if runes := []rune(name); len(runes) > 180 {
		name = string(runes[:180])
	}

	// D4: the output item, its unit and the recipe are created together, so a
	// failure cannot leave a half-built sheet behind.
	itemRes, err := tx.ExecContext(r.Context(), `
		INSERT INTO stock_items (restaurant_id,name,kind,base_dimension,base_unit,is_tracked,deduction_source)
		VALUES (?,?,'SEMI_FINISHED','COUNT','ud',1,'SALE')`, a.ActiveRestaurantID, name)
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

	recipeRes, err := tx.ExecContext(r.Context(), `
		INSERT INTO stock_recipes (restaurant_id,name,output_item_id,output_qty_base,waste_pct,portions,status,is_active,draft_owner_user_id)
		VALUES (?,?,?,1,0,1,'DRAFT',1,?)`,
		a.ActiveRestaurantID, name, outputItemID, a.User.ID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creando la ficha tecnica")
		return
	}
	sheetID, _ := recipeRes.LastInsertId()

	// Linking inside the same transaction is what stops the sheet becoming an
	// orphan if anything after this fails.
	if _, err = tx.ExecContext(r.Context(),
		fmt.Sprintf(`UPDATE %s SET production_type='MANUFACTURED', stock_recipe_id=?, stock_item_id=?
			 WHERE restaurant_id=? AND %s=?`, table, idColumn),
		sheetID, outputItemID, a.ActiveRestaurantID, in.ItemID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error vinculando la ficha tecnica")
		return
	}

	if err := tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error preparando la ficha tecnica")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true, "sheetId": sheetID, "outputItemId": outputItemID,
	})
}
