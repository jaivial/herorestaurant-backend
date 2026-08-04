package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"preactvillacarmen/internal/httpx"
)

// A comida product is either RAW (bought and sold as-is, e.g. a bottled soft
// drink) or MANUFACTURED (produced from a technical sheet). The distinction is
// what tells the POS whether to deduct the product's own stock item or the
// sheet's semi-finished output.

func (s *Server) handleBOComidaProductionTypePatch(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	itemID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)

	var in struct {
		ProductionType string `json:"productionType"`
		StockRecipeID  *int64 `json:"stockRecipeId"`
		// Wine lives in its own table keyed by `num`, so the caller says which
		// catalogue the id belongs to. Anything else is a comida_items row.
		Source string `json:"source"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Datos invalidos")
		return
	}
	productionType := strings.ToUpper(strings.TrimSpace(in.ProductionType))
	if productionType != "RAW" && productionType != "MANUFACTURED" {
		httpx.WriteError(w, http.StatusBadRequest, "Tipo de produccion invalido")
		return
	}

	var recipeID, outputItemID any
	if productionType == "MANUFACTURED" && in.StockRecipeID != nil {
		var output int64
		// Scoping the lookup by tenant is what stops one restaurant from
		// linking another restaurant's recipe.
		if err := s.db.QueryRowContext(r.Context(),
			`SELECT output_item_id FROM stock_recipes WHERE restaurant_id=? AND id=?`,
			a.ActiveRestaurantID, *in.StockRecipeID).Scan(&output); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Ficha tecnica no valida")
			return
		}
		recipeID = *in.StockRecipeID
		// The item link is written alongside the recipe link: the POS deducts
		// the stock item, so a recipe link on its own would be unusable.
		outputItemID = output
	}
	// Going back to RAW clears the recipe link so no stale deduction survives.
	// The stock item link is NOT cleared: every catalogue product owns a RAW
	// stock item created by the backfill, keyed by SKU. Nulling it left the
	// product silently unlinked from stock with no way back short of re-running
	// the backfill, so the product's own item is restored instead.
	if productionType == "RAW" {
		// cmd/comida-stock-backfill keys the SKU by the tipo slug held in
		// comida_items.source_type ("platos", "bebidas", "cafes") - not by the
		// literal "comida" - so the slug is read from the row rather than
		// guessed from the request.
		var ownItem int64
		var skuSource string
		switch strings.ToLower(strings.TrimSpace(in.Source)) {
		case "vinos":
			skuSource = "vinos"
		case "postres":
			skuSource = "postres"
		default:
			if err := s.db.QueryRowContext(r.Context(),
				`SELECT COALESCE(source_type,'') FROM comida_items WHERE restaurant_id=? AND id=?`,
				a.ActiveRestaurantID, itemID).Scan(&skuSource); err != nil {
				skuSource = ""
			}
		}
		if skuSource != "" {
			sku := fmt.Sprintf("catalog:%s:%d", strings.ToLower(skuSource), itemID)
			if err := s.db.QueryRowContext(r.Context(),
				`SELECT id FROM stock_items WHERE restaurant_id=? AND sku=? AND deleted_at IS NULL`,
				a.ActiveRestaurantID, sku).Scan(&ownItem); err == nil {
				outputItemID = ownItem
			}
			// No such item (a product created before the backfill) simply stays
			// unlinked, exactly as it was.
		}
	}


	// The catalogue lives in three tables with different primary keys, so the
	// statement is chosen by source rather than contorting one query to serve
	// all of them.
	updateSQL := `UPDATE comida_items SET production_type=?, stock_recipe_id=?, stock_item_id=?
		 WHERE restaurant_id=? AND id=?`
	sourceTable, sourceIDColumn := "comida_items", "id"
	switch strings.ToLower(strings.TrimSpace(in.Source)) {
	case "vinos":
		updateSQL = `UPDATE VINOS SET production_type=?, stock_recipe_id=?, stock_item_id=?
		 WHERE restaurant_id=? AND num=?`
		sourceTable, sourceIDColumn = "VINOS", "num"
	case "postres":
		updateSQL = `UPDATE POSTRES SET production_type=?, stock_recipe_id=?, stock_item_id=?
		 WHERE restaurant_id=? AND NUM=?`
		sourceTable, sourceIDColumn = "POSTRES", "NUM"
	}
	// Reverting to raw abandons the sheet. An untouched draft is discarded here
	// so it does not accumulate as an unreachable orphan in the stock
	// catalogue. A sheet with real content is deliberately kept: losing typed
	// ingredients to a mis-click would be far worse than a stray row.
	var abandoned int64
	if productionType == "RAW" {
		if err := s.db.QueryRowContext(r.Context(),
			fmt.Sprintf(`SELECT COALESCE(stock_recipe_id,0) FROM %s WHERE restaurant_id=? AND %s=?`,
				sourceTable, sourceIDColumn),
			a.ActiveRestaurantID, itemID).Scan(&abandoned); err != nil {
			abandoned = 0
		}
	}

	res, err := s.db.ExecContext(r.Context(), updateSQL,
		productionType, recipeID, outputItemID, a.ActiveRestaurantID, itemID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando el producto")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		httpx.WriteError(w, http.StatusNotFound, "Producto no encontrado")
		return
	}
	if abandoned > 0 {
		s.discardUntouchedDraftSheet(r, a.ActiveRestaurantID, abandoned)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

// sheetUsage reports what would break if a sheet changed or disappeared.
type sheetUsageProduct struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Source string `json:"source"`
}

func (s *Server) loadSheetUsage(r *http.Request, restaurantID int, sheetID int64) ([]sheetUsageProduct, []string, error) {
	// Wine is a separate table, so it needs its own branch of the union; leaving
	// it out would let a sheet a wine depends on be deleted.
	rows, err := s.db.QueryContext(r.Context(),
		// comida_items and VINOS use different collations, so the name columns
		// are converted to one explicit collation; MySQL refuses the UNION
		// otherwise ("illegal mix of collations").
		`SELECT id, CONVERT(COALESCE(NULLIF(titulo,''),nombre) USING utf8mb4) COLLATE utf8mb4_unicode_ci AS name,
		        'COMIDA' AS source
		   FROM comida_items WHERE restaurant_id=? AND stock_recipe_id=?
		  UNION ALL
		 SELECT num, CONVERT(nombre USING utf8mb4) COLLATE utf8mb4_unicode_ci, 'VINO'
		   FROM VINOS WHERE restaurant_id=? AND stock_recipe_id=?
		  UNION ALL
		 SELECT NUM, CONVERT(DESCRIPCION USING utf8mb4) COLLATE utf8mb4_unicode_ci, 'POSTRE'
		   FROM POSTRES WHERE restaurant_id=? AND stock_recipe_id=?`,
		restaurantID, sheetID, restaurantID, sheetID, restaurantID, sheetID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	products := []sheetUsageProduct{}
	for rows.Next() {
		var p sheetUsageProduct
		if err := rows.Scan(&p.ID, &p.Name, &p.Source); err != nil {
			return nil, nil, err
		}
		products = append(products, p)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	// A sheet can also be an ingredient of another sheet.
	parentRows, err := s.db.QueryContext(r.Context(),
		`SELECT DISTINCT r.name FROM stock_recipe_components c
		   JOIN stock_recipes r ON r.restaurant_id=c.restaurant_id AND r.id=c.recipe_id
		  WHERE c.restaurant_id=? AND c.sub_recipe_id=?`, restaurantID, sheetID)
	if err != nil {
		return nil, nil, err
	}
	defer parentRows.Close()
	parents := []string{}
	for parentRows.Next() {
		var name string
		if err := parentRows.Scan(&name); err != nil {
			return nil, nil, err
		}
		parents = append(parents, name)
	}
	return products, parents, parentRows.Err()
}

func (s *Server) handleBOTechnicalSheetUsage(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	sheetID := sheetIDParam(r)
	if _, err := s.sheetOwnedByTenant(r.Context(), a.ActiveRestaurantID, sheetID); err != nil {
		httpx.WriteError(w, http.StatusNotFound, "Ficha tecnica no encontrada")
		return
	}
	products, parents, err := s.loadSheetUsage(r, a.ActiveRestaurantID, sheetID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error cargando el uso de la ficha")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true, "products": products, "usedBySheets": parents,
		"inUse": len(products) > 0 || len(parents) > 0,
	})
}

func (s *Server) handleBOTechnicalSheetDelete(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	sheetID := sheetIDParam(r)

	outputItemID, err := s.sheetOwnedByTenant(r.Context(), a.ActiveRestaurantID, sheetID)
	if err == sql.ErrNoRows {
		httpx.WriteError(w, http.StatusNotFound, "Ficha tecnica no encontrada")
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error cargando la ficha")
		return
	}

	// Deleting a sheet a product still sells would leave that product unable to
	// deduct stock, so the caller is told what to unlink first instead.
	products, parents, err := s.loadSheetUsage(r, a.ActiveRestaurantID, sheetID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error comprobando el uso de la ficha")
		return
	}
	if len(products) > 0 || len(parents) > 0 {
		httpx.WriteJSON(w, http.StatusConflict, map[string]any{
			"success": false, "code": "SHEET_IN_USE",
			"message":  "La ficha esta en uso y no puede eliminarse",
			"products": products, "usedBySheets": parents,
		})
		return
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error eliminando la ficha")
		return
	}
	defer tx.Rollback()

	for _, statement := range []string{
		`DELETE FROM stock_recipe_components WHERE restaurant_id=? AND recipe_id=?`,
		`DELETE FROM stock_recipe_steps WHERE restaurant_id=? AND recipe_id=?`,
		`DELETE FROM stock_recipes WHERE restaurant_id=? AND id=?`,
	} {
		if _, err := tx.ExecContext(r.Context(), statement, a.ActiveRestaurantID, sheetID); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error eliminando la ficha")
			return
		}
	}

	// The output item is removed only when it never moved: an item with ledger
	// history is part of the audit trail and must stay.
	var movements int
	tx.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM stock_movements WHERE restaurant_id=? AND stock_item_id=?`,
		a.ActiveRestaurantID, outputItemID).Scan(&movements)
	if movements == 0 {
		tx.ExecContext(r.Context(), `DELETE FROM stock_item_units WHERE restaurant_id=? AND stock_item_id=?`,
			a.ActiveRestaurantID, outputItemID)
		tx.ExecContext(r.Context(), `DELETE FROM stock_items WHERE restaurant_id=? AND id=?`,
			a.ActiveRestaurantID, outputItemID)
	}

	if err := tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error eliminando la ficha")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

