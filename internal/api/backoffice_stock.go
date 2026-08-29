package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"preactvillacarmen/internal/httpx"
)

const (
	stockPermissionView             = "stock.view"
	stockPermissionItemsManage      = "stock.items.manage"
	stockPermissionWarehousesManage = "stock.warehouses.manage"
	stockPermissionAdjust           = "stock.adjust"
	stockPermissionWasteRecord      = "stock.waste.record"
	stockPermissionTransfer         = "stock.transfer"
	stockPermissionCountPerform     = "stock.count.perform"
	stockPermissionCountClose       = "stock.count.close"
	stockPermissionRecipesView      = "stock.recipes.view"
	stockPermissionRecipesManage    = "stock.recipes.manage"
	stockPermissionProduction       = "stock.production.perform"
	stockPermissionForecastView     = "stock.forecast.view"
	stockPermissionOCRUpload        = "stock.ocr.upload"
	stockPermissionOCRConfirm       = "stock.ocr.confirm"
	stockPermissionCostsView        = "stock.costs.view"
	stockPermissionCostsManage      = "stock.costs.manage"
	stockPermissionSettingsManage   = "stock.settings.manage"

	// Technical sheets (fichas tecnicas) carry their own authority so a cook can
	// edit steps without being able to publish a sheet, delete it, or spend AI
	// image credits. These are deliberately finer-grained than stock.recipes.*.
	stockPermissionSheetsView         = "stock.sheets.view"
	stockPermissionSheetsManage       = "stock.sheets.manage"
	stockPermissionSheetsPublish      = "stock.sheets.publish"
	stockPermissionSheetsDelete       = "stock.sheets.delete"
	stockPermissionSheetsStepsManage  = "stock.sheets.steps.manage"
	stockPermissionSheetsImagesManage = "stock.sheets.images.manage"
	stockPermissionSheetsImagesAI     = "stock.sheets.images.ai"

	// Flipping a comida item between RAW and MANUFACTURED changes how it is
	// deducted from stock, so it is not a plain catalogue edit.
	comidaPermissionProductionTypeManage = "comida.production_type.manage"
)

// stockPermissionKeys is the catalogue offered to role administration. It must
// stay a superset of every key ever persisted: dropping a key here would make
// the next role PUT silently discard that override.
var stockPermissionKeys = []string{
	stockPermissionView,
	stockPermissionItemsManage,
	stockPermissionWarehousesManage,
	stockPermissionAdjust,
	stockPermissionWasteRecord,
	stockPermissionTransfer,
	stockPermissionCountPerform,
	stockPermissionCountClose,
	stockPermissionRecipesView,
	stockPermissionRecipesManage,
	stockPermissionProduction,
	stockPermissionForecastView,
	stockPermissionOCRUpload,
	stockPermissionOCRConfirm,
	stockPermissionCostsView,
	stockPermissionCostsManage,
	stockPermissionSettingsManage,
	stockPermissionSheetsView,
	stockPermissionSheetsManage,
	stockPermissionSheetsPublish,
	stockPermissionSheetsDelete,
	stockPermissionSheetsStepsManage,
	stockPermissionSheetsImagesManage,
	stockPermissionSheetsImagesAI,
	comidaPermissionProductionTypeManage,
}

func withBOStockTimeout(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) boStockPermissionAllowed(ctx context.Context, a boAuth, permission string) (bool, error) {
	var allowed int
	err := s.db.QueryRowContext(ctx, `SELECT is_allowed FROM stock_role_permissions WHERE restaurant_id=? AND role_slug=? AND permission_key=? LIMIT 1`, a.ActiveRestaurantID, a.Role, permission).Scan(&allowed)
	if err == sql.ErrNoRows {
		return a.Role == "root" || a.Role == "admin", nil
	}
	return allowed != 0, err
}

func (s *Server) requireBOStockPermission(permission string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			a, ok := boAuthFromContext(r.Context())
			if !ok {
				httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
				return
			}
			// A/B version gate: stock is a v0.2 module; v0.1 users are blocked even
			// if their role permission would allow it.
			if !appCapabilityAllowed(boCapabilityStock, a.User.AppVersion) {
				httpx.WriteError(w, http.StatusForbidden, "Forbidden")
				return
			}
			allowed, err := s.boStockPermissionAllowed(r.Context(), a, permission)
			if err != nil {
				httpx.WriteError(w, http.StatusInternalServerError, "Error validating stock permission")
				return
			}
			if !allowed {
				httpx.WriteError(w, http.StatusForbidden, "Forbidden")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

type stockWarehouse struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Code      string `json:"code,omitempty"`
	Type      string `json:"type"`
	IsDefault bool   `json:"isDefault"`
	IsActive  bool   `json:"isActive"`
	SortOrder int    `json:"sortOrder"`
	Notes     string `json:"notes,omitempty"`
}

type stockUnit struct {
	ID                int64   `json:"id"`
	Code              string  `json:"code"`
	Label             string  `json:"label"`
	FactorToBase      float64 `json:"factorToBase"`
	IsDefaultPurchase bool    `json:"isDefaultPurchase"`
	IsDefaultDisplay  bool    `json:"isDefaultDisplay"`
}

type stockItemCard struct {
	ID               int64     `json:"id"`
	Name             string    `json:"name"`
	SKU              string    `json:"sku,omitempty"`
	CategoryName     string    `json:"categoryName,omitempty"`
	Kind             string    `json:"kind"`
	BaseDimension    string    `json:"baseDimension"`
	BaseUnit         string    `json:"baseUnit"`
	IsTracked        bool      `json:"isTracked"`
	DeductionSource  string    `json:"deductionSource"`
	QuantityBase     float64   `json:"quantityBase"`
	ParLevelBase     float64   `json:"parLevelBase"`
	ReorderPointBase float64   `json:"reorderPointBase"`
	DisplayUnit      stockUnit `json:"displayUnit"`
	WarehouseID      *int64    `json:"warehouseId,omitempty"`
}

type stockMovementRow struct {
	ID            int64   `json:"id"`
	QuantityBase  float64 `json:"quantityBase"`
	Type          string  `json:"type"`
	WasteReason   string  `json:"wasteReason,omitempty"`
	EnteredQty    float64 `json:"enteredQuantity"`
	EnteredUnit   string  `json:"enteredUnit"`
	WarehouseName string  `json:"warehouseName"`
	Note          string  `json:"note,omitempty"`
	ActorName     string  `json:"actorName"`
	OccurredAt    string  `json:"occurredAt"`
}

func stockBaseUnitForDimension(dimension string) (string, bool) {
	switch strings.ToUpper(strings.TrimSpace(dimension)) {
	case "MASS":
		return "g", true
	case "VOLUME":
		return "ml", true
	case "COUNT":
		return "ud", true
	default:
		return "", false
	}
}

func normalizeStockMovementQuantity(movementType string, enteredQty, factor float64) (float64, error) {
	if enteredQty <= 0 || factor <= 0 {
		return 0, errors.New("quantity and unit factor must be positive")
	}
	qty := enteredQty * factor
	switch strings.ToUpper(strings.TrimSpace(movementType)) {
	case "PURCHASE", "PRODUCTION_IN", "TRANSFER_IN", "RETURN":
		return qty, nil
	case "ADJUSTMENT", "PRODUCTION_OUT", "SALE", "WASTE", "TRANSFER_OUT":
		return -qty, nil
	default:
		return 0, errors.New("unsupported movement type")
	}
}

func (s *Server) handleBOStockWarehousesList(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	_, _ = s.db.ExecContext(r.Context(), `
		INSERT INTO stock_warehouses (restaurant_id, name, code, type, is_default)
		SELECT ?, 'Almacén principal', 'MAIN', 'STORAGE', 1
		WHERE NOT EXISTS (SELECT 1 FROM stock_warehouses WHERE restaurant_id = ? AND deleted_at IS NULL)
	`, a.ActiveRestaurantID, a.ActiveRestaurantID)
	rows, err := s.db.QueryContext(r.Context(), `SELECT id, name, COALESCE(code,''), type, is_default, is_active, sort_order, COALESCE(notes,'') FROM stock_warehouses WHERE restaurant_id = ? AND deleted_at IS NULL ORDER BY is_default DESC, sort_order, name`, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, 500, "Error loading warehouses")
		return
	}
	defer rows.Close()
	out := []stockWarehouse{}
	for rows.Next() {
		var x stockWarehouse
		var d, active int
		if err := rows.Scan(&x.ID, &x.Name, &x.Code, &x.Type, &d, &active, &x.SortOrder, &x.Notes); err != nil {
			httpx.WriteError(w, 500, "Error reading warehouses")
			return
		}
		x.IsDefault, x.IsActive = d != 0, active != 0
		out = append(out, x)
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "warehouses": out})
}

