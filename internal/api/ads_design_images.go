package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	"preactvillacarmen/internal/httpx"
	"preactvillacarmen/internal/lib/specialmenuimage"
)

// Coordination id: ads_html_editor_v1 (image AI)
//
// The image AI of the HTML ad editor: "Mejorar con IA" turns an uploaded
// photo into a polished campaign photo and "Generar con IA" creates one from
// a description. Same providers and models as the previous ad editor
// (boAdEnhanceModel / boAdT2IModel); the result only goes to the restaurant's
// CDN and its URL comes back: the editor places it in the design, so the
// legacy content and the image status of the ad stay untouched.

const adDesignImagePromptMax = 600

// adDesignImagePrompt wraps what the person asked for with the house style.
// It describes a real photograph only: naming where the image will be used
// ("a website popup") made the model draw a website mockup with text on a
// tablet instead of the scene.
func adDesignImagePrompt(description string) string {
	return "Professional food and hospitality photograph of a real scene, editorial style, realistic, warm natural light, shallow depth of field. " +
		"Not a screenshot, not a mockup, no screens, devices, frames, posters or signs; no text, letters, numbers, logos or watermarks anywhere. " +
		"Scene: " + description
}

func (s *Server) serveBOAdDesignImageEnhance(w http.ResponseWriter, r *http.Request, restaurantID int, adID int64, provider resolvedAIImageProvider) {
	raw, filename, ct, err := readBOAdMultipartImage(r, specialmenuimage.MaxInputBytes)
	if err != nil {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "Imagen no válida"})
		return
	}
	compressed, err := specialmenuimage.NormalizeToWebPWithLimit(r.Context(), raw, filename, ct, boAdMaxImageBytes)
	if err != nil {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.openAIRequestTimeout())
	defer cancel()
	output, err := s.callComidaImageEdit(ctx, boAdEnhanceURL(provider.BaseURL), provider.APIKey, "Enhance this restaurant advertising image into a premium website campaign photo. Preserve the subject and composition, improve lighting, detail and polish, and do not add any text or logos.", compressed, "image/webp")
	if err != nil {
		slog.Default().Warn("ad.design.image.enhance_failed", "restaurant_id", restaurantID, "ad_id", adID, "err", err.Error())
		httpx.WriteJSON(w, 502, map[string]any{"success": false, "message": aiFailureMessage("No se pudo mejorar la imagen con IA", err)})
		return
	}
	s.writeBOAdDesignImage(w, r, restaurantID, adID, output, "enhanced", "ai")
}

func (s *Server) serveBOAdDesignImageGenerate(w http.ResponseWriter, r *http.Request, restaurantID int, adID int64, provider resolvedAIImageProvider) {
	var in struct {
		Prompt string `json:"prompt"`
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 16<<10))
	_ = json.Unmarshal(body, &in)
	description := strings.TrimSpace(in.Prompt)
	if description == "" {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "Describe la imagen que quieres generar"})
		return
	}
	if utf8.RuneCountInString(description) > adDesignImagePromptMax {
		description = string([]rune(description)[:adDesignImagePromptMax])
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.openAIRequestTimeout())
	defer cancel()
	output, err := s.callBOAdTextToImage(ctx, boAdTextToImageURL(provider.BaseURL), provider.APIKey, adDesignImagePrompt(description))
	if err != nil {
		slog.Default().Warn("ad.design.image.generate_failed", "restaurant_id", restaurantID, "ad_id", adID, "err", err.Error())
		httpx.WriteJSON(w, 502, map[string]any{"success": false, "message": aiFailureMessage("No se pudo generar la imagen con IA", err)})
		return
	}
	s.writeBOAdDesignImage(w, r, restaurantID, adID, output, "generated", "generated")
}

// writeBOAdDesignImage stores an AI image of the design on the CDN and answers its URL.
func (s *Server) writeBOAdDesignImage(w http.ResponseWriter, r *http.Request, restaurantID int, adID int64, output []byte, name, suffix string) {
	url, err := s.saveBOAdImage(r.Context(), restaurantID, adID, output, name, http.DetectContentType(output), suffix)
	if err != nil {
		httpx.WriteJSON(w, 500, map[string]any{"success": false, "message": "No se pudo guardar la imagen en el CDN"})
		return
	}
	slog.Default().Info("ad.design.image.ready", "restaurant_id", restaurantID, "ad_id", adID, "kind", name)
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "url": url})
}
