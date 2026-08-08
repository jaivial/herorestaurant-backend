package api

import (
	"context"
	"encoding/json"
	"strconv"
)

// --- Menús typed reads (reuse backoffice_group_menus_v2.go handlers) ---

func (s *Server) assistantMenusList(ctx context.Context, _ int, input json.RawMessage) (string, error) {
	var in struct {
		IncludeDrafts bool `json:"include_drafts"`
	}
	_ = json.Unmarshal(input, &in)
	q := map[string]string{}
	if in.IncludeDrafts {
		q["includeDrafts"] = "1"
	}
	body, code, err := s.assistantCallHandler(ctx, s.handleBOGroupMenusV2List, assistantHandlerInput{Query: q})
	if err != nil {
		return "", err
	}
	if code >= 400 {
		return "", assistantHandlerError("menus_list", body, code)
	}
	return botHandlerResponse("menus_list", body)
}

func (s *Server) assistantMenuGet(ctx context.Context, _ int, input json.RawMessage) (string, error) {
	var in struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(input, &in)
	body, code, err := s.assistantCallHandler(ctx, s.handleBOGroupMenusV2Get, assistantHandlerInput{
		URLParam: map[string]string{"id": strconv.FormatInt(in.ID, 10)},
	})
	if err != nil {
		return "", err
	}
	if code >= 400 {
		return "", assistantHandlerError("menu_get", body, code)
	}
	return botHandlerResponse("menu_get", body)
}

func (s *Server) assistantMenuSectionsGet(ctx context.Context, _ int, input json.RawMessage) (string, error) {
	var in struct {
		ID        int64 `json:"id"`
		SectionID int64 `json:"section_id"`
	}
	_ = json.Unmarshal(input, &in)
	body, code, err := s.assistantCallHandler(ctx, s.handleBOGroupMenusV2GetSectionDishes, assistantHandlerInput{
		URLParam: map[string]string{"id": strconv.FormatInt(in.ID, 10), "sectionId": strconv.FormatInt(in.SectionID, 10)},
	})
	if err != nil {
		return "", err
	}
	if code >= 400 {
		return "", assistantHandlerError("menu_sections_get", body, code)
	}
	return botHandlerResponse("menu_sections_get", body)
}

// assistantMenuToggleActive flips the active flag of a menu. Requires
// confirmation.
func (s *Server) assistantMenuToggleActive(ctx context.Context, rid int, input json.RawMessage) (string, error) {
	var in struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(input, &in)
	return s.assistantConfirmedMutation(ctx, rid, "menu_toggle_active", s.handleBOGroupMenusV2ToggleActive, input, assistantHandlerInput{
		URLParam: map[string]string{"id": strconv.FormatInt(in.ID, 10)},
	})
}
