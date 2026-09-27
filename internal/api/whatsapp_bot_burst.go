package api

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// Coordination id: wa_bot_burst_coalesce_v1
//
// WhatsApp customers often split one request into several quick messages
// ("hola" / "quería reservar" / "el sábado para 4"). Running one agent turn
// per message produced parallel, contradictory replies and racing booking
// tools. Messages of the same conversation are now:
//   1. serialized: one turn at a time per (restaurant, sender);
//   2. coalesced: a turn waits for a short quiet window (botBurstWindow) and
//      takes every message received meanwhile as ONE customer turn.
// Each message is still recorded individually in the transcript.

const botBurstWindow = 2500 * time.Millisecond

type botBurstQueue struct {
	pending []botWebhookMessage
	running bool
	last    time.Time
}

type botBurstCoalescer struct {
	mu     sync.Mutex
	queues map[string]*botBurstQueue
}

func newBotBurstCoalescer() *botBurstCoalescer {
	return &botBurstCoalescer{queues: map[string]*botBurstQueue{}}
}

// enqueue adds a message; it returns true when the caller must start the
// worker for this conversation (no worker is running yet).
func (c *botBurstCoalescer) enqueue(key string, msg botWebhookMessage) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	q := c.queues[key]
	if q == nil {
		q = &botBurstQueue{}
		c.queues[key] = q
	}
	q.pending = append(q.pending, msg)
	q.last = time.Now()
	if q.running {
		return false
	}
	q.running = true
	return true
}

// release drops a conversation queue without processing it (load shedding).
func (c *botBurstCoalescer) release(key string) {
	c.mu.Lock()
	delete(c.queues, key)
	c.mu.Unlock()
}

func botBurstKey(restaurantID int, sender string) string {
	return fmt.Sprintf("%d:%s", restaurantID, digitsOnly(sender))
}

// take waits for the quiet window and returns the burst (nil = done; the
// worker must exit and the queue is released).
func (c *botBurstCoalescer) take(key string) []botWebhookMessage {
	for {
		c.mu.Lock()
		q := c.queues[key]
		if q == nil || len(q.pending) == 0 {
			delete(c.queues, key)
			c.mu.Unlock()
			return nil
		}
		wait := botBurstWindow - time.Since(q.last)
		if wait <= 0 {
			burst := q.pending
			q.pending = nil
			c.mu.Unlock()
			return burst
		}
		c.mu.Unlock()
		time.Sleep(wait)
	}
}

// botMergeBurst folds a burst into one message: texts joined by newlines, the
// last message id/push name kept, voice notes flagged when any was audio.
func botMergeBurst(burst []botWebhookMessage) botWebhookMessage {
	out := burst[len(burst)-1]
	if len(burst) == 1 {
		return out
	}
	parts := make([]string, 0, len(burst))
	transcribed := false
	for _, m := range burst {
		if t := strings.TrimSpace(m.Text); t != "" {
			parts = append(parts, t)
		}
		transcribed = transcribed || m.Transcribed
	}
	out.Text = strings.Join(parts, "\n")
	out.Transcribed = transcribed
	out.Burst = len(burst)
	return out
}

// botRunConversationWorker drains the conversation queue, one turn per burst.
func (s *Server) botRunConversationWorker(restaurantID int, sender string) {
	key := botBurstKey(restaurantID, sender)
	for {
		burst := s.botBursts.take(key)
		if burst == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		for i := range burst {
			burst[i] = s.botPrepareInbound(ctx, restaurantID, burst[i])
		}
		kept := burst[:0]
		for _, m := range burst {
			if strings.TrimSpace(m.Text) != "" {
				kept = append(kept, m)
			}
		}
		if len(kept) > 0 {
			msg := botMergeBurst(kept)
			if msg.Burst > 1 {
				log.Printf("[bot] checkpoint wa_bot_burst_coalesce_v1 restaurant_id=%d sender=%s messages=%d", restaurantID, sender, msg.Burst)
			}
			func() {
				defer func() {
					if rec := recover(); rec != nil {
						log.Printf("[bot] restaurant=%d sender=%s PANIC recovered: %v", restaurantID, sender, rec)
					}
				}()
				if err := s.botProcessMessage(ctx, restaurantID, msg, kept...); err != nil {
					log.Printf("[bot] restaurant=%d sender=%s error: %v", restaurantID, sender, err)
				}
			}()
		}
		cancel()
	}
}

// botPrepareInbound transcribes a voice note (empty Text = nothing usable;
// the customer already received the "escríbemelo" fallback).
func (s *Server) botPrepareInbound(ctx context.Context, restaurantID int, msg botWebhookMessage) botWebhookMessage {
	if msg.Text != "" || !msg.IsAudio || (msg.AudioB64 == "" && msg.MessageID == "") {
		return msg
	}
	text := s.botTranscribeAudio(ctx, restaurantID, msg)
	msg.AudioB64 = ""
	if text == "" {
		if gw, ok := s.botGatewayFor(ctx, restaurantID); ok {
			_ = s.sendWhatsAppTextTracked(ctx, restaurantID, gw, msg.Sender, "Perdona, no he podido entender bien el audio. ¿Me lo puedes escribir por aquí, por favor?", "unsupported_content")
		}
		return msg
	}
	msg.Text, msg.Transcribed = text, true
	return msg
}
