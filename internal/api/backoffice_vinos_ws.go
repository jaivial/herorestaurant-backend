package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"

	"preactvillacarmen/internal/httpx"
	"preactvillacarmen/internal/lib/specialmenuimage"
)

const boVinoAIPrompt = "Create a premium restaurant wine bottle photoshoot image. Place the wine bottle centered in a 1:1 frame with comfortable margins on all sides so there is clear breathing space around the bottle. Use a clean, elegant background that complements the wine label. Apply high-end natural studio lighting with sharp focus to highlight the label details, bottle shape, and any unique features. Make the wine look premium, appetizing, and visually appealing for a restaurant wine list, while staying realistic and natural. Preserve the original label text, colors, and branding exactly."

var boVinoAIWSUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type boVinoAIHub struct {
	mu    sync.RWMutex
	rooms map[string]map[*boVinoAIClient]struct{}
}

type boVinoAIClient struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

type boVinoAIImageJob struct {
	RestaurantID int
	WineNum      int
	RawImage     []byte
	ContentType  string
}

func (s *Server) logBOVinoAITrace(format string, args ...any) {
	log.Printf("[bo-vino-ai] "+format, args...)
}

func boVinoAIRoomKey(restaurantID int) string {
	return strconv.Itoa(restaurantID)
}

func newBOVinoAIHub() *boVinoAIHub {
	return &boVinoAIHub{rooms: map[string]map[*boVinoAIClient]struct{}{}}
}

func (h *boVinoAIHub) add(restaurantID int, c *boVinoAIClient) {
	if h == nil || restaurantID <= 0 || c == nil {
		return
	}
	key := boVinoAIRoomKey(restaurantID)
	h.mu.Lock()
	defer h.mu.Unlock()
	room := h.rooms[key]
	if room == nil {
		room = map[*boVinoAIClient]struct{}{}
		h.rooms[key] = room
	}
	room[c] = struct{}{}
}

func (h *boVinoAIHub) remove(restaurantID int, c *boVinoAIClient) {
	if h == nil || restaurantID <= 0 || c == nil {
		return
	}
	key := boVinoAIRoomKey(restaurantID)
	h.mu.Lock()
	defer h.mu.Unlock()
	room := h.rooms[key]
	if room == nil {
		return
	}
	delete(room, c)
	if len(room) == 0 {
		delete(h.rooms, key)
	}
}

func (h *boVinoAIHub) list(restaurantID int) []*boVinoAIClient {
	if h == nil || restaurantID <= 0 {
		return nil
	}
	key := boVinoAIRoomKey(restaurantID)
	h.mu.RLock()
	defer h.mu.RUnlock()
	room := h.rooms[key]
	if len(room) == 0 {
		return nil
	}
	out := make([]*boVinoAIClient, 0, len(room))
	for c := range room {
		out = append(out, c)
	}
	return out
}

func (h *boVinoAIHub) broadcast(restaurantID int, payload any) {
	if h == nil || restaurantID <= 0 {
		return
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	for _, c := range h.list(restaurantID) {
		if err := c.writeText(raw); err != nil {
			h.remove(restaurantID, c)
			_ = c.close()
		}
	}
}

func (c *boVinoAIClient) writeText(raw []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(7 * time.Second))
	return c.conn.WriteMessage(websocket.TextMessage, raw)
}

func (c *boVinoAIClient) writeJSON(v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.writeText(raw)
}

func (c *boVinoAIClient) ping() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(7 * time.Second))
	return c.conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(7*time.Second))
}

func (c *boVinoAIClient) close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.Close()
}

func (s *Server) handleBOVinosAIWS(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		s.logBOVinoAITrace("ws reject unauthorized remote=%s", r.RemoteAddr)
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	s.logBOVinoAITrace("ws connect request restaurant=%d remote=%s", a.ActiveRestaurantID, r.RemoteAddr)

	conn, err := boVinoAIWSUpgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logBOVinoAITrace("ws upgrade error restaurant=%d err=%v", a.ActiveRestaurantID, err)
		return
	}

	client := &boVinoAIClient{conn: conn}
	s.vinoAIHub.add(a.ActiveRestaurantID, client)
	s.logBOVinoAITrace("ws connected restaurant=%d clients=%d", a.ActiveRestaurantID, len(s.vinoAIHub.list(a.ActiveRestaurantID)))
	defer func() {
		s.vinoAIHub.remove(a.ActiveRestaurantID, client)
		s.logBOVinoAITrace("ws disconnected restaurant=%d clients=%d", a.ActiveRestaurantID, len(s.vinoAIHub.list(a.ActiveRestaurantID)))
		_ = client.close()
	}()

	conn.SetReadLimit(1 << 20)
	_ = conn.SetReadDeadline(time.Now().Add(70 * time.Second))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(70 * time.Second))
	})

	if tracker, err := s.loadBOVinoAIImageTracker(r.Context(), a.ActiveRestaurantID); err == nil {
		_ = client.writeJSON(map[string]any{
			"type":          "hello",
			"restaurant_id": a.ActiveRestaurantID,
			"at":            time.Now().UTC().Format(time.RFC3339),
			"tracker":       tracker,
		})
		s.logBOVinoAITrace("ws hello snapshot sent restaurant=%d items=%d", a.ActiveRestaurantID, len(tracker.Items))
	} else {
		s.logBOVinoAITrace("ws hello snapshot load error restaurant=%d err=%v", a.ActiveRestaurantID, err)
	}

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if len(raw) == 0 {
				continue
			}
			var msg struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(raw, &msg); err != nil {
				continue
			}
			typ := strings.ToLower(strings.TrimSpace(msg.Type))
			if typ != "sync" && typ != "refresh" {
				continue
			}
			tracker, err := s.loadBOVinoAIImageTracker(r.Context(), a.ActiveRestaurantID)
			if err != nil {
				continue
			}
			_ = client.writeJSON(map[string]any{
				"type":          "snapshot",
				"restaurant_id": a.ActiveRestaurantID,
				"at":            time.Now().UTC().Format(time.RFC3339),
				"tracker":       tracker,
			})
		}
	}()

	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-readDone:
			return
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if err := client.ping(); err != nil {
				s.logBOVinoAITrace("ws ping failed restaurant=%d err=%v", a.ActiveRestaurantID, err)
				return
			}
		}
	}
}

