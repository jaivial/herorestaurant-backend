package api

import (
	"context"
	"encoding/json"
	"strconv"
)

// --- Stock reads (reuse backoffice_stock.go handlers) ---

func (s *Server) assistantStockWarehousesList(ctx context.Context, _ int, _ json.RawMessage) (string, error) {
	body, code, err := s.assistantCallHandler(ctx, s.handleBOStockWarehousesList, assistantHandlerInput{})
	if err != nil {
		return "", err
	}
	if code >= 400 {
		return "", assistantHandlerError("stock_warehouses_list", body, code)
	}
	return botHandlerResponse("stock_warehouses_list", body)
}

func (s *Server) assistantStockCategoriesList(ctx context.Context, _ int, _ json.RawMessage) (string, error) {
	body, code, err := s.assistantCallHandler(ctx, s.handleBOStockCategoriesList, assistantHandlerInput{})
	if err != nil {
		return "", err
	}
	if code >= 400 {
		return "", assistantHandlerError("stock_categories_list", body, code)
	}
	return botHandlerResponse("stock_categories_list", body)
}

func (s *Server) assistantStockItemsList(ctx context.Context, _ int, input json.RawMessage) (string, error) {
	var in struct {
		Search      string `json:"search"`
		Kind        string `json:"kind"`
		CategoryID  int64  `json:"category_id"`
		WarehouseID int64  `json:"warehouse_id"`
		Page        int    `json:"page"`
		PageSize    int    `json:"page_size"`
		Sort        string `json:"sort"`
	}
	_ = json.Unmarshal(input, &in)
	q := map[string]string{}
	if in.Search != "" {
		q["q"] = in.Search
	}
	if in.Kind != "" {
		q["kind"] = in.Kind
	}
	if in.CategoryID > 0 {
		q["categoryId"] = strconv.FormatInt(in.CategoryID, 10)
	}
	if in.WarehouseID > 0 {
		q["warehouseId"] = strconv.FormatInt(in.WarehouseID, 10)
	}
	if in.Page > 0 {
		q["page"] = strconv.Itoa(in.Page)
	}
	if in.PageSize > 0 {
		q["pageSize"] = strconv.Itoa(in.PageSize)
	}
	if in.Sort != "" {
		q["sort"] = in.Sort
	}
	body, code, err := s.assistantCallHandler(ctx, s.handleBOStockItemsList, assistantHandlerInput{Query: q})
	if err != nil {
		return "", err
	}
	if code >= 400 {
		return "", assistantHandlerError("stock_items_list", body, code)
	}
	return botHandlerResponse("stock_items_list", body)
}

func (s *Server) assistantStockItemMovementsList(ctx context.Context, _ int, input json.RawMessage) (string, error) {
	var in struct {
		ID       int64 `json:"id"`
		Page     int   `json:"page"`
		PageSize int   `json:"page_size"`
	}
	_ = json.Unmarshal(input, &in)
	q := map[string]string{}
	if in.Page > 0 {
		q["page"] = strconv.Itoa(in.Page)
	}
	if in.PageSize > 0 {
		q["pageSize"] = strconv.Itoa(in.PageSize)
	}
	body, code, err := s.assistantCallHandler(ctx, s.handleBOStockItemMovementsList, assistantHandlerInput{
		Query:    q,
		URLParam: map[string]string{"id": strconv.FormatInt(in.ID, 10)},
	})
	if err != nil {
		return "", err
	}
	if code >= 400 {
		return "", assistantHandlerError("stock_item_movements_list", body, code)
	}
	return botHandlerResponse("stock_item_movements_list", body)
}

func (s *Server) assistantStockSummary(ctx context.Context, _ int, _ json.RawMessage) (string, error) {
	body, code, err := s.assistantCallHandler(ctx, s.handleBOStockSummary, assistantHandlerInput{})
	if err != nil {
		return "", err
	}
	if code >= 400 {
		return "", assistantHandlerError("stock_summary", body, code)
	}
	return botHandlerResponse("stock_summary", body)
}
