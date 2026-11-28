package api

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"
)

// Coordination id: wa_bot_human_handoff_v1.
//
// When restaurant staff write manually from the WhatsApp phone, the bot must
// stop answering that customer for a while: the staff member now owns the
// conversation. Messages the backend itself sent come back from the provider
// as fromMe echoes too, so their ids are remembered on send and excluded.

const botDefaultHumanTakeoverMinutes = 120

// botOutboundIDs remembers provider message ids sent by the backend so their
// fromMe webhook echo is not mistaken for a manual staff message.
var botOutboundIDs = struct {
	sync.Mutex
	ids map[string]int64
}{ids: map[string]int64{}}

// botRememberOutboundID records an id returned by the provider on send.
func botRememberOutboundID(id string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	now := time.Now().Unix()
	botOutboundIDs.Lock()
	defer botOutboundIDs.Unlock()
	if len(botOutboundIDs.ids) > 4096 {
		for k, ts := range botOutboundIDs.ids {
			if now-ts > 3600 {
				delete(botOutboundIDs.ids, k)
			}
		}
	}
	botOutboundIDs.ids[id] = now
}

// botIsOwnOutboundID reports whether a fromMe message id was sent by us.
func botIsOwnOutboundID(id string) bool {
	botOutboundIDs.Lock()
	defer botOutboundIDs.Unlock()
	_, ok := botOutboundIDs.ids[strings.TrimSpace(id)]
	return ok
}

// botHumanTakeoverMinutes resolves the tenant pause window (0 disables).
func botHumanTakeoverMinutes(tenant botTenantConfig) int {
	if tenant.HumanTakeoverMinutes == nil {
		return botDefaultHumanTakeoverMinutes
	}
	if *tenant.HumanTakeoverMinutes < 0 {
		return 0
	}
	return *tenant.HumanTakeoverMinutes
}

// botHandleManualStaffMessage records a message typed by staff on the
// restaurant phone and pauses the bot for that conversation. Echoes of
// backend-sent messages are ignored. Returns true when it was a manual message.
func (s *Server) botHandleManualStaffMessage(ctx context.Context, restaurantID int, sender, messageID, text, mediaKind string) bool {
	if messageID != "" && botIsOwnOutboundID(messageID) {
		return false
	}
	content := strings.TrimSpace(text)
	if content == "" && mediaKind != "" {
		content = "[El personal del restaurante envió " + mediaKind + "]"
	}
	if content == "" {
		return false
	}
	s.botRecordConversationMessage(ctx, restaurantID, sender, "assistant", content, "", "manual_whatsapp")
	minutes := botHumanTakeoverMinutes(s.loadBotTenantConfig(ctx, restaurantID))
	// One upsert: keeps the customer's push_name (the fromMe pushName is the
	// restaurant's own name) and sets/clears the pause window.
	st := s.botStateDB()
	q := `
		INSERT INTO whatsapp_bot_sessions (restaurant_id, user_phone, push_name, last_message_at, bot_paused_until)
		VALUES (?, ?, '', NOW(3), IF(? > 0, DATE_ADD(NOW(3), INTERVAL ? MINUTE), NULL))
		ON DUPLICATE KEY UPDATE last_message_at = NOW(3), bot_paused_until = VALUES(bot_paused_until)
	`
	if st.Postgres { // wa_bot_store_pg_mysql_v1
		q = `INSERT INTO whatsapp_bot_sessions (restaurant_id, user_phone, push_name, last_message_at, bot_paused_until)
		VALUES (?, ?, '', LOCALTIMESTAMP(3), CASE WHEN CAST(? AS integer) > 0 THEN LOCALTIMESTAMP(3) + make_interval(mins => CAST(? AS integer)) ELSE NULL END)
		ON CONFLICT (restaurant_id, user_phone) DO UPDATE SET last_message_at = LOCALTIMESTAMP(3), bot_paused_until = EXCLUDED.bot_paused_until`
	}
	if _, err := st.ExecContext(ctx, selectedSQL(q), restaurantID, sender, minutes, minutes); /* q chosen by st.Postgres */ err != nil && !isSQLSchemaError(err) {
		log.Printf("[bot] restaurant=%d pause session: %v", restaurantID, err)
	}
	botCheckpoint("wa_bot_human_takeover_started", "restaurant_id=%d sender=%s minutes=%d", restaurantID, sender, minutes)
	return true
}

// botIsPausedForHuman reports whether staff took over this conversation.
func (s *Server) botIsPausedForHuman(ctx context.Context, restaurantID int, sender string) bool {
	var paused int
	st := s.botStateDB()
	phoneEq := "user_phone = ?"
	if st.Postgres { // padfk1: PAD-aware key, pgnative-B: app.norm on the raw PK columns
		phoneEq = "app.norm(user_phone) = app.norm(?)"
	}
	err := st.QueryRowContext(ctx, selectedSQL(`
		SELECT COUNT(*) FROM whatsapp_bot_sessions
		WHERE restaurant_id = ? AND `+phoneEq+` AND bot_paused_until > `+botSQLNow(st, 3)+`
	`), restaurantID, sender).Scan(&paused)
	return err == nil && paused > 0
}
