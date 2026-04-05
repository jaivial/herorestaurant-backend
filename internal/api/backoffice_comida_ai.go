package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"

	"preactvillacarmen/internal/httpx"
	"preactvillacarmen/internal/lib/specialmenuimage"
)

const boComidaBebidasAIPrompt = "Create a premium restaurant beverage photoshoot image. Always display the main focus of the image over a white marble surface, with a neutral creme/white color background and a natural light weight sun coming from up left side which causes shadow of the element to fall to the right. Place the beverage centered on the white marble table surface. Use a clean, neutral background with a very slight shadow of the beverage for depth. Apply high-end natural studio lighting with sharp focus to highlight condensation, glass texture, and color of the drink. Make the beverage look premium, refreshing, and visually appealing for a restaurant menu, while staying realistic and natural. Preserve the original label text, colors, and branding exactly."

const boComidaCafesAIPrompt = "Create a premium restaurant coffee photoshoot image. Always display the main focus of the image over a white marble surface, with a neutral creme/white color background and a natural light weight sun coming from up left side which causes shadow of the element to fall to the right. Place the coffee item centered on the white marble table surface. Use a clean, neutral background with a very slight shadow for depth. Apply high-end natural studio lighting with sharp focus to highlight coffee texture, cup warmth, steam (if applicable), and rich colors. Make the coffee look premium, inviting, and visually appealing for a restaurant menu, while staying realistic and natural."

var boComidaAIWSUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type boComidaAIHub struct {
	mu    sync.RWMutex
	rooms map[string]map[*boComidaAIClient]struct{}
}

type boComidaAIClient struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

type boComidaAIImageJob struct {
	RestaurantID int
	Tipo         string
	ItemNum      int
	RawImage     []byte
	ContentType  string
}

func (s *Server) logBOComidaAITrace(format string, args ...any) {
	log.Printf("[bo-comida-ai] "+format, args...)
}

func boComidaAIRoomKey(restaurantID int) string {
	return strconv.Itoa(restaurantID)
}

func newBOComidaAIHub() *boComidaAIHub {
	return &boComidaAIHub{rooms: map[string]map[*boComidaAIClient]struct{}{}}
}

func (h *boComidaAIHub) add(restaurantID int, c *boComidaAIClient) {
	if h == nil || restaurantID <= 0 || c == nil {
		return
	}
	key := boComidaAIRoomKey(restaurantID)
	h.mu.Lock()
	defer h.mu.Unlock()
	room := h.rooms[key]
	if room == nil {
		room = map[*boComidaAIClient]struct{}{}
		h.rooms[key] = room
	}
	room[c] = struct{}{}
}

func (h *boComidaAIHub) remove(restaurantID int, c *boComidaAIClient) {
	if h == nil || restaurantID <= 0 || c == nil {
		return
	}
	key := boComidaAIRoomKey(restaurantID)
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

func (h *boComidaAIHub) list(restaurantID int) []*boComidaAIClient {
	if h == nil || restaurantID <= 0 {
		return nil
	}
	key := boComidaAIRoomKey(restaurantID)
	h.mu.RLock()
	defer h.mu.RUnlock()
	room := h.rooms[key]
	if len(room) == 0 {
		return nil
	}
	out := make([]*boComidaAIClient, 0, len(room))
	for c := range room {
		out = append(out, c)
	}
	return out
}

func (h *boComidaAIHub) broadcast(restaurantID int, payload any) {
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

func (c *boComidaAIClient) writeText(raw []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(7 * time.Second))
	return c.conn.WriteMessage(websocket.TextMessage, raw)
}

func (c *boComidaAIClient) writeJSON(v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.writeText(raw)
}

func (c *boComidaAIClient) ping() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(7 * time.Second))
	return c.conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(7*time.Second))
}

func (c *boComidaAIClient) close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.Close()
}

// --- WebSocket handler ---

