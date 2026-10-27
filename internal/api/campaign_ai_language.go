package api

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Coordination id: campaign_ai_language_v1
//
// The design AI writes the copy of the email in Spanish, but MiniMax was seen
// mixing languages inside one sentence ("Ensure su mesa del 3 al 30 de
// noviembre y venha a probar nuestros arroces"). The prompt rule alone does
// not stop it, so every rewritten design is checked: the visible texts are
// scanned for words that are never Spanish (frequent English and Portuguese
// words, Portuguese-only letters) and ONLY the flagged texts go back to the
// model in one short call that returns their Spanish versions, applied in
// place. Nothing else of the design is touched, and words of the brand name
// are never flagged.

// campaignForeignWords are frequent words of the languages the model drifts
// into that are never Spanish (nor Valencian, the other language a restaurant
// here may use on purpose).
var campaignForeignWords = map[string]bool{
	// English
	"the": true, "and": true, "your": true, "you": true, "our": true, "with": true, "for": true,
	"this": true, "that": true, "is": true, "are": true, "we": true, "to": true, "of": true,
	"ensure": true, "enjoy": true, "welcome": true, "today": true, "join": true, "discover": true,
	"taste": true, "book": true, "now": true, "dont": true, "don't": true, "here": true, "best": true,
	"special": true, "offer": true, "table": true, "dish": true, "dishes": true, "season": true, "autumn": true,
	// Portuguese
	"venha": true, "você": true, "vocês": true, "voce": true, "nosso": true, "nossa": true, "nossos": true,
	"nossas": true, "não": true, "também": true, "obrigado": true, "obrigada": true, "aproveite": true,
	"conheça": true, "até": true, "então": true, "agora": true, "muito": true, "muita": true, "sua": true,
	"seus": true, "suas": true, "uma": true, "garanta": true, "faça": true,
}

// campaignTextNodeRe captures the text between two tags of the design.
var campaignTextNodeRe = regexp.MustCompile(`>([^<>]+)<`)

func campaignWords(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && r != '\'' })
}

// campaignForeignTerms lists the non-Spanish words of one text, ignoring the
// words of the brand name ("The Garden" may well be a restaurant).
func campaignForeignTerms(text string, brand map[string]bool) []string {
	var out []string
	for _, w := range campaignWords(html.UnescapeString(text)) {
		if brand[w] {
			continue
		}
		if campaignForeignWords[w] || strings.ContainsAny(w, "ãõ") {
			out = append(out, w)
		}
	}
	return out
}

// campaignLanguageIssues returns the text nodes of a design that carry
// non-Spanish words (raw, as they appear in the HTML) and the words found.
func campaignLanguageIssues(design, brandName string) ([]string, []string) {
	brand := map[string]bool{}
	for _, w := range campaignWords(brandName) {
		brand[w] = true
	}
	seen := map[string]bool{}
	terms := map[string]bool{}
	var flagged []string
	for _, m := range campaignTextNodeRe.FindAllStringSubmatch(design, -1) {
		raw := m[1]
		if strings.TrimSpace(raw) == "" || seen[raw] {
			continue
		}
		seen[raw] = true
		found := campaignForeignTerms(raw, brand)
		if len(found) == 0 {
			continue
		}
		flagged = append(flagged, raw)
		for _, t := range found {
			terms[t] = true
		}
	}
	list := make([]string, 0, len(terms))
	for t := range terms {
		list = append(list, t)
	}
	sort.Strings(list)
	return flagged, list
}

const (
	campaignLanguageFixTimeout = 60 * time.Second
	campaignLanguageFixMaxText = 20
)

