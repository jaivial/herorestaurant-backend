package api

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func outboxRowState(t *testing.T, db *sql.DB, id int64) (status string, attempts int, nextAttempt sql.NullTime, errText sql.NullString) {
	t.Helper()
	if err := db.QueryRow(`SELECT status, attempts, next_attempt_at, error FROM message_deliveries WHERE id = ?`, id).
		Scan(&status, &attempts, &nextAttempt, &errText); err != nil {
		t.Fatalf("read row %d: %v", id, err)
	}
	return
}

func lastOutboxID(t *testing.T, db *sql.DB, deliveryKey string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(`SELECT id FROM message_deliveries WHERE delivery_key = ?`, deliveryKey).Scan(&id); err != nil {
		t.Fatalf("find row %q: %v", deliveryKey, err)
	}
	return id
}

// The delivery key is what stops a retried request from producing a duplicate
// WhatsApp, so a second enqueue of the same logical message must be a no-op.
func TestEnqueueWhatsAppDelivery_IsIdempotent_DB(t *testing.T) {
	restaurantID, cleanup := provisionInstance(t, "evolution")
	defer cleanup()

	db := testDB(t)
	defer db.Close()
	s := newTestServer(t, db)
	ctx := context.Background()

	key := "test_outbox:" + time.Now().Format("150405.000000")
	defer func() { _, _ = db.Exec(`DELETE FROM message_deliveries WHERE delivery_key = ?`, key) }()

	payload := whatsappOutboxPayload{Text: "hola", Choices: []string{"Ver|https://example.com"}}
	for i := 0; i < 3; i++ {
		if err := s.enqueueWhatsAppDelivery(ctx, restaurantID, "test_event", key, "34600111222", payload, errors.New("boom")); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM message_deliveries WHERE delivery_key = ?`, key).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("rows for delivery key = %d, want 1", count)
	}
}

// A restaurant whose gateway is unreachable must leave the row pending with a
// future retry, not silently lose the message and not spin immediately.
func TestWhatsAppOutbox_FailedSendReschedules_DB(t *testing.T) {
	restaurantID, cleanup := provisionInstance(t, "evolution")
	defer cleanup()

	db := testDB(t)
	defer db.Close()
	s := newTestServer(t, db)
	ctx := context.Background()

	key := "test_outbox_retry:" + time.Now().Format("150405.000000")
	defer func() { _, _ = db.Exec(`DELETE FROM message_deliveries WHERE delivery_key = ?`, key) }()

	// provisionInstance points at a non-resolvable host, so the send must fail.
	if err := s.enqueueWhatsAppDelivery(ctx, restaurantID, "test_event", key, "34600111222",
		whatsappOutboxPayload{Text: "hola"}, nil); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	id := lastOutboxID(t, db, key)

	sent, err := s.runWhatsAppOutboxOnce(ctx)
	if err != nil {
		t.Fatalf("run outbox: %v", err)
	}
	if sent != 0 {
		t.Errorf("sent = %d, want 0 (gateway host is unreachable)", sent)
	}

	status, attempts, nextAttempt, errText := outboxRowState(t, db, id)
	if status != "pending" {
		t.Errorf("status = %q, want pending", status)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1", attempts)
	}
	if !nextAttempt.Valid || !nextAttempt.Time.After(time.Now().Add(30*time.Second)) {
		t.Errorf("next_attempt_at = %v, want a backoff in the future", nextAttempt)
	}
	if !errText.Valid || errText.String == "" {
		t.Error("error column should record why the send failed")
	}
}

// Once the attempt budget is spent the row must stop being claimed, otherwise a
// permanently bad recipient is retried forever.
func TestWhatsAppOutbox_GivesUpAfterMaxAttempts_DB(t *testing.T) {
	restaurantID, cleanup := provisionInstance(t, "evolution")
	defer cleanup()

	db := testDB(t)
	defer db.Close()
	s := newTestServer(t, db)
	ctx := context.Background()

	key := "test_outbox_giveup:" + time.Now().Format("150405.000000")
	defer func() { _, _ = db.Exec(`DELETE FROM message_deliveries WHERE delivery_key = ?`, key) }()

	if err := s.enqueueWhatsAppDelivery(ctx, restaurantID, "test_event", key, "34600111222",
		whatsappOutboxPayload{Text: "hola"}, nil); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	id := lastOutboxID(t, db, key)

	// Park the row one attempt short of the budget and make it immediately due.
	if _, err := db.Exec(`UPDATE message_deliveries SET attempts = ?, next_attempt_at = NOW() WHERE id = ?`,
		whatsappOutboxMaxAttempts-1, id); err != nil {
		t.Fatal(err)
	}

	if _, err := s.runWhatsAppOutboxOnce(ctx); err != nil {
		t.Fatalf("run outbox: %v", err)
	}

	status, attempts, _, _ := outboxRowState(t, db, id)
	if status != "failed" {
		t.Errorf("status = %q, want failed after %d attempts", status, attempts)
	}

	// A failed row must not be picked up again.
	rows, err := s.claimWhatsAppDeliveries(ctx, "probe-token", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.ID == id {
			t.Error("a failed delivery was claimed again")
		}
	}
}

// Two workers scanning at once must never both claim the same row.
func TestWhatsAppOutbox_ClaimIsExclusive_DB(t *testing.T) {
	restaurantID, cleanup := provisionInstance(t, "evolution")
	defer cleanup()

	db := testDB(t)
	defer db.Close()
	s := newTestServer(t, db)
	ctx := context.Background()

	key := "test_outbox_claim:" + time.Now().Format("150405.000000")
	defer func() { _, _ = db.Exec(`DELETE FROM message_deliveries WHERE delivery_key = ?`, key) }()

	if err := s.enqueueWhatsAppDelivery(ctx, restaurantID, "test_event", key, "34600111222",
		whatsappOutboxPayload{Text: "hola"}, nil); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	id := lastOutboxID(t, db, key)

	first, err := s.claimWhatsAppDeliveries(ctx, "worker-a", 10)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.claimWhatsAppDeliveries(ctx, "worker-b", 10)
	if err != nil {
		t.Fatal(err)
	}

	got := func(rows []whatsappOutboxRow) bool {
		for _, r := range rows {
			if r.ID == id {
				return true
			}
		}
		return false
	}
	if !got(first) {
		t.Fatal("first worker should have claimed the row")
	}
	if got(second) {
		t.Error("second worker claimed a row already locked by the first")
	}
}