func (s *Server) handleBOStockWarehouseCreate(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, 401, "Unauthorized")
		return
	}
	var in stockWarehouse
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || strings.TrimSpace(in.Name) == "" {
		httpx.WriteError(w, 400, "Name is required")
		return
	}
	if in.Type == "" {
		in.Type = "STORAGE"
	}
	if !validStockWarehouseType(in.Type) {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid warehouse type")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, 500, "Error creating warehouse")
		return
	}
	defer tx.Rollback()
	var tenantLock int
	if err = tx.QueryRowContext(r.Context(), `SELECT id FROM restaurants WHERE id=? FOR UPDATE`, a.ActiveRestaurantID).Scan(&tenantLock); err != nil {
		httpx.WriteError(w, 500, "Error creating warehouse")
		return
	}
	var warehouseCount int
	if err = tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM stock_warehouses WHERE restaurant_id=? AND deleted_at IS NULL`, a.ActiveRestaurantID).Scan(&warehouseCount); err != nil {
		httpx.WriteError(w, 500, "Error creating warehouse")
		return
	}
	if warehouseCount == 0 {
		in.IsDefault = true
	}
	if in.IsDefault {
		if _, err = tx.ExecContext(r.Context(), `UPDATE stock_warehouses SET is_default = 0 WHERE restaurant_id = ?`, a.ActiveRestaurantID); err != nil {
			httpx.WriteError(w, 500, "Error creating warehouse")
			return
		}
	}
	res, err := tx.ExecContext(r.Context(), `INSERT INTO stock_warehouses (restaurant_id,name,code,type,is_default,sort_order,notes) VALUES (?,?,?,?,?,?,?)`, a.ActiveRestaurantID, strings.TrimSpace(in.Name), stockNullableString(in.Code), in.Type, stockBoolInt(in.IsDefault), in.SortOrder, stockNullableString(in.Notes))
	if err != nil {
		httpx.WriteError(w, 400, "Warehouse could not be created")
		return
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, 500, "Error creating warehouse")
		return
	}
	id, _ := res.LastInsertId()
	httpx.WriteJSON(w, 201, map[string]any{"success": true, "id": id})
}

func (s *Server) handleBOStockWarehousePatch(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, 401, "Unauthorized")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteError(w, 400, "Invalid warehouse")
		return
	}
	var in stockWarehouse
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || strings.TrimSpace(in.Name) == "" {
		httpx.WriteError(w, 400, "Name is required")
		return
	}
	if !validStockWarehouseType(in.Type) {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid warehouse type")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, 500, "Error updating warehouse")
		return
	}
	defer tx.Rollback()
	var tenantLock int
	if err = tx.QueryRowContext(r.Context(), `SELECT id FROM restaurants WHERE id=? FOR UPDATE`, a.ActiveRestaurantID).Scan(&tenantLock); err != nil {
		httpx.WriteError(w, 500, "Error updating warehouse")
		return
	}
	if in.IsDefault {
		if _, err = tx.ExecContext(r.Context(), `UPDATE stock_warehouses SET is_default = 0 WHERE restaurant_id = ?`, a.ActiveRestaurantID); err != nil {
			httpx.WriteError(w, 500, "Error updating warehouse")
			return
		}
	}
	res, err := tx.ExecContext(r.Context(), `UPDATE stock_warehouses SET name=?, code=?, type=?, is_default=?, is_active=?, sort_order=?, notes=? WHERE id=? AND restaurant_id=? AND deleted_at IS NULL`, strings.TrimSpace(in.Name), stockNullableString(in.Code), in.Type, stockBoolInt(in.IsDefault), stockBoolInt(in.IsActive), in.SortOrder, stockNullableString(in.Notes), id, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, 400, "Warehouse could not be updated")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		httpx.WriteError(w, 404, "Warehouse not found")
		return
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, 500, "Error updating warehouse")
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true})
}

func (s *Server) handleBOStockWarehouseDelete(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, 401, "Unauthorized")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteError(w, 400, "Invalid warehouse")
		return
	}
	var nonZero, isDefault int
	err = s.db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM stock_levels WHERE restaurant_id=? AND warehouse_id=? AND qty_base<>0), COALESCE((SELECT is_default FROM stock_warehouses WHERE id=? AND restaurant_id=?),0)`, a.ActiveRestaurantID, id, id, a.ActiveRestaurantID).Scan(&nonZero, &isDefault)
	if err != nil {
		httpx.WriteError(w, 500, "Error deleting warehouse")
		return
	}
	if nonZero != 0 {
		httpx.WriteError(w, 409, "Transfer or write off stock before deleting this warehouse")
		return
	}
	if isDefault != 0 {
		httpx.WriteError(w, 409, "Default warehouse cannot be deleted")
		return
	}
	res, err := s.db.ExecContext(r.Context(), `UPDATE stock_warehouses SET deleted_at=NOW(), is_active=0 WHERE id=? AND restaurant_id=? AND deleted_at IS NULL`, id, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, 500, "Error deleting warehouse")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		httpx.WriteError(w, 404, "Warehouse not found")
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true})
}

func (s *Server) handleBOStockCategoriesList(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,name,sort_order,is_active FROM stock_categories WHERE restaurant_id=? ORDER BY sort_order,name`, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading categories")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var name string
		var sortOrder, active int
		if err = rows.Scan(&id, &name, &sortOrder, &active); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error reading categories")
			return
		}
		out = append(out, map[string]any{"id": id, "name": name, "sortOrder": sortOrder, "isActive": active != 0})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "categories": out})
}

func (s *Server) handleBOStockCategoryCreate(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	var in struct {
		Name      string `json:"name"`
		SortOrder int    `json:"sortOrder"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || strings.TrimSpace(in.Name) == "" {
		httpx.WriteError(w, http.StatusBadRequest, "Name is required")
		return
	}
	res, err := s.db.ExecContext(r.Context(), `INSERT INTO stock_categories (restaurant_id,name,sort_order) VALUES (?,?,?)`, a.ActiveRestaurantID, strings.TrimSpace(in.Name), in.SortOrder)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Category could not be created")
		return
	}
	id, _ := res.LastInsertId()
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"success": true, "id": id})
}

