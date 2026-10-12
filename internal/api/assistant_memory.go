package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// assistantMemoryNode is one remembered fact/preference/pattern scoped to a
// (restaurant, user) pair. user_id 0 means restaurant-level memory shared by
// every user of the tenant.
type assistantMemoryNode struct {
	ID     int64
	Kind   string
	Label  string
	Detail string
	Weight float64
}

// memoryExtraction is the JSON shape the extractor LLM returns.
type memoryExtraction struct {
	Nodes []struct {
		Kind   string `json:"kind"`
		Label  string `json:"label"`
		Detail string `json:"detail"`
		Shared bool   `json:"shared"`
	} `json:"nodes"`
	Edges []struct {
		From string `json:"from"`
		To   string `json:"to"`
		Rel  string `json:"rel"`
	} `json:"edges"`
}

const assistantMemoryExtractPrompt = "Extrae memorias duraderas de este intercambio entre un usuario del backoffice de un restaurante y el asistente. " +
	"Guarda SOLO hechos, preferencias o patrones con valor futuro (preferencias de formato, decisiones del usuario sobre su restaurante, datos estables que el usuario afirma, correcciones que hace al asistente). " +
	"NO guardes saludos, preguntas puntuales de datos, ni nada que ya devuelvan las herramientas. " +
	"Devuelve SOLO JSON: {\"nodes\":[{\"kind\":\"preference|fact|pattern|correction\",\"label\":\"clave_snake_corta\",\"detail\":\"frase breve\",\"shared\":true|false}],\"edges\":[{\"from\":\"label\",\"to\":\"label\",\"rel\":\"relacion\"}]}. " +
	"shared=true si aplica a todo el restaurante, false si es del usuario. Sin memorias: {\"nodes\":[],\"edges\":[]}. " +
	"Maximo 3 nodos por intercambio. Labels en snake_case, estables entre conversaciones."

// extractAssistantMemories runs the extraction pass for one finished exchange
// and upserts the resulting graph. Best-effort: errors are swallowed by the
// caller (memory must never break the chat).
func (s *Server) extractAssistantMemories(ctx context.Context, restaurantID, userID int, userMsg, reply string) {
	if s.db == nil || restaurantID <= 0 {
		return
	}
	userMsg = strings.TrimSpace(userMsg)
	reply = strings.TrimSpace(reply)
	if userMsg == "" || reply == "" {
		return
	}
	if len(userMsg) > 1200 {
		userMsg = userMsg[:1200]
	}
	if len(reply) > 2000 {
		reply = reply[:2000]
	}
	msgs := []assistantChatMessage{{Role: "user", Content: fmt.Sprintf("USUARIO: %s\nASISTENTE: %s", userMsg, reply)}}
	result, err := s.assistantCall(ctx, restaurantID, assistantMemoryExtractPrompt, msgs, nil, nil)
	if err != nil {
		return
	}
	text := assistantRecoverEncodedReply(result.Text)
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return
	}
	var ext memoryExtraction
	if err := json.Unmarshal([]byte(text[start:end+1]), &ext); err != nil {
		return
	}
	ids := map[string]int64{}
	for _, n := range ext.Nodes {
		label := strings.ToLower(strings.TrimSpace(n.Label))
		if label == "" || len(label) > 190 {
			continue
		}
		kind := strings.ToLower(strings.TrimSpace(n.Kind))
		switch kind {
		case "preference", "fact", "pattern", "correction":
		default:
			kind = "fact"
		}
		scopeUser := userID
		if n.Shared {
			scopeUser = 0
		}
		detail := strings.TrimSpace(n.Detail)
		id, err := s.upsertMemoryNode(ctx, restaurantID, scopeUser, kind, label, detail)
		if err == nil && id > 0 {
			ids[label] = id
		}
	}
	for _, e := range ext.Edges {
		from, ok1 := ids[strings.ToLower(strings.TrimSpace(e.From))]
		to, ok2 := ids[strings.ToLower(strings.TrimSpace(e.To))]
		rel := strings.ToLower(strings.TrimSpace(e.Rel))
		if !ok1 || !ok2 || rel == "" || from == to {
			continue
		}
		_, _ = s.db.ExecContext(ctx,
			`INSERT INTO assistant_memory_edges (restaurant_id, from_node, to_node, rel) VALUES (?, ?, ?, ?)
			 ON DUPLICATE KEY UPDATE weight = weight + 1`,
			restaurantID, from, to, rel)
	}
}

func (s *Server) upsertMemoryNode(ctx context.Context, restaurantID, userID int, kind, label, detail string) (int64, error) {
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO assistant_memory_nodes (restaurant_id, user_id, kind, label, detail)
		 VALUES (?, ?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE detail = VALUES(detail), kind = VALUES(kind), weight = weight + 0.25`,
		restaurantID, userID, kind, label, detail); err != nil {
		return 0, err
	}
	var id int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM assistant_memory_nodes WHERE restaurant_id = ? AND user_id = ? AND label = ?`,
		restaurantID, userID, label).Scan(&id)
	return id, err
}

// loadAssistantMemories returns the strongest memories for the (restaurant,
// user) scope: the user's own nodes plus restaurant-level shared ones, plus
// one hop of graph neighbors of the user's nodes.
func (s *Server) loadAssistantMemories(ctx context.Context, restaurantID, userID int, limit int) []assistantMemoryNode {
	if s.db == nil || restaurantID <= 0 || limit <= 0 {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT n.id, n.kind, n.label, COALESCE(n.detail, ''), n.weight
		FROM assistant_memory_nodes n
		LEFT JOIN assistant_memory_edges e ON e.from_node = n.id AND e.restaurant_id = n.restaurant_id
		WHERE n.restaurant_id = ? AND (n.user_id = ? OR n.user_id = 0)
		GROUP BY n.id
		ORDER BY (n.weight + COALESCE(SUM(e.weight), 0) * 0.5) DESC, n.updated_at DESC
		LIMIT ?`, restaurantID, userID, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []assistantMemoryNode
	for rows.Next() {
		var n assistantMemoryNode
		if err := rows.Scan(&n.ID, &n.Kind, &n.Label, &n.Detail, &n.Weight); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// assistantMemoryPromptBlock renders the remembered facts for the system prompt.
func assistantMemoryPromptBlock(memories []assistantMemoryNode) string {
	if len(memories) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(" MEMORIA (hechos y preferencias que recuerdas de este usuario y restaurante; usalos para personalizar, sin mencionar que vienen de memoria):")
	for _, m := range memories {
		detail := m.Detail
		if detail == "" {
			detail = m.Label
		}
		sb.WriteString(" - " + detail)
	}
	return sb.String()
}

// kickAssistantMemoryExtraction runs extraction detached from the request so
// the chat never waits on it. Copies the parent scope values it needs.
func (s *Server) kickAssistantMemoryExtraction(parent context.Context, restaurantID, userID int, userMsg, reply string) {
	if restaurantID <= 0 {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		s.extractAssistantMemories(ctx, restaurantID, userID, userMsg, reply)
	}()
}

// compile-time guard: sql import used by db handles in other files.
var _ = sql.ErrNoRows
