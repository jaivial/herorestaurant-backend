package api

import (
	"regexp"
	"strings"
	"unicode"
)

// Coordination id: wa_bot_outbound_guard_v1 (+ wa_bot_outbound_split_v2)
//
// Every text the bot sends to a person goes through botOutboundText:
//   - sentences that leak INTERNAL jargon are removed (tool names from both
//     catalogs, snake_case tokens, JSON/code fragments): the chat must read
//     like a person, never like a stack trace;
//   - anything longer than botOutboundMaxRunes becomes SEVERAL SHORT
//     messages split at sentence/paragraph boundaries (never a mid-sentence
//     cut), so WhatsApp never shows a collapsed wall of text and no content
//     is ever dropped (wa_bot_outbound_split_v2).
//
// The heuristics are line-level (the model writes short lines) and zero-cost:
// they run on the outbound text only, never on tool results.

const (
	// botOutboundMaxRunes is the maximum size of ONE WhatsApp text part.
	botOutboundMaxRunes = 500
	// botOutboundMaxParts bounds the split. The remainder is folded into the
	// last part, NEVER discarded: a lost sentence is a wrong answer.
	// Coordination id: wa_bot_outbound_split_v2.
	botOutboundMaxParts = 12
)

// botOutboundUnit is one atomic piece of the reply: a whole line, a whole
// sentence of a long line, a clause of one over-long sentence or a run of
// words. startsLine records whether this unit began a new line in the
// original text, so the parts keep the model's paragraph layout.
type botOutboundUnit struct {
	text       string
	startsLine bool
}

// botOutboundSentenceCuts returns the rune end-indexes (exclusive, including
// the trailing spaces) of every sentence in r. A terminator is '.', '!', '?'
// or '…' followed by optional closing quotes/brackets and then a space or the
// end of the line; "12.50" (digit after the dot) never cuts.
func botOutboundSentenceCuts(r []rune) []int {
	const terminators = ".!?…"
	const closers = "\"')»”’]"
	var cuts []int
	for i := 0; i < len(r); i++ {
		if !strings.ContainsRune(terminators, r[i]) {
			continue
		}
		j := i + 1
		for j < len(r) && strings.ContainsRune(closers, r[j]) {
			j++
		}
		if j < len(r) && r[j] != ' ' && r[j] != '\t' {
			continue // e.g. the dot of 12.50: not a sentence end
		}
		for j < len(r) && (r[j] == ' ' || r[j] == '\t') {
			j++
		}
		cuts = append(cuts, j)
		i = j - 1
	}
	return cuts
}

// botOutboundClauseCuts returns the rune end-indexes of the clause ends in r
// (',', ';', ':' or a dash followed by a space). Used only inside a sentence
// that is longer than the cap on its own.
func botOutboundClauseCuts(r []rune) []int {
	const clauses = ",;:—–"
	var cuts []int
	for i := 0; i+1 < len(r); i++ {
		if !strings.ContainsRune(clauses, r[i]) {
			continue
		}
		j := i + 1
		for j < len(r) && (r[j] == ' ' || r[j] == '\t') {
			j++
		}
		if j > i+1 {
			cuts = append(cuts, j)
		}
	}
	return cuts
}

// botOutboundSplitUnits breaks text into atomic units. Priority: whole lines;
// whole sentences of a long line; clauses ONLY of one sentence that exceeds
// the cap alone; then word runs; and only a spaceless token longer than the
// cap (a pasted URL/hash) is hard-cut by runes. Sentences are never cut at a
// comma while a sentence boundary fits (wa_bot_outbound_split_v2).
func botOutboundSplitUnits(text string) []botOutboundUnit {
	capR := botOutboundMaxRunes
	units := make([]botOutboundUnit, 0, 8)
	emit := func(piece []rune, startsLine bool) {
		if trimmed := strings.TrimRight(string(piece), " \t"); trimmed != "" {
			units = append(units, botOutboundUnit{text: trimmed, startsLine: startsLine})
		}
	}
	for li, line := range strings.Split(text, "\n") {
		startsLine := li > 0
		r := []rune(strings.TrimRight(line, "\r"))
		if len(r) <= capR {
			emit(r, startsLine)
			continue
		}
		for len(r) > 0 {
			r = trimLeftSpaces(r)
			if len(r) == 0 {
				break
			}
			sc := botOutboundSentenceCuts(r)
			firstSentence := len(r)
			if len(sc) > 0 {
				firstSentence = sc[0]
			}
			if firstSentence <= capR {
				// ONE sentence per unit: the grouping then packs them evenly
				// with the rest of the reply (a short title line merges with
				// the first sentences instead of travelling alone).
				emit(r[:firstSentence], startsLine)
				r = r[firstSentence:]
				startsLine = false
				continue
			}
			// The FIRST sentence alone exceeds the cap: fall back inside THAT
			// sentence only, so a later sentence's comma can never cut a
			// different sentence mid-phrase.
			piece := r[:firstSentence]
			cc := botOutboundClauseCuts(piece)
			cut := 0
			for _, e := range cc {
				if e <= capR {
					cut = e
				} else {
					break
				}
			}
			if cut == 0 {
				// Last space inside the window (word run).
				limit := capR
				if limit > len(piece) {
					limit = len(piece)
				}
				for i := 0; i < limit; i++ {
					if piece[i] == ' ' || piece[i] == '\t' {
						cut = i + 1
					}
				}
			}
			if cut == 0 {
				cut = capR // spaceless token (URL/hash): hard rune cut
				if cut > len(piece) {
					cut = len(piece)
				}
			}
			emit(r[:cut], startsLine)
			r = r[cut:]
			startsLine = false
		}
	}
	return units
}

