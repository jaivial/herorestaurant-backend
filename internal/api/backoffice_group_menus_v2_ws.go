package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"preactvillacarmen/internal/httpx"
)

const boGroupMenuV2AIPrompt = "Create a premium restaurant food photoshoot version of this dish. Keep the same dish identity and plating realistic. Place the dish centered in a 1:1 frame with comfortable margins on all sides (avoid tight crop) so there is clear breathing space around the plate. Preserve the original background style and color palette, but refine it into an elegant, clean, matching restaurant scenario and remove distracting non-elegant objects. Maximize perceived image quality and make the food look highly appetizing and visually appealing for a restaurant menu, while staying realistic and natural. Use high-end natural studio lighting, sharp focus, and rich yet realistic food textures and colors."

var waveSpeedKnownStatuses = map[string]bool{
	"pending":     true,
	"queued":      true,
	"processing":  true,
	"in_progress": true,
	"running":     true,
	"completed":   true,
	"succeeded":   true,
	"failed":      true,
	"error":       true,
	"cancelled":   true,
	"canceled":    true,
}

var boGroupMenuV2AIWSUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		// WS is session-authenticated; local proxies may differ in host.
		return true
	},
}

type boV2AIImagesTracker struct {
	TotalRequested  int                    `json:"total_requested"`
	TotalGenerating int                    `json:"total_generating"`
	Items           []boV2AIImagesDishItem `json:"items"`
}

type boV2AIImagesDishItem struct {
	DishID         int64   `json:"dish_id"`
	AIRequested    bool    `json:"ai_requested"`
	AIGenerating   bool    `json:"ai_generating"`
	AIGeneratedImg *string `json:"ai_generated_img"`
}

type boGroupMenuV2AIHub struct {
	mu    sync.RWMutex
	rooms map[string]map[*boGroupMenuV2AIClient]struct{}
}

type boGroupMenuV2AIClient struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

type boGroupMenuV2AIImageJob struct {
	RestaurantID int
	MenuID       int64
	SectionID    int64
	DishID       int64
	RawImage     []byte
	ContentType  string
}

func (s *Server) logBOGroupMenuV2AITrace(format string, args ...any) {
	log.Printf("[bo-group-menu-v2-ai] "+format, args...)
}

func boGroupMenuV2AIJobLabel(job boGroupMenuV2AIImageJob) string {
	return fmt.Sprintf("restaurant=%d menu=%d section=%d dish=%d", job.RestaurantID, job.MenuID, job.SectionID, job.DishID)
}

func newBOGroupMenuV2AIHub() *boGroupMenuV2AIHub {
	return &boGroupMenuV2AIHub{rooms: map[string]map[*boGroupMenuV2AIClient]struct{}{}}
}

func boGroupMenuV2RoomKey(restaurantID int, menuID int64) string {
	return strconv.Itoa(restaurantID) + ":" + strconv.FormatInt(menuID, 10)
}

func (h *boGroupMenuV2AIHub) add(restaurantID int, menuID int64, c *boGroupMenuV2AIClient) {
	if h == nil || restaurantID <= 0 || menuID <= 0 || c == nil {
		return
	}
	key := boGroupMenuV2RoomKey(restaurantID, menuID)
	h.mu.Lock()
	defer h.mu.Unlock()
	room := h.rooms[key]
	if room == nil {
		room = map[*boGroupMenuV2AIClient]struct{}{}
		h.rooms[key] = room
	}
	room[c] = struct{}{}
}

func (h *boGroupMenuV2AIHub) remove(restaurantID int, menuID int64, c *boGroupMenuV2AIClient) {
	if h == nil || restaurantID <= 0 || menuID <= 0 || c == nil {
		return
	}
	key := boGroupMenuV2RoomKey(restaurantID, menuID)
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

func (h *boGroupMenuV2AIHub) list(restaurantID int, menuID int64) []*boGroupMenuV2AIClient {
	if h == nil || restaurantID <= 0 || menuID <= 0 {
		return nil
	}
	key := boGroupMenuV2RoomKey(restaurantID, menuID)
	h.mu.RLock()
	defer h.mu.RUnlock()
	room := h.rooms[key]
	if len(room) == 0 {
		return nil
	}
	out := make([]*boGroupMenuV2AIClient, 0, len(room))
	for c := range room {
		out = append(out, c)
	}
	return out
}

func (h *boGroupMenuV2AIHub) broadcast(restaurantID int, menuID int64, payload any) {
	if h == nil || restaurantID <= 0 || menuID <= 0 {
		return
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	for _, c := range h.list(restaurantID, menuID) {
		if err := c.writeText(raw); err != nil {
			h.remove(restaurantID, menuID, c)
			_ = c.close()
		}
	}
}

func (c *boGroupMenuV2AIClient) writeText(raw []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(7 * time.Second))
	return c.conn.WriteMessage(websocket.TextMessage, raw)
}

func (c *boGroupMenuV2AIClient) writeJSON(v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.writeText(raw)
}

func (c *boGroupMenuV2AIClient) ping() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(7 * time.Second))
	return c.conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(7*time.Second))
}

func (c *boGroupMenuV2AIClient) close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.Close()
}

