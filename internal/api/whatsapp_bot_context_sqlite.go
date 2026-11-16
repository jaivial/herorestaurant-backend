package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"preactvillacarmen/internal/botstore"
	"preactvillacarmen/internal/config"
	"preactvillacarmen/internal/sqldb"
)

type botConversationStore struct {
	db *botstore.DB
}

// newBotConversationStoreFor opens the configured backend (wa_bot_store_pg_v1).
// Postgres never falls back to SQLite and refuses an unmigrated database, so
// a cutover is all-or-nothing.
func newBotConversationStoreFor(cfg config.Config) (*botConversationStore, error) {
	// repair-v21 (all-PostgreSQL requirement): with DB_DRIVER=postgres the
	// bot context store is PostgreSQL too, in the unified DB's bot_context
	// schema (the SQLite-origin tables; MySQL-origin bot tables live in public).
	// An explicit non-postgres BOT_CONTEXT_DRIVER is refused (no silent SQLite).
	if strings.EqualFold(strings.TrimSpace(cfg.DBDriver), "postgres") {
		if drv := strings.TrimSpace(os.Getenv("BOT_CONTEXT_DRIVER")); drv != "" && !strings.EqualFold(drv, botstore.DriverPostgres) {
			return nil, fmt.Errorf("DB_DRIVER=postgres requires the bot context store on PostgreSQL: BOT_CONTEXT_DRIVER=%q refused (no SQLite fallback)", drv)
		}
		dsn := cfg.BotContextPostgresDSN
		if dsn == "" {
			dsn = cfg.DatabaseURL
		}
		schema := cfg.BotContextPostgresSchema
		if schema == "" {
			schema = sqldb.SchemaBotContext
		}
		return openBotConversationStoreSchema(botstore.DriverPostgres, dsn, schema, cfg.BotContextPostgresAllowEmpty)
	}
	if cfg.BotContextDriver == botstore.DriverPostgres {
		return openBotConversationStoreSchema(botstore.DriverPostgres, cfg.BotContextPostgresDSN, cfg.BotContextPostgresSchema, cfg.BotContextPostgresAllowEmpty)
	}
	return openBotConversationStore(cfg.BotContextDriver, cfg.BotContextSQLitePath, false)
}

func newBotConversationStore(path string) (*botConversationStore, error) {
	return openBotConversationStore(botstore.DriverSQLite, path, false)
}

func openBotConversationStore(driver, dsn string, allowEmpty bool) (*botConversationStore, error) {
	return openBotConversationStoreSchema(driver, dsn, "", allowEmpty)
}

func openBotConversationStoreSchema(driver, dsn, schema string, allowEmpty bool) (*botConversationStore, error) {
	db, err := botstore.OpenSchema(driver, dsn, schema)
	if err != nil {
		return nil, err
	}
	if err := db.Init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := db.RequireMigrated(allowEmpty); err != nil {
		_ = db.Close()
		return nil, err
	}

	botCheckpoint("wa_bot_store_pg_v1", "driver=%s postgres=%t", driver, db.Postgres)
	return &botConversationStore{db: db}, nil
}

// DB exposes the shared store handle (knowledge index and chat images live
// beside the transcript). Nil-safe.
func (s *botConversationStore) DB() *botstore.DB {
	if s == nil {
		return nil
	}
	return s.db
}

