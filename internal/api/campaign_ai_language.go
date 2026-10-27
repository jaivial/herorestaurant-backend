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
	// campaignLanguageFixReserve is left to the caller after the correction:
	// the tools still apply and compile the design inside the same deadline.
	campaignLanguageFixReserve = 45 * time.Second
	campaignLanguageFixMinimum = 10 * time.Second
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
	// The correction is only an improvement: it must never eat the time the
	// caller needs to save the design (campaign_design runs the main rewrite
	// and this call inside one tool deadline).
	timeout := campaignLanguageFixTimeout
	if deadline, ok := ctx.Deadline(); ok {
		left := time.Until(deadline) - campaignLanguageFixReserve
		if left < campaignLanguageFixMinimum {
			slog.Default().Warn("campaign.ai.language.fix_skipped", "coord_id", coordID, "terms", strings.Join(terms, ","), "reason", "deadline")
			return design, terms
		}
		if left < timeout {
			timeout = left
		}
	}
	payload, _ := json.Marshal(items)
	system := "Eres corrector de textos de marketing de un restaurante en España. Te paso textos de un email que mezclan palabras de otro idioma (" + strings.Join(terms, ", ") + "). " +
		"Reescribe cada texto en español de España natural, con el mismo sentido y una longitud parecida. No cambies nombres propios, marcas, precios, fechas, horas ni códigos (por ejemplo, cupones). " +
		`Responde SOLO con un array JSON de objetos {"i": número, "texto": "texto corregido"}, sin explicaciones.`
	msgs := []botMessage{{Role: "user", Content: []botBlock{{Type: "text", Text: string(payload)}}}}
	started := time.Now()
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	resp, err := s.botCallModelRef(withBotCallTimeout(callCtx, timeout), restaurantID, ref, coordID, system, msgs, nil, 2000)
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

// Language requests in the user's words. A target is "en/al/a/in/to <idioma>"
// ("hazlo en inglés", "tradúcelo al inglés", "in English"); merely naming a
// language does not count ("hay palabras en inglés, ponlo en español" asks for
// Spanish). Bilingual requests keep both languages on purpose. The Valencian
// forms need the accent ("valencià"): "en Valencia" is the city.
const campaignOtherLanguages = `ingl[eé]s|english|portugu[eé]s|portuguese|franc[eé]s|french|alem[aá]n|german|italiano|italian|catal[aá]n|valenciano|valenci[àá]|gallego|euskera`

var (
	campaignOtherTargetRe   = regexp.MustCompile(`(?i)(?:^|[^\p{L}])(?:en|al|a|in|to)\s+(?:` + campaignOtherLanguages + `)`)
	campaignSpanishTargetRe = regexp.MustCompile(`(?i)(?:^|[^\p{L}])(?:en|al|a|in|to)\s+(?:espa[nñ]ol|castellano|spanish)`)
	campaignBilingualRe     = regexp.MustCompile(`(?i)biling[uü]e|bilingual|(?:dos|ambos|varios)\s+idiomas|(?:espa[nñ]ol|castellano)\s+(?:y|e)\s+(?:en\s+)?(?:` + campaignOtherLanguages + `)|(?:` + campaignOtherLanguages + `)\s+y\s+(?:en\s+)?(?:espa[nñ]ol|castellano)`)
)

// campaignAILanguageRequest reads the newest user message that asks for a
// language: "other" (another language or bilingual), "spanish", or "" when
// no message asks for one.
func campaignAILanguageRequest(msgs []campaignAIChatMessage) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if !strings.EqualFold(strings.TrimSpace(msgs[i].Role), "user") {
			continue
		}
		text := msgs[i].Content
		switch {
		case campaignBilingualRe.MatchString(text):
			return "other"
		case campaignSpanishTargetRe.MatchString(text):
			return "spanish"
		case campaignOtherTargetRe.MatchString(text):
			return "other"
		}
	}
	return ""
}

// campaignDesignMostlyForeign reports whether a design's copy is mostly in
// another language: then it was made that way on purpose (an English or a
// bilingual campaign) and an unrelated edit must not translate it.
func campaignDesignMostlyForeign(design, brandName string) bool {
	brand := map[string]bool{}
	for _, w := range campaignWords(brandName) {
		brand[w] = true
	}
	total, foreign := 0, 0
	for _, m := range campaignTextNodeRe.FindAllStringSubmatch(design, -1) {
		if len(campaignWords(html.UnescapeString(m[1]))) < 3 {
			continue
		}
		total++
		if len(campaignForeignTerms(m[1], brand)) > 0 {
			foreign++
		}
	}
	return foreign >= 2 && foreign*100 >= total*35
}

// campaignAIWantsSpanishCheck decides whether the Spanish correction runs on
// a rewrite: an explicit request wins (newest first); without one, a design
// that already came in mostly in another language is left alone.
func campaignAIWantsSpanishCheck(msgs []campaignAIChatMessage, inputDesign, brandName string) bool {
	switch campaignAILanguageRequest(msgs) {
	case "spanish":
		return true
	case "other":
		return false
	}
	return !campaignDesignMostlyForeign(inputDesign, brandName)
}

// campaignLanguageWarning words the leftovers for the people reading them.
func campaignLanguageWarning(terms []string) string {
	if len(terms) == 0 {
		return ""
	}
	return fmt.Sprintf("El texto aún contiene palabras que no parecen español (%s): revísalo antes de enviar.", strings.Join(terms, ", "))
}
