package api

import (
	"context"
	"encoding/json"
	"strconv"
)

// --- Stock writes (reuse backoffice_stock.go handlers, confirmation on) ---

// assistantStockMovementCreate registers a manual stock movement (adjustment or
// waste) for one item. Requires confirmation.
func (s *Server) assistantStockMovementCreate(ctx context.Context, rid int, input json.RawMessage) (string, error) {
	var in struct {
		ItemID         int64   `json:"item_id"`
		WarehouseID    int64   `json:"warehouse_id"`
		Quantity       float64 `json:"quantity"`
		UnitID         int64   `json:"unit_id"`
		Type           string  `json:"type"`
		Direction      string  `json:"direction"`
		WasteReason    string  `json:"waste_reason"`
		Note           string  `json:"note"`
		IdempotencyKey string  `json:"idempotency_key"`
	}
	_ = json.Unmarshal(input, &in)
	return s.assistantConfirmedMutation(ctx, rid, "stock_movement_create", s.handleBOStockMovementCreate, input, assistantHandlerInput{
		URLParam: map[string]string{"id": strconv.FormatInt(in.ItemID, 10)},
		Body: map[string]any{
			"warehouseId":    in.WarehouseID,
			"quantity":       in.Quantity,
			"unitId":         in.UnitID,
			"type":           in.Type,
			"direction":      in.Direction,
			"wasteReason":    in.WasteReason,
			"note":           in.Note,
			"idempotencyKey": in.IdempotencyKey,
		},
	})
}

// assistantStockTransferCreate moves stock between two warehouses of the active
// restaurant. Requires confirmation.
func (s *Server) assistantStockTransferCreate(ctx context.Context, rid int, input json.RawMessage) (string, error) {
	var in struct {
		ItemID          int64   `json:"item_id"`
		FromWarehouseID int64   `json:"from_warehouse_id"`
		ToWarehouseID   int64   `json:"to_warehouse_id"`
		Quantity        float64 `json:"quantity"`
		UnitID          int64   `json:"unit_id"`
		IdempotencyKey  string  `json:"idempotency_key"`
		Note            string  `json:"note"`
	}
	_ = json.Unmarshal(input, &in)
	return s.assistantConfirmedMutation(ctx, rid, "stock_transfer_create", s.handleBOStockTransferCreate, input, assistantHandlerInput{
		Body: map[string]any{
			"itemId":          in.ItemID,
			"fromWarehouseId": in.FromWarehouseID,
			"toWarehouseId":   in.ToWarehouseID,
			"quantity":        in.Quantity,
			"unitId":          in.UnitID,
			"idempotencyKey":  in.IdempotencyKey,
			"note":            in.Note,
		},
	})
}