func (s *botConversationStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// botStateDB is where the bot-owned runtime state lives (inbound queue,
// sessions, message log): the PG bot store when BOT_CONTEXT_DRIVER=postgres,
// otherwise the shared MySQL as before (wa_bot_store_pg_mysql_v1). Never nil.
func (s *Server) botStateDB() *botstore.DB {
	// repair-wiring2: the MySQL-origin bot tables (botstore.MySQLTables:
	// bot_inbound_queue, whatsapp_bot_sessions, whatsapp_message_logs,
	// bot_conversation_messages, whatsapp_bot_messages) are imported with the
	// full business migration into the business schema ("public"); the bot
	// context schema holds only the SQLite-origin tables (botstore.Init unified
	// mode). They are always read/written on the business pool in the business
	// dialect, never on the bot context pool (verified scratch 09:05:
	// bot_context has no whatsapp_bot_sessions).
	return &botstore.DB{DB: s.db, Postgres: sqlDialect == sqldb.Postgres, Schema: sqldb.SchemaBusiness}
}

// botSQLNow is the server clock at 0 or 3 fractional digits per dialect.
func botSQLNow(st *botstore.DB, frac int) string {
	if st.Postgres {
		return fmt.Sprintf("LOCALTIMESTAMP(%d)", frac)
	}
	if frac > 0 {
		return fmt.Sprintf("NOW(%d)", frac)
	}
	return "NOW()"
}

// botSQLAgo is "now minus N UNIT" (UNIT = SECOND|MINUTE|HOUR) per dialect.
func botSQLAgo(st *botstore.DB, interval string) string {
	if st.Postgres {
		return "(LOCALTIMESTAMP - INTERVAL '" + strings.ToLower(interval) + "')"
	}
	return "(NOW() - INTERVAL " + interval + ")"
}

func (s *botConversationStore) Append(ctx context.Context, restaurantID int, userPhone, role, content, toolName, source string) error {
	if s == nil || s.db == nil || restaurantID <= 0 || strings.TrimSpace(userPhone) == "" || strings.TrimSpace(content) == "" {
		return nil
	}
	if role != "user" && role != "assistant" {
		return fmt.Errorf("invalid conversation role %q", role)
	}
	includeInContext := 1
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(source)), "security_") {
		includeInContext = 0
	}
	_, err := s.db.ExecContext(ctx, selectedSQL(`INSERT INTO conversation_messages
        (restaurant_id,user_phone,role,content,tool_name,source,include_in_context,created_at_ms)
        VALUES (?,?,?,?,?,?,?,?)`), restaurantID, botConversationKey(userPhone), role, content, toolName, source, includeInContext, time.Now().UnixMilli())
	return err
}

