package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// assistantRequireConfirmation issues a short-lived confirmation token for a
// mutation, returning the requires_confirmation tool response with the token.
// A store is created lazily so the flow works in any configuration.
func (s *Server) assistantRequireConfirmation(rid int, tool string, input json.RawMessage) (string, error) {
	if s.confirmationStore == nil {
		s.confirmationStore = newConfirmationStore(nil)
	}
	tok, err := s.confirmationStore.Issue("", fmt.Sprint(rid), tool, confirmationArguments(input), "", 2*time.Minute)
	if err != nil {
		return "", err
	}
	return botJSON(map[string]any{"requires_confirmation": true, "confirmation_token": tok, "expires_in_seconds": 120}), nil
}

// assistantConsumeConfirmation validates and consumes the one-shot token bound
// to the given mutation (user, restaurant, tool and canonical arguments). A
// missing, expired, used or mismatched token is rejected.
func (s *Server) assistantConsumeConfirmation(token string, rid int, tool string, input json.RawMessage) error {
	if s.confirmationStore == nil || strings.TrimSpace(token) == "" {
		return fmt.Errorf("confirmation_token requerido")
	}
	return s.confirmationStore.Consume(token, "", fmt.Sprint(rid), tool, confirmationArguments(input), "")
}

// assistantConfirmedMutation is the shared confirmation+execute flow for write
// tools that reuse a backoffice domain handler: confirmed=false issues a token;
// confirmed=true consumes it and forwards the payload to the handler.
func (s *Server) assistantConfirmedMutation(ctx context.Context, rid int, tool string, handler http.HandlerFunc, input json.RawMessage, in assistantHandlerInput) (string, error) {
	var c struct {
		Confirmed         bool   `json:"confirmed"`
		ConfirmationToken string `json:"confirmation_token"`
	}
	_ = json.Unmarshal(input, &c)
	if !c.Confirmed {
		return s.assistantRequireConfirmation(rid, tool, input)
	}
	if err := s.assistantConsumeConfirmation(c.ConfirmationToken, rid, tool, input); err != nil {
		return "", err
	}
	if in.Method == "" {
		in.Method = "POST"
	}
	body, code, err := s.assistantCallHandler(ctx, handler, in)
	if err != nil {
		return "", err
	}
	if code >= 400 {
		return "", assistantHandlerError(tool, body, code)
	}
	return botHandlerResponse(tool, body)
}
