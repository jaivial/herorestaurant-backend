package api

import (
	"regexp"
	"strings"
)

// Coordination id: wa_bot_whatsapp_format_v1
//
// LLMs answer in Markdown; WhatsApp renders *bold*, _italic_, ~strike~ and
// ```mono``` only. "**x**", "## Title" and "[text](url)" are shown literally
// (seen in 7/100 live scenarios). Every model-authored message is normalized
// before sending; fixed server texts are already WhatsApp-native.

var (
	botMdBoldItRe  = regexp.MustCompile(`\*\*\*([^*\n]+)\*\*\*`)
	botMdBoldRe    = regexp.MustCompile(`\*\*([^*\n]+)\*\*`)
	botMdBoldUsRe  = regexp.MustCompile(`__([^_\n]+)__`)
	botMdHeaderRe  = regexp.MustCompile(`(?m)^\s{0,3}#{1,6}\s+(.+?)\s*#*\s*$`)
	botMdLinkRe    = regexp.MustCompile(`\[([^\]\n]+)\]\((https?://[^)\s]+)\)`)
	botMdBulletRe  = regexp.MustCompile(`(?m)^(\s*)[*+]\s+`)
	botMdHrRe      = regexp.MustCompile(`(?m)^\s*(-{3,}|\*{3,}|_{3,})\s*$\n?`)
	botBlankRunsRe = regexp.MustCompile(`\n{3,}`)
)

// botWhatsAppFormat converts common Markdown into WhatsApp formatting.
func botWhatsAppFormat(text string) string {
	t := strings.ReplaceAll(text, "\r\n", "\n")
	t = botMdHrRe.ReplaceAllString(t, "\n")
	t = botMdLinkRe.ReplaceAllStringFunc(t, func(m string) string {
		p := botMdLinkRe.FindStringSubmatch(m)
		if strings.TrimSpace(p[1]) == p[2] {
			return p[2]
		}
		return p[1] + ": " + p[2]
	})
	// Bullets "* item" would collide with the bold marker: use "- item".
	t = botMdBulletRe.ReplaceAllString(t, "${1}- ")
	t = botMdBoldItRe.ReplaceAllString(t, "*$1*")
	t = botMdBoldRe.ReplaceAllString(t, "*$1*")
	t = botMdBoldUsRe.ReplaceAllString(t, "*$1*")
	t = botMdHeaderRe.ReplaceAllString(t, "*$1*")
	t = botBlankRunsRe.ReplaceAllString(t, "\n\n")
	return strings.TrimSpace(t)
}

// botModelAuthoredSources are the outbound sources whose text the LLM wrote.
var botModelAuthoredSources = map[string]bool{
	"agent": true, "agent_plain_text": true, "agent_contact_intro": true,
}
