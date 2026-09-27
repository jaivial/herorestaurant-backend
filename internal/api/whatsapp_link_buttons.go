package api

import (
	"net/url"
	"regexp"
	"strings"
)

// Coordination id: wa_link_buttons_v1
//
// WhatsApp silently drops plain-text messages that contain links when they
// come from this business number: the provider acks them as delivered but the
// customer never sees them (confirmed live on 2026-09-27 with 34692747052:
// three bot replies with https://.../reservas?date=... never showed up, while
// the same number received link-free messages and interactive URL buttons).
// Every outgoing text is therefore checked: URLs are removed from the body and
// sent as interactive URL buttons (max 3, WhatsApp's limit) in a sendButtons
// message. Texts without links are sent unchanged.

var botURLRe = regexp.MustCompile(`https?://[^\s<>()\[\]"']+`)

// botLinkButtonMessage is a text split into body + URL buttons.
type botLinkButtonMessage struct {
	Body    string
	Choices []string // "Label|https://..." (SendMenu URL-button convention)
}

// botSplitLinks extracts up to 3 URLs from text. ok=false when the text has no
// link (send as plain text).
func botSplitLinks(text string) (botLinkButtonMessage, bool) {
	locs := botURLRe.FindAllStringIndex(text, -1)
	if len(locs) == 0 {
		return botLinkButtonMessage{}, false
	}
	seen := map[string]bool{}
	var choices []string
	var b strings.Builder
	last := 0
	for _, loc := range locs {
		raw := text[loc[0]:loc[1]]
		u := strings.TrimRight(raw, ".,;:!?)*_~")
		trail := raw[len(u):]
		b.WriteString(text[last:loc[0]])
		last = loc[1]
		b.WriteString(trail)
		if !isHTTPURL(u) || seen[u] || len(choices) >= 3 {
			continue
		}
		seen[u] = true
		choices = append(choices, botLinkButtonLabel(u)+"|"+u)
	}
	b.WriteString(text[last:])
	body := botTidyAfterLinkRemoval(b.String())
	if body == "" {
		body = "👇"
	}
	return botLinkButtonMessage{Body: body, Choices: choices}, len(choices) > 0
}

// botLinkButtonLabel names the button from the URL (WhatsApp limit: 20 chars).
func botLinkButtonLabel(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "Abrir enlace"
	}
	p := strings.ToLower(u.Path)
	switch {
	case strings.Contains(p, "reserva"):
		if u.Query().Get("date") != "" {
			return "Reservar esta fecha"
		}
		return "Reservar online"
	case strings.Contains(p, "carta") || strings.Contains(p, "menu"):
		return "Ver la carta"
	case strings.Contains(u.Host, "maps") || strings.Contains(p, "maps"):
		return "Cómo llegar"
	case strings.Contains(p, "pdf") || strings.Contains(p, "comprobante") || strings.Contains(p, "receipt"):
		return "Ver documento"
	case strings.Contains(p, "pago") || strings.Contains(p, "pay") || strings.Contains(p, "checkout"):
		return "Pagar"
	case p == "" || p == "/":
		return "Visitar la web"
	}
	return "Abrir enlace"
}

var (
	botEmptyLinkLeadRe = regexp.MustCompile(`(?m)[ \t]*(🔗|👉|➡️|:)?[ \t]*$`)
	botMultiBlankRe    = regexp.MustCompile(`\n{3,}`)
	// "reserva en." / "aquí:" left dangling where the URL was.
	botDanglingRe = regexp.MustCompile(`(?i)\s+(en|aquí|aqui|desde|en la web|en este enlace|here|at)\s*([.:,!]?)(\s*)$`)
)

// botTidyAfterLinkRemoval drops the arrows / "🔗" left alone on a line and
// collapses blank lines, so the body reads naturally without the URL.
func botTidyAfterLinkRemoval(t string) string {
	lines := strings.Split(t, "\n")
	for i, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed == "🔗" || trimmed == "👉" || trimmed == "➡️" || trimmed == "." {
			lines[i] = ""
			continue
		}
		lines[i] = strings.TrimRight(l, " \t")
	}
	t = strings.Join(lines, "\n")
	t = strings.ReplaceAll(t, " 👉 ", " ")
	t = strings.ReplaceAll(t, " .", ".")
	t = strings.ReplaceAll(t, " ,", ",")
	t = strings.ReplaceAll(t, "🔗 \n", "\n")
	t = botMultiBlankRe.ReplaceAllString(t, "\n\n")
	lines2 := strings.Split(t, "\n")
	for i, l := range lines2 {
		lines2[i] = botDanglingRe.ReplaceAllString(l, " (botón abajo 👇)$2$3")
	}
	t = strings.Join(lines2, "\n")
	for strings.Contains(t, "  ") {
		t = strings.ReplaceAll(t, "  ", " ")
	}
	return strings.TrimSpace(t)
}
