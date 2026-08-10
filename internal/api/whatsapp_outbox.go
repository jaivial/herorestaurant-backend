package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
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
		SELECT id, restaurant_id, recipient, attempts, payload_json
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
		if err := rows.Scan(&row.ID, &row.RestaurantID, &row.Recipient, &row.Attempts, &payload); err != nil {
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
		if err := gw.SendMenu(ctx, row.Recipient, row.Payload.Text, row.Payload.Choices); err == nil {
			return nil
		}
	}
	return gw.SendText(ctx, row.Recipient, row.Payload.Text)
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