func (s *Server) handleBOStockCategoryPatch(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid category")
		return
	}
	var in struct {
		Name      string `json:"name"`
		SortOrder int    `json:"sortOrder"`
		IsActive  bool   `json:"isActive"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || strings.TrimSpace(in.Name) == "" {
		httpx.WriteError(w, http.StatusBadRequest, "Name is required")
		return
	}
	res, err := s.db.ExecContext(r.Context(), `UPDATE stock_categories SET name=?,sort_order=?,is_active=? WHERE id=? AND restaurant_id=?`, strings.TrimSpace(in.Name), in.SortOrder, stockBoolInt(in.IsActive), id, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Category could not be updated")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		httpx.WriteError(w, http.StatusNotFound, "Category not found")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleBOStockCategoryDelete(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid category")
		return
	}
	var used int
	if err = s.db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM stock_items WHERE restaurant_id=? AND category_id=? AND deleted_at IS NULL)`, a.ActiveRestaurantID, id).Scan(&used); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error deleting category")
		return
	}
	if used != 0 {
		httpx.WriteError(w, http.StatusConflict, "Category is in use")
		return
	}
	res, err := s.db.ExecContext(r.Context(), `DELETE FROM stock_categories WHERE id=? AND restaurant_id=?`, id, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error deleting category")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		httpx.WriteError(w, http.StatusNotFound, "Category not found")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleBOStockItemsList(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, 401, "Unauthorized")
		return
	}
	page := stockQueryInt(r, "page", 1, 1, 100000)
	pageSize := stockQueryInt(r, "pageSize", 24, 1, 100)
	offset := (page - 1) * pageSize
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	warehouseID, _ := strconv.ParseInt(r.URL.Query().Get("warehouseId"), 10, 64)
	where := `i.restaurant_id=? AND i.deleted_at IS NULL AND i.is_active=1`
	whereArgs := []any{a.ActiveRestaurantID}
	if q != "" {
		where += ` AND (i.name LIKE ? OR i.sku LIKE ?)`
		like := "%" + q + "%"
		whereArgs = append(whereArgs, like, like)
	}
	if tracked := strings.TrimSpace(r.URL.Query().Get("isTracked")); tracked == "true" || tracked == "false" {
		where += ` AND i.is_tracked=?`
		whereArgs = append(whereArgs, stockBoolInt(tracked == "true"))
	}
	if kind := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("kind"))); kind != "" {
		where += ` AND i.kind=?`
		whereArgs = append(whereArgs, kind)
	}
	if categoryID, _ := strconv.ParseInt(r.URL.Query().Get("categoryId"), 10, 64); categoryID > 0 {
		where += ` AND i.category_id=?`
		whereArgs = append(whereArgs, categoryID)
	}
	joinLevel := `LEFT JOIN (SELECT restaurant_id, stock_item_id, SUM(qty_base) qty_base, SUM(par_level_base) par_level_base, SUM(reorder_point_base) reorder_point_base FROM stock_levels`
	joinArgs := []any{}
	if warehouseID > 0 {
		joinLevel += ` WHERE warehouse_id=?`
		joinArgs = append(joinArgs, warehouseID)
	}
	joinLevel += ` GROUP BY restaurant_id, stock_item_id) l ON l.restaurant_id=i.restaurant_id AND l.stock_item_id=i.id`
	var total int
	if err := s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM stock_items i WHERE `+where, whereArgs...).Scan(&total); err != nil {
		httpx.WriteError(w, 500, "Error loading stock")
		return
	}
	sortSQL := "i.name"
	switch r.URL.Query().Get("sort") {
	case "stock_asc":
		sortSQL = "COALESCE(l.qty_base,0),i.name"
	case "stock_desc":
		sortSQL = "COALESCE(l.qty_base,0) DESC,i.name"
	case "updated_at":
		sortSQL = "i.updated_at DESC"
	}
	query := `SELECT i.id,i.name,COALESCE(i.sku,''),COALESCE(c.name,''),i.kind,i.base_dimension,i.base_unit,i.is_tracked,i.deduction_source,COALESCE(l.qty_base,0),COALESCE(l.par_level_base,0),COALESCE(l.reorder_point_base,0),u.id,u.code,u.label,u.factor_to_base,u.is_default_purchase,u.is_default_display FROM stock_items i LEFT JOIN stock_categories c ON c.id=i.category_id AND c.restaurant_id=i.restaurant_id ` + joinLevel + ` JOIN stock_item_units u ON u.restaurant_id=i.restaurant_id AND u.stock_item_id=i.id AND u.is_default_display=1 WHERE ` + where + ` ORDER BY ` + sortSQL + ` LIMIT ? OFFSET ?`
	args := append(joinArgs, whereArgs...)
	args = append(args, pageSize, offset)
	rows, err := s.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		httpx.WriteError(w, 500, "Error loading stock")
		return
	}
	defer rows.Close()
	items := []stockItemCard{}
	for rows.Next() {
		var x stockItemCard
		var tracked, dp, dd int
		if err := rows.Scan(&x.ID, &x.Name, &x.SKU, &x.CategoryName, &x.Kind, &x.BaseDimension, &x.BaseUnit, &tracked, &x.DeductionSource, &x.QuantityBase, &x.ParLevelBase, &x.ReorderPointBase, &x.DisplayUnit.ID, &x.DisplayUnit.Code, &x.DisplayUnit.Label, &x.DisplayUnit.FactorToBase, &dp, &dd); err != nil {
			httpx.WriteError(w, 500, "Error reading stock")
			return
		}
		x.IsTracked = tracked != 0
		x.DisplayUnit.IsDefaultPurchase = dp != 0
		x.DisplayUnit.IsDefaultDisplay = dd != 0
		if warehouseID > 0 {
			x.WarehouseID = &warehouseID
		}
		items = append(items, x)
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "items": items, "page": page, "pageSize": pageSize, "total": total, "totalPages": (total + pageSize - 1) / pageSize})
}

func (s *Server) handleBOStockItemOptions(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	q := "%" + strings.TrimSpace(r.URL.Query().Get("q")) + "%"
	rows, err := s.db.QueryContext(r.Context(), `SELECT i.id,i.name,i.kind,i.is_tracked,u.id,u.code,u.label,u.factor_to_base FROM stock_items i JOIN stock_item_units u ON u.restaurant_id=i.restaurant_id AND u.stock_item_id=i.id AND u.is_default_display=1 WHERE i.restaurant_id=? AND i.is_active=1 AND i.deleted_at IS NULL AND (?='%%' OR i.name LIKE ? OR i.sku LIKE ?) ORDER BY i.name LIMIT 500`, a.ActiveRestaurantID, q, q, q)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading item options")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, unitID int64
		var name, kind, code, label string
		var tracked int
		var factor float64
		if err := rows.Scan(&id, &name, &kind, &tracked, &unitID, &code, &label, &factor); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error reading item options")
			return
		}
		items = append(items, map[string]any{"id": id, "name": name, "kind": kind, "isTracked": tracked != 0, "displayUnit": map[string]any{"id": unitID, "code": code, "label": label, "factorToBase": factor}})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "items": items})
}

func (s *Server) handleBOStockItemCreate(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, 401, "Unauthorized")
		return
	}
	var in struct {
		Name              string  `json:"name"`
		SKU               string  `json:"sku"`
		CategoryID        *int64  `json:"categoryId"`
		Kind              string  `json:"kind"`
		BaseDimension     string  `json:"baseDimension"`
		IsTracked         *bool   `json:"isTracked"`
		DeductionSource   string  `json:"deductionSource"`
		DisplayUnitLabel  string  `json:"displayUnitLabel"`
		DisplayUnitCode   string  `json:"displayUnitCode"`
		DisplayUnitFactor float64 `json:"displayUnitFactor"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || strings.TrimSpace(in.Name) == "" {
		httpx.WriteError(w, 400, "Name is required")
		return
	}
	baseUnit, ok := stockBaseUnitForDimension(in.BaseDimension)
	if !ok {
		httpx.WriteError(w, 400, "Invalid base dimension")
		return
	}
	if in.Kind == "" {
		in.Kind = "RAW"
	}
	if !validStockItemKind(in.Kind) {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid item kind")
		return
	}
	if in.DeductionSource == "" {
		in.DeductionSource = "BOTH_MANUAL"
	}
	if !validStockDeductionSource(in.DeductionSource) {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid deduction source")
		return
	}
	if in.DisplayUnitFactor <= 0 {
		in.DisplayUnitFactor = 1
	}
	if in.DisplayUnitCode == "" {
		in.DisplayUnitCode = baseUnit
	}
	if in.DisplayUnitLabel == "" {
		in.DisplayUnitLabel = in.DisplayUnitCode
	}
	isTracked := true
	if in.IsTracked != nil {
		isTracked = *in.IsTracked
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, 500, "Error creating item")
		return
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(r.Context(), `INSERT INTO stock_items (restaurant_id,category_id,sku,name,kind,base_dimension,base_unit,is_tracked,deduction_source) VALUES (?,?,?,?,?,?,?,?,?)`, a.ActiveRestaurantID, in.CategoryID, stockNullableString(in.SKU), strings.TrimSpace(in.Name), in.Kind, strings.ToUpper(in.BaseDimension), baseUnit, stockBoolInt(isTracked), in.DeductionSource)
	if err != nil {
		httpx.WriteError(w, 400, "Item could not be created")
		return
	}
	id, _ := res.LastInsertId()
	_, err = tx.ExecContext(r.Context(), `INSERT INTO stock_item_units (restaurant_id,stock_item_id,code,label,factor_to_base,is_default_purchase,is_default_display,can_purchase,can_recipe,can_count) VALUES (?,?,?,?,?,1,1,1,1,1)`, a.ActiveRestaurantID, id, in.DisplayUnitCode, in.DisplayUnitLabel, in.DisplayUnitFactor)
	if err != nil {
		httpx.WriteError(w, 400, "Item unit could not be created")
		return
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, 500, "Error creating item")
		return
	}
	httpx.WriteJSON(w, 201, map[string]any{"success": true, "id": id})
}

