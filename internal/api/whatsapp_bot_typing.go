package api

import (
	"context"
	"log"
	"net/http"
	"time"
)

// botTypingRefresh re-sends "composing" before WhatsApp drops the indicator
// (it fades after ~25 s without a refresh). Coordination id: wa_bot_typing_v1
const botTypingRefresh = 20 * time.Second

// SendTyping shows (on=true) or clears the "escribiendo…" indicator in the
// customer's chat. Evolution/evo-weai: POST /chat/sendPresence.
func (g *evolutionGateway) SendTyping(ctx context.Context, to string, on bool) error {
	presence := "paused"
	if on {
		presence = "composing"
	}
	_, code, err := g.request(ctx, http.MethodPost, "/chat/sendPresence/"+g.instanceName, map[string]any{
		"number": to, "presence": presence, "delay": botTypingRefresh.Milliseconds() + 5000,
	})
	if err == nil && (code < 200 || code >= 300) {
		log.Printf("[bot] checkpoint wa_bot_typing_v1 instance=%s presence=%s http=%d", g.instanceName, presence, code)
	}
	return err
}

// botStartTyping keeps the indicator on in the sender's chat until the
// returned stop is called (the turn ended: reply sent, or nothing to send).
// Best-effort: providers without presence support are a no-op.
func (s *Server) botStartTyping(restaurantID int, sender string) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	gw, ok := s.botGatewayFor(ctx, restaurantID)
	evo, isEvo := gw.(*evolutionGateway)
	if !ok || !isEvo {
		return cancel
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(botTypingRefresh)
		defer t.Stop()
		for {
			reqCtx, reqCancel := context.WithTimeout(ctx, 10*time.Second)
			_ = evo.SendTyping(reqCtx, sender, true)
			reqCancel()
			select {
			case <-ctx.Done():
				off, offCancel := context.WithTimeout(context.Background(), 10*time.Second)
				_ = evo.SendTyping(off, sender, false)
				offCancel()
				return
			case <-t.C:
			}
		}
	}()
	return func() { cancel(); <-done }
}
