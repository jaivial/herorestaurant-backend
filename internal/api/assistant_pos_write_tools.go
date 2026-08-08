package api

import (
	"context"
	"encoding/json"
	"strconv"
)

// --- POS writes (reuse backoffice_pos_cash.go handler, confirmation on) ---

// assistantPOSCashClosureCreate closes the active cash shift (or the shift
// given by shift_id) of the restaurant. Requires confirmation.
func (s *Server) assistantPOSCashClosureCreate(ctx context.Context, rid int, input json.RawMessage) (string, error) {
	var in struct {
		ShiftID           int64  `json:"shift_id"`
		TerminalKey       string `json:"terminal_key"`
		ClosureType       string `json:"closure_type"`
		CountedCashCents  *int64 `json:"counted_cash_cents"`
		Note              string `json:"note"`
		DiscrepancyReason string `json:"discrepancy_reason"`
		IdempotencyKey    string `json:"idempotency_key"`
	}
	_ = json.Unmarshal(input, &in)
	body := map[string]any{
		"terminalKey":       in.TerminalKey,
		"closureType":       in.ClosureType,
		"note":              in.Note,
		"discrepancyReason": in.DiscrepancyReason,
		"idempotencyKey":    in.IdempotencyKey,
	}
	if in.ShiftID > 0 {
		body["shiftId"] = in.ShiftID
	}
	if in.CountedCashCents != nil {
		body["countedCashCents"] = *in.CountedCashCents
	}
	return s.assistantConfirmedMutation(ctx, rid, "pos_cash_closure_create", s.handleBOPOSCashClosureCreate, input, assistantHandlerInput{
		Body: body,
	})
}

// assistantPOSTicketLineAdd adds a product line to an open POS ticket of the
// active restaurant. Requires confirmation.
func (s *Server) assistantPOSTicketLineAdd(ctx context.Context, rid int, input json.RawMessage) (string, error) {
	var in struct {
		TicketID               int64   `json:"ticket_id"`
		ProductID              int64   `json:"product_id"`
		Quantity               float64 `json:"quantity"`
		Notes                  string  `json:"notes"`
		IdempotencyKey         string  `json:"idempotency_key"`
		UnitPriceOverrideCents *int64  `json:"unit_price_override_cents"`
	}
	_ = json.Unmarshal(input, &in)
	body := map[string]any{
		"productId":      in.ProductID,
		"quantity":       in.Quantity,
		"notes":          in.Notes,
		"idempotencyKey": in.IdempotencyKey,
	}
	if in.UnitPriceOverrideCents != nil {
		body["unitPriceOverrideCents"] = *in.UnitPriceOverrideCents
	}
	return s.assistantConfirmedMutation(ctx, rid, "pos_ticket_line_add", s.handleBOPOSLineCreate, input, assistantHandlerInput{
		URLParam: map[string]string{"id": strconv.FormatInt(in.TicketID, 10)},
		Body:     body,
	})
}
