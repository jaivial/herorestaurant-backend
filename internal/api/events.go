package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func eventEnabled(enabled []string, event string) bool {
	event = strings.TrimSpace(event)
	if event == "" {
		return false
	}
	// Default: enabled unless explicitly configured.
	if len(enabled) == 0 {
		return true
	}
	for _, e := range enabled {
		e = strings.TrimSpace(e)
		if e == "*" || e == event {
			return true
		}
	}
	return false
}

func (s *Server) emitN8nWebhookAsync(restaurantID int, event string, payload any) {
	if restaurantID <= 0 || strings.TrimSpace(event) == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.emitN8nWebhook(ctx, restaurantID, event, payload); err != nil {
			log.Printf("emit webhook failed (restaurant_id=%d event=%s): %v", restaurantID, event, err)
		}
	}()
}

func (s *Server) emitN8nWebhook(ctx context.Context, restaurantID int, event string, payload any) error {
	var webhook sql.NullString
	var enabledRaw sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT n8n_webhook_url, enabled_events_json
		FROM restaurant_integrations
		WHERE restaurant_id = ?
		LIMIT 1
	`, restaurantID).Scan(&webhook, &enabledRaw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}

	webhookURL := strings.TrimSpace(webhook.String)
	if webhookURL == "" {
		return nil
	}

	var enabled []string
	if enabledRaw.Valid && strings.TrimSpace(enabledRaw.String) != "" {
		_ = json.Unmarshal([]byte(enabledRaw.String), &enabled)
	}
	if !eventEnabled(enabled, event) {
		return nil
	}

	envelope := map[string]any{
		"event":        event,
		"restaurantId": restaurantID,
		"payload":      payload,
		"occurredAt":   time.Now().Format(time.RFC3339),
	}
	body, _ := json.Marshal(envelope)

	// Persist delivery attempt for observability.
	var deliveryID int64
	{
		res, err := s.db.ExecContext(ctx, `
			INSERT INTO message_deliveries (restaurant_id, channel, event, recipient, payload_json, status)
			VALUES (?, 'webhook', ?, ?, ?, 'pending')
		`, restaurantID, event, webhookURL, string(body))
		if err != nil {
			return err
		}
		deliveryID, _ = res.LastInsertId()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		_, _ = s.db.ExecContext(ctx, "UPDATE message_deliveries SET status = 'failed', error = ? WHERE id = ? AND restaurant_id = ?", err.Error(), deliveryID, restaurantID)
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		_, _ = s.db.ExecContext(ctx, "UPDATE message_deliveries SET status = 'failed', error = ? WHERE id = ? AND restaurant_id = ?", err.Error(), deliveryID, restaurantID)
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := "HTTP " + strconv.Itoa(resp.StatusCode) + ": " + strings.TrimSpace(string(raw))
		_, _ = s.db.ExecContext(ctx, "UPDATE message_deliveries SET status = 'failed', error = ? WHERE id = ? AND restaurant_id = ?", msg, deliveryID, restaurantID)
		return errors.New(msg)
	}

	_, _ = s.db.ExecContext(ctx, "UPDATE message_deliveries SET status = 'sent', sent_at = NOW() WHERE id = ? AND restaurant_id = ?", deliveryID, restaurantID)
	return nil
}
