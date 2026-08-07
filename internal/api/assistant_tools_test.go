package api

import (
	"context"
	"encoding/json"
	"testing"
)

func TestAssistantToolDefsExposeCRUDAndAnalytics(t *testing.T) {
	defs := assistantToolDefs()
	want := map[string]bool{"restaurant_info": false, "bookings_summary": false, "restaurant_query": false, "create_booking": false, "update_booking": false, "delete_booking": false}
	for _, d := range defs {
		if _, ok := want[d.Name]; ok {
			want[d.Name] = true
		}
		if len(d.InputSchema) == 0 {
			t.Errorf("tool %s has empty schema", d.Name)
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("missing tool %s", name)
		}
	}
}

func TestAssistantBookingMutationRequiresConfirmation(t *testing.T) {
	s := &Server{}
	got, err := s.assistantBookingMutation(context.Background(), 1, "delete_booking", json.RawMessage(`{"booking_id":4,"confirmed":false}`))
	if err != nil {
		t.Fatal(err)
	}
	if got != `{"requires_confirmation":true}` {
		t.Fatalf("got %s", got)
	}
}