type boVinoAIImagesTracker struct {
	TotalRequested  int                  `json:"total_requested"`
	TotalGenerating int                  `json:"total_generating"`
	Items           []boVinoAIImagesItem `json:"items"`
}

type boVinoAIImagesItem struct {
	WineNum        int     `json:"wine_num"`
	AIRequested    bool    `json:"ai_requested"`
	AIGenerating   bool    `json:"ai_generating"`
	AIGeneratedImg *string `json:"ai_generated_img"`
}

func (s *Server) loadBOVinoAIImageTracker(ctx context.Context, restaurantID int) (boVinoAIImagesTracker, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT num, COALESCE(ai_requested_img, 0), COALESCE(ai_generating_img, 0), ai_generated_img
		FROM VINOS
		WHERE restaurant_id = ? AND (ai_requested_img = 1 OR ai_generating_img = 1 OR ai_generated_img IS NOT NULL)
		ORDER BY num ASC
	`, restaurantID)
	if err != nil {
		return boVinoAIImagesTracker{}, err
	}
	defer rows.Close()

	tracker := boVinoAIImagesTracker{Items: make([]boVinoAIImagesItem, 0, 8)}
	for rows.Next() {
		var (
			item          boVinoAIImagesItem
			requestedInt  int
			generatingInt int
			generatedRaw  sql.NullString
		)
		if err := rows.Scan(&item.WineNum, &requestedInt, &generatingInt, &generatedRaw); err != nil {
			return boVinoAIImagesTracker{}, err
		}
		item.AIRequested = requestedInt != 0
		item.AIGenerating = generatingInt != 0
		if generatedRaw.Valid {
			if v := strings.TrimSpace(generatedRaw.String); v != "" {
				item.AIGeneratedImg = &v
			}
		}
		if item.AIRequested {
			tracker.TotalRequested++
		}
		if item.AIGenerating {
			tracker.TotalGenerating++
		}
		tracker.Items = append(tracker.Items, item)
	}
	return tracker, nil
}

func (s *Server) broadcastBOVinoAIEvent(restaurantID int, eventType string, payload map[string]any) {
	payload["type"] = eventType
	payload["restaurant_id"] = restaurantID
	s.vinoAIHub.broadcast(restaurantID, payload)
}

func (s *Server) handleBOVinoAIImageGenerate(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		s.logBOVinoAITrace("generate reject unauthorized remote=%s", r.RemoteAddr)
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	s.logBOVinoAITrace("generate request received restaurant=%d path=%s remote=%s", a.ActiveRestaurantID, r.URL.Path, r.RemoteAddr)

	if strings.TrimSpace(s.cfg.OpenAIAPIKey) == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "AI provider not configured"})
		return
	}
	if !s.bunnyConfigured() {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Image storage not configured"})
		return
	}

	wineNumStr := strings.TrimSpace(chi.URLParam(r, "id"))
	wineNum, err := strconv.Atoi(wineNumStr)
	if err != nil || wineNum <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid wine id"})
		return
	}

	maxInput := s.openAIInputMaxBytes()
	if err := r.ParseMultipartForm(int64(maxInput)); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error parsing form"})
		return
	}

	file, _, err := r.FormFile("image")
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "No image file provided"})
		return
	}
	defer file.Close()

	raw, err := io.ReadAll(io.LimitReader(file, int64(maxInput)+1))
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error reading file"})
		return
	}
	if len(raw) == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Empty file"})
		return
	}
	if len(raw) > maxInput {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Image too large"})
		return
	}

	contentType := http.DetectContentType(raw)
	allowedTypes := map[string]bool{
		"image/jpeg": true,
		"image/png":  true,
		"image/webp": true,
	}
	if !allowedTypes[contentType] {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "File type not allowed"})
		return
	}

	res, err := s.db.ExecContext(r.Context(), `
		UPDATE VINOS
		SET ai_requested_img = 1,
		    ai_generating_img = 1
		WHERE num = ? AND restaurant_id = ?
	`, wineNum, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error updating wine AI state")
		return
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Wine not found"})
		return
	}

	s.broadcastBOVinoAIEvent(a.ActiveRestaurantID, "wine_ai_started", map[string]any{
		"wine_num":      wineNum,
		"ai_requested":  true,
		"ai_generating": true,
	})

	go s.runBOVinoAIImageJob(boVinoAIImageJob{
		RestaurantID: a.ActiveRestaurantID,
		WineNum:      wineNum,
		RawImage:     raw,
		ContentType:  contentType,
	})

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"message":  "AI image generation started",
		"wine_num": wineNum,
	})
}

func (s *Server) runBOVinoAIImageJob(job boVinoAIImageJob) {
	ctx, cancel := context.WithTimeout(context.Background(), s.openAIRequestTimeout())
	defer cancel()
	s.logBOVinoAITrace("job start restaurant=%d wine=%d inputBytes=%d inputType=%s", job.RestaurantID, job.WineNum, len(job.RawImage), job.ContentType)

	if err := s.acquireBOGroupMenuV2AIWorker(ctx); err != nil {
		s.logBOVinoAITrace("job worker acquire error restaurant=%d wine=%d err=%v", job.RestaurantID, job.WineNum, err)
		s.failBOVinoAIImageJob(job, "AI generation queue timeout")
		return
	}
	defer s.releaseBOGroupMenuV2AIWorker()

	output, err := s.callOpenAIImageEditWithPrompt(ctx, job.RawImage, job.ContentType, boVinoAIPrompt)
	if err != nil {
		s.logBOVinoAITrace("job ai call error restaurant=%d wine=%d err=%v", job.RestaurantID, job.WineNum, err)
		s.failBOVinoAIImageJob(job, aiFailureMessage("AI image generation failed", err))
		return
	}
	if len(output) == 0 {
		s.failBOVinoAIImageJob(job, "AI image generation returned empty image")
		return
	}
	if len(output) > s.openAIMaxOutputBytes() {
		s.failBOVinoAIImageJob(job, "AI image is too large")
		return
	}

	outputType := strings.TrimSpace(http.DetectContentType(output))
	normalizedWebP, err := specialmenuimage.NormalizeToWebP(ctx, output, "vino-ai", outputType)
	if err != nil {
		s.logBOVinoAITrace("job normalize error restaurant=%d wine=%d err=%v", job.RestaurantID, job.WineNum, err)
		s.failBOVinoAIImageJob(job, "Failed processing generated image")
		return
	}

	objectPath, err := s.UploadWineImageV2(ctx, "", job.WineNum, normalizedWebP, "image/webp")
	if err != nil {
		s.logBOVinoAITrace("job bunny upload error restaurant=%d wine=%d err=%v", job.RestaurantID, job.WineNum, err)
		s.failBOVinoAIImageJob(job, "Failed uploading generated image")
		return
	}

	fullURL := s.bunnyPullURL(objectPath)
	_, err = s.db.ExecContext(ctx, `
		UPDATE VINOS
		SET ai_requested_img = 1,
		    ai_generating_img = 0,
		    ai_generated_img = ?,
		    foto_path = ?
		WHERE num = ? AND restaurant_id = ?
	`, fullURL, objectPath, job.WineNum, job.RestaurantID)
	if err != nil {
		s.logBOVinoAITrace("job db save error restaurant=%d wine=%d err=%v", job.RestaurantID, job.WineNum, err)
		s.failBOVinoAIImageJob(job, "Failed saving generated image")
		return
	}

	s.broadcastBOVinoAIEvent(job.RestaurantID, "wine_ai_completed", map[string]any{
		"wine_num":         job.WineNum,
		"ai_requested":     true,
		"ai_generating":    false,
		"ai_generated_img": fullURL,
		"foto_url":         fullURL,
	})
	s.logBOVinoAITrace("job completed restaurant=%d wine=%d url=%s", job.RestaurantID, job.WineNum, fullURL)
}

func (s *Server) failBOVinoAIImageJob(job boVinoAIImageJob, message string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s.logBOVinoAITrace("job failed restaurant=%d wine=%d message=%q", job.RestaurantID, job.WineNum, message)

	_, _ = s.db.ExecContext(ctx, `
		UPDATE VINOS
		SET ai_generating_img = 0
		WHERE num = ? AND restaurant_id = ?
	`, job.WineNum, job.RestaurantID)

	s.broadcastBOVinoAIEvent(job.RestaurantID, "wine_ai_failed", map[string]any{
		"wine_num":      job.WineNum,
		"ai_requested":  true,
		"ai_generating": false,
		"message":       message,
	})
}