func (s *botConversationStore) History(ctx context.Context, restaurantID int, userPhone string) ([]botMessage, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	// Coordination id: wa_bot_context_window_v1 - the full transcript stays in
	// SQLite, but the model only sees the most recent 40 messages. That bounds
	// tokens per call; older booking confirmations are recovered with
	// get_bookings. Ordered by send time, not id, so a message recorded late
	// (a backfilled campaign: wa_bot_campaign_context_v1) sits where the
	// customer saw it.
	rows, err := s.db.QueryContext(ctx, selectedSQL(`SELECT role, content, source, tool_name FROM (
            SELECT id, role, content, source, tool_name, created_at_ms FROM conversation_messages
            WHERE restaurant_id=? AND user_phone=? AND include_in_context=1
            ORDER BY created_at_ms DESC, id DESC LIMIT 40) AS recent ORDER BY created_at_ms ASC, id ASC`), restaurantID, botConversationKey(userPhone))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	raw := make([]botMessage, 0, 32)
	for rows.Next() {
		var role, content, source, toolName string
		if err := rows.Scan(&role, &content, &source, &toolName); err != nil {
			return nil, err
		}
		// Staff interventions must stay distinguishable from the bot's own
		// replies, otherwise they merge into the assistant turn and the model
		// never defers to them (wa_bot_human_handoff_v1).
		if botContextExcludedSources[source] {
			continue
		}
		switch {
		case source == "manual_whatsapp":
			content = botStaffMessagePrefix + content
		case source == botCampaignSource:
			content = botCampaignMessagePrefix + content
		case botContextTemplateSources[source] != "":
			// Fixed server-side texts are shown as events: verbatim they get
			// parroted as the answer to unrelated questions.
			content = botContextTemplateSources[source]
		case toolName == "send_contact":
			// Rendered as an event, not as text: a literal "Contacto: ..." line
			// taught the model to type the card instead of calling send_contact.
			content = "[Tarjeta de contacto enviada al cliente: " + strings.TrimSpace(strings.TrimPrefix(content, "Contacto:")) + "]"
		}
		raw = append(raw, botMessage{Role: role, Content: []botBlock{{Type: "text", Text: content}}})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return normalizeBotConversationHistory(raw), nil
}

// botContextExcludedSources are messages delivered to this phone because it
// belongs to restaurant staff (booking alerts, shift reminders, attendance,
// member messages). They are not part of a customer conversation and, when fed
// to the model, get copied into customer replies (wa_bot_context_hygiene_v1).
var botContextExcludedSources = map[string]bool{
	"restaurant_notification":   true,
	"pre_shift_reminder":        true,
	"attendance":                true,
	"backoffice_member_message": true,
}

// botContextTemplateSources maps fixed server-side messages to the marker the
// model sees instead of their literal text (wa_bot_context_hygiene_v1).
var botContextTemplateSources = map[string]string{
	"agent_contact_intro": "[Aviso automático enviado: se indicó al cliente que contacte con el restaurante]",
	"allergen_notice":     "[Aviso automático enviado: consulta de alérgenos/ingredientes derivada al restaurante]",
	"extras_notice":       "[Aviso automático enviado: cambio de extras derivado al restaurante]",
	"same_day_notice":     "[Aviso automático enviado: gestión del mismo día derivada al restaurante]",
	"unsupported_content": "[Aviso automático enviado: solo se admiten mensajes de texto]",
	"agent_fallback":      "[Aviso automático enviado: error temporal del asistente]",
	// wa_bot_special_date_policy_v1 / wa_bot_sticky_handoff_v1
	"management_handoff_notice": "[Aviso automático enviado: consulta derivada a la gestión del restaurante con tarjeta de contacto]",
	// wa_bot_management_group_v1
	"management_forwarded":         "[Aviso automático enviado: la solicitud se trasladó al equipo de gestión, que contactará al cliente]",
	"pipeline_fixed_reply":         "[Respuesta automática: el mensaje no era para el restaurante o intentaba cambiar las reglas del asistente]",
	"management_already_forwarded": "[Aviso automático enviado: se recordó al cliente que el equipo de gestión ya tiene su solicitud]",
	"special_date_prereserva_link": "[Aviso automático enviado: fecha especial, se envió el enlace de la web para reservar/pre-reservar]",
}

// botStaffMessagePrefix labels messages typed manually by restaurant staff.
const botStaffMessagePrefix = "[Mensaje escrito por el personal del restaurante]: "

// botCampaignSource tags a marketing campaign delivered to the customer by
// WhatsApp; botCampaignMessagePrefix tells the model what it is, so a short
// reply right after it ("¿es comida o cena?") is read as a question about the
// campaign, not about an older booking (wa_bot_campaign_context_v1).
const (
	botCampaignSource        = "campaign"
	botCampaignMessagePrefix = "[Campaña promocional enviada por el restaurante a este cliente por WhatsApp; si el cliente pregunta justo después, lo más probable es que se refiera a ella]:\n"
)

func normalizeBotConversationHistory(raw []botMessage) []botMessage {
	out := make([]botMessage, 0, len(raw)+1)
	for _, m := range raw {
		if len(m.Content) == 0 || strings.TrimSpace(m.Content[0].Text) == "" {
			continue
		}
		if len(out) == 0 && m.Role == "assistant" {
			out = append(out, botUserText("[Contexto previo: el restaurante inició esta conversación por WhatsApp.]"))
		}
		if len(out) > 0 && out[len(out)-1].Role == m.Role {
			out[len(out)-1].Content[0].Text += "\n" + m.Content[0].Text
			continue
		}
		out = append(out, m)
	}
	return out
}

// botDecisionRecord is one stored pipeline decision, exposed to the backoffice.
type botDecisionRecord struct {
	ID        int64           `json:"id"`
	UserPhone string          `json:"userPhone"`
	Message   string          `json:"message"`
	Decision  json.RawMessage `json:"decision"`
	ModelUsed string          `json:"modelUsed"`
	CreatedAt int64           `json:"createdAtMs"`
}

func (s *botConversationStore) AppendDecision(ctx context.Context, restaurantID int, userPhone, message string, decision any, modelUsed string) (int64, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	raw, err := json.Marshal(decision)
	if err != nil {
		return 0, err
	}
	return s.db.InsertID(ctx, selectedSQL(`INSERT INTO pipeline_decisions (restaurant_id,user_phone,message,decision_json,model_used,created_at_ms) VALUES (?,?,?,?,?,?)`),
		restaurantID, digitsOnly(userPhone), truncate(message, 500), string(raw), modelUsed, time.Now().UnixMilli())
}

// SetDecisionModel records which provider/model answered the turn plus the
// turn metrics (wa_bot_turn_metrics_v1), merged into decision_json.
func (s *botConversationStore) SetDecisionModel(ctx context.Context, id int64, modelUsed string, metrics ...map[string]any) {
	if s == nil || s.db == nil || id <= 0 {
		return
	}
	_, _ = s.db.ExecContext(ctx, selectedSQL(`UPDATE pipeline_decisions SET model_used = ? WHERE id = ?`), modelUsed, id)
	if len(metrics) > 0 && metrics[0] != nil {
		if raw, err := json.Marshal(metrics[0]); err == nil {
			q := `UPDATE pipeline_decisions SET decision_json = json_set(decision_json, '$.turn', json(?)) WHERE id = ?`
			if s.db.Postgres {
				q = `UPDATE pipeline_decisions SET decision_json = jsonb_set(decision_json::jsonb, '{turn}', ?::jsonb)::text WHERE id = ?`
			}
			_, _ = s.db.ExecContext(ctx, selectedSQL(q), string(raw), id) // q chosen by s.db.Postgres above
		}
	}
}

func (s *botConversationStore) RecentDecisions(ctx context.Context, restaurantID int, limit int) ([]botDecisionRecord, error) {
	out := []botDecisionRecord{}
	if s == nil || s.db == nil {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx, selectedSQL(`SELECT id,user_phone,message,decision_json,model_used,created_at_ms FROM pipeline_decisions WHERE restaurant_id=? ORDER BY id DESC LIMIT ?`), restaurantID, limit)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var r botDecisionRecord
		var raw string
		if err := rows.Scan(&r.ID, &r.UserPhone, &r.Message, &raw, &r.ModelUsed, &r.CreatedAt); err != nil {
			return out, err
		}
		r.Decision = json.RawMessage(raw)
		out = append(out, r)
	}
	return out, rows.Err()
}