func (s *Server) handleBOStockItemPatch(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	itemID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || itemID <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid item")
		return
	}
	var in struct {
		Name            string `json:"name"`
		SKU             string `json:"sku"`
		Kind            string `json:"kind"`
		IsTracked       bool   `json:"isTracked"`
		DeductionSource string `json:"deductionSource"`
		ShelfLifeDays   *int   `json:"shelfLifeDays"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || strings.TrimSpace(in.Name) == "" {
		httpx.WriteError(w, http.StatusBadRequest, "Name is required")
		return
	}
	if in.Kind == "" {
		in.Kind = "RAW"
	}
	if in.DeductionSource == "" {
		in.DeductionSource = "BOTH_MANUAL"
	}
	if !validStockItemKind(in.Kind) || !validStockDeductionSource(in.DeductionSource) {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid item configuration")
		return
	}
	res, err := s.db.ExecContext(r.Context(), `UPDATE stock_items SET name=?,sku=?,kind=?,is_tracked=?,deduction_source=?,shelf_life_days=? WHERE id=? AND restaurant_id=? AND deleted_at IS NULL`, strings.TrimSpace(in.Name), stockNullableString(in.SKU), in.Kind, stockBoolInt(in.IsTracked), in.DeductionSource, in.ShelfLifeDays, itemID, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Item could not be updated")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		httpx.WriteError(w, http.StatusNotFound, "Item not found")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleBOStockLevelTargetsPatch(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	itemID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var in struct {
		WarehouseID  int64   `json:"warehouseId"`
		UnitID       int64   `json:"unitId"`
		ParLevel     float64 `json:"parLevel"`
		ReorderPoint float64 `json:"reorderPoint"`
	}
	if itemID <= 0 || json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || in.WarehouseID <= 0 || in.UnitID <= 0 || in.ParLevel < 0 || in.ReorderPoint < 0 || in.ReorderPoint > in.ParLevel && in.ParLevel > 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid stock targets")
		return
	}
	var factor float64
	if err := s.db.QueryRowContext(r.Context(), `SELECT factor_to_base FROM stock_item_units WHERE restaurant_id=? AND stock_item_id=? AND id=?`, a.ActiveRestaurantID, itemID, in.UnitID).Scan(&factor); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid item unit")
		return
	}
	_, err := s.db.ExecContext(r.Context(), `INSERT INTO stock_levels (restaurant_id,stock_item_id,warehouse_id,qty_base,par_level_base,reorder_point_base) VALUES (?,?,?,0,?,?) ON DUPLICATE KEY UPDATE par_level_base=VALUES(par_level_base),reorder_point_base=VALUES(reorder_point_base),version=version+1`, a.ActiveRestaurantID, itemID, in.WarehouseID, in.ParLevel*factor, in.ReorderPoint*factor)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Stock targets could not be saved")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleBOStockItemUnitsList(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	itemID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || itemID <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid item")
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,code,label,factor_to_base,is_default_purchase,is_default_display FROM stock_item_units WHERE restaurant_id=? AND stock_item_id=? ORDER BY is_default_display DESC,label`, a.ActiveRestaurantID, itemID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading units")
		return
	}
	defer rows.Close()
	out := []stockUnit{}
	for rows.Next() {
		var unit stockUnit
		var purchase, display int
		if err = rows.Scan(&unit.ID, &unit.Code, &unit.Label, &unit.FactorToBase, &purchase, &display); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error reading units")
			return
		}
		unit.IsDefaultPurchase = purchase != 0
		unit.IsDefaultDisplay = display != 0
		out = append(out, unit)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "units": out})
}

