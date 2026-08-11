package api

import (
	"regexp"
	"strings"
	"unicode"
)

// ---------------------------------------------------------------------------
// Reply quality gate.
//
// MiniMax intermittently returns unusable text: symbol soup, letter salad, a
// base64 blob, or Spanish peppered with foreign scripts. On its own that is a
// cosmetic annoyance, but the reply is persisted and then replayed as
// conversation history on the following turn, where it primes the model to
// produce more of the same. One bad answer therefore degrades the whole
// session (18% of stored history was contaminated when this was written).
//
// Breaking that loop is what matters: garbled text is never persisted and never
// replayed as context, so a session recovers by itself on the next question.
// ---------------------------------------------------------------------------

// assistantChartBlock matches a fenced forky-chart block, which is machine
// generated JSON and must not be judged as prose.
var assistantChartBlock = regexp.MustCompile("(?s)```forky-chart.*?```")

// assistantLongBase64Run matches a base64 blob left in a reply. Runs made only
// of '-'/'=' are excluded by the mixed-alphabet check in the caller: markdown
// table separators ("|------|", "|======|") are not payloads.
var assistantLongBase64Run = regexp.MustCompile(`[A-Za-z0-9+/=_-]{60,}`)

// assistantLooksLikePayload reports whether a matched run is really base64 and
// not a markdown rule: a payload mixes letters and digits, a separator does not.
func assistantLooksLikePayload(run string) bool {
	var letters, digits int
	for _, r := range run {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z':
			letters++
		case r >= '0' && r <= '9':
			digits++
		}
	}
	return letters >= 12 && float64(letters+digits)/float64(len(run)) > 0.85
}

// assistantReplyIsGarbled reports whether a model reply is unusable text that
// must be kept out of the conversation. It is deliberately conservative: a false
// positive silences a good answer, so only strong signals count.
func assistantReplyIsGarbled(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false // nothing to judge; empty turns are handled by the caller
	}

	// A leftover base64 blob is never something to keep or replay.
	for _, run := range assistantLongBase64Run.FindAllString(strings.ReplaceAll(trimmed, "\n", ""), -1) {
		if assistantLooksLikePayload(run) {
			return true
		}
	}

	// Judge prose only: strip chart JSON and markdown table rows, which are
	// legitimately dense in punctuation and digits.
	prose := assistantChartBlock.ReplaceAllString(trimmed, "")
	lines := make([]string, 0, 8)
	for _, ln := range strings.Split(prose, "\n") {
		s := strings.TrimSpace(ln)
		if strings.HasPrefix(s, "|") || strings.HasPrefix(s, "```") {
			continue
		}
		lines = append(lines, ln)
	}
	prose = strings.TrimSpace(strings.Join(lines, "\n"))
	if prose == "" {
		return false // pure table/chart reply: structured output, not garbage
	}
	// A bare JSON object/array is a tool result echoed verbatim. That is a
	// separate prompt-following problem, not corrupted text, and it must stay
	// readable in history rather than be silently dropped.
	if (strings.HasPrefix(prose, "{") && strings.HasSuffix(prose, "}")) ||
		(strings.HasPrefix(prose, "[") && strings.HasSuffix(prose, "]")) {
		return false
	}

	var letters, spanish, foreign, symbols, replacement, vowels, total int
	for _, r := range prose {
		if unicode.IsSpace(r) {
			continue
		}
		total++
		switch {
		case r == 0xFFFD:
			replacement++
		case unicode.IsLetter(r):
			letters++
			if assistantIsVowel(r) {
				vowels++
			}
			if assistantForeignScript(r) {
				foreign++
			} else if r > 0x7F {
				spanish++
			}
		case unicode.IsDigit(r), unicode.IsPunct(r):
			// digits and punctuation are normal in prices, dates and prose
		case assistantAllowedSymbol(r):
			// emoji and currency the model legitimately uses
		default:
			symbols++
		}
	}
	if total < 12 {
		return false // too short to judge reliably (e.g. "Hecho ✅")
	}

	// Any foreign script in a Spanish reply is a strong corruption signal.
	if foreign > 0 {
		return true
	}
	// Mojibake replacement characters.
	if float64(replacement)/float64(total) > 0.05 {
		return true
	}
	// Stray symbol glyphs used as filler ("⛺⛅⛟C⌃F Rv⌃").
	if float64(symbols)/float64(total) > 0.10 {
		return true
	}
	// Letter salad: real prose is mostly letters. "ido=HHwhHHwh=1HHwh-cDD/DD"
	// is dominated by digits/punctuation with almost no words.
	if float64(letters)/float64(total) < 0.45 {
		return true
	}
	// Accent density far above natural Spanish indicates byte-level corruption.
	if letters > 0 && float64(spanish)/float64(letters) > 0.35 {
		return true
	}
	// Vowel starvation: Spanish runs ~45% vowels, so consonant salad like
	// "ido=HHwhHHwh=1HHwh-cDD/DD" (6%) is not words. Only judged on replies long
	// enough for the ratio to be meaningful.
	if letters >= 20 && float64(vowels)/float64(letters) < 0.20 {
		return true
	}
	return false
}

