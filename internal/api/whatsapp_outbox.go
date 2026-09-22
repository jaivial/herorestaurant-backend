package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

// The WhatsApp outbox turns a lost message into a delayed one. Senders attempt
// delivery inline (so the caller still learns about immediate failures) and
// enqueue on error; this worker drains message_deliveries until the provider
// accepts the message or the attempt budget runs out.

const (
	whatsappOutboxMaxAttempts = 6
	whatsappOutboxBatchSize   = 20
	whatsappOutboxScanEvery   = 30 * time.Second
	// A row locked longer than this is assumed orphaned by a crashed process.
	whatsappOutboxLockTTL = 5 * time.Minute
)

// whatsappOutboxPayload is the provider-neutral body of a queued message.
// Choices follow the shared "label|url" convention; empty means a plain text.
type whatsappOutboxPayload struct {
	Text    string   `json:"text"`
	Choices []string `json:"choices,omitempty"`
}

type whatsappOutboxRow struct {
	ID           int64
	RestaurantID int
	Recipient    string
	Event        string
	Attempts     int
	Payload      whatsappOutboxPayload
}

// whatsappOutboxBackoff spaces retries out as attempts accumulate, capped so a
// long provider outage still gets hourly retries rather than giving up early.
func whatsappOutboxBackoff(attempts int) time.Duration {
	switch {
	case attempts <= 1:
		return time.Minute
	case attempts == 2:
		return 5 * time.Minute
	case attempts == 3:
		return 15 * time.Minute
	default:
		return time.Hour
	}
}