func (s *Server) handleBOStockItemUnitCreate(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	itemID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || itemID <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid item")
		return
	}
	var in struct {
		Code              string  `json:"code"`
		Label             string  `json:"label"`
		FactorToBase      float64 `json:"factorToBase"`
		IsDefaultPurchase bool    `json:"isDefaultPurchase"`
		IsDefaultDisplay  bool    `json:"isDefaultDisplay"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || strings.TrimSpace(in.Code) == "" || strings.TrimSpace(in.Label) == "" || in.FactorToBase <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid unit")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creating unit")
		return
	}
	defer tx.Rollback()
	if in.IsDefaultPurchase {
		_, err = tx.ExecContext(r.Context(), `UPDATE stock_item_units SET is_default_purchase=0 WHERE restaurant_id=? AND stock_item_id=?`, a.ActiveRestaurantID, itemID)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error creating unit")
			return
		}
	}
	if in.IsDefaultDisplay {
		_, err = tx.ExecContext(r.Context(), `UPDATE stock_item_units SET is_default_display=0 WHERE restaurant_id=? AND stock_item_id=?`, a.ActiveRestaurantID, itemID)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error creating unit")
			return
		}
	}
	res, err := tx.ExecContext(r.Context(), `INSERT INTO stock_item_units (restaurant_id,stock_item_id,code,label,factor_to_base,is_default_purchase,is_default_display,can_purchase,can_recipe,can_count) SELECT ?,?,?,?,?,?,?,1,1,1 FROM stock_items WHERE id=? AND restaurant_id=? AND deleted_at IS NULL`, a.ActiveRestaurantID, itemID, strings.TrimSpace(in.Code), strings.TrimSpace(in.Label), in.FactorToBase, stockBoolInt(in.IsDefaultPurchase), stockBoolInt(in.IsDefaultDisplay), itemID, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Unit could not be created")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		httpx.WriteError(w, http.StatusNotFound, "Item not found")
		return
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creating unit")
		return
	}
	id, _ := res.LastInsertId()
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"success": true, "id": id})
}

func (s *Server) handleBOStockItemUnitDelete(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	itemID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	unitID, _ := strconv.ParseInt(chi.URLParam(r, "unitId"), 10, 64)
	if itemID <= 0 || unitID <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid unit")
		return
	}
	var isDefault, used int
	if err := s.db.QueryRowContext(r.Context(), `SELECT (is_default_display OR is_default_purchase),EXISTS(SELECT 1 FROM stock_movements WHERE restaurant_id=? AND entered_unit_id=?) FROM stock_item_units WHERE id=? AND stock_item_id=? AND restaurant_id=?`, a.ActiveRestaurantID, unitID, unitID, itemID, a.ActiveRestaurantID).Scan(&isDefault, &used); err != nil {
		if err == sql.ErrNoRows {
			httpx.WriteError(w, http.StatusNotFound, "Unit not found")
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error deleting unit")
		return
	}
	if isDefault != 0 || used != 0 {
		httpx.WriteError(w, http.StatusConflict, "Default or used unit cannot be deleted")
		return
	}
	_, err := s.db.ExecContext(r.Context(), `DELETE FROM stock_item_units WHERE id=? AND stock_item_id=? AND restaurant_id=?`, unitID, itemID, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error deleting unit")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleBOStockTransferCreate(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	var in struct {
		ItemID          int64   `json:"itemId"`
		FromWarehouseID int64   `json:"fromWarehouseId"`
		ToWarehouseID   int64   `json:"toWarehouseId"`
		Quantity        float64 `json:"quantity"`
		UnitID          int64   `json:"unitId"`
		IdempotencyKey  string  `json:"idempotencyKey"`
		Note            string  `json:"note"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || in.ItemID <= 0 || in.FromWarehouseID <= 0 || in.ToWarehouseID <= 0 || in.FromWarehouseID == in.ToWarehouseID || in.Quantity <= 0 || in.UnitID <= 0 || strings.TrimSpace(in.IdempotencyKey) == "" {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid transfer")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error transferring stock")
		return
	}
	defer tx.Rollback()
	var factor float64
	if err = tx.QueryRowContext(r.Context(), `SELECT factor_to_base FROM stock_item_units WHERE id=? AND stock_item_id=? AND restaurant_id=?`, in.UnitID, in.ItemID, a.ActiveRestaurantID).Scan(&factor); err != nil {
		httpx.WriteError(w, http.StatusNotFound, "Unit not found")
		return
	}
	qty := in.Quantity * factor
	for _, warehouseID := range []int64{in.FromWarehouseID, in.ToWarehouseID} {
		_, err = tx.ExecContext(r.Context(), `INSERT INTO stock_levels (restaurant_id,stock_item_id,warehouse_id,qty_base) SELECT ?,?,?,0 FROM stock_warehouses WHERE id=? AND restaurant_id=? AND is_active=1 AND deleted_at IS NULL ON DUPLICATE KEY UPDATE stock_item_id=VALUES(stock_item_id)`, a.ActiveRestaurantID, in.ItemID, warehouseID, warehouseID, a.ActiveRestaurantID)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Warehouse not found")
			return
		}
	}
	var current float64
	if err = tx.QueryRowContext(r.Context(), `SELECT qty_base FROM stock_levels WHERE restaurant_id=? AND stock_item_id=? AND warehouse_id=? FOR UPDATE`, a.ActiveRestaurantID, in.ItemID, in.FromWarehouseID).Scan(&current); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error transferring stock")
		return
	}
	var allowNegative int
	_ = tx.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT allow_negative_stock FROM stock_settings WHERE restaurant_id=?),1)`, a.ActiveRestaurantID).Scan(&allowNegative)
	if allowNegative == 0 && current < qty {
		httpx.WriteError(w, http.StatusConflict, "Insufficient stock")
		return
	}
	transferID := in.IdempotencyKey
	for index, line := range []struct {
		WarehouseID int64
		Qty         float64
		Type        string
		Suffix      string
	}{{in.FromWarehouseID, -qty, "TRANSFER_OUT", "-out"}, {in.ToWarehouseID, qty, "TRANSFER_IN", "-in"}} {
		_, err = tx.ExecContext(r.Context(), `INSERT INTO stock_movements (restaurant_id,stock_item_id,warehouse_id,qty_base,type,entered_qty,entered_unit_id,transfer_id,idempotency_key,note,actor_user_id) VALUES (?,?,?,?,?,?,?,?,?,?,?)`, a.ActiveRestaurantID, in.ItemID, line.WarehouseID, line.Qty, line.Type, in.Quantity, in.UnitID, transferID, in.IdempotencyKey+line.Suffix, stockNullableString(in.Note), a.User.ID)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Transfer could not be applied")
			return
		}
		_, err = tx.ExecContext(r.Context(), `UPDATE stock_levels SET qty_base=qty_base+?,version=version+1 WHERE restaurant_id=? AND stock_item_id=? AND warehouse_id=?`, line.Qty, a.ActiveRestaurantID, in.ItemID, line.WarehouseID)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error transferring stock")
			return
		}
		_ = index
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error transferring stock")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"success": true, "transferId": transferID})
}

func (s *Server) handleBOStockCountCreate(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	var in struct {
		WarehouseID int64  `json:"warehouseId"`
		Notes       string `json:"notes"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || in.WarehouseID <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid count sheet")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error opening count sheet")
		return
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(r.Context(), `INSERT INTO stock_count_sheets (restaurant_id,warehouse_id,opened_by,notes) SELECT ?,?,?,? FROM stock_warehouses WHERE id=? AND restaurant_id=? AND is_active=1 AND deleted_at IS NULL`, a.ActiveRestaurantID, in.WarehouseID, a.User.ID, stockNullableString(in.Notes), in.WarehouseID, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Count sheet could not be opened")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		httpx.WriteError(w, http.StatusNotFound, "Warehouse not found")
		return
	}
	sheetID, _ := res.LastInsertId()
	_, err = tx.ExecContext(r.Context(), `INSERT INTO stock_count_lines (restaurant_id,count_sheet_id,stock_item_id,expected_qty_base,entered_unit_id) SELECT i.restaurant_id,?,i.id,COALESCE(l.qty_base,0),u.id FROM stock_items i JOIN stock_item_units u ON u.restaurant_id=i.restaurant_id AND u.stock_item_id=i.id AND u.is_default_display=1 LEFT JOIN stock_levels l ON l.restaurant_id=i.restaurant_id AND l.stock_item_id=i.id AND l.warehouse_id=? WHERE i.restaurant_id=? AND i.is_tracked=1 AND i.is_active=1 AND i.deleted_at IS NULL`, sheetID, in.WarehouseID, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error opening count sheet")
		return
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error opening count sheet")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"success": true, "id": sheetID})
}

