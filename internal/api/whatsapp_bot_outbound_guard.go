package api

import (
	"regexp"
	"strings"
)

// Coordination id: wa_bot_outbound_guard_v1
//
// Every text the bot sends to a person goes through botOutboundText:
//   - sentences that leak INTERNAL jargon are removed (tool names from both
//     catalogs, snake_case tokens, JSON/code fragments): the chat must read
//     like a person, never like a stack trace;
//   - anything longer than botOutboundMaxRunes is split into short parts, so
//     WhatsApp never shows a wall of text.
//
// The heuristics are line-level (the model writes short lines) and zero-cost:
// they run on the outbound text only, never on tool results.

const (
	// botOutboundMaxRunes is the maximum size of ONE WhatsApp text part.
	botOutboundMaxRunes = 500
	// botOutboundMaxParts bounds the split; the remainder is folded into the
	// last part with an ellipsis instead of sending a fifth message.
	botOutboundMaxParts = 4
)

// botOutboundSnakeRe matches snake_case tokens (tool names, config keys).
// Spanish prose never writes like this, so a match means internal jargon.
var botOutboundSnakeRe = regexp.MustCompile(`\b[a-z][a-z0-9]{2,}(?:_[a-z0-9]+)+\b`)

// botOutboundCodeRe matches code fragments the model must never paste: code
// fences, a JSON object opening or a quoted key with a colon.
var botOutboundCodeRe = regexp.MustCompile("```" + `|\{"|"\s*:`)

// botOutboundLineLeaks reports whether one line contains internal jargon.
func botOutboundLineLeaks(line string) bool {
	return botOutboundCodeRe.MatchString(line) || botOutboundSnakeRe.MatchString(line)
}

// botOutboundStripJargon removes the lines that leak internal jargon and
// keeps the rest of the message readable.
func botOutboundStripJargon(text string) string {
	lines := strings.Split(text, "\n")
	kept := make([]string, 0, len(lines))
	removed := 0
	for _, line := range lines {
		if botOutboundLineLeaks(line) {
			removed++
			continue
		}
		kept = append(kept, line)
	}
	out := strings.TrimSpace(strings.Join(kept, "\n"))
	if removed > 0 && out != strings.TrimSpace(text) {
		botCheckpoint("wa_bot_outbound_guard_v1", "jargon_lines_removed=%d chars_in=%d", removed, len(text))
	}
	return out
}

// botOutboundSplit splits cleaned text into short parts on line/space
// boundaries so no part exceeds the cap.
func botOutboundSplit(text string) []string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) == 0 {
		return nil
	}
	if len(runes) <= botOutboundMaxRunes {
		return []string{string(runes)}
	}
	parts := make([]string, 0, 2)
	for len(runes) > 0 && len(parts) < botOutboundMaxParts {
		cut := botOutboundMaxRunes
		if len(runes) < cut {
			cut = len(runes)
		}
		chunk := string(runes[:cut])
		if idx := strings.LastIndex(chunk, "\n"); idx > botOutboundMaxRunes/2 {
			cut, chunk = idx, string(runes[:idx])
		} else if idx := strings.LastIndex(chunk, " "); idx > botOutboundMaxRunes/2 {
			cut, chunk = idx, string(runes[:idx])
		}
		if part := strings.TrimSpace(chunk); part != "" {
			parts = append(parts, part)
		}
		runes = runes[cut:]
	}
	if len(runes) > 0 && len(parts) == botOutboundMaxParts {
		parts[len(parts)-1] += " …"
	}
	return parts
}

// botOutboundText is THE outbound gate: sanitize, then split.
func botOutboundText(text string) []string {
	clean := botOutboundStripJargon(text)
	return botOutboundSplit(clean)
}
