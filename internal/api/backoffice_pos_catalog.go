package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"net/http"
	"preactvillacarmen/internal/sqldb"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"preactvillacarmen/internal/httpx"
)

type posProduct struct {
	ID              int64    `json:"id"`
	Name            string   `json:"name"`
	SKU             string   `json:"sku,omitempty"`
	CategoryID      *int64   `json:"categoryId,omitempty"`
	CategoryName    string   `json:"categoryName,omitempty"`
	PriceGrossCents int64    `json:"priceGrossCents"`
	VATRateID       *int64   `json:"vatRateId,omitempty"`
	VATRate         float64  `json:"vatRate"`
	IsActive        bool     `json:"isActive"`
	Allergens       []string `json:"allergens"`
}

func (s *Server) loadPOSProducts(ctx context.Context, restaurantID int, activeOnly bool) ([]posProduct, error) {
	where := "p.restaurant_id=? AND p.deleted_at IS NULL"
	if activeOnly {
		where += " AND p.is_active=1"
	}
	rows, err := s.db.QueryContext(ctx, portableSQL(`SELECT p.id,p.name,COALESCE(p.sku,''),p.category_id,COALESCE(c.name,''),p.price_gross_cents,p.vat_rate_id,`+posProductVATRateSQL+`,p.is_active,COALESCE(p.allergens_json,'') FROM pos_products p LEFT JOIN pos_product_categories c ON c.restaurant_id=p.restaurant_id AND c.id=p.category_id LEFT JOIN stock_vat_rates v ON v.restaurant_id=p.restaurant_id AND v.id=p.vat_rate_id WHERE `+where+` ORDER BY c.sort_order`+sqlFragment("", " NULLS FIRST")+`,c.name`+sqlFragment("", " NULLS FIRST")+`,p.name`), restaurantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []posProduct{}
	for rows.Next() {
		var product posProduct
		var category, vatRateID sql.NullInt64
		var active int
		var allergensJSON string
		if err = rows.Scan(&product.ID, &product.Name, &product.SKU, &category, &product.CategoryName, &product.PriceGrossCents, &vatRateID, &product.VATRate, &active, &allergensJSON); err != nil {
			return nil, err
		}
		product.Allergens = decodePOSAllergens(allergensJSON)
		if category.Valid {
			value := category.Int64
			product.CategoryID = &value
		}
		if vatRateID.Valid {
			value := vatRateID.Int64
			product.VATRateID = &value
		}
		product.IsActive = active != 0
		out = append(out, product)
	}
	return out, rows.Err()
}

func (s *Server) handleBOPOSProductsList(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	products, err := s.loadPOSProducts(r.Context(), a.ActiveRestaurantID, false)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading POS products")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "products": products})
}

func (s *Server) savePOSProduct(w http.ResponseWriter, r *http.Request, id int64) {
	a, _ := boAuthFromContext(r.Context())
	var in struct {
		Name            string   `json:"name"`
		SKU             string   `json:"sku"`
		CategoryID      *int64   `json:"categoryId"`
		PriceGrossCents int64    `json:"priceGrossCents"`
		VATRateID       *int64   `json:"vatRateId"`
		IsActive        bool     `json:"isActive"`
		Allergens       []string `json:"allergens"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || strings.TrimSpace(in.Name) == "" || in.PriceGrossCents < 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid POS product")
		return
	}
	allergens, ok := normalisePOSAllergens(in.Allergens)
	if !ok {
		httpx.WriteError(w, http.StatusBadRequest, "Alergeno no reconocido: use los 14 alergenos de la UE, sulfitos o altramuz.")
		return
	}
	if id == 0 {
		newID, err := sqldb.InsertID(r.Context(), sqlDialect, s.db, portableSQL(`INSERT INTO pos_products (restaurant_id,category_id,name,source_type,sku,price_gross_cents,vat_rate_id,is_active,allergens_json) VALUES (?,?,?,'MANUAL',?,?,?,?,?)`), "id", a.ActiveRestaurantID, in.CategoryID, strings.TrimSpace(in.Name), stockNullableString(in.SKU), in.PriceGrossCents, in.VATRateID, stockBoolInt(in.IsActive), encodePOSAllergens(allergens))
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "POS product could not be created")
			return
		}
		id = newID
		httpx.WriteJSON(w, http.StatusCreated, map[string]any{"success": true, "id": id})
		return
	}
	res, err := s.db.ExecContext(r.Context(), portableSQL(`UPDATE pos_products SET category_id=?,name=?,sku=?,price_gross_cents=?,vat_rate_id=?,is_active=?,allergens_json=?,version=version+1 WHERE restaurant_id=? AND id=? AND deleted_at IS NULL`), []any{in.CategoryID, strings.TrimSpace(in.Name), stockNullableString(in.SKU), in.PriceGrossCents, in.VATRateID, stockBoolInt(in.IsActive), encodePOSAllergens(allergens), a.ActiveRestaurantID, id}...)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "POS product could not be updated")
		return
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		httpx.WriteError(w, http.StatusNotFound, "POS product not found")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleBOPOSProductCreate(w http.ResponseWriter, r *http.Request) {
	s.savePOSProduct(w, r, 0)
}
func (s *Server) handleBOPOSProductPatch(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	s.savePOSProduct(w, r, id)
}
func (s *Server) handleBOPOSProductDelete(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	res, err := s.db.ExecContext(r.Context(), nativeSQL(`UPDATE pos_products SET is_active=0,deleted_at=NOW(),version=version+1 WHERE restaurant_id=? AND id=? AND deleted_at IS NULL`, `UPDATE pos_products SET is_active=0,deleted_at=date_trunc('second', statement_timestamp()),version=version+1 WHERE restaurant_id=? AND id=? AND deleted_at IS NULL`), a.ActiveRestaurantID, id)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error deleting POS product")
		return
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		httpx.WriteError(w, http.StatusNotFound, "POS product not found")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleBOPOSImportPreview(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	// COLLATE forced on both sides of the UNION: comida_items is
	// utf8mb4_unicode_ci while the wine catalogue is utf8mb4_0900_ai_ci, and
	// mixing them fails with "Illegal mix of collations".
	rows, err := s.db.QueryContext(r.Context(), portableSQL(`SELECT source_type,source_id,name,price,category,`+sqlFragment("EXISTS(", "CAST(EXISTS(")+`SELECT 1 FROM pos_products p WHERE p.restaurant_id=? AND p.source_type=source_type AND p.source_id=source_id)`+sqlFragment("", " AS integer)")+` FROM (SELECT 'COMIDA_ITEM' source_type,c.id source_id,c.nombre`+sqlFragment(" COLLATE utf8mb4_unicode_ci", "")+` name,`+sqlFragment("c.precio", "CAST(c.precio AS double precision)")+` price,COALESCE(c.categoria,'')`+sqlFragment(" COLLATE utf8mb4_unicode_ci", "")+` category FROM comida_items c WHERE c.restaurant_id=? AND c.active=1 UNION ALL SELECT 'VINO',`+vinoV2NumSQL()+`,v.nombre`+sqlFragment(" COLLATE utf8mb4_unicode_ci", "")+`,`+sqlFragment("v.precio", "CAST(v.precio AS double precision)")+`,COALESCE(v.tipo,'')`+sqlFragment(" COLLATE utf8mb4_unicode_ci", "")+` `+vinoV2From+` WHERE v.restaurant_id=? AND v.active=1) sources ORDER BY name`), a.ActiveRestaurantID, a.ActiveRestaurantID, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading Carta products")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id int64
		var sourceType, name, category string
		var price float64
		var imported int
		if err = rows.Scan(&sourceType, &id, &name, &price, &category, &imported); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error reading Carta products")
			return
		}
		items = append(items, map[string]any{"sourceType": sourceType, "sourceId": id, "name": name, "category": category, "priceGrossCents": int64(math.Round(price * 100)), "imported": imported != 0})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "items": items})
}

func (s *Server) handleBOPOSImportConfirm(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	var in struct {
		Items []struct {
			SourceType string `json:"sourceType"`
			SourceID   int64  `json:"sourceId"`
		} `json:"items"`
		SourceIDs []int64 `json:"sourceIds"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || len(in.Items) == 0 && len(in.SourceIDs) == 0 || len(in.Items)+len(in.SourceIDs) > 500 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid import")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error importing products")
		return
	}
	defer tx.Rollback()
	for _, sourceID := range in.SourceIDs {
		in.Items = append(in.Items, struct {
			SourceType string `json:"sourceType"`
			SourceID   int64  `json:"sourceId"`
		}{SourceType: "COMIDA_ITEM", SourceID: sourceID})
	}
	count := 0
	for _, item := range in.Items {
		item.SourceType = strings.ToUpper(strings.TrimSpace(item.SourceType))
		if item.SourceType == "COMIDA_ITEM" {
			// repair-ignore2: categoria varchar(140) vs name varchar(120). MySQL
			// IGNORE stored a 120-char cut (orphan category, product left with
			// NULL category); PG raised and the error was swallowed. Both now
			// refuse the whole import (tx rolled back) instead of losing it.
			var catLen int
			if err := tx.QueryRowContext(r.Context(), portableSQL(`SELECT COALESCE(MAX(CHAR_LENGTH(categoria)),0) FROM comida_items WHERE restaurant_id=? AND id=? AND TRIM(COALESCE(categoria,''))<>''`), a.ActiveRestaurantID, item.SourceID).Scan(&catLen); err != nil {
				httpx.WriteError(w, http.StatusInternalServerError, "Error importing products")
				return
			}
			if catLen > 120 {
				httpx.WriteError(w, http.StatusBadRequest, "Carta category name is longer than 120 characters")
				return
			}
			// ON CONFLICT on the only collidable key uq(restaurant_id,name).
			// collation_pad_v1 (PG only): MySQL utf8mb4_unicode_ci is PAD SPACE,
			// so "Postres " and "Postres" are ONE category there; PG old-nondeterministic-collation is
			// not padded. The PG text strips trailing ASCII spaces (rtrim ' ')
			// on insert and on the link join, mirroring PAD SPACE equality.
			// Any other error now aborts the import (was swallowed).
			if err := sqldb.TxStatementScope(r.Context(), sqlDialect, tx, func() error {
				_, e := tx.ExecContext(r.Context(), nativeSQL(`INSERT IGNORE INTO pos_product_categories (restaurant_id,name,sort_order,is_active) SELECT restaurant_id,categoria,0,1 FROM comida_items WHERE restaurant_id=? AND id=? AND TRIM(COALESCE(categoria,''))<>''`, `INSERT INTO pos_product_categories (restaurant_id,name,sort_order,is_active) SELECT restaurant_id,rtrim(categoria, ' '),0,1 FROM comida_items WHERE restaurant_id=? AND id=? AND TRIM(COALESCE(categoria,''))<>'' ON CONFLICT (restaurant_id, app.norm(name)) DO NOTHING`), []any{a.ActiveRestaurantID, item.SourceID}...)
				return e
			}); err != nil {
				httpx.WriteError(w, http.StatusInternalServerError, "Error importing product category")
				return
			}
		}
		// pos_products.source_id keeps holding the legacy num for wines, which
		// is what the catalogue already stored before the switch, so the wine
		// statements resolve it through legacy_wine_map.
		if item.SourceType == "VINO" {
			// tipo varchar(50) always fits name varchar(120); errors propagate.
			if err := sqldb.TxStatementScope(r.Context(), sqlDialect, tx, func() error {
				_, e := tx.ExecContext(r.Context(), nativeSQL(`INSERT IGNORE INTO pos_product_categories (restaurant_id,name,sort_order,is_active) SELECT restaurant_id,tipo,0,1 FROM group_menu_wines_v2 WHERE restaurant_id=? AND id=(SELECT w.v2_wine_id FROM legacy_wine_map w WHERE w.restaurant_id=? AND w.legacy_num=?) AND TRIM(COALESCE(tipo,''))<>''`, `INSERT INTO pos_product_categories (restaurant_id,name,sort_order,is_active) SELECT restaurant_id,rtrim(tipo, ' '),0,1 FROM group_menu_wines_v2 WHERE restaurant_id=? AND id=(SELECT w.v2_wine_id FROM legacy_wine_map w WHERE w.restaurant_id=? AND w.legacy_num=?) AND TRIM(COALESCE(tipo,''))<>'' ON CONFLICT (restaurant_id, app.norm(name)) DO NOTHING`), []any{a.ActiveRestaurantID, a.ActiveRestaurantID, item.SourceID}...)
				return e
			}); err != nil {
				httpx.WriteError(w, http.StatusInternalServerError, "Error importing product category")
				return
			}
		}
		// Only collidable unique key: uq_pos_product_source (restaurant_id,
		// source_type, source_id) - sku is NULL here, id is generated.
		posUpsert := sqlFragment(" ON DUPLICATE KEY UPDATE name=VALUES(name),price_gross_cents=VALUES(price_gross_cents),is_active=VALUES(is_active),deleted_at=NULL,version=version+1", " ON CONFLICT (restaurant_id, source_type, source_id) DO UPDATE SET name=EXCLUDED.name,price_gross_cents=EXCLUDED.price_gross_cents,is_active=EXCLUDED.is_active,deleted_at=NULL,version=pos_products.version+1")
		query := `INSERT INTO pos_products (restaurant_id,name,source_type,source_id,price_gross_cents,is_active) SELECT c.restaurant_id,LEFT(TRIM(c.nombre),180),'COMIDA_ITEM',c.id,ROUND(c.precio*100),c.active FROM comida_items c WHERE restaurant_id=? AND id=?` + posUpsert
		queryArgs := []any{a.ActiveRestaurantID, item.SourceID}
		if item.SourceType == "VINO" {
			// FLOAT precio: MySQL promotes to DOUBLE before *100; PG real*int stays
			// float4, so the PG twin casts to double precision first.
			query = `INSERT INTO pos_products (restaurant_id,name,source_type,source_id,price_gross_cents,is_active) SELECT v.restaurant_id,LEFT(TRIM(v.nombre),180),'VINO',` + vinoV2NumSQL() + `,ROUND(` + sqlFragment("v.precio", "CAST(v.precio AS double precision)") + `*100),v.active ` + vinoV2From + ` WHERE ` + vinoV2WhereLegacyNumJoined + posUpsert
			queryArgs = vinoV2LegacyNumArgs(a.ActiveRestaurantID, int(item.SourceID))
		}
		if item.SourceType != "COMIDA_ITEM" && item.SourceType != "VINO" {
			httpx.WriteError(w, http.StatusBadRequest, "Unsupported Carta source")
			return
		}
		res, execErr := tx.ExecContext(r.Context(), portableSQL(query), queryArgs...)
		if execErr != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Carta product could not be imported")
			return
		}
		if affected, _ := res.RowsAffected(); affected > 0 {
			count++
		}
		var linkErr error
		if item.SourceType == "COMIDA_ITEM" {
			// UPDATE..JOIN -> UPDATE..FROM (LEFT JOIN kept: no category -> NULL).
			// repair-ignore2: a failed link no longer leaves the product
			// silently uncategorised; the import aborts.
			linkErr = sqldb.TxStatementScope(r.Context(), sqlDialect, tx, func() error {
				_, e := tx.ExecContext(r.Context(), nativeSQL(`UPDATE pos_products p JOIN comida_items c ON c.restaurant_id=p.restaurant_id AND c.id=p.source_id LEFT JOIN pos_product_categories pc ON pc.restaurant_id=c.restaurant_id AND pc.name=c.categoria COLLATE utf8mb4_unicode_ci SET p.category_id=pc.id WHERE p.restaurant_id=? AND p.source_type='COMIDA_ITEM' AND p.source_id=?`, `UPDATE pos_products p SET category_id=pc.id FROM comida_items c LEFT JOIN pos_product_categories pc ON pc.restaurant_id=c.restaurant_id AND app.norm(pc.name) = app.norm(c.categoria) WHERE c.restaurant_id=p.restaurant_id AND c.id=p.source_id AND p.restaurant_id=? AND p.source_type='COMIDA_ITEM' AND p.source_id=?`), a.ActiveRestaurantID, item.SourceID)
				return e
			})
		} else {
			linkErr = sqldb.TxStatementScope(r.Context(), sqlDialect, tx, func() error {
				_, e := tx.ExecContext(r.Context(), nativeSQL(`UPDATE pos_products p JOIN legacy_wine_map w ON w.restaurant_id=p.restaurant_id AND w.legacy_num=p.source_id JOIN group_menu_wines_v2 v ON v.restaurant_id=p.restaurant_id AND v.id=w.v2_wine_id LEFT JOIN pos_product_categories pc ON pc.restaurant_id=v.restaurant_id AND pc.name=v.tipo COLLATE utf8mb4_unicode_ci SET p.category_id=pc.id WHERE p.restaurant_id=? AND p.source_type='VINO' AND p.source_id=?`, `UPDATE pos_products p SET category_id=pc.id FROM legacy_wine_map w JOIN group_menu_wines_v2 v ON v.restaurant_id=w.restaurant_id AND v.id=w.v2_wine_id LEFT JOIN pos_product_categories pc ON pc.restaurant_id=v.restaurant_id AND app.norm(pc.name) = app.norm(v.tipo) WHERE w.restaurant_id=p.restaurant_id AND w.legacy_num=p.source_id AND p.restaurant_id=? AND p.source_type='VINO' AND p.source_id=?`), a.ActiveRestaurantID, item.SourceID)
				return e
			})
		}
		if linkErr != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error linking product category")
			return
		}
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error importing products")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "processed": count})
}