func (s *Server) handleBOStockCountGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	sheetID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if sheetID <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid count sheet")
		return
	}
	var warehouseID int64
	var warehouseName, status string
	if err := s.db.QueryRowContext(r.Context(), `SELECT s.warehouse_id,w.name,s.status FROM stock_count_sheets s JOIN stock_warehouses w ON w.restaurant_id=s.restaurant_id AND w.id=s.warehouse_id WHERE s.id=? AND s.restaurant_id=?`, sheetID, a.ActiveRestaurantID).Scan(&warehouseID, &warehouseName, &status); err != nil {
		if err == sql.ErrNoRows {
			httpx.WriteError(w, http.StatusNotFound, "Count sheet not found")
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading count sheet")
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT l.stock_item_id,i.name,l.expected_qty_base,l.observed_qty_base,l.entered_qty,l.entered_unit_id,u.label,u.factor_to_base FROM stock_count_lines l JOIN stock_items i ON i.restaurant_id=l.restaurant_id AND i.id=l.stock_item_id JOIN stock_item_units u ON u.restaurant_id=l.restaurant_id AND u.id=l.entered_unit_id WHERE l.restaurant_id=? AND l.count_sheet_id=? ORDER BY i.name`, a.ActiveRestaurantID, sheetID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading count sheet")
		return
	}
	defer rows.Close()
	lines := []map[string]any{}
	for rows.Next() {
		var itemID, unitID int64
		var name, label string
		var expected, factor float64
		var observed, entered sql.NullFloat64
		if err = rows.Scan(&itemID, &name, &expected, &observed, &entered, &unitID, &label, &factor); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error reading count sheet")
			return
		}
		lines = append(lines, map[string]any{"itemId": itemID, "name": name, "expectedQuantityBase": expected, "observedQuantityBase": nullableFloat(observed), "enteredQuantity": nullableFloat(entered), "unitId": unitID, "unitLabel": label, "factorToBase": factor})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "id": sheetID, "warehouseId": warehouseID, "warehouseName": warehouseName, "status": status, "lines": lines})
}

func (s *Server) handleBOStockCountClose(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	sheetID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if sheetID <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid count sheet")
		return
	}
	var in struct {
		Lines []struct {
			ItemID   int64   `json:"itemId"`
			Quantity float64 `json:"quantity"`
			UnitID   int64   `json:"unitId"`
		} `json:"lines"`
		IdempotencyKey string `json:"idempotencyKey"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&in) != nil || len(in.Lines) == 0 || strings.TrimSpace(in.IdempotencyKey) == "" {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid count data")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error closing count sheet")
		return
	}
	defer tx.Rollback()
	var warehouseID int64
	var status string
	if err = tx.QueryRowContext(r.Context(), `SELECT warehouse_id,status FROM stock_count_sheets WHERE id=? AND restaurant_id=? FOR UPDATE`, sheetID, a.ActiveRestaurantID).Scan(&warehouseID, &status); err != nil {
		httpx.WriteError(w, http.StatusNotFound, "Count sheet not found")
		return
	}
	if status != "OPEN" {
		httpx.WriteError(w, http.StatusConflict, "Count sheet is not open")
		return
	}
	for index, line := range in.Lines {
		if line.ItemID <= 0 || line.UnitID <= 0 || line.Quantity < 0 {
			httpx.WriteError(w, http.StatusBadRequest, "Invalid count line")
			return
		}
		var factor float64
		if err = tx.QueryRowContext(r.Context(), `SELECT factor_to_base FROM stock_item_units WHERE id=? AND stock_item_id=? AND restaurant_id=?`, line.UnitID, line.ItemID, a.ActiveRestaurantID).Scan(&factor); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "Invalid count unit")
			return
		}
		observed := line.Quantity * factor
		_, err = tx.ExecContext(r.Context(), `INSERT INTO stock_levels (restaurant_id,stock_item_id,warehouse_id,qty_base) VALUES (?,?,?,0) ON DUPLICATE KEY UPDATE stock_item_id=VALUES(stock_item_id)`, a.ActiveRestaurantID, line.ItemID, warehouseID)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error closing count sheet")
			return
		}
		var current float64
		if err = tx.QueryRowContext(r.Context(), `SELECT qty_base FROM stock_levels WHERE restaurant_id=? AND stock_item_id=? AND warehouse_id=? FOR UPDATE`, a.ActiveRestaurantID, line.ItemID, warehouseID).Scan(&current); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error closing count sheet")
			return
		}
		delta := observed - current
		_, err = tx.ExecContext(r.Context(), `UPDATE stock_count_lines SET observed_qty_base=?,entered_qty=?,entered_unit_id=? WHERE restaurant_id=? AND count_sheet_id=? AND stock_item_id=?`, observed, line.Quantity, line.UnitID, a.ActiveRestaurantID, sheetID, line.ItemID)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error closing count sheet")
			return
		}
		if delta != 0 {
			_, err = tx.ExecContext(r.Context(), `INSERT INTO stock_movements (restaurant_id,stock_item_id,warehouse_id,qty_base,type,entered_qty,entered_unit_id,ref_type,ref_id,idempotency_key,actor_user_id) VALUES (?,?,?,?,?,?,?,?,?,?,?)`, a.ActiveRestaurantID, line.ItemID, warehouseID, delta, "INVENTORY_COUNT", line.Quantity, line.UnitID, "stock_count", sheetID, in.IdempotencyKey+"-"+strconv.Itoa(index), a.User.ID)
			if err != nil {
				httpx.WriteError(w, http.StatusBadRequest, "Count movement could not be applied")
				return
			}
			_, err = tx.ExecContext(r.Context(), `UPDATE stock_levels SET qty_base=?,version=version+1 WHERE restaurant_id=? AND stock_item_id=? AND warehouse_id=?`, observed, a.ActiveRestaurantID, line.ItemID, warehouseID)
			if err != nil {
				httpx.WriteError(w, http.StatusInternalServerError, "Error closing count sheet")
				return
			}
		}
	}
	_, err = tx.ExecContext(r.Context(), `UPDATE stock_count_sheets SET status='CLOSED',closed_by=?,closed_at=NOW() WHERE id=? AND restaurant_id=?`, a.User.ID, sheetID, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error closing count sheet")
		return
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error closing count sheet")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleBOStockItemDelete(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	itemID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || itemID <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid item")
		return
	}
	var nonZero int
	if err = s.db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM stock_levels WHERE restaurant_id=? AND stock_item_id=? AND qty_base<>0)`, a.ActiveRestaurantID, itemID).Scan(&nonZero); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error deleting item")
		return
	}
	if nonZero != 0 {
		httpx.WriteError(w, http.StatusConflict, "Set stock to zero before deleting this item")
		return
	}
	res, err := s.db.ExecContext(r.Context(), `UPDATE stock_items SET deleted_at=NOW(),is_active=0 WHERE id=? AND restaurant_id=? AND deleted_at IS NULL`, itemID, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error deleting item")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		httpx.WriteError(w, http.StatusNotFound, "Item not found")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleBOStockItemMovementsList(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	itemID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || itemID <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid item")
		return
	}
	page := stockQueryInt(r, "page", 1, 1, 100000)
	pageSize := stockQueryInt(r, "pageSize", 30, 1, 100)
	var total int
	if err = s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM stock_movements WHERE restaurant_id=? AND stock_item_id=?`, a.ActiveRestaurantID, itemID).Scan(&total); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading movements")
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT m.id,m.qty_base,m.type,COALESCE(m.waste_reason,''),m.entered_qty,u.label,w.name,COALESCE(m.note,''),COALESCE(NULLIF(bu.name,''),bu.email),DATE_FORMAT(m.occurred_at,'%Y-%m-%dT%H:%i:%sZ') FROM stock_movements m JOIN stock_item_units u ON u.restaurant_id=m.restaurant_id AND u.id=m.entered_unit_id JOIN stock_warehouses w ON w.restaurant_id=m.restaurant_id AND w.id=m.warehouse_id JOIN bo_users bu ON bu.id=m.actor_user_id WHERE m.restaurant_id=? AND m.stock_item_id=? ORDER BY m.occurred_at DESC,m.id DESC LIMIT ? OFFSET ?`, a.ActiveRestaurantID, itemID, pageSize, (page-1)*pageSize)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading movements")
		return
	}
	defer rows.Close()
	movements := []stockMovementRow{}
	for rows.Next() {
		var movement stockMovementRow
		if err = rows.Scan(&movement.ID, &movement.QuantityBase, &movement.Type, &movement.WasteReason, &movement.EnteredQty, &movement.EnteredUnit, &movement.WarehouseName, &movement.Note, &movement.ActorName, &movement.OccurredAt); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error reading movements")
			return
		}
		movements = append(movements, movement)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "movements": movements, "page": page, "pageSize": pageSize, "total": total, "totalPages": (total + pageSize - 1) / pageSize})
}

func (s *Server) handleBOStockSettingsGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	var displayMode, cadence string
	var businessProfile string
	var seasonality json.RawMessage
	var allowNegative, labour, onboarding int
	err := s.db.QueryRowContext(r.Context(), `SELECT warehouse_display_mode,count_cadence,allow_negative_stock,labour_cost_enabled,COALESCE(business_profile,''),COALESCE(seasonality_profile,JSON_OBJECT()),onboarding_completed FROM stock_settings WHERE restaurant_id=?`, a.ActiveRestaurantID).Scan(&displayMode, &cadence, &allowNegative, &labour, &businessProfile, &seasonality, &onboarding)
	if err == sql.ErrNoRows {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "warehouseDisplayMode": "AGGREGATED", "countCadence": "WEEKLY", "allowNegativeStock": true, "labourCostEnabled": false, "businessProfile": "", "seasonalityProfile": map[string]any{}, "onboardingCompleted": false})
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading stock settings")
		return
	}
	var seasonalityValue any
	_ = json.Unmarshal(seasonality, &seasonalityValue)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "warehouseDisplayMode": displayMode, "countCadence": cadence, "allowNegativeStock": allowNegative != 0, "labourCostEnabled": labour != 0, "businessProfile": businessProfile, "seasonalityProfile": seasonalityValue, "onboardingCompleted": onboarding != 0})
}

func (s *Server) handleBOStockSettingsPatch(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	var in struct {
		WarehouseDisplayMode string          `json:"warehouseDisplayMode"`
		CountCadence         string          `json:"countCadence"`
		AllowNegativeStock   bool            `json:"allowNegativeStock"`
		LabourCostEnabled    bool            `json:"labourCostEnabled"`
		BusinessProfile      string          `json:"businessProfile"`
		SeasonalityProfile   json.RawMessage `json:"seasonalityProfile"`
		OnboardingCompleted  bool            `json:"onboardingCompleted"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid settings")
		return
	}
	if in.WarehouseDisplayMode != "AGGREGATED" && in.WarehouseDisplayMode != "BY_WAREHOUSE" {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid warehouse display mode")
		return
	}
	switch in.CountCadence {
	case "DAILY", "WEEKLY", "BIWEEKLY", "MONTHLY", "NEVER":
	default:
		httpx.WriteError(w, http.StatusBadRequest, "Invalid count cadence")
		return
	}
	if len(in.SeasonalityProfile) == 0 {
		in.SeasonalityProfile = json.RawMessage(`{}`)
	}
	if !json.Valid(in.SeasonalityProfile) {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid seasonality profile")
		return
	}
	_, err := s.db.ExecContext(r.Context(), `INSERT INTO stock_settings (restaurant_id,warehouse_display_mode,count_cadence,allow_negative_stock,labour_cost_enabled,business_profile,seasonality_profile,onboarding_completed) VALUES (?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE warehouse_display_mode=VALUES(warehouse_display_mode),count_cadence=VALUES(count_cadence),allow_negative_stock=VALUES(allow_negative_stock),labour_cost_enabled=VALUES(labour_cost_enabled),business_profile=VALUES(business_profile),seasonality_profile=VALUES(seasonality_profile),onboarding_completed=VALUES(onboarding_completed)`, a.ActiveRestaurantID, in.WarehouseDisplayMode, in.CountCadence, stockBoolInt(in.AllowNegativeStock), stockBoolInt(in.LabourCostEnabled), stockNullableString(in.BusinessProfile), in.SeasonalityProfile, stockBoolInt(in.OnboardingCompleted))
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error saving stock settings")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleBOStockMovementCreate(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, 401, "Unauthorized")
		return
	}
	itemID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || itemID <= 0 {
		httpx.WriteError(w, 400, "Invalid item")
		return
	}
	var in struct {
		WarehouseID    int64   `json:"warehouseId"`
		Quantity       float64 `json:"quantity"`
		UnitID         int64   `json:"unitId"`
		Type           string  `json:"type"`
		Direction      string  `json:"direction"`
		WasteReason    string  `json:"wasteReason"`
		Note           string  `json:"note"`
		IdempotencyKey string  `json:"idempotencyKey"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || in.WarehouseID <= 0 || in.UnitID <= 0 || in.Quantity <= 0 || strings.TrimSpace(in.IdempotencyKey) == "" {
		httpx.WriteError(w, 400, "Invalid movement")
		return
	}
	requiredPermission := stockPermissionAdjust
	if strings.EqualFold(in.Type, "WASTE") {
		requiredPermission = stockPermissionWasteRecord
	}
	allowed, permissionErr := s.boStockPermissionAllowed(r.Context(), a, requiredPermission)
	if permissionErr != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error validating stock permission")
		return
	}
	if !allowed {
		httpx.WriteError(w, http.StatusForbidden, "Forbidden")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, 500, "Error applying movement")
		return
	}
	defer tx.Rollback()
	var factor float64
	var tracked int
	var deductionSource string
	err = tx.QueryRowContext(r.Context(), `SELECT u.factor_to_base,i.is_tracked,i.deduction_source FROM stock_item_units u JOIN stock_items i ON i.id=u.stock_item_id AND i.restaurant_id=u.restaurant_id WHERE u.id=? AND u.stock_item_id=? AND u.restaurant_id=? AND i.deleted_at IS NULL`, in.UnitID, itemID, a.ActiveRestaurantID).Scan(&factor, &tracked, &deductionSource)
	if err != nil {
		httpx.WriteError(w, 404, "Item unit not found")
		return
	}
	if tracked == 0 {
		httpx.WriteError(w, 409, "Item is not tracked")
		return
	}
	movementType := strings.ToUpper(strings.TrimSpace(in.Type))
	if movementType == "SALE" && deductionSource == "PRODUCTION" {
		httpx.WriteError(w, http.StatusConflict, "This item is deducted at production time")
		return
	}
	if movementType == "PRODUCTION_OUT" && deductionSource == "SALE" {
		httpx.WriteError(w, http.StatusConflict, "This item is deducted at sale time")
		return
	}
	var warehouseExists int
	if err = tx.QueryRowContext(r.Context(), `SELECT 1 FROM stock_warehouses WHERE id=? AND restaurant_id=? AND is_active=1 AND deleted_at IS NULL`, in.WarehouseID, a.ActiveRestaurantID).Scan(&warehouseExists); err != nil {
		httpx.WriteError(w, 404, "Warehouse not found")
		return
	}
	qtyBase, err := normalizeStockMovementQuantity(movementType, in.Quantity, factor)
	if movementType == "ADJUSTMENT" && strings.EqualFold(in.Direction, "ADD") {
		qtyBase = in.Quantity * factor
	}
	if movementType == "ADJUSTMENT" && in.Direction != "" && !strings.EqualFold(in.Direction, "ADD") && !strings.EqualFold(in.Direction, "SUBTRACT") {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid adjustment direction")
		return
	}
	if err != nil {
		httpx.WriteError(w, 400, err.Error())
		return
	}
	_, err = tx.ExecContext(r.Context(), `INSERT INTO stock_levels (restaurant_id,stock_item_id,warehouse_id,qty_base) VALUES (?,?,?,0) ON DUPLICATE KEY UPDATE stock_item_id=VALUES(stock_item_id)`, a.ActiveRestaurantID, itemID, in.WarehouseID)
	if err != nil {
		httpx.WriteError(w, 500, "Error applying movement")
		return
	}
	var current float64
	if err = tx.QueryRowContext(r.Context(), `SELECT qty_base FROM stock_levels WHERE restaurant_id=? AND stock_item_id=? AND warehouse_id=? FOR UPDATE`, a.ActiveRestaurantID, itemID, in.WarehouseID).Scan(&current); err != nil {
		httpx.WriteError(w, 500, "Error applying movement")
		return
	}
	var allowNegative int
	_ = tx.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT allow_negative_stock FROM stock_settings WHERE restaurant_id=?),1)`, a.ActiveRestaurantID).Scan(&allowNegative)
	if allowNegative == 0 && current+qtyBase < 0 {
		httpx.WriteError(w, 409, "Insufficient stock")
		return
	}
	if movementType == "WASTE" && strings.TrimSpace(in.WasteReason) == "" {
		httpx.WriteError(w, 400, "Waste reason is required")
		return
	}
	if strings.TrimSpace(in.WasteReason) != "" && !validStockWasteReason(in.WasteReason) {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid waste reason")
		return
	}
	res, err := tx.ExecContext(r.Context(), `INSERT INTO stock_movements (restaurant_id,stock_item_id,warehouse_id,qty_base,type,waste_reason,entered_qty,entered_unit_id,idempotency_key,note,actor_user_id) VALUES (?,?,?,?,?,?,?,?,?,?,?)`, a.ActiveRestaurantID, itemID, in.WarehouseID, qtyBase, movementType, stockNullableString(in.WasteReason), in.Quantity, in.UnitID, in.IdempotencyKey, stockNullableString(in.Note), a.User.ID)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			httpx.WriteJSON(w, 200, map[string]any{"success": true, "duplicate": true})
			return
		}
		httpx.WriteError(w, 400, "Movement could not be applied")
		return
	}
	_, err = tx.ExecContext(r.Context(), `UPDATE stock_levels SET qty_base=qty_base+?,version=version+1 WHERE restaurant_id=? AND stock_item_id=? AND warehouse_id=?`, qtyBase, a.ActiveRestaurantID, itemID, in.WarehouseID)
	if err != nil {
		httpx.WriteError(w, 500, "Error applying movement")
		return
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, 500, "Error applying movement")
		return
	}
	movementID, _ := res.LastInsertId()
	httpx.WriteJSON(w, 201, map[string]any{"success": true, "movementId": movementID, "quantityBase": current + qtyBase})
}