func (s *Server) handleBOGroupMenusV2AIWS(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		s.logBOGroupMenuV2AITrace("ws reject unauthorized remote=%s", r.RemoteAddr)
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	menuIDRaw := strings.TrimSpace(r.URL.Query().Get("menuId"))
	s.logBOGroupMenuV2AITrace(
		"ws connect request restaurant=%d menuRaw=%q remote=%s",
		a.ActiveRestaurantID,
		menuIDRaw,
		r.RemoteAddr,
	)
	menuID, err := strconv.ParseInt(menuIDRaw, 10, 64)
	if err != nil || menuID <= 0 {
		s.logBOGroupMenuV2AITrace("ws reject invalid menu restaurant=%d menuRaw=%q", a.ActiveRestaurantID, menuIDRaw)
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid menu id"})
		return
	}

	owns, err := s.ensureBOMenuV2Belongs(a.ActiveRestaurantID, menuID)
	if err != nil {
		s.logBOGroupMenuV2AITrace("ws menu ownership check error restaurant=%d menu=%d err=%v", a.ActiveRestaurantID, menuID, err)
		httpx.WriteError(w, http.StatusInternalServerError, "Error checking menu")
		return
	}
	if !owns {
		s.logBOGroupMenuV2AITrace("ws reject menu not found restaurant=%d menu=%d", a.ActiveRestaurantID, menuID)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Menu not found"})
		return
	}

	conn, err := boGroupMenuV2AIWSUpgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logBOGroupMenuV2AITrace("ws upgrade error restaurant=%d menu=%d err=%v", a.ActiveRestaurantID, menuID, err)
		return
	}

	client := &boGroupMenuV2AIClient{conn: conn}
	s.groupMenusV2AIHub.add(a.ActiveRestaurantID, menuID, client)
	s.logBOGroupMenuV2AITrace(
		"ws connected restaurant=%d menu=%d clients=%d",
		a.ActiveRestaurantID,
		menuID,
		len(s.groupMenusV2AIHub.list(a.ActiveRestaurantID, menuID)),
	)
	defer func() {
		s.groupMenusV2AIHub.remove(a.ActiveRestaurantID, menuID, client)
		s.logBOGroupMenuV2AITrace(
			"ws disconnected restaurant=%d menu=%d clients=%d",
			a.ActiveRestaurantID,
			menuID,
			len(s.groupMenusV2AIHub.list(a.ActiveRestaurantID, menuID)),
		)
		_ = client.close()
	}()

	conn.SetReadLimit(1 << 20)
	_ = conn.SetReadDeadline(time.Now().Add(70 * time.Second))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(70 * time.Second))
	})

	if tracker, err := s.loadBOMenuV2AIImageTracker(r.Context(), a.ActiveRestaurantID, menuID); err == nil {
		_ = client.writeJSON(map[string]any{
			"type":          "hello",
			"restaurant_id": a.ActiveRestaurantID,
			"menu_id":       menuID,
			"at":            time.Now().UTC().Format(time.RFC3339),
			"tracker":       tracker,
		})
		s.logBOGroupMenuV2AITrace(
			"ws hello snapshot sent restaurant=%d menu=%d requested=%d generating=%d",
			a.ActiveRestaurantID,
			menuID,
			tracker.TotalRequested,
			tracker.TotalGenerating,
		)
	} else {
		s.logBOGroupMenuV2AITrace("ws hello snapshot load error restaurant=%d menu=%d err=%v", a.ActiveRestaurantID, menuID, err)
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
				Type   string `json:"type"`
				MenuID int64  `json:"menuId"`
			}
			if err := json.Unmarshal(raw, &msg); err != nil {
				s.logBOGroupMenuV2AITrace("ws message invalid json restaurant=%d menu=%d err=%v", a.ActiveRestaurantID, menuID, err)
				continue
			}
			typ := strings.ToLower(strings.TrimSpace(msg.Type))
			if typ != "sync" && typ != "refresh" && typ != "join" && typ != "join_menu" && typ != "join_group_menu" {
				s.logBOGroupMenuV2AITrace("ws message ignored type restaurant=%d menu=%d type=%q", a.ActiveRestaurantID, menuID, typ)
				continue
			}
			if msg.MenuID > 0 && msg.MenuID != menuID {
				s.logBOGroupMenuV2AITrace(
					"ws message ignored menu mismatch restaurant=%d menu=%d msgMenu=%d type=%q",
					a.ActiveRestaurantID,
					menuID,
					msg.MenuID,
					typ,
				)
				continue
			}
			s.logBOGroupMenuV2AITrace("ws snapshot requested restaurant=%d menu=%d type=%q", a.ActiveRestaurantID, menuID, typ)
			tracker, err := s.loadBOMenuV2AIImageTracker(r.Context(), a.ActiveRestaurantID, menuID)
			if err != nil {
				s.logBOGroupMenuV2AITrace("ws snapshot load error restaurant=%d menu=%d err=%v", a.ActiveRestaurantID, menuID, err)
				continue
			}
			_ = client.writeJSON(map[string]any{
				"type":          "snapshot",
				"restaurant_id": a.ActiveRestaurantID,
				"menu_id":       menuID,
				"at":            time.Now().UTC().Format(time.RFC3339),
				"tracker":       tracker,
			})
			s.logBOGroupMenuV2AITrace(
				"ws snapshot sent restaurant=%d menu=%d requested=%d generating=%d",
				a.ActiveRestaurantID,
				menuID,
				tracker.TotalRequested,
				tracker.TotalGenerating,
			)
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
				s.logBOGroupMenuV2AITrace("ws ping failed restaurant=%d menu=%d err=%v", a.ActiveRestaurantID, menuID, err)
				return
			}
		}
	}
}

