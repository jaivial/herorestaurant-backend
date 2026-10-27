package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"preactvillacarmen/internal/httpx"
)

// Coordination id: campaign_html_builder_ai_v1
//
// AI assistant of the campaign HTML builder. The backoffice sends the design
// being edited (Tailwind HTML fragment) plus the conversation, the configured
// model rewrites the design and the builder applies it live on its canvas.
// Stateless: the conversation lives in the browser, nothing is persisted.

const (
	campaignAIMaxHTMLBytes    = 200 * 1024
	campaignAIMaxMessages     = 12
	campaignAIMaxMessageRunes = 6000
	// A full email rewritten by a reasoning model needs far more room (and
	// time) than a WhatsApp reply: both are opted in only for this call.
	campaignAIMaxTokens = 16000
	campaignAITimeout   = 3 * time.Minute
)

type campaignAIModel struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Label    string `json:"label"`
}

type campaignAIChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type campaignAIChatRequest struct {
	Model        string                  `json:"model"`
	Messages     []campaignAIChatMessage `json:"messages"`
	HTML         string                  `json:"html"`
	SelectedHTML string                  `json:"selected_html"`
	CoordID      string                  `json:"coord_id"`
}

// campaignAIHTMLBlockRe captures the fenced ```html block the model answers with
// (any case: some models write ```HTML).
var campaignAIHTMLBlockRe = regexp.MustCompile("(?is)```html\\s*\\n(.*?)```")

// campaignAIModels lists every model of the catalog whose provider has an API
// key for the restaurant, plus the default: the restaurant primary model when
// it is usable, the first usable model otherwise.
func (s *Server) campaignAIModels(ctx context.Context, restaurantID int) ([]campaignAIModel, string) {
	out := []campaignAIModel{}
	for _, p := range botAIProviders {
		if s.botProviderKey(ctx, restaurantID, p.ID) == "" {
			continue
		}
		for _, m := range p.Models {
			out = append(out, campaignAIModel{ID: p.ID + "/" + m, Name: m, Provider: p.ID, Label: p.Label})
		}
	}
	def := ""
	primary := s.loadBotAIRouting(ctx, restaurantID).PrimaryModel
	for _, m := range out {
		if m.ID == primary {
			def = primary
		}
	}
	if def == "" && len(out) > 0 {
		def = out[0].ID
	}
	return out, def
}

// GET /api/admin/campanas/ai/models
func (s *Server) handleBOCampaignAIModels(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	models, def := s.campaignAIModels(r.Context(), a.ActiveRestaurantID)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "models": models, "default": def})
}

func campaignAISystemPrompt(brand restaurantBrandingCfg, websiteURL string) string {
	return fmt.Sprintf(`Eres el diseñador de emails de marketing del restaurante %q. Trabajas dentro de un editor visual de campañas y editas en tiempo real el diseño del email que el usuario tiene abierto.

El diseño es un fragmento HTML (un único div raíz, sin <html>, <head> ni <body>) maquetado SOLO con clases de Tailwind CSS v3. El editor compila esas clases y las inlinea para enviar el email.

Reglas del HTML que devuelves:
- Solo clases de Tailwind CSS v3. Se permiten valores arbitrarios: bg-[#097969], text-[15px], max-w-[600px]. Nunca uses atributos style, <style>, <script>, JavaScript, formularios, iframes, vídeo ni SVG.
- Responsive y mobile-first: los estilos base son para móvil y los prefijos sm: y md: mejoran tablet y escritorio. Contenido centrado en un contenedor mx-auto w-full max-w-[600px]. Columnas con md:flex que en móvil se apilan.
- Etiquetas aptas para email: div, h1, h2, h3, p, a, img, ul, ol, li, hr, strong, em, span, br, table si hace falta.
- Imágenes con class "block max-w-full h-auto" y alt descriptivo. No inventes URLs de imágenes: reutiliza las del diseño o usa https://placehold.co/600x300?text=Imagen como marcador.
- Botones: enlaces <a> con padding, fondo de color, texto blanco, rounded y no-underline.
- Conserva SIEMPRE en el pie el enlace de baja con href="{{unsubscribe_url}}": es obligatorio por ley.
- Logo del restaurante: %s. Web del restaurante: %s. Los botones de reservar o de visitar la web enlazan EXACTAMENTE a esa web.
- Textos en español de España, tono cercano y profesional, sin faltas. TODO el texto visible va en español (salvo que el usuario pida expresamente otro idioma): nunca mezcles palabras de otro idioma (inglés, portugués…) aunque sean parecidas ("ensure", "enjoy", "venha", "você"). Antes de responder, relee cada frase y corrige cualquier palabra que no sea española.

Formato de tu respuesta:
1. Primero, en 1-3 frases, qué has cambiado (sin repetir el código).
2. Después, el diseño COMPLETO actualizado dentro de un único bloque `+"```html ... ```"+`.
Si el usuario solo pregunta algo y no pide cambios, responde sin bloque de código.`, brand.BrandName, firstNonEmpty(brand.LogoURL, "(sin logo)"), firstNonEmpty(websiteURL, "(sin web)"))
}

