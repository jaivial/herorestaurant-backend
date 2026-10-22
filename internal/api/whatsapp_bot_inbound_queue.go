package api

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// Coordination id: wa_bot_inbound_queue_v1
//
// Durable inbound queue for the agent turns. The burst coalescer and the turn
// semaphore were memory-only: a deploy or a crash silently dropped every
// accepted-but-unprocessed turn (incident 2026-10-22 14:35, group Bot
// Alqueria: quick ack sent, the turn died with the container).
//
// Contract (the outbound side already has one, message_deliveries):
//   - a message is PERSISTED before it enters the burst coalescer, so the
//     provider's 200 answer to the webhook no longer means "processed";
//   - the worker marks the rows of a burst done/failed with the turn;
//   - rows still pending/running at boot are REPLAYED (bounded: last 6 hours,
//     100 rows), so a turn interrupted by a deploy answers after restart.

const (
	// botInboundReplayGrace keeps rows created moments before a crash out of
	// the replay: the provider may still redeliver them on its own.
	botInboundReplayGrace = 15 * time.Second
	// botInboundReplayMaxAge bounds replay: an answer to a much older message
	// is worse than none (the conversation has moved on).
	botInboundReplayMaxAge = 6 * time.Hour
	// botInboundReplayLimit bounds the boot replay batch.
	botInboundReplayLimit = 100
)