// campaignAIFixLanguage corrects the non-Spanish texts of a design with one
// short call to the same model. It returns the design (corrected when the
// call worked) and the words still found afterwards, which the callers show
// as warnings. A failed call never loses the design.
func (s *Server) campaignAIFixLanguage(ctx context.Context, restaurantID int, ref, coordID, design, brandName string) (string, []string) {
	flagged, terms := campaignLanguageIssues(design, brandName)
	if len(flagged) == 0 {
		return design, nil
	}
	if len(flagged) > campaignLanguageFixMaxText {
		flagged = flagged[:campaignLanguageFixMaxText]
	}
	type item struct {
		I     int    `json:"i"`
		Texto string `json:"texto"`
	}
	items := make([]item, len(flagged))
	for i, raw := range flagged {
		items[i] = item{I: i, Texto: strings.TrimSpace(html.UnescapeString(raw))}
	}
	payload, _ := json.Marshal(items)
	system := "Eres corrector de textos de marketing de un restaurante en España. Te paso textos de un email que mezclan palabras de otro idioma (" + strings.Join(terms, ", ") + "). " +
		"Reescribe cada texto en español de España natural, con el mismo sentido y una longitud parecida. No cambies nombres propios, marcas, precios, fechas, horas ni códigos (por ejemplo, cupones). " +
		`Responde SOLO con un array JSON de objetos {"i": número, "texto": "texto corregido"}, sin explicaciones.`
	msgs := []botMessage{{Role: "user", Content: []botBlock{{Type: "text", Text: string(payload)}}}}
	started := time.Now()
	resp, err := s.botCallModelRef(withBotCallTimeout(ctx, campaignLanguageFixTimeout), restaurantID, ref, coordID, system, msgs, nil, 2000)
	if err != nil {
		slog.Default().Warn("campaign.ai.language.fix_failed", "coord_id", coordID, "terms", strings.Join(terms, ","), "err", err.Error())
		return design, terms
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
	var fixes []item
	if start, end := strings.Index(text, "["), strings.LastIndex(text, "]"); start >= 0 && end > start {
		_ = json.Unmarshal([]byte(text[start:end+1]), &fixes)
	}
	out := design
	applied := 0
	for _, f := range fixes {
		fixed := strings.TrimSpace(f.Texto)
		if f.I < 0 || f.I >= len(flagged) || fixed == "" {
			continue
		}
		raw := flagged[f.I]
		// Keep the whitespace around the text: the design's own formatting.
		lead := raw[:len(raw)-len(strings.TrimLeft(raw, " \t\r\n"))]
		trail := raw[len(strings.TrimRight(raw, " \t\r\n")):]
		replacement := lead + html.EscapeString(fixed) + trail
		if next := strings.ReplaceAll(out, ">"+raw+"<", ">"+replacement+"<"); next != out {
			out = next
			applied++
		}
	}
	_, remaining := campaignLanguageIssues(out, brandName)
	slog.Default().Info("campaign.ai.language.fixed", "coord_id", coordID, "terms", strings.Join(terms, ","), "texts", len(flagged), "applied", applied, "remaining", strings.Join(remaining, ","), "ms", time.Since(started).Milliseconds())
	return out, remaining
}

// campaignOtherLanguageRe spots a request for copy in another language ("en
// inglés", "in English", "tradúcelo", "bilingüe"): then the design is meant
// to carry those words and the Spanish correction must not run.
var campaignOtherLanguageRe = regexp.MustCompile(`(?i)ingl[eé]s|english|portugu[eé]s|franc[eé]s|alem[aá]n|italiano|catal[aá]n|valenci[aà]|idioma|biling[uü]e|tradu[cz]`)

// campaignAIAsksOtherLanguage reports whether the latest user message of the
// conversation asks for another language.
func campaignAIAsksOtherLanguage(msgs []campaignAIChatMessage) bool {
	for i := len(msgs) - 1; i >= 0; i-- {
		if strings.EqualFold(strings.TrimSpace(msgs[i].Role), "user") {
			return campaignOtherLanguageRe.MatchString(msgs[i].Content)
		}
	}
	return false
}

// campaignLanguageWarning words the leftovers for the people reading them.
func campaignLanguageWarning(terms []string) string {
	if len(terms) == 0 {
		return ""
	}
	return fmt.Sprintf("El texto aún contiene palabras que no parecen español (%s): revísalo antes de enviar.", strings.Join(terms, ", "))
}