// enqueueWhatsAppDelivery records a message for later retry. deliveryKey makes
// the insert idempotent, so re-enqueueing the same logical message (a retried
// request, a second process) never produces a duplicate WhatsApp.
func (s *Server) enqueueWhatsAppDelivery(ctx context.Context, restaurantID int, event, deliveryKey, recipient string, payload whatsappOutboxPayload, cause error) error {
	if restaurantID <= 0 || strings.TrimSpace(recipient) == "" || strings.TrimSpace(payload.Text) == "" {
		return nil
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	var causeText any
	if cause != nil {
		causeText = truncate(cause.Error(), 500)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT IGNORE INTO message_deliveries
			(restaurant_id, channel, event, delivery_key, recipient, payload_json, status, attempts, next_attempt_at, error)
		VALUES (?, 'whatsapp', ?, ?, ?, ?, 'pending', 0, NOW(), ?)
	`, restaurantID, event, nullIfEmpty(deliveryKey), recipient, string(raw), causeText)
	if err != nil && isSQLSchemaError(err) {
		return nil
	}
	return err
}

// claimWhatsAppDeliveries locks a batch of due rows for this process. The lock
// token is unique per batch so a concurrent worker cannot read our rows.
func (s *Server) claimWhatsAppDeliveries(ctx context.Context, lockToken string, limit int) ([]whatsappOutboxRow, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE message_deliveries
		SET locked_at = NOW(), locked_by = ?, attempts = attempts + 1
		WHERE channel = 'whatsapp'
		  AND status = 'pending'
		  AND (next_attempt_at IS NULL OR next_attempt_at <= NOW())
		  AND (locked_at IS NULL OR locked_at <= NOW() - INTERVAL ? SECOND)
		ORDER BY id
		LIMIT ?
	`, lockToken, int(whatsappOutboxLockTTL.Seconds()), limit)
	if err != nil {
		if isSQLSchemaError(err) {
			return nil, nil
		}
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, nil
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, restaurant_id, recipient, event, attempts, payload_json
		FROM message_deliveries
		WHERE locked_by = ? AND status = 'pending'
		ORDER BY id
	`, lockToken)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []whatsappOutboxRow{}
	for rows.Next() {
		var (
			row     whatsappOutboxRow
			payload sql.NullString
		)
		if err := rows.Scan(&row.ID, &row.RestaurantID, &row.Recipient, &row.Event, &row.Attempts, &payload); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(payload.String), &row.Payload); err != nil {
			// An unparseable payload will never succeed; drop it out of the queue.
			s.finishWhatsAppDelivery(ctx, row.ID, row.Attempts, fmt.Errorf("payload ilegible: %w", err), true)
			continue
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// finishWhatsAppDelivery settles a claimed row: sent on success, failed once the
// attempt budget is exhausted, otherwise pending with a backoff.
func (s *Server) finishWhatsAppDelivery(ctx context.Context, id int64, attempts int, sendErr error, giveUp bool) {
	if sendErr == nil {
		_, err := s.db.ExecContext(ctx, `
			UPDATE message_deliveries
			SET status = 'sent', sent_at = NOW(), locked_at = NULL, locked_by = NULL, next_attempt_at = NULL, error = NULL
			WHERE id = ?
		`, id)
		if err != nil {
			log.Printf("whatsapp outbox: marking %d as sent failed: %v", id, err)
		}
		return
	}

	msg := truncate(sendErr.Error(), 500)
	if giveUp {
		if _, err := s.db.ExecContext(ctx, `
			UPDATE message_deliveries
			SET status = 'failed', locked_at = NULL, locked_by = NULL, next_attempt_at = NULL, error = ?
			WHERE id = ?
		`, msg, id); err != nil {
			log.Printf("whatsapp outbox: marking %d as failed failed: %v", id, err)
		}
		return
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE message_deliveries
		SET status = 'pending', locked_at = NULL, locked_by = NULL, next_attempt_at = NOW() + INTERVAL ? SECOND, error = ?
		WHERE id = ?
	`, int(whatsappOutboxBackoff(attempts).Seconds()), msg, id); err != nil {
		log.Printf("whatsapp outbox: rescheduling %d failed: %v", id, err)
	}
}

// sendWhatsAppOutboxRow delivers one queued message through the restaurant's
// gateway, preferring buttons when the payload carries choices.
func (s *Server) sendWhatsAppOutboxRow(ctx context.Context, row whatsappOutboxRow) error {
	gw, ok := s.botGatewayFor(ctx, row.RestaurantID)
	if !ok {
		return fmt.Errorf("WhatsApp no configurado para el restaurante %d", row.RestaurantID)
	}
	if len(row.Payload.Choices) > 0 {
		if err := s.sendWhatsAppMenuTracked(ctx, row.RestaurantID, gw, row.Recipient, row.Payload.Text, row.Payload.Choices, row.Event); err == nil {
			return nil
		}
	}
	return s.sendWhatsAppTextTracked(ctx, row.RestaurantID, gw, row.Recipient, row.Payload.Text, row.Event)
}

// runWhatsAppOutboxOnce drains one batch and returns how many were delivered.
func (s *Server) runWhatsAppOutboxOnce(ctx context.Context) (int, error) {
	lockToken := whatsappOutboxLockToken()
	rows, err := s.claimWhatsAppDeliveries(ctx, lockToken, whatsappOutboxBatchSize)
	if err != nil {
		return 0, err
	}

	sent := 0
	for _, row := range rows {
		sendErr := s.sendWhatsAppOutboxRow(ctx, row)
		if sendErr == nil {
			sent++
		}
		s.finishWhatsAppDelivery(ctx, row.ID, row.Attempts, sendErr, sendErr != nil && row.Attempts >= whatsappOutboxMaxAttempts)
	}
	return sent, nil
}

func whatsappOutboxLockToken() string {
	host, _ := os.Hostname()
	return fmt.Sprintf("%s/%d/%d", host, os.Getpid(), time.Now().UnixNano())
}

func (s *Server) runWhatsAppOutboxLoop(ctx context.Context) {
	ticker := time.NewTicker(whatsappOutboxScanEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := s.runWhatsAppOutboxOnce(ctx); err != nil {
				log.Printf("whatsapp outbox: batch failed: %v", err)
			}
		}
	}
}

// whatsappOutboxReconnectMinInterval throttles queue-on-reconnect triggers so
// rapid-fire connection.update / refresh events do not flood the worker. The
// first trigger always fires; subsequent triggers within this window are
// coalesced into a single follow-up run.
const whatsappOutboxReconnectMinInterval = 5 * time.Second

// whatsappOutboxReconnectTrigger is the per-restaurant debounce state. Stored
// in memory so the same process never floods itself with duplicates.
var (
	whatsappOutboxReconnectMu    sync.Mutex
	whatsappOutboxReconnectLast = map[int]time.Time{}
)

// whatsappOutboxReconnectNotify wakes the outbox loop without waiting for the
// 30s ticker. Channel is buffered so a missing receiver (race during startup
// or shutdown) never blocks the caller.
var whatsappOutboxReconnectNotifyCh = make(chan struct{}, 1)