// botHandoffState is the open "this topic belongs to a human" marker for a
// conversation (wa_bot_sticky_handoff_v1).
type botHandoffState struct {
	Topic     string `json:"topic"`
	Reason    string `json:"reason"`
	Repeats   int    `json:"repeats"`
	UpdatedAt int64  `json:"updated_at_ms"`
}

func (s *botConversationStore) GetHandoff(ctx context.Context, restaurantID int, userPhone string) (*botHandoffState, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	var h botHandoffState
	err := s.db.QueryRowContext(ctx, selectedSQL(`SELECT topic, reason, repeats, updated_at_ms FROM handoff_state WHERE restaurant_id=? AND user_phone=?`),
		restaurantID, digitsOnly(userPhone)).Scan(&h.Topic, &h.Reason, &h.Repeats, &h.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &h, nil
}

// SetHandoff opens (or refreshes) the handoff. repeat=true increments the
// counter for a repeated redirect on the same topic.
func (s *botConversationStore) SetHandoff(ctx context.Context, restaurantID int, userPhone, topic, reason string, repeat bool) error {
	if s == nil || s.db == nil {
		return nil
	}
	now := time.Now().UnixMilli()
	if repeat {
		_, err := s.db.ExecContext(ctx, selectedSQL(`UPDATE handoff_state SET repeats = repeats + 1, updated_at_ms = ? WHERE restaurant_id=? AND user_phone=?`), now, restaurantID, digitsOnly(userPhone))
		return err
	}
	_, err := s.db.ExecContext(ctx, selectedSQL(`INSERT INTO handoff_state (restaurant_id,user_phone,topic,reason,repeats,updated_at_ms) VALUES (?,?,?,?,0,?)
		ON CONFLICT(restaurant_id,user_phone) DO UPDATE SET topic=excluded.topic, reason=excluded.reason, repeats=0, updated_at_ms=excluded.updated_at_ms`),
		restaurantID, digitsOnly(userPhone), truncate(topic, 300), reason, now)
	return err
}

func (s *botConversationStore) ClearHandoff(ctx context.Context, restaurantID int, userPhone string) error {
	if s == nil || s.db == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx, selectedSQL(`DELETE FROM handoff_state WHERE restaurant_id=? AND user_phone=?`), restaurantID, digitsOnly(userPhone))
	return err
}