func (s *Server) loadBOMenuV2AIImageTracker(ctx context.Context, restaurantID int, menuID int64) (boV2AIImagesTracker, error) {
	s.logBOGroupMenuV2AITrace("tracker load start restaurant=%d menu=%d", restaurantID, menuID)
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, COALESCE(ai_requested_img, 0), COALESCE(ai_generating_img, 0), ai_generated_img
		FROM group_menu_section_dishes_v2
		WHERE restaurant_id = ? AND menu_id = ?
		ORDER BY id ASC
	`, restaurantID, menuID)
	if err != nil {
		s.logBOGroupMenuV2AITrace("tracker load query error restaurant=%d menu=%d err=%v", restaurantID, menuID, err)
		return boV2AIImagesTracker{}, err
	}
	defer rows.Close()

	tracker := boV2AIImagesTracker{Items: make([]boV2AIImagesDishItem, 0, 16)}
	for rows.Next() {
		var (
			item          boV2AIImagesDishItem
			requestedInt  int
			generatingInt int
			generatedRaw  sql.NullString
		)
		if err := rows.Scan(&item.DishID, &requestedInt, &generatingInt, &generatedRaw); err != nil {
			s.logBOGroupMenuV2AITrace("tracker load scan error restaurant=%d menu=%d err=%v", restaurantID, menuID, err)
			return boV2AIImagesTracker{}, err
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
	s.logBOGroupMenuV2AITrace(
		"tracker load done restaurant=%d menu=%d items=%d requested=%d generating=%d",
		restaurantID,
		menuID,
		len(tracker.Items),
		tracker.TotalRequested,
		tracker.TotalGenerating,
	)
	return tracker, nil
}

func (s *Server) handleBOGroupMenusV2GenerateSectionDishAIImage(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		s.logBOGroupMenuV2AITrace("generate reject unauthorized remote=%s", r.RemoteAddr)
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	s.logBOGroupMenuV2AITrace("generate request received restaurant=%d path=%s remote=%s", a.ActiveRestaurantID, r.URL.Path, r.RemoteAddr)
	if strings.TrimSpace(s.cfg.OpenAIAPIKey) == "" {
		s.logBOGroupMenuV2AITrace("generate reject ai provider key missing restaurant=%d", a.ActiveRestaurantID)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "WaveSpeed AI not configured"})
		return
	}
	if !s.bunnyConfigured() {
		s.logBOGroupMenuV2AITrace("generate reject bunny missing restaurant=%d", a.ActiveRestaurantID)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Image storage not configured"})
		return
	}

	menuID, err := parseChiPositiveInt64(r, "id")
	if err != nil {
		s.logBOGroupMenuV2AITrace("generate invalid menu id restaurant=%d err=%v", a.ActiveRestaurantID, err)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid menu id"})
		return
	}
	sectionID, err := parseChiPositiveInt64(r, "sectionId")
	if err != nil {
		s.logBOGroupMenuV2AITrace("generate invalid section id restaurant=%d menu=%d err=%v", a.ActiveRestaurantID, menuID, err)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid section id"})
		return
	}
	dishID, err := parseChiPositiveInt64(r, "dishId")
	if err != nil {
		s.logBOGroupMenuV2AITrace("generate invalid dish id restaurant=%d menu=%d section=%d err=%v", a.ActiveRestaurantID, menuID, sectionID, err)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid dish id"})
		return
	}
	s.logBOGroupMenuV2AITrace("generate ids parsed restaurant=%d menu=%d section=%d dish=%d", a.ActiveRestaurantID, menuID, sectionID, dishID)

	owns, err := s.ensureBOMenuV2Belongs(a.ActiveRestaurantID, menuID)
	if err != nil {
		s.logBOGroupMenuV2AITrace("generate ownership check error restaurant=%d menu=%d err=%v", a.ActiveRestaurantID, menuID, err)
		httpx.WriteError(w, http.StatusInternalServerError, "Error checking menu")
		return
	}
	if !owns {
		s.logBOGroupMenuV2AITrace("generate reject menu not found restaurant=%d menu=%d", a.ActiveRestaurantID, menuID)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Menu not found"})
		return
	}

	exists, err := s.ensureBOGroupMenuV2DishExists(r.Context(), a.ActiveRestaurantID, menuID, sectionID, dishID)
	if err != nil {
		s.logBOGroupMenuV2AITrace("generate dish check error restaurant=%d menu=%d section=%d dish=%d err=%v", a.ActiveRestaurantID, menuID, sectionID, dishID, err)
		httpx.WriteError(w, http.StatusInternalServerError, "Error checking dish")
		return
	}
	if !exists {
		s.logBOGroupMenuV2AITrace("generate reject dish not found restaurant=%d menu=%d section=%d dish=%d", a.ActiveRestaurantID, menuID, sectionID, dishID)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Dish not found"})
		return
	}

	maxInput := s.openAIInputMaxBytes()
	if err := r.ParseMultipartForm(int64(maxInput)); err != nil {
		s.logBOGroupMenuV2AITrace("generate parse form error restaurant=%d menu=%d section=%d dish=%d err=%v", a.ActiveRestaurantID, menuID, sectionID, dishID, err)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error parsing form"})
		return
	}

	file, _, err := r.FormFile("image")
	if err != nil {
		s.logBOGroupMenuV2AITrace("generate missing image file restaurant=%d menu=%d section=%d dish=%d err=%v", a.ActiveRestaurantID, menuID, sectionID, dishID, err)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "No image file provided"})
		return
	}
	defer file.Close()

	raw, err := io.ReadAll(io.LimitReader(file, int64(maxInput)+1))
	if err != nil {
		s.logBOGroupMenuV2AITrace("generate read file error restaurant=%d menu=%d section=%d dish=%d err=%v", a.ActiveRestaurantID, menuID, sectionID, dishID, err)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error reading file"})
		return
	}
	if len(raw) == 0 {
		s.logBOGroupMenuV2AITrace("generate reject empty file restaurant=%d menu=%d section=%d dish=%d", a.ActiveRestaurantID, menuID, sectionID, dishID)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Empty file"})
		return
	}
	if len(raw) > maxInput {
		s.logBOGroupMenuV2AITrace("generate reject large file restaurant=%d menu=%d section=%d dish=%d bytes=%d max=%d", a.ActiveRestaurantID, menuID, sectionID, dishID, len(raw), maxInput)
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
		s.logBOGroupMenuV2AITrace("generate reject invalid content-type restaurant=%d menu=%d section=%d dish=%d contentType=%s", a.ActiveRestaurantID, menuID, sectionID, dishID, contentType)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "File type not allowed"})
		return
	}
	s.logBOGroupMenuV2AITrace("generate image accepted restaurant=%d menu=%d section=%d dish=%d bytes=%d contentType=%s", a.ActiveRestaurantID, menuID, sectionID, dishID, len(raw), contentType)

	res, err := s.db.ExecContext(r.Context(), `
		UPDATE group_menu_section_dishes_v2
		SET ai_requested_img = 1,
		    ai_generating_img = 1
		WHERE id = ? AND section_id = ? AND menu_id = ? AND restaurant_id = ?
	`, dishID, sectionID, menuID, a.ActiveRestaurantID)
	if err != nil {
		s.logBOGroupMenuV2AITrace("generate db state update error restaurant=%d menu=%d section=%d dish=%d err=%v", a.ActiveRestaurantID, menuID, sectionID, dishID, err)
		httpx.WriteError(w, http.StatusInternalServerError, "Error updating dish AI state")
		return
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		s.logBOGroupMenuV2AITrace("generate db state update no rows restaurant=%d menu=%d section=%d dish=%d", a.ActiveRestaurantID, menuID, sectionID, dishID)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Dish not found"})
		return
	}
	s.logBOGroupMenuV2AITrace("generate db state updated restaurant=%d menu=%d section=%d dish=%d affected=%d", a.ActiveRestaurantID, menuID, sectionID, dishID, affected)

	s.broadcastBOGroupMenuV2AIEvent(a.ActiveRestaurantID, menuID, "ai_image_started", map[string]any{
		"dish_id":       dishID,
		"section_id":    sectionID,
		"ai_requested":  true,
		"ai_generating": true,
	})
	s.logBOGroupMenuV2AITrace("generate broadcast started event restaurant=%d menu=%d section=%d dish=%d", a.ActiveRestaurantID, menuID, sectionID, dishID)

	go s.runBOGroupMenuV2AIImageJob(boGroupMenuV2AIImageJob{
		RestaurantID: a.ActiveRestaurantID,
		MenuID:       menuID,
		SectionID:    sectionID,
		DishID:       dishID,
		RawImage:     raw,
		ContentType:  contentType,
	})
	s.logBOGroupMenuV2AITrace("generate job dispatched restaurant=%d menu=%d section=%d dish=%d", a.ActiveRestaurantID, menuID, sectionID, dishID)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "AI image generation started",
		"dish_id": dishID,
	})
}

func (s *Server) runBOGroupMenuV2AIImageJob(job boGroupMenuV2AIImageJob) {
	ctx, cancel := context.WithTimeout(context.Background(), s.openAIRequestTimeout())
	defer cancel()
	s.logBOGroupMenuV2AITrace("job start %s inputBytes=%d inputType=%s", boGroupMenuV2AIJobLabel(job), len(job.RawImage), job.ContentType)

	s.logBOGroupMenuV2AITrace("job waiting for worker %s queueLen=%d queueCap=%d", boGroupMenuV2AIJobLabel(job), len(s.groupMenusV2AIQueue), cap(s.groupMenusV2AIQueue))
	if err := s.acquireBOGroupMenuV2AIWorker(ctx); err != nil {
		s.logBOGroupMenuV2AITrace("job worker acquire error %s err=%v", boGroupMenuV2AIJobLabel(job), err)
		s.failBOGroupMenuV2AIImageJob(job, "AI generation queue timeout")
		return
	}
	defer s.releaseBOGroupMenuV2AIWorker()
	s.logBOGroupMenuV2AITrace("job worker acquired %s queueLen=%d queueCap=%d", boGroupMenuV2AIJobLabel(job), len(s.groupMenusV2AIQueue), cap(s.groupMenusV2AIQueue))

	s.logBOGroupMenuV2AITrace("job ai call start %s model=%s", boGroupMenuV2AIJobLabel(job), s.openAIImageEditModel())
	output, err := s.callOpenAIImageEdit(ctx, job.RawImage, job.ContentType)
	if err != nil {
		s.logBOGroupMenuV2AITrace("job ai call error %s err=%v", boGroupMenuV2AIJobLabel(job), err)
		s.failBOGroupMenuV2AIImageJob(job, aiFailureMessage("AI image generation failed", err))
		return
	}
	s.logBOGroupMenuV2AITrace("job ai call done %s outputBytes=%d", boGroupMenuV2AIJobLabel(job), len(output))
	if len(output) == 0 {
		s.logBOGroupMenuV2AITrace("job ai empty output %s", boGroupMenuV2AIJobLabel(job))
		s.failBOGroupMenuV2AIImageJob(job, "AI image generation returned empty image")
		return
	}
	if len(output) > s.openAIMaxOutputBytes() {
		s.logBOGroupMenuV2AITrace("job ai output too large %s bytes=%d max=%d", boGroupMenuV2AIJobLabel(job), len(output), s.openAIMaxOutputBytes())
		s.failBOGroupMenuV2AIImageJob(job, "AI image is too large")
		return
	}

	uploadType := strings.TrimSpace(http.DetectContentType(output))
	lowerUploadType := strings.ToLower(uploadType)
	allowedOutputTypes := map[string]bool{
		"image/webp": true,
		"image/png":  true,
		"image/jpeg": true,
	}
	if !allowedOutputTypes[lowerUploadType] {
		s.logBOGroupMenuV2AITrace("job reject output content-type %s uploadType=%s", boGroupMenuV2AIJobLabel(job), uploadType)
		s.failBOGroupMenuV2AIImageJob(job, "AI image format not supported")
		return
	}
	ext := fileExtForContentType(lowerUploadType)
	if ext == "" {
		ext = ".webp"
	}
	generationVersion := strconv.FormatInt(time.Now().UTC().UnixMilli(), 10)
	fileName := strconv.FormatInt(job.DishID, 10) + "-" + generationVersion + ext
	objectPath := path.Join(
		strconv.Itoa(job.RestaurantID),
		"pictures",
		strconv.FormatInt(job.MenuID, 10),
		"ai-generated",
		fileName,
	)
	s.logBOGroupMenuV2AITrace("job bunny upload start %s objectPath=%s contentType=%s bytes=%d generationVersion=%s", boGroupMenuV2AIJobLabel(job), objectPath, uploadType, len(output), generationVersion)
	if err := s.bunnyPut(ctx, objectPath, output, uploadType); err != nil {
		s.logBOGroupMenuV2AITrace("job bunny upload error %s objectPath=%s err=%v", boGroupMenuV2AIJobLabel(job), objectPath, err)
		s.failBOGroupMenuV2AIImageJob(job, "Failed uploading generated image")
		return
	}
	s.logBOGroupMenuV2AITrace("job bunny upload done %s objectPath=%s", boGroupMenuV2AIJobLabel(job), objectPath)

	fullURL := s.bunnyPullURL(objectPath)
	s.logBOGroupMenuV2AITrace("job db save start %s fullURL=%s", boGroupMenuV2AIJobLabel(job), fullURL)
	_, err = s.db.ExecContext(ctx, `
		UPDATE group_menu_section_dishes_v2
		SET ai_requested_img = 1,
		    ai_generating_img = 0,
		    ai_generated_img = ?,
		    foto_path = ?
		WHERE id = ? AND section_id = ? AND menu_id = ? AND restaurant_id = ?
	`, fullURL, objectPath, job.DishID, job.SectionID, job.MenuID, job.RestaurantID)
	if err != nil {
		s.logBOGroupMenuV2AITrace("job db save error %s err=%v", boGroupMenuV2AIJobLabel(job), err)
		s.failBOGroupMenuV2AIImageJob(job, "Failed saving generated image")
		return
	}
	s.logBOGroupMenuV2AITrace("job db save done %s", boGroupMenuV2AIJobLabel(job))

	s.broadcastBOGroupMenuV2AIEvent(job.RestaurantID, job.MenuID, "ai_image_completed", map[string]any{
		"dish_id":          job.DishID,
		"section_id":       job.SectionID,
		"ai_requested":     true,
		"ai_generating":    false,
		"ai_generated_img": fullURL,
		"foto_url":         fullURL,
	})
	s.logBOGroupMenuV2AITrace("job completed broadcast sent %s", boGroupMenuV2AIJobLabel(job))
}

func (s *Server) failBOGroupMenuV2AIImageJob(job boGroupMenuV2AIImageJob, message string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s.logBOGroupMenuV2AITrace("job failed %s message=%q", boGroupMenuV2AIJobLabel(job), message)

	_, _ = s.db.ExecContext(ctx, `
		UPDATE group_menu_section_dishes_v2
		SET ai_generating_img = 0
		WHERE id = ? AND section_id = ? AND menu_id = ? AND restaurant_id = ?
	`, job.DishID, job.SectionID, job.MenuID, job.RestaurantID)

	s.broadcastBOGroupMenuV2AIEvent(job.RestaurantID, job.MenuID, "ai_image_failed", map[string]any{
		"dish_id":       job.DishID,
		"section_id":    job.SectionID,
		"ai_requested":  true,
		"ai_generating": false,
		"message":       message,
	})
}

func aiFailureMessage(base string, err error) string {
	base = strings.TrimSpace(base)
	if err == nil {
		return base
	}
	detail := strings.TrimSpace(err.Error())
	if detail == "" {
		return base
	}
	detail = strings.Join(strings.Fields(detail), " ")
	if len(detail) > 280 {
		detail = detail[:277] + "..."
	}
	if base == "" {
		return detail
	}
	return base + ": " + detail
}

func (s *Server) acquireBOGroupMenuV2AIWorker(ctx context.Context) error {
	if s.groupMenusV2AIQueue == nil {
		s.logBOGroupMenuV2AITrace("worker queue unavailable")
		return errors.New("ai worker queue unavailable")
	}
	select {
	case s.groupMenusV2AIQueue <- struct{}{}:
		s.logBOGroupMenuV2AITrace("worker acquired queueLen=%d queueCap=%d", len(s.groupMenusV2AIQueue), cap(s.groupMenusV2AIQueue))
		return nil
	case <-ctx.Done():
		s.logBOGroupMenuV2AITrace("worker acquire context done err=%v", ctx.Err())
		return ctx.Err()
	}
}

func (s *Server) releaseBOGroupMenuV2AIWorker() {
	if s.groupMenusV2AIQueue == nil {
		return
	}
	select {
	case <-s.groupMenusV2AIQueue:
		s.logBOGroupMenuV2AITrace("worker released queueLen=%d queueCap=%d", len(s.groupMenusV2AIQueue), cap(s.groupMenusV2AIQueue))
	default:
		s.logBOGroupMenuV2AITrace("worker release skipped empty queue")
	}
}

func (s *Server) openAIRequestTimeout() time.Duration {
	if s.cfg.OpenAITimeout > 0 {
		return s.cfg.OpenAITimeout
	}
	return 180 * time.Second
}

func (s *Server) openAIFetchTimeout() time.Duration {
	if s.cfg.OpenAIFetchTimeout > 0 {
		return s.cfg.OpenAIFetchTimeout
	}
	return 30 * time.Second
}

func (s *Server) openAIInputMaxBytes() int {
	if s.cfg.OpenAIMaxInputBytes > 0 {
		return s.cfg.OpenAIMaxInputBytes
	}
	return 8 << 20
}

func (s *Server) openAIMaxOutputBytes() int {
	if s.cfg.OpenAIMaxOutputBytes > 0 {
		return s.cfg.OpenAIMaxOutputBytes
	}
	return 16 << 20
}

func (s *Server) openAIImageEditModel() string {
	model := strings.TrimSpace(s.cfg.OpenAIImageEditModel)
	if model == "" {
		return "openai/gpt-image-1.5/edit"
	}
	return model
}

func (s *Server) openAIImageEditURL() string {
	rawURL := strings.TrimSpace(s.cfg.OpenAIImageEditURL)
	if rawURL == "" {
		return "https://api.wavespeed.ai/api/v3/openai/gpt-image-1.5/edit"
	}
	return rawURL
}

func (s *Server) waveSpeedBaseURL() string {
	editURL := strings.TrimSpace(s.openAIImageEditURL())
	parsed, err := url.Parse(editURL)
	if err != nil {
		return "https://api.wavespeed.ai"
	}
	scheme := strings.TrimSpace(parsed.Scheme)
	host := strings.TrimSpace(parsed.Host)
	if scheme == "" || host == "" {
		return "https://api.wavespeed.ai"
	}
	return scheme + "://" + host
}

func (s *Server) waveSpeedStatusFetchURL(requestID string) string {
	requestID = strings.TrimSpace(requestID)
	return strings.TrimRight(s.waveSpeedBaseURL(), "/") + "/api/v3/predictions/" + requestID
}

func (s *Server) waveSpeedResultFetchURL(requestID string) string {
	requestID = strings.TrimSpace(requestID)
	return strings.TrimRight(s.waveSpeedBaseURL(), "/") + "/api/v3/predictions/" + requestID + "/result"
}

func (s *Server) callOpenAIImageEdit(ctx context.Context, input []byte, inputContentType string) ([]byte, error) {
	if len(input) == 0 {
		s.logBOGroupMenuV2AITrace("ai call rejected empty input")
		return nil, errors.New("empty input image")
	}
	apiKey := strings.TrimSpace(s.cfg.OpenAIAPIKey)
	if apiKey == "" {
		s.logBOGroupMenuV2AITrace("ai call rejected key missing")
		return nil, errors.New("ai key missing")
	}
	contentType := strings.TrimSpace(inputContentType)
	if contentType == "" || !strings.HasPrefix(strings.ToLower(contentType), "image/") {
		contentType = strings.TrimSpace(http.DetectContentType(input))
	}
	if contentType == "" || !strings.HasPrefix(strings.ToLower(contentType), "image/") {
		contentType = "image/webp"
	}
	s.logBOGroupMenuV2AITrace(
		"wavespeed call start model=%s url=%s inputType=%s inputBytes=%d timeout=%s",
		s.openAIImageEditModel(),
		s.openAIImageEditURL(),
		contentType,
		len(input),
		s.openAIRequestTimeout(),
	)

	body := map[string]any{
		"enable_base64_output": false,
		"enable_sync_mode":     false,
		"images": []string{
			"data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(input),
		},
		"input_fidelity": "high",
		"output_format":  "jpeg",
		"prompt":         boGroupMenuV2AIPrompt,
		"quality":        "high",
		"size":           "1024*1024",
	}
	rawBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	payload, statusCode, responseContentType, err := s.doAIProviderRequest(ctx, http.MethodPost, s.openAIImageEditURL(), rawBody, "application/json")
	if err != nil {
		s.logBOGroupMenuV2AITrace("wavespeed call http error err=%v", err)
		return nil, err
	}
	s.logBOGroupMenuV2AITrace(
		"wavespeed call response status=%d contentType=%q bytes=%d",
		statusCode,
		responseContentType,
		len(payload),
	)

	if statusCode < 200 || statusCode >= 300 {
		return nil, fmt.Errorf("wavespeed request failed (%d): %s", statusCode, strings.TrimSpace(string(payload)))
	}

	lowerResponseType := strings.ToLower(strings.TrimSpace(responseContentType))
	if strings.HasPrefix(lowerResponseType, "image/") {
		s.logBOGroupMenuV2AITrace("wavespeed call returned direct image contentType=%s bytes=%d", lowerResponseType, len(payload))
		return payload, nil
	}

	s.logBOGroupMenuV2AITrace("wavespeed call returned json payload bytes=%d", len(payload))
	return s.extractOpenAIImagePayload(ctx, payload)
}

func (s *Server) extractOpenAIImagePayload(ctx context.Context, payload []byte) ([]byte, error) {
	var root any
	if err := json.Unmarshal(payload, &root); err != nil {
		s.logBOGroupMenuV2AITrace("ai extract json parse error bytes=%d err=%v", len(payload), err)
		return nil, errors.New("invalid AI JSON response")
	}

	if out, found, err := s.extractAIImageFromParsedPayload(ctx, root); err != nil {
		s.logBOGroupMenuV2AITrace("ai extract image parse error err=%v", err)
		return nil, err
	} else if found {
		return out, nil
	}

	requestID, status, errMessage := waveSpeedEnvelopeFromParsedPayload(root)
	if requestID != "" {
		switch status {
		case "completed", "pending", "processing", "in_progress", "queued", "running":
			s.logBOGroupMenuV2AITrace("wavespeed extract polling id=%s status=%s", requestID, status)
			return s.pollWaveSpeedImagePayload(ctx, requestID)
		}
	}
	if status == "failed" || status == "error" {
		if errMessage == "" {
			errMessage = "AI provider generation failed"
		}
		s.logBOGroupMenuV2AITrace("wavespeed extract failed id=%s message=%q", requestID, errMessage)
		return nil, errors.New(errMessage)
	}

	s.logBOGroupMenuV2AITrace("ai extract failed no image payload bytes=%d", len(payload))
	return nil, errors.New("AI response does not include image data")
}

func (s *Server) extractAIImageFromParsedPayload(ctx context.Context, root any) ([]byte, bool, error) {
	if raw, found := findOpenAIImageBytes(root); found {
		if len(raw) == 0 {
			return nil, false, errors.New("empty AI image payload")
		}
		s.logBOGroupMenuV2AITrace("ai extract bytes payload found bytes=%d", len(raw))
		return raw, true, nil
	}

	if b64 := findOpenAIImageBase64(root); b64 != "" {
		decoded, err := decodeOpenAIBase64Image(b64)
		if err != nil {
			return nil, false, err
		}
		if len(decoded) == 0 {
			return nil, false, errors.New("empty decoded AI image")
		}
		s.logBOGroupMenuV2AITrace("ai extract base64 decoded bytes=%d", len(decoded))
		return decoded, true, nil
	}

	if imageURL := findOpenAIImageURL(root); imageURL != "" {
		s.logBOGroupMenuV2AITrace("ai extract using download url=%s", imageURL)
		if requestID := s.waveSpeedRequestIDFromURL(imageURL); requestID != "" {
			s.logBOGroupMenuV2AITrace("ai extract url mapped to wavespeed request id=%s", requestID)
			raw, err := s.pollWaveSpeedImagePayload(ctx, requestID)
			if err == nil {
				return raw, true, nil
			}
			s.logBOGroupMenuV2AITrace("ai extract wavespeed request poll from url failed id=%s err=%v", requestID, err)
		}
		raw, err := s.downloadOpenAIImageURL(ctx, imageURL)
		if err != nil {
			return nil, false, err
		}
		return raw, true, nil
	}

	return nil, false, nil
}

func waveSpeedEnvelopeFromParsedPayload(root any) (requestID string, status string, errMessage string) {
	requestID = findWaveSpeedRequestID(root)
	status = findWaveSpeedStatus(root)
	errMessage = findWaveSpeedErrorMessage(root)
	return requestID, status, errMessage
}

func normalizeWaveSpeedStatus(raw string) string {
	status := strings.ToLower(strings.TrimSpace(raw))
	switch status {
	case "in-progress":
		status = "in_progress"
	case "success", "succeeded", "done":
		status = "completed"
	}
	if waveSpeedKnownStatuses[status] {
		return status
	}
	return ""
}

func findWaveSpeedStatus(node any) string {
	switch v := node.(type) {
	case map[string]any:
		for _, key := range []string{"status", "state", "phase"} {
			if status := normalizeWaveSpeedStatus(anyToStringWS(v[key])); status != "" {
				return status
			}
		}
		if child, ok := v["data"]; ok {
			if status := findWaveSpeedStatus(child); status != "" {
				return status
			}
		}
		for _, child := range v {
			if status := findWaveSpeedStatus(child); status != "" {
				return status
			}
		}
	case []any:
		for _, child := range v {
			if status := findWaveSpeedStatus(child); status != "" {
				return status
			}
		}
	}
	return ""
}

func looksLikeWaveSpeedRequestID(raw string) bool {
	v := strings.TrimSpace(raw)
	if v == "" {
		return false
	}
	lower := strings.ToLower(v)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return false
	}
	return true
}

func findWaveSpeedRequestID(node any) string {
	switch v := node.(type) {
	case map[string]any:
		for _, key := range []string{"id", "prediction_id", "request_id", "task_id"} {
			if candidate := strings.TrimSpace(anyToStringWS(v[key])); looksLikeWaveSpeedRequestID(candidate) {
				return candidate
			}
		}
		if child, ok := v["data"]; ok {
			if id := findWaveSpeedRequestID(child); id != "" {
				return id
			}
		}
		for _, child := range v {
			if id := findWaveSpeedRequestID(child); id != "" {
				return id
			}
		}
	case []any:
		for _, child := range v {
			if id := findWaveSpeedRequestID(child); id != "" {
				return id
			}
		}
	}
	return ""
}

func extractWaveSpeedError(raw any) string {
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v)
	case map[string]any:
		for _, key := range []string{"message", "error", "detail"} {
			if value := strings.TrimSpace(anyToStringWS(v[key])); value != "" {
				return value
			}
		}
	case []any:
		for _, child := range v {
			if value := extractWaveSpeedError(child); value != "" {
				return value
			}
		}
	}
	return ""
}

func findWaveSpeedErrorMessage(node any) string {
	switch v := node.(type) {
	case map[string]any:
		if msg := extractWaveSpeedError(v["error"]); msg != "" {
			return msg
		}
		for _, key := range []string{"message", "detail", "reason"} {
			msg := strings.TrimSpace(anyToStringWS(v[key]))
			if msg == "" {
				continue
			}
			lower := strings.ToLower(msg)
			if lower != "ok" && lower != "success" {
				return msg
			}
		}
		if child, ok := v["data"]; ok {
			if msg := findWaveSpeedErrorMessage(child); msg != "" {
				return msg
			}
		}
		for _, child := range v {
			if msg := findWaveSpeedErrorMessage(child); msg != "" {
				return msg
			}
		}
	case []any:
		for _, child := range v {
			if msg := findWaveSpeedErrorMessage(child); msg != "" {
				return msg
			}
		}
	}
	return ""
}

func anyToStringWS(v any) string {
	switch value := v.(type) {
	case string:
		return value
	case json.Number:
		return value.String()
	case float64:
		return strconv.FormatInt(int64(value), 10)
	case float32:
		return strconv.FormatInt(int64(value), 10)
	case int:
		return strconv.Itoa(value)
	case int64:
		return strconv.FormatInt(value, 10)
	case int32:
		return strconv.FormatInt(int64(value), 10)
	case uint:
		return strconv.FormatUint(uint64(value), 10)
	case uint64:
		return strconv.FormatUint(value, 10)
	case uint32:
		return strconv.FormatUint(uint64(value), 10)
	default:
		return ""
	}
}

func (s *Server) pollWaveSpeedImagePayload(parent context.Context, requestID string) ([]byte, error) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return nil, errors.New("WaveSpeed request id missing")
	}
	ctx, cancel := context.WithTimeout(parent, s.openAIRequestTimeout())
	defer cancel()

	interval := 1200 * time.Millisecond
	if fetchTimeout := s.openAIFetchTimeout(); fetchTimeout > 0 {
		candidate := fetchTimeout / 18
		if candidate >= 500*time.Millisecond && candidate <= 5*time.Second {
			interval = candidate
		}
	}
	statusURL := s.waveSpeedStatusFetchURL(requestID)
	resultURL := s.waveSpeedResultFetchURL(requestID)
	s.logBOGroupMenuV2AITrace("wavespeed poll start id=%s statusURL=%s resultURL=%s interval=%s", requestID, statusURL, resultURL, interval)
	statusEndpointUnavailable := false

	for attempt := 1; ; attempt++ {
		status := ""
		errMessage := ""
		statusBytes := 0
		if !statusEndpointUnavailable {
			statusPayload, statusCode, statusContentType, err := s.doAIProviderRequest(ctx, http.MethodGet, statusURL, nil, "")
			if err != nil {
				s.logBOGroupMenuV2AITrace("wavespeed poll status request error id=%s attempt=%d err=%v", requestID, attempt, err)
				return nil, err
			}
			if statusCode < 200 || statusCode >= 300 {
				if statusCode == http.StatusNotFound {
					statusEndpointUnavailable = true
					s.logBOGroupMenuV2AITrace("wavespeed poll status endpoint unavailable id=%s attempt=%d status=%d", requestID, attempt, statusCode)
				} else {
					s.logBOGroupMenuV2AITrace("wavespeed poll status request bad status id=%s attempt=%d status=%d", requestID, attempt, statusCode)
					return nil, fmt.Errorf("wavespeed status request failed (%d): %s", statusCode, strings.TrimSpace(string(statusPayload)))
				}
			}
			statusBytes = len(statusPayload)
			if !statusEndpointUnavailable {
				if strings.HasPrefix(strings.ToLower(statusContentType), "image/") {
					s.logBOGroupMenuV2AITrace("wavespeed poll status returned direct image id=%s attempt=%d bytes=%d", requestID, attempt, len(statusPayload))
					return statusPayload, nil
				}

				var statusRoot any
				if err := json.Unmarshal(statusPayload, &statusRoot); err != nil {
					s.logBOGroupMenuV2AITrace("wavespeed poll status json parse error id=%s attempt=%d err=%v", requestID, attempt, err)
					return nil, errors.New("invalid WaveSpeed status JSON response")
				}
				if out, found, err := s.extractAIImageFromParsedPayload(ctx, statusRoot); err != nil {
					return nil, err
				} else if found {
					s.logBOGroupMenuV2AITrace("wavespeed poll status image found id=%s attempt=%d bytes=%d", requestID, attempt, len(out))
					return out, nil
				}

				_, status, errMessage = waveSpeedEnvelopeFromParsedPayload(statusRoot)
			}
		}
		if status == "" {
			s.logBOGroupMenuV2AITrace("wavespeed poll status unknown id=%s attempt=%d bytes=%d", requestID, attempt, statusBytes)
		}
		switch status {
		case "failed", "error", "cancelled", "canceled":
			if errMessage == "" {
				errMessage = "WaveSpeed generation failed"
			}
			s.logBOGroupMenuV2AITrace("wavespeed poll failed id=%s attempt=%d status=%s message=%q", requestID, attempt, status, errMessage)
			return nil, errors.New(errMessage)
		case "completed":
			resultPayload, resultCode, resultContentType, err := s.doAIProviderRequest(ctx, http.MethodGet, resultURL, nil, "")
			if err != nil {
				s.logBOGroupMenuV2AITrace("wavespeed poll result request error id=%s attempt=%d err=%v", requestID, attempt, err)
				return nil, err
			}
			if resultCode < 200 || resultCode >= 300 {
				s.logBOGroupMenuV2AITrace("wavespeed poll result request bad status id=%s attempt=%d status=%d", requestID, attempt, resultCode)
				return nil, fmt.Errorf("wavespeed result request failed (%d): %s", resultCode, strings.TrimSpace(string(resultPayload)))
			}
			if strings.HasPrefix(strings.ToLower(resultContentType), "image/") {
				s.logBOGroupMenuV2AITrace("wavespeed poll result returned direct image id=%s attempt=%d bytes=%d", requestID, attempt, len(resultPayload))
				return resultPayload, nil
			}
			var resultRoot any
			if err := json.Unmarshal(resultPayload, &resultRoot); err != nil {
				s.logBOGroupMenuV2AITrace("wavespeed poll result json parse error id=%s attempt=%d err=%v", requestID, attempt, err)
				return nil, errors.New("invalid WaveSpeed result JSON response")
			}
			if out, found, err := s.extractAIImageFromDownloadedJSON(ctx, resultRoot, resultURL); err != nil {
				return nil, err
			} else if found {
				s.logBOGroupMenuV2AITrace("wavespeed poll result image found id=%s attempt=%d bytes=%d", requestID, attempt, len(out))
				return out, nil
			}
			if errMessage == "" {
				errMessage = extractWaveSpeedError(resultRoot)
			}
			if errMessage == "" {
				errMessage = "WaveSpeed completed without output image"
			}
			s.logBOGroupMenuV2AITrace("wavespeed poll result missing image id=%s attempt=%d", requestID, attempt)
			return nil, errors.New(errMessage)
		default:
			// Some provider responses omit status but still expose result payload.
			if status == "" {
				resultPayload, resultCode, resultContentType, err := s.doAIProviderRequest(ctx, http.MethodGet, resultURL, nil, "")
				if err != nil {
					s.logBOGroupMenuV2AITrace("wavespeed poll fallback result request error id=%s attempt=%d err=%v", requestID, attempt, err)
				} else if resultCode >= 200 && resultCode < 300 {
					if strings.HasPrefix(strings.ToLower(resultContentType), "image/") {
						s.logBOGroupMenuV2AITrace("wavespeed poll fallback result direct image id=%s attempt=%d bytes=%d", requestID, attempt, len(resultPayload))
						return resultPayload, nil
					}
					var resultRoot any
					if err := json.Unmarshal(resultPayload, &resultRoot); err == nil {
						if out, found, err := s.extractAIImageFromDownloadedJSON(ctx, resultRoot, resultURL); err != nil {
							return nil, err
						} else if found {
							s.logBOGroupMenuV2AITrace("wavespeed poll fallback result image found id=%s attempt=%d bytes=%d", requestID, attempt, len(out))
							return out, nil
						}
					}
				}
			}
			s.logBOGroupMenuV2AITrace("wavespeed poll waiting id=%s attempt=%d status=%s", requestID, attempt, status)
		}

		select {
		case <-ctx.Done():
			s.logBOGroupMenuV2AITrace("wavespeed poll timeout id=%s attempt=%d err=%v", requestID, attempt, ctx.Err())
			return nil, fmt.Errorf("wavespeed polling timeout: %w", ctx.Err())
		case <-time.After(interval):
		}
	}
}

func (s *Server) doAIProviderRequest(ctx context.Context, method string, requestURL string, body []byte, contentType string) ([]byte, int, string, error) {
	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, requestURL, reader)
	if err != nil {
		return nil, 0, "", err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(s.cfg.OpenAIAPIKey))
	if strings.TrimSpace(contentType) != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json, image/webp, image/png, image/jpeg, image/*")

	cli := &http.Client{Timeout: s.openAIRequestTimeout()}
	res, err := cli.Do(req)
	if err != nil {
		return nil, 0, "", err
	}
	defer res.Body.Close()

	maxOut := s.openAIMaxOutputBytes()
	payload, err := io.ReadAll(io.LimitReader(res.Body, int64(maxOut)+1))
	if err != nil {
		return nil, 0, "", err
	}
	if len(payload) > maxOut {
		return nil, 0, "", errors.New("AI provider response too large")
	}
	return payload, res.StatusCode, strings.TrimSpace(res.Header.Get("Content-Type")), nil
}

func (s *Server) downloadOpenAIImageURL(parent context.Context, imageURL string) ([]byte, error) {
	if !strings.HasPrefix(imageURL, "http://") && !strings.HasPrefix(imageURL, "https://") {
		s.logBOGroupMenuV2AITrace("ai download invalid url=%q", imageURL)
		return nil, errors.New("invalid AI image URL")
	}
	s.logBOGroupMenuV2AITrace("ai download start url=%s timeout=%s", imageURL, s.openAIFetchTimeout())
	ctx, cancel := context.WithTimeout(parent, s.openAIFetchTimeout())
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		return nil, err
	}
	if s.shouldAttachAIProviderAuth(imageURL) {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(s.cfg.OpenAIAPIKey))
		s.logBOGroupMenuV2AITrace("ai download auth attached url=%s", imageURL)
	}
	req.Header.Set("Accept", "application/json, image/webp, image/png, image/jpeg, image/*")

	cli := &http.Client{Timeout: s.openAIFetchTimeout()}
	res, err := cli.Do(req)
	if err != nil {
		s.logBOGroupMenuV2AITrace("ai download http error url=%s err=%v", imageURL, err)
		return nil, err
	}
	defer res.Body.Close()

	maxOut := s.openAIMaxOutputBytes()
	payload, err := io.ReadAll(io.LimitReader(res.Body, int64(maxOut)+1))
	if err != nil {
		s.logBOGroupMenuV2AITrace("ai download read error url=%s err=%v", imageURL, err)
		return nil, err
	}
	if len(payload) > maxOut {
		s.logBOGroupMenuV2AITrace("ai download too large url=%s bytes=%d max=%d", imageURL, len(payload), maxOut)
		return nil, errors.New("AI image download too large")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		errBody := strings.TrimSpace(string(payload))
		if len(errBody) > 280 {
			errBody = errBody[:277] + "..."
		}
		s.logBOGroupMenuV2AITrace("ai download bad status url=%s status=%d body=%q", imageURL, res.StatusCode, errBody)
		if errBody != "" {
			return nil, fmt.Errorf("AI image download failed (%d): %s", res.StatusCode, errBody)
		}
		return nil, fmt.Errorf("AI image download failed (%d)", res.StatusCode)
	}
	responseContentType := strings.TrimSpace(res.Header.Get("Content-Type"))
	if isLikelyJSONPayload(responseContentType, payload) {
		s.logBOGroupMenuV2AITrace("ai download returned json payload url=%s bytes=%d contentType=%q", imageURL, len(payload), responseContentType)
		var root any
		if err := json.Unmarshal(payload, &root); err != nil {
			s.logBOGroupMenuV2AITrace("ai download json parse error url=%s err=%v", imageURL, err)
		} else {
			if out, found, err := s.extractAIImageFromDownloadedJSON(ctx, root, imageURL); err != nil {
				return nil, err
			} else if found {
				return out, nil
			}
			return nil, errors.New("AI image download JSON response has no output image")
		}
	}
	s.logBOGroupMenuV2AITrace("ai download done url=%s bytes=%d contentType=%q", imageURL, len(payload), strings.TrimSpace(res.Header.Get("Content-Type")))
	return payload, nil
}

func isLikelyJSONPayload(contentType string, payload []byte) bool {
	lower := strings.ToLower(strings.TrimSpace(contentType))
	if strings.Contains(lower, "json") {
		return true
	}
	trimmed := strings.TrimSpace(string(payload))
	return strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")
}

func sameNormalizedURL(a string, b string) bool {
	left, errLeft := url.Parse(strings.TrimSpace(a))
	right, errRight := url.Parse(strings.TrimSpace(b))
	if errLeft != nil || errRight != nil {
		return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
	}
	return strings.EqualFold(strings.TrimSpace(left.Scheme), strings.TrimSpace(right.Scheme)) &&
		strings.EqualFold(strings.TrimSpace(left.Host), strings.TrimSpace(right.Host)) &&
		strings.TrimRight(strings.TrimSpace(left.Path), "/") == strings.TrimRight(strings.TrimSpace(right.Path), "/") &&
		strings.TrimSpace(left.RawQuery) == strings.TrimSpace(right.RawQuery)
}

func (s *Server) extractAIImageFromDownloadedJSON(ctx context.Context, root any, sourceURL string) ([]byte, bool, error) {
	sourceRequestID := s.waveSpeedRequestIDFromURL(sourceURL)

	if raw, found := findOpenAIImageBytes(root); found {
		if len(raw) == 0 {
			return nil, false, errors.New("empty AI image payload")
		}
		return raw, true, nil
	}
	if b64 := findOpenAIImageBase64(root); b64 != "" {
		decoded, err := decodeOpenAIBase64Image(b64)
		if err != nil {
			return nil, false, err
		}
		if len(decoded) == 0 {
			return nil, false, errors.New("empty decoded AI image")
		}
		return decoded, true, nil
	}
	if nestedURL := findOpenAIImageURL(root); nestedURL != "" {
		if sameNormalizedURL(nestedURL, sourceURL) {
			s.logBOGroupMenuV2AITrace("ai download json skip self-referential url=%s", nestedURL)
		} else {
			if requestID := s.waveSpeedRequestIDFromURL(nestedURL); requestID != "" {
				if sourceRequestID != "" && requestID == sourceRequestID {
					s.logBOGroupMenuV2AITrace("ai download json skip recursive wavespeed poll id=%s source=%s nested=%s", requestID, sourceURL, nestedURL)
					return nil, false, nil
				}
				s.logBOGroupMenuV2AITrace("ai download json mapped to wavespeed request id=%s", requestID)
				raw, err := s.pollWaveSpeedImagePayload(ctx, requestID)
				if err == nil {
					return raw, true, nil
				}
				s.logBOGroupMenuV2AITrace("ai download json wavespeed poll failed id=%s err=%v", requestID, err)
			}
			raw, err := s.downloadOpenAIImageURL(ctx, nestedURL)
			if err != nil {
				return nil, false, err
			}
			return raw, true, nil
		}
	}

	requestID, status, errMessage := waveSpeedEnvelopeFromParsedPayload(root)
	switch status {
	case "completed", "pending", "processing", "in_progress", "queued", "running":
		if requestID == "" {
			return nil, false, nil
		}
		if sourceRequestID != "" && requestID == sourceRequestID {
			s.logBOGroupMenuV2AITrace("ai download json status references same request id=%s; continue outer polling", requestID)
			return nil, false, nil
		}
		s.logBOGroupMenuV2AITrace("ai download json polling wavespeed id=%s status=%s", requestID, status)
		raw, err := s.pollWaveSpeedImagePayload(ctx, requestID)
		if err != nil {
			return nil, false, err
		}
		return raw, true, nil
	case "failed", "error":
		if errMessage == "" {
			errMessage = "AI provider generation failed"
		}
		return nil, false, errors.New(errMessage)
	}

	return nil, false, nil
}

func (s *Server) shouldAttachAIProviderAuth(rawURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host == "" {
		return false
	}
	base, err := url.Parse(strings.TrimSpace(s.waveSpeedBaseURL()))
	if err == nil {
		baseHost := strings.ToLower(strings.TrimSpace(base.Hostname()))
		if baseHost != "" && host == baseHost && strings.HasPrefix(strings.ToLower(strings.TrimSpace(parsed.Path)), "/api/") {
			return true
		}
	}
	return host == "api.wavespeed.ai" && strings.HasPrefix(strings.ToLower(strings.TrimSpace(parsed.Path)), "/api/")
}

func (s *Server) waveSpeedRequestIDFromURL(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host == "" {
		return ""
	}
	base, err := url.Parse(strings.TrimSpace(s.waveSpeedBaseURL()))
	if err != nil {
		return ""
	}
	baseHost := strings.ToLower(strings.TrimSpace(base.Hostname()))
	if baseHost == "" || host != baseHost {
		return ""
	}
	pathParts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(pathParts) < 4 {
		return ""
	}
	// /api/v3/predictions/{id}[/result]
	if !strings.EqualFold(pathParts[0], "api") || !strings.EqualFold(pathParts[2], "predictions") {
		return ""
	}
	id := strings.TrimSpace(pathParts[3])
	if id == "" {
		return ""
	}
	return id
}

func findOpenAIImageBytes(node any) ([]byte, bool) {
	switch v := node.(type) {
	case map[string]any:
		for _, key := range []string{"bytes", "image_bytes"} {
			arr, ok := v[key].([]any)
			if !ok {
				continue
			}
			if raw, ok := byteSliceFromJSONArray(arr); ok {
				return raw, true
			}
		}
		for _, key := range []string{"data", "output", "result", "results", "images", "image", "response"} {
			if child, ok := v[key]; ok {
				if raw, found := findOpenAIImageBytes(child); found {
					return raw, true
				}
			}
		}
		for _, child := range v {
			if raw, found := findOpenAIImageBytes(child); found {
				return raw, true
			}
		}
	case []any:
		for _, child := range v {
			if raw, found := findOpenAIImageBytes(child); found {
				return raw, true
			}
		}
	}
	return nil, false
}

func findOpenAIImageBase64(node any) string {
	switch v := node.(type) {
	case map[string]any:
		for _, key := range []string{"b64_json", "base64", "image_base64", "output_b64"} {
			raw, ok := v[key].(string)
			if !ok {
				continue
			}
			raw = strings.TrimSpace(raw)
			if raw != "" {
				return raw
			}
		}
		for _, key := range []string{"data", "output", "result", "results", "images", "image", "response"} {
			if child, ok := v[key]; ok {
				if b64 := findOpenAIImageBase64(child); b64 != "" {
					return b64
				}
			}
		}
		for _, child := range v {
			if b64 := findOpenAIImageBase64(child); b64 != "" {
				return b64
			}
		}
	case []any:
		for _, child := range v {
			if b64 := findOpenAIImageBase64(child); b64 != "" {
				return b64
			}
		}
	}
	return ""
}

func findOpenAIImageURL(node any) string {
	switch v := node.(type) {
	case string:
		raw := strings.TrimSpace(v)
		if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
			return raw
		}
	case map[string]any:
		for _, key := range []string{"url", "image_url", "output_url"} {
			raw, ok := v[key].(string)
			if !ok {
				continue
			}
			raw = strings.TrimSpace(raw)
			if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
				return raw
			}
		}
		for _, key := range []string{"data", "output", "result", "results", "images", "image", "response"} {
			if child, ok := v[key]; ok {
				if url := findOpenAIImageURL(child); url != "" {
					return url
				}
			}
		}
		for _, child := range v {
			if url := findOpenAIImageURL(child); url != "" {
				return url
			}
		}
	case []any:
		for _, child := range v {
			if url := findOpenAIImageURL(child); url != "" {
				return url
			}
		}
	}
	return ""
}

func decodeOpenAIBase64Image(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(strings.ToLower(raw), "data:") {
		if idx := strings.Index(raw, ","); idx >= 0 && idx+1 < len(raw) {
			raw = raw[idx+1:]
		}
	}
	raw = strings.ReplaceAll(raw, "\n", "")
	raw = strings.ReplaceAll(raw, "\r", "")
	raw = strings.ReplaceAll(raw, " ", "")
	if raw == "" {
		return nil, errors.New("empty base64 payload")
	}
	if decoded, err := base64.StdEncoding.DecodeString(raw); err == nil {
		return decoded, nil
	}
	if decoded, err := base64.RawStdEncoding.DecodeString(raw); err == nil {
		return decoded, nil
	}
	return nil, errors.New("invalid base64 image payload")
}

func byteSliceFromJSONArray(arr []any) ([]byte, bool) {
	if len(arr) == 0 {
		return nil, false
	}
	out := make([]byte, 0, len(arr))
	for _, it := range arr {
		n, ok := it.(float64)
		if !ok || n < 0 || n > 255 {
			return nil, false
		}
		out = append(out, byte(n))
	}
	return out, true
}

func (s *Server) ensureBOGroupMenuV2DishExists(ctx context.Context, restaurantID int, menuID int64, sectionID int64, dishID int64) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM group_menu_section_dishes_v2
		WHERE id = ? AND section_id = ? AND menu_id = ? AND restaurant_id = ?
	`, dishID, sectionID, menuID, restaurantID).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *Server) broadcastBOGroupMenuV2AIEvent(restaurantID int, menuID int64, eventType string, payload map[string]any) {
	if s == nil || s.groupMenusV2AIHub == nil || restaurantID <= 0 || menuID <= 0 {
		return
	}
	out := map[string]any{
		"type":          eventType,
		"restaurant_id": restaurantID,
		"menu_id":       menuID,
		"at":            time.Now().UTC().Format(time.RFC3339),
	}
	for k, v := range payload {
		out[k] = v
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if tracker, err := s.loadBOMenuV2AIImageTracker(ctx, restaurantID, menuID); err == nil {
		out["tracker"] = tracker
	}
	s.logBOGroupMenuV2AITrace(
		"broadcast event=%s restaurant=%d menu=%d clients=%d payloadKeys=%d",
		eventType,
		restaurantID,
		menuID,
		len(s.groupMenusV2AIHub.list(restaurantID, menuID)),
		len(payload),
	)
	s.groupMenusV2AIHub.broadcast(restaurantID, menuID, out)
}