func (s *Server) handleBOPOSStockRulesGet(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	productID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	rows, err := s.db.QueryContext(r.Context(), portableSQL(`SELECT r.id,r.stock_item_id,i.name,r.stock_recipe_id,r.warehouse_id,w.name,r.qty_base_per_sale,r.is_active FROM pos_product_stock_rules r JOIN stock_items i ON i.restaurant_id=r.restaurant_id AND i.id=r.stock_item_id JOIN stock_warehouses w ON w.restaurant_id=r.restaurant_id AND w.id=r.warehouse_id WHERE r.restaurant_id=? AND r.pos_product_id=? AND r.is_active=1 ORDER BY r.sort_order,r.id`), a.ActiveRestaurantID, productID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading stock rules")
		return
	}
	defer rows.Close()
	rules := []map[string]any{}
	for rows.Next() {
		var id, itemID, warehouseID int64
		var recipeID sql.NullInt64
		var itemName, warehouseName string
		var qty float64
		var active int
		if err = rows.Scan(&id, &itemID, &itemName, &recipeID, &warehouseID, &warehouseName, &qty, &active); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error reading stock rules")
			return
		}
		rules = append(rules, map[string]any{"id": id, "stockItemId": itemID, "stockItemName": itemName, "stockRecipeId": stockNullableDBInt(recipeID), "warehouseId": warehouseID, "warehouseName": warehouseName, "quantityBasePerSale": qty, "isActive": active != 0})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "rules": rules})
}

