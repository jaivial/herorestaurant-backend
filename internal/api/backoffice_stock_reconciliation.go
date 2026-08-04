package api

import (
	"net/http"

	"preactvillacarmen/internal/httpx"
)

type stockReconciliationDiff struct {
	ItemID      int64   `json:"itemId"`
	WarehouseID int64   `json:"warehouseId"`
	LevelQty    float64 `json:"levelQuantityBase"`
	LedgerQty   float64 `json:"ledgerQuantityBase"`
	Difference  float64 `json:"differenceBase"`
}

func (s *Server) stockReconciliationDiffs(r *http.Request) ([]stockReconciliationDiff, error) {
	a, _ := boAuthFromContext(r.Context())
	rows, err := s.db.QueryContext(r.Context(), `SELECT keys_.stock_item_id,keys_.warehouse_id,COALESCE(l.qty_base,0),COALESCE(m.qty_base,0),COALESCE(l.qty_base,0)-COALESCE(m.qty_base,0) FROM (SELECT stock_item_id,warehouse_id FROM stock_levels WHERE restaurant_id=? UNION SELECT stock_item_id,warehouse_id FROM stock_movements WHERE restaurant_id=?) keys_ LEFT JOIN stock_levels l ON l.restaurant_id=? AND l.stock_item_id=keys_.stock_item_id AND l.warehouse_id=keys_.warehouse_id LEFT JOIN (SELECT stock_item_id,warehouse_id,SUM(qty_base) qty_base FROM stock_movements WHERE restaurant_id=? GROUP BY stock_item_id,warehouse_id) m ON m.stock_item_id=keys_.stock_item_id AND m.warehouse_id=keys_.warehouse_id WHERE ABS(COALESCE(l.qty_base,0)-COALESCE(m.qty_base,0))>0.0001 ORDER BY keys_.stock_item_id,keys_.warehouse_id`, a.ActiveRestaurantID, a.ActiveRestaurantID, a.ActiveRestaurantID, a.ActiveRestaurantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	diffs := []stockReconciliationDiff{}
	for rows.Next() {
		var diff stockReconciliationDiff
		if err := rows.Scan(&diff.ItemID, &diff.WarehouseID, &diff.LevelQty, &diff.LedgerQty, &diff.Difference); err != nil {
			return nil, err
		}
		diffs = append(diffs, diff)
	}
	return diffs, rows.Err()
}

func (s *Server) handleBOStockReconciliationGet(w http.ResponseWriter, r *http.Request) {
	diffs, err := s.stockReconciliationDiffs(r)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error reconciling stock")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "consistent": len(diffs) == 0, "differences": diffs})
}

func (s *Server) handleBOStockReconciliationRebuild(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error rebuilding stock")
		return
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(r.Context(), `UPDATE stock_levels l LEFT JOIN (SELECT stock_item_id,warehouse_id,SUM(qty_base) qty_base FROM stock_movements WHERE restaurant_id=? GROUP BY stock_item_id,warehouse_id) m ON m.stock_item_id=l.stock_item_id AND m.warehouse_id=l.warehouse_id SET l.qty_base=COALESCE(m.qty_base,0),l.version=l.version+1 WHERE l.restaurant_id=?`, a.ActiveRestaurantID, a.ActiveRestaurantID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error rebuilding stock")
		return
	}
	if _, err = tx.ExecContext(r.Context(), `INSERT INTO stock_levels (restaurant_id,stock_item_id,warehouse_id,qty_base) SELECT ?,m.stock_item_id,m.warehouse_id,SUM(m.qty_base) FROM stock_movements m LEFT JOIN stock_levels l ON l.restaurant_id=m.restaurant_id AND l.stock_item_id=m.stock_item_id AND l.warehouse_id=m.warehouse_id WHERE m.restaurant_id=? AND l.stock_item_id IS NULL GROUP BY m.stock_item_id,m.warehouse_id`, a.ActiveRestaurantID, a.ActiveRestaurantID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error rebuilding stock")
		return
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error rebuilding stock")
		return
	}
	diffs, err := s.stockReconciliationDiffs(r)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error verifying stock")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "consistent": len(diffs) == 0, "differences": diffs})
}
