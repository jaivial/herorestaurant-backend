package api

import (
	"context"
	"encoding/json"
	"strconv"
)

// --- Members writes (reuse backoffice_labour_cost.go handler, confirmation on) ---

// assistantMemberCompensationCreate records a compensation (salary period) for
// a member of the active restaurant. Requires confirmation.
func (s *Server) assistantMemberCompensationCreate(ctx context.Context, rid int, input json.RawMessage) (string, error) {
	var in struct {
		MemberID        int      `json:"member_id"`
		PayType         string   `json:"pay_type"`
		GrossAmount     float64  `json:"gross_amount"`
		MonthlyHours    *float64 `json:"monthly_hours"`
		EmployerCostPct float64  `json:"employer_cost_pct"`
		EffectiveFrom   string   `json:"effective_from"`
		EffectiveTo     *string  `json:"effective_to"`
		Notes           *string  `json:"notes"`
	}
	_ = json.Unmarshal(input, &in)
	body := map[string]any{
		"payType":         in.PayType,
		"grossAmount":     in.GrossAmount,
		"employerCostPct": in.EmployerCostPct,
		"effectiveFrom":   in.EffectiveFrom,
	}
	if in.MonthlyHours != nil {
		body["monthlyHours"] = *in.MonthlyHours
	}
	if in.EffectiveTo != nil {
		body["effectiveTo"] = *in.EffectiveTo
	}
	if in.Notes != nil {
		body["notes"] = *in.Notes
	}
	return s.assistantConfirmedMutation(ctx, rid, "member_compensation_create", s.handleBOMemberCompensationCreate, input, assistantHandlerInput{
		URLParam: map[string]string{"id": strconv.Itoa(in.MemberID)},
		Body:     body,
	})
}