func (s *Server) handleBOComidaAIWS(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		s.logBOComidaAITrace("ws reject unauthorized remote=%s", r.RemoteAddr)
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	s.logBOComidaAITrace("ws connect request restaurant=%d remote=%s", a.ActiveRestaurantID, r.RemoteAddr)

	conn, err := boComidaAIWSUpgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logBOComidaAITrace("ws upgrade error restaurant=%d err=%v", a.ActiveRestaurantID, err)
		return
	}

	client := &boComidaAIClient{conn: conn}
	s.comidaAIHub.add(a.ActiveRestaurantID, client)
	s.logBOComidaAITrace("ws connected restaurant=%d clients=%d", a.ActiveRestaurantID, len(s.comidaAIHub.list(a.ActiveRestaurantID)))
	defer func() {
		s.comidaAIHub.remove(a.ActiveRestaurantID, client)
		s.logBOComidaAITrace("ws disconnected restaurant=%d clients=%d", a.ActiveRestaurantID, len(s.comidaAIHub.list(a.ActiveRestaurantID)))
		_ = client.close()
	}()

	conn.SetReadLimit(1 << 20)
	_ = conn.SetReadDeadline(time.Now().Add(70 * time.Second))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(70 * time.Second))
	})

	_ = client.writeJSON(map[string]any{
		"type":          "hello",
		"restaurant_id": a.ActiveRestaurantID,
		"at":            time.Now().UTC().Format(time.RFC3339),
	})

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
			// ignore incoming messages for now
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
				s.logBOComidaAITrace("ws ping failed restaurant=%d err=%v", a.ActiveRestaurantID, err)
				return
			}
		}
	}
}

// --- AI image generation HTTP endpoint ---

func (s *Server) handleBOComidaImageAI(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		s.logBOComidaAITrace("generate reject unauthorized remote=%s", r.RemoteAddr)
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	s.logBOComidaAITrace("generate request received restaurant=%d path=%s remote=%s", a.ActiveRestaurantID, r.URL.Path, r.RemoteAddr)

	if strings.TrimSpace(s.cfg.OpenAIAPIKey) == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "AI provider not configured"})
		return
	}
	if !s.bunnyConfigured() {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Image storage not configured"})
		return
	}

	tipoStr := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "tipo")))
	ct, ok := normalizeComidaTipo(tipoStr)
	if !ok || ct == comidaTipoVinos || ct == comidaTipoPostres {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Tipo no soportado para IA de imagenes"})
		return
	}

	itemNumStr := strings.TrimSpace(chi.URLParam(r, "id"))
	itemNum, err := strconv.Atoi(itemNumStr)
	if err != nil || itemNum <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "ID invalido"})
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

	// Verify item exists and belongs to restaurant
	var exists int
	err = s.db.QueryRowContext(r.Context(),
		"SELECT 1 FROM comida_items WHERE id = ? AND restaurant_id = ? AND source_type = ?",
		itemNum, a.ActiveRestaurantID, string(ct),
	).Scan(&exists)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Elemento no encontrado"})
		return
	}

	s.db.ExecContext(r.Context(),
		"UPDATE comida_items SET ai_generating = 1 WHERE id = ? AND restaurant_id = ? AND source_type = ?",
		itemNum, a.ActiveRestaurantID, string(ct))

	s.broadcastBOComidaAIEvent(a.ActiveRestaurantID, "comida_ai_started", map[string]any{
		"tipo":    string(ct),
		"item_id": itemNum,
	})

	go s.runBOComidaAIImageJob(boComidaAIImageJob{
		RestaurantID: a.ActiveRestaurantID,
		Tipo:         string(ct),
		ItemNum:      itemNum,
		RawImage:     raw,
		ContentType:  contentType,
	})

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "AI image generation started",
		"tipo":    string(ct),
		"item_id": itemNum,
	})
}

// --- Job runner ---

func (s *Server) comidaAIPromptForTipo(tipo string) string {
	switch strings.ToLower(strings.TrimSpace(tipo)) {
	case "bebidas":
		return boComidaBebidasAIPrompt
	case "cafes":
		return boComidaCafesAIPrompt
	default:
		return boGroupMenuV2AIPrompt
	}
}

