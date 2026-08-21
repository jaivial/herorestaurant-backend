package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"preactvillacarmen/internal/config"
	"preactvillacarmen/internal/httpx"
)

// handleBOStockOCRScan turns a photographed document (albaran, etiqueta, ficha)
// into structured stock-article data using MiniMax vision. The key + model are
// resolved from the per-restaurant DB config (restaurant_minimax_config); the
// model stored there is the one the user picked (MiniMax-M3), falling back to
// the global MINIMAX_MODEL env when no DB row exists.
//
// The camera flow in the backoffice "Nuevo articulo" modal posts the captured
// frame here; the modal then pre-fills the manual form from extraction.name.
func (s *Server) handleBOStockOCRScan(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	mediaType, payload, err := readStockOCRScanImage(r)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(payload) == 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Imagen vacia")
		return
	}
	if len(payload) > 8<<20 {
		httpx.WriteError(w, http.StatusBadRequest, "Imagen demasiado grande (max 8 MB)")
		return
	}

	model := s.resolveMiniMaxModel(r.Context(), a.ActiveRestaurantID, "")
	apiKey := s.resolveMiniMaxKey(r.Context(), a.ActiveRestaurantID)
	if apiKey == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "MiniMax no esta configurado para este restaurante",
		})
		return
	}

	raw, extractErr := s.minimaxVisionOCR(r.Context(), a.ActiveRestaurantID, model, apiKey, mediaType, payload)
	if extractErr != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "No se pudo leer el documento con MiniMax: " + extractErr.Error(),
		})
		return
	}

	extraction := parseStockOCRScanJSON(raw)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":    true,
		"model":      model,
		"rawText":    raw,
		"extraction": extraction,
	})
}

// readStockOCRScanImage accepts either a multipart upload (field "image") or a
// JSON body {"image": "data:image/jpeg;base64,....", "mediaType": "image/jpeg"}.
func readStockOCRScanImage(r *http.Request) (string, []byte, error) {
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(12 << 20); err != nil {
			return "", nil, fmt.Errorf("formulario invalido")
		}
		file, _, err := r.FormFile("image")
		if err != nil {
			return "", nil, fmt.Errorf("falta la imagen")
		}
		defer file.Close()
		mediaType := r.FormValue("mediaType")
		if mediaType == "" {
			mediaType = "image/jpeg"
		}
		data, err := io.ReadAll(io.LimitReader(file, 8<<20))
		if err != nil {
			return "", nil, fmt.Errorf("no se pudo leer la imagen")
		}
		return mediaType, data, nil
	}

	var in struct {
		Image     string `json:"image"`
		MediaType string `json:"mediaType"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in) != nil || strings.TrimSpace(in.Image) == "" {
		return "", nil, fmt.Errorf("imagen no proporcionada")
	}
	mediaType := strings.TrimSpace(in.MediaType)
	if mediaType == "" {
		mediaType = "image/jpeg"
	}
	b64 := in.Image
	if idx := strings.Index(b64, ","); idx >= 0 {
		// Strip a data URL prefix like "data:image/jpeg;base64,".
		if meta := b64[:idx]; strings.HasPrefix(meta, "data:") {
			if parts := strings.SplitN(meta, ":", 2); len(parts) == 2 {
				if sub := strings.SplitN(parts[1], ";", 2); len(sub) == 2 {
					mediaType = sub[0]
				}
			}
		}
		b64 = b64[idx+1:]
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return "", nil, fmt.Errorf("imagen base64 invalida")
	}
	return mediaType, data, nil
}

// minimaxVisionOCR posts the image to the MiniMax Anthropic-compatible Messages
// endpoint and returns the assistant's text reply.
func (s *Server) minimaxVisionOCR(ctx context.Context, restaurantID int, model, apiKey, mediaType string, payload []byte) (string, error) {
	prompt := "Eres un extractor de datos de albaranes y etiquetas de producto para un restaurante. " +
		"Analiza la imagen y, si ves un articulo de stock (producto, bebida o plato), " +
		"devuelve SOLO un objeto JSON con este formato, sin texto adicional ni marcado: " +
		"{\"name\": string, \"quantity\": number|null, \"unit\": string|null, \"note\": string|null}. " +
		"Usa 'name' para el nombre del producto tal como aparece. Si no hay un producto claro, devuelve {\"name\": null}."

	baseURL := strings.TrimRight(s.cfg.MiniMaxBaseURL, "/")
	if baseURL == "" {
		baseURL = "https://api.minimax.io/anthropic"
	}
	// MiniMax's Anthropic-compatible endpoint expects Anthropic image blocks.
	body := map[string]any{
		"model":      model,
		"max_tokens": 1500,
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{
						"type": "image",
						"source": map[string]any{
							"type":       "base64",
							"media_type": mediaType,
							"data":       base64.StdEncoding.EncodeToString(payload),
						},
					},
					{"type": "text", "text": prompt},
				},
			},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(ctx, configMiniMaxOCRTimeout(s.cfg))
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/messages", bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("minimax http %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var envelope struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		// Some MiniMax dialects wrap the reply differently; fall back to the raw body.
		return strings.TrimSpace(string(respBody)), nil
	}
	var sb strings.Builder
	for _, block := range envelope.Content {
		if block.Type == "text" {
			sb.WriteString(block.Text)
		}
	}
	if sb.Len() == 0 {
		return strings.TrimSpace(string(respBody)), nil
	}
	return sb.String(), nil
}

func configMiniMaxOCRTimeout(cfg config.Config) time.Duration {
	if cfg.MiniMaxTranslateTimeout > 0 {
		return cfg.MiniMaxTranslateTimeout
	}
	return 60 * time.Second
}

var stockOCRScanJSONRe = regexp.MustCompile(`\{[\s\S]*\}`)

// parseStockOCRScanJSON extracts the first JSON object from a free-form reply,
// tolerating code fences and surrounding prose MiniMax sometimes adds.
func parseStockOCRScanJSON(raw string) map[string]any {
	if raw == "" {
		return map[string]any{}
	}
	match := stockOCRScanJSONRe.FindString(raw)
	if match == "" {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(match), &out); err != nil {
		return map[string]any{}
	}
	return out
}