// sheetAllergensFromJSON resolves the effective allergen list from the columns
// the sheet already persists, rather than walking the component tree per row:
// the list endpoint renders up to 100 cards and a tree walk each would be a
// query storm. refreshSheetDerivedAllergens keeps these columns current.
func sheetAllergensFromJSON(derivedJSON, manualJSON string) []string {
	var derived []string
	if strings.TrimSpace(derivedJSON) != "" {
		_ = json.Unmarshal([]byte(derivedJSON), &derived)
	}
	var manual manualAllergens
	if strings.TrimSpace(manualJSON) != "" {
		_ = json.Unmarshal([]byte(manualJSON), &manual)
	}
	// Always a slice, never nil: the client iterates it directly.
	resolved := resolveSheetAllergens(derived, manual)
	if resolved == nil {
		return []string{}
	}
	return resolved
}

// handleBOTechnicalSheetList backs the sheet picker. Search is a simple
// prefix/substring match on the name; the live-typing path goes over the
// WebSocket, but REST stays the hydration source of truth so a dropped socket
// never leaves the picker empty.
func (s *Server) handleBOTechnicalSheetList(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	status := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("status")))

	// The browser fills the product form straight from a card, so the list
	// carries the same values the form needs. Fetching each sheet separately
	// just to render a card would be a request per row.
	querySQL := `SELECT r.id, r.name, r.status, COALESCE(r.portions,1), COALESCE(i.image_url,''),
	               (SELECT COUNT(*) FROM comida_items p
	                 WHERE p.restaurant_id=r.restaurant_id AND p.stock_recipe_id=r.id),
	               COALESCE(c.id,0), COALESCE(c.name,''),
	               r.selling_price_gross, r.prep_time_min, COALESCE(r.instructions,''),
	               COALESCE(r.derived_allergens_json,''), COALESCE(r.manual_allergens_json,''),
	               (SELECT COUNT(*) FROM stock_recipe_components rc
	                 WHERE rc.restaurant_id=r.restaurant_id AND rc.recipe_id=r.id),
	               (SELECT COUNT(*) FROM stock_recipe_steps rs
	                 WHERE rs.restaurant_id=r.restaurant_id AND rs.recipe_id=r.id)
	          FROM stock_recipes r
	          JOIN stock_items i ON i.restaurant_id=r.restaurant_id AND i.id=r.output_item_id
	          LEFT JOIN stock_categories c ON c.restaurant_id=i.restaurant_id AND c.id=i.category_id
	         WHERE r.restaurant_id=? AND r.is_active=1`
	args := []any{a.ActiveRestaurantID}
	if query != "" {
		querySQL += ` AND r.name LIKE ?`
		args = append(args, "%"+query+"%")
	}
	if status == "PUBLISHED" || status == "DRAFT" {
		querySQL += ` AND r.status=?`
		args = append(args, status)
	}
	// The category belongs to the sheet's output item, which is where the
	// stock catalogue records it.
	if categoryID, _ := strconv.ParseInt(r.URL.Query().Get("categoryId"), 10, 64); categoryID > 0 {
		querySQL += ` AND i.category_id=?`
		args = append(args, categoryID)
	}
	querySQL += ` ORDER BY r.name LIMIT 100`

	rows, err := s.db.QueryContext(r.Context(), querySQL, args...)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error cargando fichas tecnicas")
		return
	}
	defer rows.Close()
	sheets := []map[string]any{}
	for rows.Next() {
		var id, categoryID int64
		var name, sheetStatus, imageURL, categoryName, instructions string
		var derivedJSON, manualJSON string
		var portions, usageCount, componentCount, stepCount int
		var sellingPrice sql.NullFloat64
		var prepTime sql.NullInt64
		if err := rows.Scan(&id, &name, &sheetStatus, &portions, &imageURL, &usageCount,
			&categoryID, &categoryName, &sellingPrice, &prepTime, &instructions,
			&derivedJSON, &manualJSON, &componentCount, &stepCount); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo fichas tecnicas")
			return
		}
		row := map[string]any{
			"id": id, "name": name, "status": sheetStatus, "portions": portions,
			"imageUrl": imageURL, "usageCount": usageCount,
			"categoryId": categoryID, "categoryName": categoryName,
			"instructions": instructions,
			// Never null: the client calls .length on these.
			"componentCount": componentCount, "stepCount": stepCount,
			"allergens": sheetAllergensFromJSON(derivedJSON, manualJSON),
		}
		// A sheet with no price is not a sheet priced at zero, so the field stays
		// null and the form leaves the product's own price alone.
		if sellingPrice.Valid {
			row["sellingPriceGross"] = sellingPrice.Float64
		} else {
			row["sellingPriceGross"] = nil
		}
		if prepTime.Valid {
			row["prepTimeMin"] = prepTime.Int64
		} else {
			row["prepTimeMin"] = nil
		}
		sheets = append(sheets, row)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "sheets": sheets})
}

