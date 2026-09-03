package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"preactvillacarmen/internal/httpx"
)

type stockSupplierAliasInput struct {
	SupplierCode string `json:"supplierCode"`
	Description  string `json:"description"`
	StockItemID  int64  `json:"stockItemId"`
	StockUnitID  int64  `json:"stockUnitId"`
}

func (s *Server) handleBOStockSuppliersList(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	restaurantID := a.ActiveRestaurantID
	rows, err := s.db.QueryContext(r.Context(), `SELECT sup.id,sup.name,sup.notes,sup.is_active,
		(SELECT COUNT(*) FROM stock_supplier_aliases sa WHERE sa.restaurant_id=? AND sa.supplier_name=sup.name),
		(SELECT COUNT(DISTINCT sa.stock_item_id) FROM stock_supplier_aliases sa WHERE sa.restaurant_id=? AND sa.supplier_name=sup.name),
		(SELECT COUNT(*) FROM stock_item_prices p WHERE p.restaurant_id=? AND p.supplier_name=sup.name),
		(SELECT DATE_FORMAT(MAX(p.effective_at),'%Y-%m-%d') FROM stock_item_prices p WHERE p.restaurant_id=? AND p.supplier_name=sup.name)
		FROM stock_suppliers sup WHERE sup.restaurant_id=? ORDER BY sup.is_active DESC, sup.name`,
		restaurantID, restaurantID, restaurantID, restaurantID, restaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading suppliers")
		return
	}
	defer rows.Close()
	suppliers := []map[string]any{}
	for rows.Next() {
		var id int64
		var name, notes string
		var active int
		var aliasCount, itemCount, priceCount int
		var lastPriceAt *string
		if err = rows.Scan(&id, &name, &notes, &active, &aliasCount, &itemCount, &priceCount, &lastPriceAt); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error reading suppliers")
			return
		}
		suppliers = append(suppliers, map[string]any{
			"id": id, "name": name, "notes": notes, "isActive": active != 0,
			"aliasCount": aliasCount, "itemCount": itemCount,
			"pricePointCount": priceCount, "lastPriceAt": stockEmptyNil(lastPriceAt),
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "suppliers": suppliers})
}

func (s *Server) handleBOStockSupplierCreate(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	var in struct {
		Name  string `json:"name"`
		Notes string `json:"notes"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || strings.TrimSpace(in.Name) == "" {
		httpx.WriteError(w, http.StatusBadRequest, "Name is required")
		return
	}
	name := strings.TrimSpace(in.Name)
	if len(name) > 180 {
		httpx.WriteError(w, http.StatusBadRequest, "Name is too long")
		return
	}
	var existing int
	if err := s.db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM stock_suppliers WHERE restaurant_id=? AND name=?)`, a.ActiveRestaurantID, name).Scan(&existing); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creating supplier")
		return
	}
	if existing != 0 {
		httpx.WriteError(w, http.StatusConflict, "Supplier already exists")
		return
	}
	res, err := s.db.ExecContext(r.Context(), `INSERT INTO stock_suppliers (restaurant_id,name,notes) VALUES (?,?,?)`, a.ActiveRestaurantID, name, strings.TrimSpace(in.Notes))
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Supplier could not be created")
		return
	}
	id, _ := res.LastInsertId()
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"success": true, "id": id})
}

func (s *Server) handleBOStockSupplierPatch(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid supplier")
		return
	}
	var in struct {
		Name     string `json:"name"`
		Notes    string `json:"notes"`
		IsActive bool   `json:"isActive"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || strings.TrimSpace(in.Name) == "" {
		httpx.WriteError(w, http.StatusBadRequest, "Name is required")
		return
	}
	name := strings.TrimSpace(in.Name)
	if len(name) > 180 {
		httpx.WriteError(w, http.StatusBadRequest, "Name is too long")
		return
	}
	var clash int64
	if err := s.db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM stock_suppliers WHERE restaurant_id=? AND name=? AND id<>?)`, a.ActiveRestaurantID, name, id).Scan(&clash); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error updating supplier")
		return
	}
	if clash != 0 {
		httpx.WriteError(w, http.StatusConflict, "Supplier already exists")
		return
	}
	res, err := s.db.ExecContext(r.Context(), `UPDATE stock_suppliers SET name=?,notes=?,is_active=? WHERE id=? AND restaurant_id=?`, name, strings.TrimSpace(in.Notes), stockBoolInt(in.IsActive), id, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Supplier could not be updated")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		httpx.WriteError(w, http.StatusNotFound, "Supplier not found")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleBOStockSupplierDelete(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid supplier")
		return
	}
	res, err := s.db.ExecContext(r.Context(), `DELETE FROM stock_suppliers WHERE id=? AND restaurant_id=?`, id, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error deleting supplier")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		httpx.WriteError(w, http.StatusNotFound, "Supplier not found")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

// Aliases are keyed by supplier_name (the same string OCR writes into
// stock_document_scans / stock_item_prices), so the registry row is resolved
// to its name before touching stock_supplier_aliases.
func (s *Server) stockSupplierName(r *http.Request, w http.ResponseWriter, restaurantID int, id int64) (string, bool) {
	var name string
	err := s.db.QueryRowContext(r.Context(), `SELECT name FROM stock_suppliers WHERE restaurant_id=? AND id=?`, restaurantID, id).Scan(&name)
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, "Supplier not found")
		return "", false
	}
	return name, true
}