// botManagementRequest is one issue already forwarded to management.
type botManagementRequest struct {
	ID        int64  `json:"id"`
	Reason    string `json:"reason"`
	Summary   string `json:"summary"`
	GroupSent bool   `json:"group_sent"`
	CreatedAt int64  `json:"created_at_ms"`
}

// RecentManagementRequests returns the customer's forwarded issues newer
// than since (newest first).
func (s *botConversationStore) RecentManagementRequests(ctx context.Context, restaurantID int, userPhone string, since time.Time) ([]botManagementRequest, error) {
	out := []botManagementRequest{}
	if s == nil || s.db == nil {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx, selectedSQL(`SELECT id, reason, summary, group_sent, created_at_ms FROM management_requests
		WHERE restaurant_id=? AND user_phone=? AND created_at_ms>=? ORDER BY id DESC LIMIT 10`), restaurantID, digitsOnly(userPhone), since.UnixMilli())
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var r botManagementRequest
		var sent int
		if err := rows.Scan(&r.ID, &r.Reason, &r.Summary, &sent, &r.CreatedAt); err != nil {
			return out, err
		}
		r.GroupSent = sent != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *botConversationStore) AddManagementRequest(ctx context.Context, restaurantID int, userPhone, reason, summary string, groupSent bool) error {
	if s == nil || s.db == nil {
		return nil
	}
	sent := 0
	if groupSent {
		sent = 1
	}
	_, err := s.db.ExecContext(ctx, selectedSQL(`INSERT INTO management_requests (restaurant_id,user_phone,reason,summary,group_sent,created_at_ms) VALUES (?,?,?,?,?,?)`),
		restaurantID, digitsOnly(userPhone), reason, truncate(summary, 500), sent, time.Now().UnixMilli())
	return err
}

