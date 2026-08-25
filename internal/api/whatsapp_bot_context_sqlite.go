package api

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type botConversationStore struct {
	db *sql.DB
}

func newBotConversationStore(path string) (*botConversationStore, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = ":memory:"
	} else if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("create bot context dir: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open bot context sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	for _, stmt := range []string{
		`PRAGMA busy_timeout = 5000`,
		`PRAGMA journal_mode = WAL`,
		`CREATE TABLE IF NOT EXISTS conversation_messages (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            restaurant_id INTEGER NOT NULL,
            user_phone TEXT NOT NULL,
            role TEXT NOT NULL CHECK(role IN ('user','assistant')),
            content TEXT NOT NULL,
            tool_name TEXT NOT NULL DEFAULT '',
            source TEXT NOT NULL DEFAULT '',
            include_in_context INTEGER NOT NULL DEFAULT 1,
            created_at_ms INTEGER NOT NULL
        )`,
		`CREATE INDEX IF NOT EXISTS idx_conversation_messages_thread
            ON conversation_messages(restaurant_id, user_phone, id)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("init bot context sqlite: %w", err)
		}
	}
	// Forward-compatible migration for databases created by early builds of
	// this feature before include_in_context existed.
	if _, err := db.Exec(`ALTER TABLE conversation_messages ADD COLUMN include_in_context INTEGER NOT NULL DEFAULT 1`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
		_ = db.Close()
		return nil, fmt.Errorf("migrate bot context sqlite: %w", err)
	}
	return &botConversationStore{db: db}, nil
}

func (s *botConversationStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
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
	_, err := s.db.ExecContext(ctx, `INSERT INTO conversation_messages
        (restaurant_id,user_phone,role,content,tool_name,source,include_in_context,created_at_ms)
        VALUES (?,?,?,?,?,?,?,?)`, restaurantID, digitsOnly(userPhone), role, content, toolName, source, includeInContext, time.Now().UnixMilli())
	return err
}

func (s *botConversationStore) History(ctx context.Context, restaurantID int, userPhone string) ([]botMessage, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT role, content FROM conversation_messages
        WHERE restaurant_id=? AND user_phone=? AND include_in_context=1 ORDER BY id ASC`, restaurantID, digitsOnly(userPhone))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	raw := make([]botMessage, 0, 32)
	for rows.Next() {
		var role, content string
		if err := rows.Scan(&role, &content); err != nil {
			return nil, err
		}
		raw = append(raw, botMessage{Role: role, Content: []botBlock{{Type: "text", Text: content}}})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return normalizeBotConversationHistory(raw), nil
}

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