// triggerWhatsAppQueueOnReconnect is the single entry point every reconnect
// path must call when a restaurant's WhatsApp instance transitions to a
// healthy state. It:
//
//  1. re-arms every `failed` outbox row for this restaurant back to
//     `pending` with `next_attempt_at = NOW()` (a clean retry budget, the
//     previous 6 attempts are wiped because the transient outage is over),
//  2. brings forward every `pending` row whose `next_attempt_at` is still
//     in the future so we don't sleep through the backoff after a recovery,
//  3. clears the booking-reminder circuit breaker so the per-minute
//     reminder scanner re-engages immediately,
//  4. fires a non-blocking ping into `whatsappOutboxReconnectNotifyCh` so the
//     outbox loop wakes on the next iteration (no 30s wait),
//  5. debounces by `whatsappOutboxReconnectMinInterval` per restaurant so a
//     burst of webhook + refresh events does not pile up duplicate scans.
//
// Returns the number of outbox rows the call re-armed (for logging).
func (s *Server) triggerWhatsAppQueueOnReconnect(ctx context.Context, restaurantID int) (int, error) {
	if restaurantID <= 0 {
		return 0, nil
	}

	// Debounce: first call wins, subsequent calls within the window are
	// coalesced. The coalesced call still wakes the worker so a follow-up
	// tick processes any rows added between the first and second event.
	whatsappOutboxReconnectMu.Lock()
	last := whatsappOutboxReconnectLast[restaurantID]
	now := time.Now()
	if !last.IsZero() && now.Sub(last) < whatsappOutboxReconnectMinInterval {
		whatsappOutboxReconnectMu.Unlock()
		select {
		case whatsappOutboxReconnectNotifyCh <- struct{}{}:
		default:
		}
		log.Printf("[whatsapp][obs][CP-RECONNECT-QUEUE-COALESCE] restaurant=%d since=%s", restaurantID, now.Sub(last).String())
		return 0, nil
	}
	whatsappOutboxReconnectLast[restaurantID] = now
	whatsappOutboxReconnectMu.Unlock()

	rearmed := 0

	// 1. failed -> pending. Reset attempts so the row gets a fresh 6-attempt
	//    budget against the now-healthy provider; clear the locked-by token
	//    defensively (should never be set for failed rows but cheap to set).
	if _, err := s.db.ExecContext(ctx, `
		UPDATE message_deliveries
		SET status = 'pending',
		    attempts = 0,
		    next_attempt_at = NOW(),
		    locked_at = NULL,
		    locked_by = NULL,
		    error = NULL
		WHERE restaurant_id = ? AND channel = 'whatsapp' AND status = 'failed'
	`, restaurantID); err != nil && !isSQLSchemaError(err) {
		return rearmed, fmt.Errorf("re-arm failed rows: %w", err)
	}

	// 2. Bring pending rows whose backoff hasn't elapsed yet into the
	//    immediate window. Capped at 1000 to keep the immediate scan
	//    bounded even on a stale backlog.
	if _, err := s.db.ExecContext(ctx, `
		UPDATE message_deliveries
		SET next_attempt_at = NOW()
		WHERE restaurant_id = ? AND channel = 'whatsapp' AND status = 'pending'
		  AND next_attempt_at IS NOT NULL AND next_attempt_at > NOW()
		ORDER BY id
		LIMIT 1000
	`, restaurantID); err != nil && !isSQLSchemaError(err) {
		return rearmed, fmt.Errorf("flush pending rows: %w", err)
	}

	// Count pending rows for observability.
	if r2, err := s.db.QueryContext(ctx, `
		SELECT COUNT(*) FROM message_deliveries
		WHERE restaurant_id = ? AND channel = 'whatsapp' AND status = 'pending'
	`, restaurantID); err == nil {
		defer r2.Close()
		if r2.Next() {
			_ = r2.Scan(&rearmed)
		}
	}

	// 3. Clear the booking-reminder breaker (cross-package helper in this
	//    same package). Safe even when the breaker was never opened.
	clearReminderBreaker(restaurantID)

	// 4. Wake the outbox worker; non-blocking via buffered channel.
	select {
	case whatsappOutboxReconnectNotifyCh <- struct{}{}:
	default:
	}

	log.Printf("[whatsapp][obs][CP-RECONNECT-QUEUE-RESUME] restaurant=%d pending=%d", restaurantID, rearmed)
	return rearmed, nil
}

// runWhatsAppOutboxLoopWithNotify drains the queue. It still ticks every
// `whatsappOutboxScanEvery`, but a notify channel short-circuits the wait so
// `triggerWhatsAppQueueOnReconnect` does not have to wait up to 30s for the
// next scan.
func (s *Server) runWhatsAppOutboxLoopWithNotify(ctx context.Context) {
	ticker := time.NewTicker(whatsappOutboxScanEvery)
	defer ticker.Stop()
	// Drain any initial trigger so a reconnect before the first tick still fires.
	select {
	case <-whatsappOutboxReconnectNotifyCh:
	default:
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-whatsappOutboxReconnectNotifyCh:
		}
		if _, err := s.runWhatsAppOutboxOnce(ctx); err != nil {
			log.Printf("whatsapp outbox: batch failed: %v", err)
		}
	}
}
