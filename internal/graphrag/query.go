package graphrag

import (
	"context"
	"os"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// Snippet is one retrieved node.
type Snippet struct {
	UID, Kind, Title, Body string
	Score                  int
}

// Enabled: VC_GRAPHRAG=1 (default OFF).
func Enabled() bool { return os.Getenv("VC_GRAPHRAG") == "1" }

// maxScan bounds the candidate nodes read per query.
const maxScan = 2000

// Query returns up to k snippets of restaurantID's graph ranked by term
// overlap with text. restaurantID must come from the authenticated context.
// Any error (flag off, not registered, permissions, instance down) yields nil
// so callers fall back to the existing RAG. Rows of another restaurant are
// dropped (defence in depth; the instance is per restaurant).
func Query(ctx context.Context, restaurantID int, text string, k int) []Snippet {
	if !Enabled() || restaurantID <= 0 || k <= 0 {
		return nil
	}
	in, err := Lookup(RegistryPath(), restaurantID)
	if err != nil {
		return nil
	}
	res, err := in.Query(ctx, "bot", Statement{
		Cypher: "MATCH (n:Doc {restaurant_id: $r}) RETURN n.uid, n.kind, n.title, n.body, n.restaurant_id LIMIT " + itoa(maxScan),
		Params: map[string]any{"r": restaurantID},
	})
	if err != nil {
		return nil
	}
	terms := Terms(text)
	if len(terms) == 0 {
		return nil
	}
	var out []Snippet
	for _, row := range res.Rows {
		if len(row) < 5 || toInt(row[4]) != restaurantID {
			continue
		}
		s := Snippet{UID: str(row[0]), Kind: str(row[1]), Title: str(row[2]), Body: str(row[3])}
		hay := " " + strings.Join(Terms(s.Title+" "+s.Body), " ") + " "
		title := " " + strings.Join(Terms(s.Title), " ") + " "
		for _, t := range terms {
			if strings.Contains(hay, " "+t) {
				s.Score++
				if strings.Contains(title, " "+t) {
					s.Score += 2
				}
			}
		}
		if s.Score > 0 {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > k {
		out = out[:k]
	}
	return out
}

var stop = map[string]bool{"de": true, "la": true, "el": true, "los": true, "las": true, "y": true, "o": true, "un": true, "una": true, "con": true, "sin": true, "que": true, "en": true, "por": true, "para": true, "del": true, "al": true, "the": true, "and": true}

// Terms lowercases, strips accents and splits into words of >= 3 letters.
func Terms(s string) []string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	s, _, _ = transform.String(t, strings.ToLower(s))
	var out []string
	for _, w := range strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len([]rune(w)) >= 3 && !stop[w] {
			out = append(out, w)
		}
	}
	return out
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func toInt(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	}
	return -1
}

func itoa(n int) string {
	b := []byte{}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if len(b) == 0 {
		return "0"
	}
	return string(b)
}