func (s *Server) handleBOStockSummary(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, 401, "Unauthorized")
		return
	}
	var tracked, belowPar, belowReorder, out, negative int
	var coverage sql.NullFloat64
	err := s.db.QueryRowContext(r.Context(), `SELECT COUNT(DISTINCT i.id),COUNT(DISTINCT CASE WHEN COALESCE(totals.par,0)>0 AND COALESCE(totals.qty,0)<totals.par THEN i.id END),COUNT(DISTINCT CASE WHEN COALESCE(totals.reorder_point,0)>0 AND COALESCE(totals.qty,0)<totals.reorder_point THEN i.id END),COUNT(DISTINCT CASE WHEN COALESCE(totals.qty,0)=0 THEN i.id END),COUNT(DISTINCT CASE WHEN COALESCE(totals.qty,0)<0 THEN i.id END),SUM(LEAST(GREATEST(COALESCE(totals.qty,0),0),COALESCE(totals.par,0)))/NULLIF(SUM(COALESCE(totals.par,0)),0)*100 FROM stock_items i LEFT JOIN (SELECT restaurant_id,stock_item_id,SUM(qty_base) qty,SUM(par_level_base) par,SUM(reorder_point_base) reorder_point FROM stock_levels WHERE restaurant_id=? GROUP BY restaurant_id,stock_item_id) totals ON totals.restaurant_id=i.restaurant_id AND totals.stock_item_id=i.id WHERE i.restaurant_id=? AND i.is_active=1 AND i.is_tracked=1 AND i.deleted_at IS NULL`, a.ActiveRestaurantID, a.ActiveRestaurantID).Scan(&tracked, &belowPar, &belowReorder, &out, &negative, &coverage)
	if err != nil {
		httpx.WriteError(w, 500, "Error loading stock summary")
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "itemsTracked": tracked, "belowPar": belowPar, "belowReorder": belowReorder, "outOfStock": out, "negative": negative, "coveragePct": coverage.Float64})
}

func stockBoolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
func stockNullableString(v string) any {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	return v
}
func stockQueryInt(r *http.Request, key string, def, min, max int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil {
		return def
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
func nullableFloat(v sql.NullFloat64) any {
	if !v.Valid {
		return nil
	}
	return v.Float64
}
func validStockWarehouseType(v string) bool {
	switch strings.ToUpper(strings.TrimSpace(v)) {
	case "KITCHEN", "BAR", "STORAGE", "COLD", "FREEZER", "CELLAR", "OTHER":
		return true
	}
	return false
}
func validStockItemKind(v string) bool {
	switch strings.ToUpper(strings.TrimSpace(v)) {
	case "RAW", "SEMI_FINISHED", "FINISHED", "CONSUMABLE":
		return true
	}
	return false
}
func validStockDeductionSource(v string) bool {
	switch strings.ToUpper(strings.TrimSpace(v)) {
	case "PRODUCTION", "SALE", "BOTH_MANUAL":
		return true
	}
	return false
}
func validStockWasteReason(v string) bool {
	switch strings.ToUpper(strings.TrimSpace(v)) {
	case "SPOILAGE", "BREAKAGE", "OVERPRODUCTION", "STAFF_MEAL", "CUSTOMER_RETURN", "PREP_LOSS", "THEFT", "OTHER":
		return true
	}
	return false
}
