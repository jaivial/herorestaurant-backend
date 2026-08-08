package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// --- WhatsApp config write (reuse whatsapp_bot_admin.go handler, confirmation
// on) ---

// assistantWhatsappBotConfigUpdate upserts the WhatsApp bot config of the
// active restaurant. Requires confirmation.
func (s *Server) assistantWhatsappBotConfigUpdate(ctx context.Context, rid int, input json.RawMessage) (string, error) {
	var in struct {
		Model              string `json:"model"`
		LanguageDefault    string `json:"language_default"`
		Tone               string `json:"tone"`
		GreetingStyle      string `json:"greeting_style"`
		DisableAttachments bool   `json:"disable_attachments"`
		CustomInstructions string `json:"custom_instructions"`
		ContactPhone       string `json:"contact_phone"`
		Rules              string `json:"rules"`
	}
	_ = json.Unmarshal(input, &in)
	return s.assistantConfirmedMutation(ctx, rid, "whatsapp_bot_config_update", s.handleBOBotConfigPut, input, assistantHandlerInput{
		Body: map[string]any{
			"model":               in.Model,
			"language_default":    in.LanguageDefault,
			"tone":                in.Tone,
			"greeting_style":      in.GreetingStyle,
			"disable_attachments": in.DisableAttachments,
			"custom_instructions": in.CustomInstructions,
			"contact_phone":       in.ContactPhone,
			"rules":               in.Rules,
		},
	})
}

// --- Fichaje self-service (reuse the admin punch handler with the caller's
// clock member, so no DNI/password is needed) ---

// assistantFichajeClockMember resolves the authenticated user's clock member.
func (s *Server) assistantFichajeClockMember(ctx context.Context, rid int) (int, error) {
	auth, ok := boAuthFromContext(ctx)
	if !ok {
		return 0, fmt.Errorf("autenticación requerida")
	}
	m, err := s.getBOClockMemberForUser(ctx, rid, auth.User.ID, "")
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("no hay miembro de fichaje asociado a tu cuenta")
		}
		return 0, err
	}
	return m.ID, nil
}

func (s *Server) assistantFichajeStart(ctx context.Context, rid int, input json.RawMessage) (string, error) {
	memberID, err := s.assistantFichajeClockMember(ctx, rid)
	if err != nil {
		return "", err
	}
	return s.assistantConfirmedMutation(ctx, rid, "fichaje_start", s.handleBOFichajeAdminStart, input, assistantHandlerInput{
		Body: map[string]any{"memberId": memberID},
	})
}

func (s *Server) assistantFichajeStop(ctx context.Context, rid int, input json.RawMessage) (string, error) {
	memberID, err := s.assistantFichajeClockMember(ctx, rid)
	if err != nil {
		return "", err
	}
	return s.assistantConfirmedMutation(ctx, rid, "fichaje_stop", s.handleBOFichajeAdminStop, input, assistantHandlerInput{
		Body: map[string]any{"memberId": memberID},
	})
}