// discardUntouchedDraftSheet removes a draft nobody has worked on. It is
// deliberately conservative: any ingredient, step or non-draft status means the
// sheet represents real work and is left alone, even though that can leave an
// unlinked row behind.
func (s *Server) discardUntouchedDraftSheet(r *http.Request, restaurantID int, sheetID int64) {
	var status string
	var outputItemID int64
	var components, steps int
	if err := s.db.QueryRowContext(r.Context(), `
		SELECT r.status, r.output_item_id,
		       (SELECT COUNT(*) FROM stock_recipe_components c
		         WHERE c.restaurant_id=r.restaurant_id AND c.recipe_id=r.id),
		       (SELECT COUNT(*) FROM stock_recipe_steps p
		         WHERE p.restaurant_id=r.restaurant_id AND p.recipe_id=r.id)
		  FROM stock_recipes r WHERE r.restaurant_id=? AND r.id=?`,
		restaurantID, sheetID).Scan(&status, &outputItemID, &components, &steps); err != nil {
		return
	}
	if status != "DRAFT" || components > 0 || steps > 0 {
		return
	}

	// Another product may have been linked to the same sheet meanwhile.
	var stillUsed int
	s.db.QueryRowContext(r.Context(), `
		SELECT (SELECT COUNT(*) FROM comida_items WHERE restaurant_id=? AND stock_recipe_id=?)
		     + (SELECT COUNT(*) FROM VINOS WHERE restaurant_id=? AND stock_recipe_id=?)
		     + (SELECT COUNT(*) FROM POSTRES WHERE restaurant_id=? AND stock_recipe_id=?)`,
		restaurantID, sheetID, restaurantID, sheetID, restaurantID, sheetID).Scan(&stillUsed)
	if stillUsed > 0 {
		return
	}

	// The output item goes too, unless it already has ledger history.
	var movements int
	s.db.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM stock_movements WHERE restaurant_id=? AND stock_item_id=?`,
		restaurantID, outputItemID).Scan(&movements)

	s.db.ExecContext(r.Context(), `DELETE FROM stock_recipes WHERE restaurant_id=? AND id=?`, restaurantID, sheetID)
	if movements == 0 {
		s.db.ExecContext(r.Context(), `DELETE FROM stock_item_units WHERE restaurant_id=? AND stock_item_id=?`,
			restaurantID, outputItemID)
		s.db.ExecContext(r.Context(), `DELETE FROM stock_items WHERE restaurant_id=? AND id=?`,
			restaurantID, outputItemID)
	}
}
