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

// Re-arm on reconnect: a `failed` row whose retries were exhausted during the
// outage must come back to `pending` the moment the instance reconnects, and
// any pending rows whose backoff is still in the future must be brought to
// the head of the queue immediately.
func TestTriggerWhatsAppQueueOnReconnect_ReArmsFailedAndFlushesPending_DB(t *testing.T) {
	restaurantID, cleanup := provisionInstance(t, "evolution")
	defer cleanup()

	db := testDB(t)
	defer db.Close()
	s := newTestServer(t, db)
	ctx := context.Background()

	keyFailed := "test_outbox_reconnect_failed:" + time.Now().Format("150405.000000000")
	keyPending := "test_outbox_reconnect_pending:" + time.Now().Format("150405.000000000")
	keyPendingFar := "test_outbox_reconnect_far:" + time.Now().Format("150405.000000000")
	defer func() {
		_, _ = db.Exec(`DELETE FROM message_deliveries WHERE delivery_key IN (?,?,?)`,
			keyFailed, keyPending, keyPendingFar)
	}()

	// failed row: stuck at 6 attempts with a stale error.
	if _, err := db.Exec(`
		INSERT INTO message_deliveries
			(restaurant_id, channel, event, delivery_key, recipient, payload_json,
			 status, attempts, next_attempt_at, error)
		VALUES (?, 'whatsapp', 'test_event', ?, '34600111222', '{}',
		        'failed', 6, NULL, 'evolution http 500')
	`, restaurantID, keyFailed); err != nil {
		t.Fatalf("seed failed row: %v", err)
	}

	// pending row whose backoff hasn't elapsed (5 min in the future).
	if _, err := db.Exec(`
		INSERT INTO message_deliveries
			(restaurant_id, channel, event, delivery_key, recipient, payload_json,
			 status, attempts, next_attempt_at, error)
		VALUES (?, 'whatsapp', 'test_event', ?, '34600333333', '{}',
		        'pending', 2, NOW() + INTERVAL 5 MINUTE, NULL)
	`, restaurantID, keyPending); err != nil {
		t.Fatalf("seed pending row: %v", err)
	}

	// pending row already due (control: must remain pending).
	if _, err := db.Exec(`
		INSERT INTO message_deliveries
			(restaurant_id, channel, event, delivery_key, recipient, payload_json,
			 status, attempts, next_attempt_at, error)
		VALUES (?, 'whatsapp', 'test_event', ?, '34600444444', '{}',
		        'pending', 1, NOW(), NULL)
	`, restaurantID, keyPendingFar); err != nil {
		t.Fatalf("seed due pending row: %v", err)
	}

	rearmed, err := s.triggerWhatsAppQueueOnReconnect(ctx, restaurantID)
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	if rearmed < 3 {
		t.Errorf("rearmed=%d, want at least 3 (failed + 2 pending)", rearmed)
	}

	// failed -> pending, attempts reset.
	if status, attempts, _, errText := outboxRowState(t, db, lastOutboxID(t, db, keyFailed)); status != "pending" || attempts != 0 || errText.Valid {
		t.Errorf("failed row after reconnect: status=%s attempts=%d errText.Valid=%v", status, attempts, errText.Valid)
	}

	// pending whose next_attempt_at was in the future -> NOW().
	var futureNext sql.NullTime
	if err := db.QueryRow(`SELECT next_attempt_at FROM message_deliveries WHERE delivery_key = ?`, keyPending).Scan(&futureNext); err != nil {
		t.Fatal(err)
	}
	if !futureNext.Valid {
		t.Fatalf("next_attempt_at was cleared for the pending row: %+v", futureNext)
	}
	if time.Until(futureNext.Time) > 5*time.Second {
		t.Errorf("pending backoff not flushed: next_attempt_at = %v (should be ~now)", futureNext.Time)
	}
}

// Coalescing: a second trigger within the debounce window must not duplicate
// the SQL re-arms (the notify ping is still emitted so a follow-up tick can
// observe any rows added between the two calls).
func TestTriggerWhatsAppQueueOnReconnect_Debounces_DB(t *testing.T) {
	restaurantID, cleanup := provisionInstance(t, "evolution")
	defer cleanup()

	db := testDB(t)
	defer db.Close()
	s := newTestServer(t, db)
	ctx := context.Background()

	key := "test_outbox_reconnect_debounce:" + time.Now().Format("150405.000000000")
	defer func() { _, _ = db.Exec(`DELETE FROM message_deliveries WHERE delivery_key = ?`, key) }()

	if _, err := db.Exec(`
		INSERT INTO message_deliveries
			(restaurant_id, channel, event, delivery_key, recipient, payload_json,
			 status, attempts, next_attempt_at, error)
		VALUES (?, 'whatsapp', 'test_event', ?, '34600555555', '{}',
		        'failed', 6, NULL, 'evolution http 500')
	`, restaurantID, key); err != nil {
		t.Fatal(err)
	}

	// First call: should re-arm.
	first, err := s.triggerWhatsAppQueueOnReconnect(ctx, restaurantID)
	if err != nil {
		t.Fatal(err)
	}
	if first < 1 {
		t.Errorf("first trigger rearmed %d rows, want >=1", first)
	}

	// Mark it failed again to count whether the second call re-armed.
	if _, err := db.Exec(`UPDATE message_deliveries SET status='failed', attempts=6, next_attempt_at=NULL WHERE delivery_key=?`, key); err != nil {
		t.Fatal(err)
	}

	// Second call within the debounce window: should NOT re-arm but must still ping.
	second, err := s.triggerWhatsAppQueueOnReconnect(ctx, restaurantID)
	if err != nil {
		t.Fatal(err)
	}
	if second != 0 {
		t.Errorf("second trigger rearmed %d rows, want 0 (debounced)", second)
	}

	if status, _, _, _ := outboxRowState(t, db, lastOutboxID(t, db, key)); status != "failed" {
		t.Errorf("row was re-armed inside debounce window: status=%s", status)
	}
}