func trimLeftSpaces(r []rune) []rune {
	i := 0
	for i < len(r) && (r[i] == ' ' || r[i] == '\t') {
		i++
	}
	return r[i:]
}

// botOutboundSplit groups the units into parts no longer than the cap,
// keeping the original line breaks between units of the same part. A part is
// a short WhatsApp message of COMPLETE sentences: never a mid-sentence cut.
// Coordination id: wa_bot_outbound_split_v2 - several short messages instead
// of one collapsed wall; content is never dropped, the tail folds into the
// last part when the safety cap is reached.
func botOutboundSplit(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	units := botOutboundSplitUnits(text)
	parts := make([]string, 0, 2)
	var cur strings.Builder
	curRunes := 0
	flush := func() {
		if part := strings.TrimSpace(cur.String()); part != "" {
			parts = append(parts, part)
		}
		cur.Reset()
		curRunes = 0
	}
	for _, u := range units {
		uRunes := len([]rune(u.text))
		joiner := ""
		if cur.Len() > 0 {
			if u.startsLine {
				joiner = "\n"
			} else {
				joiner = " "
			}
		}
		if curRunes+len([]rune(joiner))+uRunes > botOutboundMaxRunes && cur.Len() > 0 {
			flush()
			joiner = ""
		}
		cur.WriteString(joiner)
		cur.WriteString(u.text)
		curRunes += len([]rune(joiner)) + uRunes
	}
	flush()
	if len(parts) > botOutboundMaxParts {
		// Safety valve for pathological output: fold, never discard.
		last := strings.Join(parts[botOutboundMaxParts-1:], "\n")
		parts = append(parts[:botOutboundMaxParts-1], last)
		botCheckpoint("wa_bot_outbound_split_v2", "folded_tail parts=%d", len(parts))
	}
	if len(parts) > 1 {
		botCheckpoint("wa_bot_outbound_split_v2", "multi_part parts=%d chars=%d", len(parts), len(text))
	}
	return parts
}

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

// botOutboundText is THE outbound gate: sanitize, then split.
func botOutboundText(text string) []string {
	clean := botOutboundStripJargon(botOutboundStripCJK(text))
	return botOutboundSplit(clean)
}

// botOutboundCJKRe matches runs of Chinese ideographs and CJK/full-width
// punctuation.
var botOutboundCJKRe = regexp.MustCompile(`[\p{Han}\x{3000}-\x{303F}\x{FF00}-\x{FFEF}]+`)
var botOutboundSpacesRe = regexp.MustCompile(`[ \t]{2,}`)

// botOutboundStripCJK removes the Chinese fragments MiniMax leaks into a reply
// written in another language (wa_bot_outbound_cjk_v1, prod 2026-10-30:
// "Ojal\u00e1\u7684\u7a7a\u4f4d\u8fd8\u5728\u3002"). A reply that is mostly Chinese (a
// Chinese-speaking customer) is left untouched.
func botOutboundStripCJK(text string) string {
	letters, han := 0, 0
	for _, r := range text {
		if unicode.IsLetter(r) {
			letters++
			if unicode.Is(unicode.Han, r) {
				han++
			}
		}
	}
	if han == 0 || han*2 > letters {
		return text
	}
	out := strings.TrimSpace(botOutboundSpacesRe.ReplaceAllString(botOutboundCJKRe.ReplaceAllString(text, " "), " "))
	botCheckpoint("wa_bot_outbound_cjk_v1", "han_removed=%d chars_in=%d", han, len(text))
	return out
}