// botInboundEnqueue persists one accepted inbound message. Best effort: a
// failure must never block the live turn, it is logged instead.
func (s *Server) botInboundEnqueue(ctx context.Context, restaurantID int, chatKey string, msg botWebhookMessage) {
	if s.db == nil || strings.TrimSpace(msg.MessageID) == "" {
		return
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT IGNORE INTO bot_inbound_queue (restaurant_id, chat_key, message_id, payload_json)
		VALUES (?, ?, ?, ?)
	`, restaurantID, chatKey, msg.MessageID, string(payload)); err != nil {
		botCheckpoint("wa_bot_inbound_queue_v1", "restaurant_id=%d enqueue_error err=%v", restaurantID, err)
	}
}

// botInboundFinish closes the queue rows of one processed burst.
func (s *Server) botInboundFinish(restaurantID int, messageIDs []string, state string) {
	if s.db == nil || len(messageIDs) == 0 {
		return
	}
	ph := strings.Repeat("?,", len(messageIDs)-1) + "?"
	args := make([]any, 0, len(messageIDs)+2)
	args = append(args, state)
	for _, id := range messageIDs {
		args = append(args, id)
	}
	args = append(args, restaurantID)
	// Bounded: this runs in the worker's defer, a hung DB call must never
	// freeze the conversation (PR review iter 1).
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := s.db.ExecContext(ctx, `
		UPDATE bot_inbound_queue SET state = ?
		WHERE message_id IN (`+ph+`) AND restaurant_id = ? AND state <> 'done'
	`, args...); err != nil {
		botCheckpoint("wa_bot_inbound_queue_v1", "restaurant_id=%d finish_error state=%s err=%v", restaurantID, state, err)
	}
}

// botInboundClaim marks the rows of a burst as running (observability).
func (s *Server) botInboundClaim(restaurantID int, messageIDs []string) {
	if s.db == nil || len(messageIDs) == 0 {
		return
	}
	ph := strings.Repeat("?,", len(messageIDs)-1) + "?"
	args := make([]any, 0, len(messageIDs)+1)
	for _, id := range messageIDs {
		args = append(args, id)
	}
	args = append(args, restaurantID)
	// Bounded (PR review iter 1): the claim runs inline in the worker.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := s.db.ExecContext(ctx, `
		UPDATE bot_inbound_queue SET state = 'running', started_at = NOW()
		WHERE message_id IN (`+ph+`) AND restaurant_id = ? AND state = 'pending'
	`, args...); err != nil {
		botCheckpoint("wa_bot_inbound_queue_v1", "restaurant_id=%d claim_error err=%v", restaurantID, err)
	}
}

// botBurstIDs returns the provider message ids of a burst (queue rows).
func botBurstIDs(burst []botWebhookMessage) []string {
	ids := make([]string, 0, len(burst))
	for _, m := range burst {
		if id := strings.TrimSpace(m.MessageID); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// botInboundRecover replays the turns a crash or a deploy interrupted. Called
// once at boot, behind the background-workers gate. The SELECT is retried so a
// transient DB hiccup right after boot does not silently disable the replay
// (PR review iter 1); migrations.Apply already ran before NewServer, so the
// table exists.
func (s *Server) botInboundRecover() {
	if s.db == nil {
		return
	}
	var found []struct {
		rid     int
		key     string
		msgID   string
		payload []byte
	}
	for attempt := 1; attempt <= 3; attempt++ {
		var err error
		found, err = s.botInboundRecoverScan(context.Background())
		if err == nil {
			break
		}
		botCheckpoint("wa_bot_inbound_queue_v1", "recover_error attempt=%d err=%v", attempt, err)
		if attempt < 3 {
			time.Sleep(2 * time.Second)
		}
	}
	if len(found) == 0 {
		return
	}
	botCheckpoint("wa_bot_inbound_queue_v1", "recover_rows=%d", len(found))
	replayed := map[string]bool{}
	for _, r := range found {
		var msg botWebhookMessage
		if err := json.Unmarshal(r.payload, &msg); err != nil || strings.TrimSpace(msg.Sender) == "" {
			// Unreadable payload: close it, never block the boot.
			s.botInboundFinish(r.rid, []string{r.msgID}, "failed")
			continue
		}
		if !replayed[r.key] {
			// One worker per conversation, same as the webhook path: an
			// enqueue that starts the worker does it, the rest ride along.
			if s.botBursts.enqueue(r.key, msg) {
				replayed[r.key] = true
				select {
				case s.botSem <- struct{}{}:
					go func(rid int, key string, sender string) {
						defer func() { <-s.botSem }()
						botCheckpoint("wa_bot_inbound_queue_v1", "replay_start restaurant_id=%d chat=%s", rid, key)
						s.botRunConversationWorker(rid, sender)
					}(r.rid, r.key, msg.Sender)
				default:
					s.botBursts.release(r.key)
					botCheckpoint("wa_bot_inbound_queue_v1", "recover_busy chat=%s", r.key)
				}
				continue
			}
		}
		// A worker is already running for this conversation: the row rides
		// the same burst (enqueue is idempotent for the coalescer).
		s.botBursts.enqueue(r.key, msg)
	}
}

// botInboundRecoverScan closes the expired rows and returns the ones to
// replay. The SELECT is separate so the caller can retry it alone.
func (s *Server) botInboundRecoverScan(ctx context.Context) ([]struct {
	rid     int
	key     string
	msgID   string
	payload []byte
}, error) {
	// Too old to answer: close them instead of surprising a customer hours
	// later with a reply to a conversation that has moved on.
	if res, err := s.db.ExecContext(ctx, `
		UPDATE bot_inbound_queue SET state = 'expired'
		WHERE state IN ('pending','running') AND created_at < (NOW() - INTERVAL 6 HOUR)
	`); err == nil {
		if n, _ := res.RowsAffected(); n > 0 {
			botCheckpoint("wa_bot_inbound_queue_v1", "expired=%d", n)
		}
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT restaurant_id, chat_key, message_id, payload_json
		FROM bot_inbound_queue
		WHERE state IN ('pending','running') AND created_at < (NOW() - INTERVAL 15 SECOND)
		  AND created_at >= (NOW() - INTERVAL 6 HOUR)
		ORDER BY id ASC LIMIT ?
	`, botInboundReplayLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var found []struct {
		rid     int
		key     string
		msgID   string
		payload []byte
	}
	for rows.Next() {
		var r struct {
			rid     int
			key     string
			msgID   string
			payload []byte
		}
		if rows.Scan(&r.rid, &r.key, &r.msgID, &r.payload) != nil {
			continue
		}
		found = append(found, r)
	}
	return found, rows.Err()
}