// campaignAIConversation turns the browser conversation into alternating
// Messages API turns. The design travels only in the last user turn, so the
// model always edits the current canvas and old copies never cost tokens.
func campaignAIConversation(in campaignAIChatRequest) []botMessage {
	msgs := in.Messages
	if len(msgs) > campaignAIMaxMessages {
		msgs = msgs[len(msgs)-campaignAIMaxMessages:]
	}
	out := []botMessage{}
	for i, m := range msgs {
		role := strings.ToLower(strings.TrimSpace(m.Role))
		if role != "user" && role != "assistant" {
			continue
		}
		text := strings.TrimSpace(m.Content)
		if runes := []rune(text); len(runes) > campaignAIMaxMessageRunes {
			text = string(runes[:campaignAIMaxMessageRunes])
		}
		if i == len(msgs)-1 && role == "user" {
			design := "Diseño actual:\n```html\n" + in.HTML + "\n```\n"
			if sel := strings.TrimSpace(in.SelectedHTML); sel != "" {
				design += "\nElemento seleccionado en el lienzo (aplica ahí los cambios si la petición se refiere a «esto»):\n```html\n" + sel + "\n```\n"
			}
			text = design + "\nPetición: " + text
		}
		if text == "" {
			continue
		}
		// Messages API turns must alternate: consecutive turns of one role merge.
		if n := len(out); n > 0 && out[n-1].Role == role {
			out[n-1].Content[0].Text += "\n\n" + text
			continue
		}
		if len(out) == 0 && role != "user" {
			continue
		}
		out = append(out, botMessage{Role: role, Content: []botBlock{{Type: "text", Text: text}}})
	}
	return out
}

// splitCampaignAIReply separates the explanation from the rewritten design.
func splitCampaignAIReply(text string) (reply, html string) {
	matches := campaignAIHTMLBlockRe.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return strings.TrimSpace(text), ""
	}
	last := matches[len(matches)-1]
	html = strings.TrimSpace(text[last[2]:last[3]])
	reply = strings.TrimSpace(text[:matches[0][0]] + text[last[1]:])
	return reply, html
}

// campaignAIError is a refusal of the design AI with the HTTP status the
// builder endpoint answers with (the tools only read the message).
type campaignAIError struct {
	status int
	msg    string
}

func (e campaignAIError) Error() string { return e.msg }

// campaignAIResult is one answer of the design AI: the explanation, the
// rewritten design ("" when the model only answered) and the model used.
type campaignAIResult struct {
	Reply string
	HTML  string
	Model string
	// LanguageWarnings are non-Spanish words still in the design after the
	// automatic correction (campaign_ai_language_v1). Empty when clean.
	LanguageWarnings []string
}