func (s *Server) handleBOStockSupplierAliasesGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid supplier")
		return
	}
	name, ok := s.stockSupplierName(r, w, a.ActiveRestaurantID, id)
	if !ok {
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT sa.id,sa.supplier_code,sa.normalized_description,sa.stock_item_id,i.name,sa.stock_unit_id,COALESCE(u.label,''),COALESCE(u.factor_to_base,1),sa.updated_at
		FROM stock_supplier_aliases sa
		JOIN stock_items i ON i.restaurant_id=sa.restaurant_id AND i.id=sa.stock_item_id
		LEFT JOIN stock_item_units u ON u.restaurant_id=sa.restaurant_id AND u.id=sa.stock_unit_id
		WHERE sa.restaurant_id=? AND sa.supplier_name=? ORDER BY sa.normalized_description,sa.supplier_code`, a.ActiveRestaurantID, name)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading aliases")
		return
	}
	defer rows.Close()
	aliases := []map[string]any{}
	for rows.Next() {
		var aliasID, itemID, unitID int64
		var code, description, itemName, unitLabel string
		var factor float64
		var updatedAt time.Time
		if err = rows.Scan(&aliasID, &code, &description, &itemID, &itemName, &unitID, &unitLabel, &factor, &updatedAt); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error reading aliases")
			return
		}
		aliases = append(aliases, map[string]any{
			"id": aliasID, "supplierCode": code, "description": description,
			"stockItemId": itemID, "itemName": itemName,
			"stockUnitId": unitID, "unitLabel": unitLabel, "unitFactor": factor,
			"updatedAt": updatedAt.Format("2006-01-02 15:04:05"),
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "supplier": map[string]any{"id": id, "name": name}, "aliases": aliases})
}

// PUT is a full replace: rows present in the payload are upserted by natural
// key (supplier_code + normalized description) and rows for this supplier
// missing from the payload are deleted, all in one transaction.
func (s *Server) handleBOStockSupplierAliasesPut(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid supplier")
		return
	}
	var in struct {
		Aliases []stockSupplierAliasInput `json:"aliases"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&in) != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid payload")
		return
	}
	if len(in.Aliases) > 500 {
		httpx.WriteError(w, http.StatusBadRequest, "Too many aliases")
		return
	}
	restaurantID := a.ActiveRestaurantID
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error saving aliases")
		return
	}
	defer tx.Rollback()
	var name string
	if err = tx.QueryRowContext(r.Context(), `SELECT name FROM stock_suppliers WHERE restaurant_id=? AND id=? FOR UPDATE`, restaurantID, id).Scan(&name); err != nil {
		httpx.WriteError(w, http.StatusNotFound, "Supplier not found")
		return
	}
	type aliasKey struct{ code, description string }
	seen := map[aliasKey]bool{}
	keptIDs := []any{}
	for _, alias := range in.Aliases {
		description := stockAliasDescription(alias.Description)
		if description == "" || alias.StockItemID <= 0 {
			httpx.WriteError(w, http.StatusBadRequest, "Each alias needs a description and an item")
			return
		}
		code := strings.TrimSpace(alias.SupplierCode)
		key := aliasKey{code, description}
		if seen[key] {
			httpx.WriteError(w, http.StatusBadRequest, "Duplicate alias in payload")
			return
		}
		seen[key] = true
		var unitID int64
		if alias.StockUnitID > 0 {
			err = tx.QueryRowContext(r.Context(), `SELECT id FROM stock_item_units WHERE restaurant_id=? AND id=? AND stock_item_id=?`, restaurantID, alias.StockUnitID, alias.StockItemID).Scan(&unitID)
			if err != nil {
				httpx.WriteError(w, http.StatusBadRequest, "Alias unit does not belong to the item")
				return
			}
		} else {
			err = tx.QueryRowContext(r.Context(), `SELECT id FROM stock_item_units WHERE restaurant_id=? AND stock_item_id=? AND is_default_purchase=1`, restaurantID, alias.StockItemID).Scan(&unitID)
			if err != nil {
				httpx.WriteError(w, http.StatusBadRequest, "Alias item has no default purchase unit")
				return
			}
		}
		if _, err = tx.ExecContext(r.Context(), `INSERT INTO stock_supplier_aliases (restaurant_id,supplier_name,supplier_code,normalized_description,stock_item_id,stock_unit_id) VALUES (?,?,?,?,?,?)
			ON DUPLICATE KEY UPDATE stock_item_id=VALUES(stock_item_id),stock_unit_id=VALUES(stock_unit_id)`,
			restaurantID, name, code, description, alias.StockItemID, unitID); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error saving aliases")
			return
		}
		var keptID int64
		if err = tx.QueryRowContext(r.Context(), `SELECT id FROM stock_supplier_aliases WHERE restaurant_id=? AND supplier_name=? AND supplier_code=? AND normalized_description=?`, restaurantID, name, code, description).Scan(&keptID); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error saving aliases")
			return
		}
		keptIDs = append(keptIDs, keptID)
	}
	if len(keptIDs) > 0 {
		placeholders := strings.TrimRight(strings.Repeat("?,", len(keptIDs)), ",")
		args := append([]any{restaurantID, name}, keptIDs...)
		if _, err = tx.ExecContext(r.Context(), `DELETE FROM stock_supplier_aliases WHERE restaurant_id=? AND supplier_name=? AND id NOT IN (`+placeholders+`)`, args...); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error saving aliases")
			return
		}
	} else if _, err = tx.ExecContext(r.Context(), `DELETE FROM stock_supplier_aliases WHERE restaurant_id=? AND supplier_name=?`, restaurantID, name); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error saving aliases")
		return
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error saving aliases")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleBOStockSupplierPricesGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid supplier")
		return
	}
	days := 180
	if raw := r.URL.Query().Get("days"); raw != "" {
		if parsed, parseErr := strconv.Atoi(raw); parseErr == nil {
			days = parsed
		}
	}
	if days < 7 {
		days = 7
	}
	if days > 730 {
		days = 730
	}
	name, ok := s.stockSupplierName(r, w, a.ActiveRestaurantID, id)
	if !ok {
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT p.stock_item_id,i.name,i.base_unit,COUNT(*) samples,MIN(p.unit_cost_base),MAX(p.unit_cost_base),AVG(p.unit_cost_base),
		SUBSTRING_INDEX(GROUP_CONCAT(p.unit_cost_base ORDER BY p.effective_at DESC,p.id DESC SEPARATOR '|'),'|',1)+0,
		DATE_FORMAT(MAX(p.effective_at),'%Y-%m-%d')
		FROM stock_item_prices p
		JOIN stock_items i ON i.restaurant_id=p.restaurant_id AND i.id=p.stock_item_id
		WHERE p.restaurant_id=? AND p.supplier_name=? AND p.effective_at>=NOW()-INTERVAL ? DAY
		GROUP BY p.stock_item_id,i.name,i.base_unit ORDER BY MAX(p.effective_at) DESC LIMIT 200`, a.ActiveRestaurantID, name, days)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading price history")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	itemIDs := []int64{}
	for rows.Next() {
		var itemID int64
		var itemName, baseUnit string
		var samples int
		var minCost, maxCost, avgCost, lastCost float64
		var lastAt string
		if err = rows.Scan(&itemID, &itemName, &baseUnit, &samples, &minCost, &maxCost, &avgCost, &lastCost, &lastAt); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error reading price history")
			return
		}
		itemIDs = append(itemIDs, itemID)
		items = append(items, map[string]any{
			"itemId": itemID, "itemName": itemName, "baseUnit": baseUnit,
			"samples": samples, "minCost": minCost, "maxCost": maxCost,
			"avgCost": avgCost, "lastCost": lastCost, "lastAt": lastAt,
			"others": []map[string]any{},
		})
	}
	if len(itemIDs) > 0 {
		placeholders := strings.TrimRight(strings.Repeat("?,", len(itemIDs)), ",")
		args := []any{a.ActiveRestaurantID, name}
		for _, itemID := range itemIDs {
			args = append(args, itemID)
		}
		args = append(args, days)
		otherRows, err := s.db.QueryContext(r.Context(), `SELECT p.stock_item_id,p.supplier_name,AVG(p.unit_cost_base),COUNT(*)
			FROM stock_item_prices p
			WHERE p.restaurant_id=? AND p.supplier_name<>'' AND p.supplier_name<>? AND p.stock_item_id IN (`+placeholders+`) AND p.effective_at>=NOW()-INTERVAL ? DAY
			GROUP BY p.stock_item_id,p.supplier_name`, args...)
		if err == nil {
			defer otherRows.Close()
			byItem := map[int64][]map[string]any{}
			for otherRows.Next() {
				var itemID int64
				var otherName string
				var avgCost float64
				var samples int
				if err = otherRows.Scan(&itemID, &otherName, &avgCost, &samples); err == nil {
					byItem[itemID] = append(byItem[itemID], map[string]any{"supplierName": otherName, "avgCost": avgCost, "samples": samples})
				}
			}
			for _, item := range items {
				if others, found := byItem[item["itemId"].(int64)]; found {
					item["others"] = others
				}
			}
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "supplier": map[string]any{"id": id, "name": name}, "days": days, "items": items})
}

func stockEmptyNil(value *string) any {
	if value == nil || *value == "" {
		return nil
	}
	return *value
}