// ManagementRequestStats summarises what was forwarded to the management
// group in the last 30 days, by reason, plus the latest 10 requests.
func (s *botConversationStore) ManagementRequestStats(ctx context.Context, restaurantID int) map[string]any {
	out := map[string]any{"by_reason": map[string]int{}, "recent": []map[string]any{}, "total": 0}
	if s == nil || s.db == nil {
		return out
	}
	since := time.Now().Add(-30 * 24 * time.Hour).UnixMilli()
	byReason := map[string]int{}
	total := 0
	if rows, err := s.db.QueryContext(ctx, selectedSQL(`SELECT reason, COUNT(*) FROM management_requests WHERE restaurant_id=? AND created_at_ms>=? GROUP BY reason`), restaurantID, since); err == nil {
		for rows.Next() {
			var r string
			var n int
			if rows.Scan(&r, &n) == nil {
				byReason[r] = n
				total += n
			}
		}
		rows.Close()
	}
	recent := []map[string]any{}
	if rows, err := s.db.QueryContext(ctx, selectedSQL(`SELECT user_phone, reason, summary, group_sent, created_at_ms FROM management_requests WHERE restaurant_id=? ORDER BY id DESC LIMIT 10`), restaurantID); err == nil {
		for rows.Next() {
			var phone, reason, summary string
			var sent int
			var at int64
			if rows.Scan(&phone, &reason, &summary, &sent, &at) == nil {
				recent = append(recent, map[string]any{"userPhone": phone, "reason": reason, "summary": summary, "groupSent": sent != 0, "createdAtMs": at})
			}
		}
		rows.Close()
	}
	out["by_reason"], out["recent"], out["total"] = byReason, recent, total
	return out
}

// DSPyDemoCandidates returns distinct customer messages Jev classified with
// high confidence (and without DSPy): the few-shot demos of the pipeline's
// DSPy disambiguator (wa_bot_dspy_compiled_v4). Only the latest label of each
// message is kept.
func (s *botConversationStore) DSPyDemoCandidates(ctx context.Context, restaurantID int, minConfidence float64, limit int) ([]map[string]string, error) {
	out := []map[string]string{}
	if s == nil || s.db == nil {
		return out, nil
	}
	q := `SELECT message, json_extract(decision_json,'$.intent') FROM pipeline_decisions
		WHERE restaurant_id = ? AND json_extract(decision_json,'$.classifier')='jev' AND json_extract(decision_json,'$.confidence') >= ?
		  AND length(message) BETWEEN 2 AND 240
		  AND id IN (SELECT MAX(id) FROM pipeline_decisions WHERE restaurant_id = ? GROUP BY message)
		ORDER BY id DESC LIMIT ?`
	if s.db.Postgres {
		// jsonb_typeof guards keep a non-numeric confidence from failing the cast.
		q = `SELECT message, COALESCE(decision_json::jsonb->>'intent','') FROM pipeline_decisions
		WHERE restaurant_id = ? AND decision_json::jsonb->>'classifier'='jev'
		  AND jsonb_typeof(decision_json::jsonb->'confidence')='number' AND (decision_json::jsonb->>'confidence')::float8 >= ?
		  AND char_length(message) BETWEEN 2 AND 240
		  AND id IN (SELECT MAX(id) FROM pipeline_decisions WHERE restaurant_id = ? GROUP BY message)
		ORDER BY id DESC LIMIT ?`
	}
	rows, err := s.db.QueryContext(ctx, selectedSQL(q), restaurantID, minConfidence, restaurantID, limit) // q chosen by s.db.Postgres above
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var msg, intent string
		if err := rows.Scan(&msg, &intent); err != nil {
			return out, err
		}
		out = append(out, map[string]string{"message": msg, "intent": intent})
	}
	return out, rows.Err()
}

// DecisionRestaurants lists the restaurants that have pipeline decisions.
func (s *botConversationStore) DecisionRestaurants(ctx context.Context) ([]int, error) {
	out := []int{}
	if s == nil || s.db == nil {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx, selectedSQL(`SELECT DISTINCT restaurant_id FROM pipeline_decisions`))
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return out, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// botConversationKey is the thread key for a transcript row. Phone numbers key
// by their digits; a group thread keys by its namespaced @g.us id so several
// members share one conversation and never collide with a phone number
// (coordination id: wa_bot_group_mention_v1).
func botConversationKey(chat string) string {
	chat = strings.TrimSpace(chat)
	if botGroupIsGroupJID(chat) {
		return botGroupConversationKey(chat)
	}
	return digitsOnly(chat)
}