// assistantIsVowel reports whether r is a Spanish vowel (accented or not).
func assistantIsVowel(r rune) bool {
	switch unicode.ToLower(r) {
	case 'a', 'e', 'i', 'o', 'u', 'á', 'é', 'í', 'ó', 'ú', 'ü':
		return true
	}
	return false
}

// assistantForeignScript reports whether r belongs to a script that never
// appears in a Spanish restaurant reply (CJK, Hebrew, Arabic, Cyrillic, etc.).
// Latin, Greek-free accents and emoji are unaffected.
func assistantForeignScript(r rune) bool {
	switch {
	case r >= 0x0400 && r <= 0x04FF: // Cyrillic
	case r >= 0x0590 && r <= 0x05FF: // Hebrew
	case r >= 0x0600 && r <= 0x06FF, r >= 0xFB50 && r <= 0xFDFF, r >= 0xFE70 && r <= 0xFEFF: // Arabic
	case r >= 0x3040 && r <= 0x30FF: // Hiragana/Katakana
	case r >= 0x3400 && r <= 0x4DBF, r >= 0x4E00 && r <= 0x9FFF: // CJK
	case r >= 0xF900 && r <= 0xFAFF: // CJK compatibility
	case r >= 0xAC00 && r <= 0xD7AF: // Hangul
	case r >= 0x0370 && r <= 0x03FF: // Greek
	default:
		return false
	}
	return true
}

// assistantAllowedSymbol reports whether r is a symbol the model legitimately
// uses: emoji, currency, arrows and the dingbats that decorate replies.
func assistantAllowedSymbol(r rune) bool {
	switch {
	case r >= 0x1F000 && r <= 0x1FAFF: // emoji planes
	case r >= 0x2600 && r <= 0x27BF: // misc symbols + dingbats (✅ ➡ ✨ ⚡)
	case r >= 0x2B00 && r <= 0x2BFF: // misc symbols and arrows (⭐)
	case r == 0x20AC, r == 0x24, r == 0xA3: // € $ £
	case r == 0xFE0F, r == 0x200D, r == 0x20E3: // variation selector, ZWJ, keycap
	case r >= 0x2000 && r <= 0x206F: // general punctuation (– — … ‘ ’ “ ”)
	case r == 0xB0, r == 0xBA, r == 0xAA: // ° º ª
	case r >= 0x1F1E6 && r <= 0x1F1FF: // regional indicators
	default:
		return false
	}
	return true
}

// assistantSanitizeForHistory returns the text to persist for a reply: empty
// when the reply is garbled, so it never re-enters the conversation.
func assistantSanitizeForHistory(text string) string {
	if assistantReplyIsGarbled(text) {
		return ""
	}
	return text
}

// assistantFilterHistory drops contaminated assistant turns from replayed
// history. Rows already stored before this gate existed would otherwise keep
// poisoning their session forever. User turns are always preserved: they carry
// the question, and dropping them would lose the thread of the conversation.
func assistantFilterHistory(msgs []assistantChatMessage) []assistantChatMessage {
	out := make([]assistantChatMessage, 0, len(msgs))
	for _, m := range msgs {
		text, isText := m.Content.(string)
		if m.Role == "assistant" && isText {
			if strings.TrimSpace(text) == "" || assistantReplyIsGarbled(text) {
				continue
			}
		}
		out = append(out, m)
	}
	return out
}