func (s *Server) handleBOPOSStockRulesPut(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	productID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var in struct {
		Rules []struct {
			StockItemID         int64   `json:"stockItemId"`
			StockRecipeID       *int64  `json:"stockRecipeId"`
			WarehouseID         int64   `json:"warehouseId"`
			QuantityBasePerSale float64 `json:"quantityBasePerSale"`
		} `json:"rules"`
	}
	if productID <= 0 || json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&in) != nil || len(in.Rules) > 20 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid stock rules")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error saving stock rules")
		return
	}
	defer tx.Rollback()
	var productExists int
	if err = tx.QueryRowContext(r.Context(), portableSQL(`SELECT 1 FROM pos_products WHERE restaurant_id=? AND id=? AND deleted_at IS NULL FOR UPDATE`), a.ActiveRestaurantID, productID).Scan(&productExists); err != nil {
		httpx.WriteError(w, http.StatusNotFound, "POS product not found")
		return
	}
	if _, err = tx.ExecContext(r.Context(), portableSQL(`UPDATE pos_product_stock_rules SET is_active=0 WHERE restaurant_id=? AND pos_product_id=? AND is_active=1`), a.ActiveRestaurantID, productID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error saving stock rules")
		return
	}
	var nextRuleVersion int
	if err = tx.QueryRowContext(r.Context(), portableSQL(`SELECT COALESCE(MAX(version),0)+1 FROM pos_product_stock_rules WHERE restaurant_id=? AND pos_product_id=?`), a.ActiveRestaurantID, productID).Scan(&nextRuleVersion); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error versioning stock rules")
		return
	}
	for index, rule := range in.Rules {
		if rule.StockItemID <= 0 || rule.WarehouseID <= 0 || rule.QuantityBasePerSale <= 0 {
			httpx.WriteError(w, http.StatusBadRequest, "Invalid stock rule")
			return
		}
		var source string
		var tracked int
		if err = tx.QueryRowContext(r.Context(), nativeSQL(`SELECT deduction_source,is_tracked FROM stock_items WHERE restaurant_id=? AND id=? AND deleted_at IS NULL`, `SELECT CAST(deduction_source AS text),is_tracked FROM stock_items WHERE restaurant_id=? AND id=? AND deleted_at IS NULL`), a.ActiveRestaurantID, rule.StockItemID).Scan(&source, &tracked); err != nil || source == "PRODUCTION" {
			httpx.WriteJSON(w, http.StatusConflict, map[string]any{"success": false, "message": "POS cannot deduct a production-only item", "code": "POS_PRODUCTION_ITEM"})
			return
		}
		if rule.StockRecipeID != nil {
			var outputItemID int64
			if err = tx.QueryRowContext(r.Context(), portableSQL(`SELECT output_item_id FROM stock_recipes WHERE restaurant_id=? AND id=? AND is_active=1`), a.ActiveRestaurantID, *rule.StockRecipeID).Scan(&outputItemID); err != nil || outputItemID != rule.StockItemID {
				httpx.WriteError(w, http.StatusBadRequest, "Recipe output must match mapped stock item")
				return
			}
		}
		res, execErr := tx.ExecContext(r.Context(), nativeSQL(`INSERT INTO pos_product_stock_rules (restaurant_id,pos_product_id,stock_item_id,stock_recipe_id,warehouse_id,qty_base_per_sale,sort_order,version) SELECT ?,?,?,?,?,?,?,? FROM stock_warehouses WHERE restaurant_id=? AND id=? AND is_active=1 AND deleted_at IS NULL`, `INSERT INTO pos_product_stock_rules (restaurant_id,pos_product_id,stock_item_id,stock_recipe_id,warehouse_id,qty_base_per_sale,sort_order,version) SELECT CAST(? AS integer),CAST(? AS bigint),CAST(? AS bigint),CAST(? AS bigint),CAST(? AS bigint),CAST(CAST(? AS double precision) AS numeric(18,4)),CAST(? AS integer),CAST(? AS integer) FROM stock_warehouses WHERE restaurant_id=? AND id=? AND is_active=1 AND deleted_at IS NULL`), a.ActiveRestaurantID, productID, rule.StockItemID, rule.StockRecipeID, rule.WarehouseID, rule.QuantityBasePerSale, index, nextRuleVersion, a.ActiveRestaurantID, rule.WarehouseID)
		if execErr != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Stock rule could not be saved")
			return
		}
		if affected, _ := res.RowsAffected(); affected == 0 {
			httpx.WriteError(w, http.StatusBadRequest, "Warehouse not found")
			return
		}
		_ = tracked
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error saving stock rules")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleBOPOSStockReadiness(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	var active, mapped, untracked, invalid int
	err := s.db.QueryRowContext(r.Context(), nativeSQL(`SELECT COUNT(*),COALESCE(SUM(has_mapping),0),COALESCE(SUM(has_untracked),0),COALESCE(SUM(has_invalid),0) FROM (SELECT p.id,EXISTS(SELECT 1 FROM pos_product_stock_rules r JOIN stock_items mi ON mi.restaurant_id=r.restaurant_id AND mi.id=r.stock_item_id JOIN stock_warehouses mw ON mw.restaurant_id=r.restaurant_id AND mw.id=r.warehouse_id WHERE r.restaurant_id=p.restaurant_id AND r.pos_product_id=p.id AND r.is_active=1 AND mi.is_active=1 AND mi.deleted_at IS NULL AND mw.is_active=1 AND mw.deleted_at IS NULL) has_mapping,EXISTS(SELECT 1 FROM pos_product_stock_rules r JOIN stock_items i ON i.restaurant_id=r.restaurant_id AND i.id=r.stock_item_id WHERE r.restaurant_id=p.restaurant_id AND r.pos_product_id=p.id AND r.is_active=1 AND i.is_tracked=0) has_untracked,EXISTS(SELECT 1 FROM pos_product_stock_rules r JOIN stock_items i ON i.restaurant_id=r.restaurant_id AND i.id=r.stock_item_id JOIN stock_warehouses w ON w.restaurant_id=r.restaurant_id AND w.id=r.warehouse_id WHERE r.restaurant_id=p.restaurant_id AND r.pos_product_id=p.id AND r.is_active=1 AND (i.deduction_source='PRODUCTION' OR i.is_active=0 OR i.deleted_at IS NOT NULL OR w.is_active=0 OR w.deleted_at IS NOT NULL)) has_invalid FROM pos_products p WHERE p.restaurant_id=? AND p.is_active=1 AND p.deleted_at IS NULL) x`, `SELECT COUNT(*),COALESCE(SUM(has_mapping),0),COALESCE(SUM(has_untracked),0),COALESCE(SUM(has_invalid),0) FROM (SELECT p.id,CAST(EXISTS(SELECT 1 FROM pos_product_stock_rules r JOIN stock_items mi ON mi.restaurant_id=r.restaurant_id AND mi.id=r.stock_item_id JOIN stock_warehouses mw ON mw.restaurant_id=r.restaurant_id AND mw.id=r.warehouse_id WHERE r.restaurant_id=p.restaurant_id AND r.pos_product_id=p.id AND r.is_active=1 AND mi.is_active=1 AND mi.deleted_at IS NULL AND mw.is_active=1 AND mw.deleted_at IS NULL) AS integer) has_mapping,CAST(EXISTS(SELECT 1 FROM pos_product_stock_rules r JOIN stock_items i ON i.restaurant_id=r.restaurant_id AND i.id=r.stock_item_id WHERE r.restaurant_id=p.restaurant_id AND r.pos_product_id=p.id AND r.is_active=1 AND i.is_tracked=0) AS integer) has_untracked,CAST(EXISTS(SELECT 1 FROM pos_product_stock_rules r JOIN stock_items i ON i.restaurant_id=r.restaurant_id AND i.id=r.stock_item_id JOIN stock_warehouses w ON w.restaurant_id=r.restaurant_id AND w.id=r.warehouse_id WHERE r.restaurant_id=p.restaurant_id AND r.pos_product_id=p.id AND r.is_active=1 AND (i.deduction_source='PRODUCTION' OR i.is_active=0 OR i.deleted_at IS NOT NULL OR w.is_active=0 OR w.deleted_at IS NOT NULL)) AS integer) has_invalid FROM pos_products p WHERE p.restaurant_id=? AND p.is_active=1 AND p.deleted_at IS NULL) x`), a.ActiveRestaurantID).Scan(&active, &mapped, &untracked, &invalid)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading stock readiness")
		return
	}
	unmapped := active - mapped
	coverage := 0.0
	if active > 0 {
		coverage = float64(mapped) / float64(active) * 100
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "activeProducts": active, "mappedProducts": mapped, "unmappedProducts": unmapped, "untrackedProducts": untracked, "invalidMappings": invalid, "salesCoveragePct": coverage})
}

// posProductVATRateSQL resolves a POS product's VAT: its own rate when set,
// otherwise the restaurant's default active rate, so imported Carta products
// never ring up with 0% tax. Expects aliases p (pos_products) and v (its rate).
// Coordination id: pos_vat_default_fallback_v1
const posProductVATRateSQL = `COALESCE(v.rate,(SELECT d.rate FROM stock_vat_rates d WHERE d.restaurant_id=p.restaurant_id AND d.is_default=1 AND d.is_active=1 ORDER BY d.id LIMIT 1),0)`
