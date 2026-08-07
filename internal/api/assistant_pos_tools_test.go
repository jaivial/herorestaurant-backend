package api

import (
	"context"
	"encoding/json"
	"testing"
)

func TestAssistantPOSMutationConfirmation(t *testing.T) {
	s := &Server{}
	out, e := s.assistantPOSMutation(context.Background(), 1, "pos_visit_create", json.RawMessage(`{"channel":"DINE_IN","covers":2,"confirmed":false}`))
	if e != nil {
		t.Fatal(e)
	}
	var v map[string]any
	if json.Unmarshal([]byte(out), &v) != nil || v["confirmation_token"] == nil {
		t.Fatalf("missing token: %s", out)
	}
}
func TestAssistantPOSMutationRejectsMissingToken(t *testing.T) {
	s := &Server{confirmationStore: newConfirmationStore()}
	if _, e := s.assistantPOSMutation(context.Background(), 1, "pos_visit_create", json.RawMessage(`{"channel":"DINE_IN","covers":2,"confirmed":true}`)); e == nil {
		t.Fatal("accepted missing token")
	}
}