// campaignAIRun asks the restaurant's model to rewrite a design. ONE core for
// the builder chat and the campaign tools of Forky and the WhatsApp bot
// (campaign_tools_v1): same prompt, model choice, limits and usage record.
func (s *Server) campaignAIRun(ctx context.Context, restaurantID int, in campaignAIChatRequest) (campaignAIResult, error) {
	in.HTML = strings.TrimSpace(in.HTML)
	if len(in.HTML) > campaignAIMaxHTMLBytes || len(in.SelectedHTML) > campaignAIMaxHTMLBytes {
		return campaignAIResult{}, campaignAIError{http.StatusBadRequest, "El diseño es demasiado grande para el asistente"}
	}
	msgs := campaignAIConversation(in)
	if len(msgs) == 0 || msgs[len(msgs)-1].Role != "user" {
		return campaignAIResult{}, campaignAIError{http.StatusBadRequest, "Escribe un mensaje"}
	}
	models, def := s.campaignAIModels(ctx, restaurantID)
	ref := def
	for _, m := range models {
		if m.ID == strings.TrimSpace(in.Model) {
			ref = m.ID
		}
	}
	if ref == "" {
		return campaignAIResult{}, campaignAIError{http.StatusOK, "No hay ningún modelo de IA configurado (Configuración → IA)"}
	}
	branding, _ := s.loadRestaurantBranding(ctx, restaurantID)
	siteURL := firstNonEmpty(strings.TrimSpace(branding.Website), s.campaignRestaurantBaseURL(ctx, restaurantID))
	coordID := firstNonEmpty(strings.TrimSpace(in.CoordID), "camp-new")
	started := time.Now()
	slog.Default().Info("campaign.ai.chat.start", "coord_id", coordID, "restaurant_id", restaurantID, "model", ref, "turns", len(msgs), "html_bytes", len(in.HTML))
	resp, err := s.botCallModelRef(withBotCallTimeout(ctx, campaignAITimeout), restaurantID, ref, coordID,
		campaignAISystemPrompt(branding, siteURL), msgs, nil, campaignAIMaxTokens)
	if err != nil {
		slog.Default().Warn("campaign.ai.chat.failed", "coord_id", coordID, "model", ref, "ms", time.Since(started).Milliseconds(), "err", err.Error())
		return campaignAIResult{}, campaignAIError{http.StatusOK, "El asistente no ha podido responder. Inténtalo de nuevo."}
	}
	provider, model := botSplitModelRef(ref)
	s.recordAIUsage(ctx, restaurantID, aiFeatureCampaignBuilder, provider, model,
		helperAnthropicUsage(resp.Usage.InputTokens, resp.Usage.OutputTokens, resp.Usage.CacheReadInputTokens, resp.Usage.CacheCreationInputTokens), false)
	text := ""
	for _, b := range resp.Content {
		if b.Type == "text" {
			text += b.Text
		}
	}
	reply, html := splitCampaignAIReply(text)
	if reply == "" && html != "" {
		reply = "Listo, he actualizado el diseño."
	}
	slog.Default().Info("campaign.ai.chat.done", "coord_id", coordID, "model", ref, "ms", time.Since(started).Milliseconds(), "html_changed", html != "", "html_bytes", len(html))
	// campaign_ai_language_v1: the copy must be Spanish; mixed-language texts
	// are corrected in place by one short call to the same model.
	var warnings []string
	if html != "" && !campaignAIAsksOtherLanguage(in.Messages) {
		html, warnings = s.campaignAIFixLanguage(ctx, restaurantID, ref, coordID, html, branding.BrandName)
	}
	return campaignAIResult{Reply: reply, HTML: html, Model: ref, LanguageWarnings: warnings}, nil
}

// POST /api/admin/campanas/ai/chat
func (s *Server) handleBOCampaignAIChat(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	var in campaignAIChatRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2*campaignAIMaxHTMLBytes)).Decode(&in); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Petición no válida"})
		return
	}
	res, err := s.campaignAIRun(r.Context(), a.ActiveRestaurantID, in)
	if err != nil {
		var refusal campaignAIError
		if errors.As(err, &refusal) {
			httpx.WriteJSON(w, refusal.status, map[string]any{"success": false, "message": refusal.msg})
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "El asistente no ha podido responder. Inténtalo de nuevo."})
		return
	}
	reply := res.Reply
	if warning := campaignLanguageWarning(res.LanguageWarnings); warning != "" {
		reply += "\n\n" + warning
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "reply": reply, "html": res.HTML, "model": res.Model, "coord_id": firstNonEmpty(strings.TrimSpace(in.CoordID), "camp-new")})
}
