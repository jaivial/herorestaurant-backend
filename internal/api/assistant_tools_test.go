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
	got, err := s.assistantDeleteBooking(context.Background(), 1, json.RawMessage(`{"booking_id":4,"confirmed":false}`))
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(got), &v); err != nil {
		t.Fatal(err)
	}
	if v["confirmation_token"] == nil {
		t.Fatalf("missing confirmation token: %s", got)
	}
}

func TestAssistantBookingListToolRegistered(t *testing.T) {
	defs := assistantToolDefs()
	names := map[string]bool{}
	for _, d := range defs {
		names[d.Name] = true
	}
	for _, want := range []string{"bookings_list", "bookings_summary", "restaurant_query"} {
		if !names[want] {
			t.Errorf("missing tool %s", want)
		}
	}
	if assistantToolWrites("bookings_list") {
		t.Error("bookings_list must be a read-only tool")
	}
}
