package api

import (
	"strings"
	"testing"
)

// The PG unique index of bot_inbound_queue is an expression index
// (restaurant_id, app.norm_ascii(message_id::text)); ON CONFLICT inference
// only matches it when the target repeats that expression (else 42P10).
func TestBotInboundEnqueuePGConflictTargetMatchesExpressionIndex(t *testing.T) {
	want := "ON CONFLICT (restaurant_id, app.norm_ascii(message_id::text)) DO NOTHING"
	if !strings.HasSuffix(botInboundEnqueuePG, want) {
		t.Fatalf("PG enqueue must end with %q, got %q", want, botInboundEnqueuePG)
	}
	if strings.Contains(botInboundEnqueuePG, "ON CONFLICT (restaurant_id, message_id)") {
		t.Fatalf("raw (restaurant_id, message_id) target cannot infer the expression index")
	}
	if n := strings.Count(botInboundEnqueuePG, "?"); n != 4 {
		t.Fatalf("placeholders = %d, want 4", n)
	}
}