func (s *Server) runBOComidaAIImageJob(job boComidaAIImageJob) {
	ctx, cancel := context.WithTimeout(context.Background(), s.openAIRequestTimeout())
	defer cancel()
	s.logBOComidaAITrace("job start restaurant=%d tipo=%s item=%d inputBytes=%d inputType=%s", job.RestaurantID, job.Tipo, job.ItemNum, len(job.RawImage), job.ContentType)

	if err := s.acquireBOGroupMenuV2AIWorker(ctx); err != nil {
		s.logBOComidaAITrace("job worker acquire error restaurant=%d tipo=%s item=%d err=%v", job.RestaurantID, job.Tipo, job.ItemNum, err)
		s.failBOComidaAIImageJob(job, "AI generation queue timeout")
		return
	}
	defer s.releaseBOGroupMenuV2AIWorker()

	prompt := s.comidaAIPromptForTipo(job.Tipo)
	output, err := s.callOpenAIImageEditWithPrompt(ctx, job.RawImage, job.ContentType, prompt)
	if err != nil {
		s.logBOComidaAITrace("job ai call error restaurant=%d tipo=%s item=%d err=%v", job.RestaurantID, job.Tipo, job.ItemNum, err)
		s.failBOComidaAIImageJob(job, aiFailureMessage("AI image generation failed", err))
		return
	}
	if len(output) == 0 {
		s.failBOComidaAIImageJob(job, "AI image generation returned empty image")
		return
	}
	if len(output) > s.openAIMaxOutputBytes() {
		s.failBOComidaAIImageJob(job, "AI image is too large")
		return
	}

	outputType := strings.TrimSpace(http.DetectContentType(output))
	normalizedWebP, err := specialmenuimage.NormalizeToWebP(ctx, output, "comida-ai", outputType)
	if err != nil {
		s.logBOComidaAITrace("job normalize error restaurant=%d tipo=%s item=%d err=%v", job.RestaurantID, job.Tipo, job.ItemNum, err)
		s.failBOComidaAIImageJob(job, "Failed processing generated image")
		return
	}

	objectPath, err := s.UploadComidaAIImage(ctx, job.Tipo, job.ItemNum, normalizedWebP, "image/webp")
	if err != nil {
		s.logBOComidaAITrace("job bunny upload error restaurant=%d tipo=%s item=%d err=%v", job.RestaurantID, job.Tipo, job.ItemNum, err)
		s.failBOComidaAIImageJob(job, "Failed uploading generated image")
		return
	}

	fullURL := s.bunnyPullURL(objectPath)
	_, err = s.db.ExecContext(ctx, `
		UPDATE comida_items
		SET foto_path = ?,
		    foto = NULL,
		    ai_generating = 0
		WHERE id = ? AND restaurant_id = ? AND source_type = ?
	`, objectPath, job.ItemNum, job.RestaurantID, job.Tipo)
	if err != nil {
		s.logBOComidaAITrace("job db save error restaurant=%d tipo=%s item=%d err=%v", job.RestaurantID, job.Tipo, job.ItemNum, err)
		s.failBOComidaAIImageJob(job, "Failed saving generated image")
		return
	}

	s.logBOComidaAITrace("job completed restaurant=%d tipo=%s item=%d url=%s", job.RestaurantID, job.Tipo, job.ItemNum, fullURL)
	s.broadcastBOComidaAIEvent(job.RestaurantID, "comida_ai_completed", map[string]any{
		"tipo":     job.Tipo,
		"item_id":  job.ItemNum,
		"foto_url": fullURL,
	})
}

func (s *Server) failBOComidaAIImageJob(job boComidaAIImageJob, message string) {
	s.logBOComidaAITrace("job failed restaurant=%d tipo=%s item=%d message=%s", job.RestaurantID, job.Tipo, job.ItemNum, message)
	s.db.ExecContext(context.Background(),
		"UPDATE comida_items SET ai_generating = 0 WHERE id = ? AND restaurant_id = ? AND source_type = ?",
		job.ItemNum, job.RestaurantID, job.Tipo)
	s.broadcastBOComidaAIEvent(job.RestaurantID, "comida_ai_failed", map[string]any{
		"tipo":    job.Tipo,
		"item_id": job.ItemNum,
		"message": message,
	})
}

func (s *Server) broadcastBOComidaAIEvent(restaurantID int, eventType string, payload map[string]any) {
	payload["type"] = eventType
	payload["restaurant_id"] = restaurantID
	s.comidaAIHub.broadcast(restaurantID, payload)
}

// UploadComidaAIImage uploads a comida AI-generated image to Bunny storage.
func (s *Server) UploadComidaAIImage(ctx context.Context, tipo string, itemNum int, img []byte, contentType string) (string, error) {
	if itemNum <= 0 {
		return "", errors.New("invalid item id")
	}
	if len(img) == 0 {
		return "", errors.New("empty image")
	}
	generationVersion := strconv.FormatInt(time.Now().UTC().UnixMilli(), 10)
	fileName := strconv.Itoa(itemNum) + "-ai-" + generationVersion + ".webp"
	objectPath := path.Join("images", "comida", tipo, fileName)
	if err := s.bunnyPut(ctx, objectPath, img, contentType); err != nil {
		return "", err
	}
	return objectPath, nil
}
