package graphrag

import (
	"context"
	"errors"
	"math"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// Snippet is one retrieved node.
type Snippet struct {
	UID, Kind, Title, Body string
	Score                  float64
}

// Enabled: VC_GRAPHRAG=1 (default OFF).
func Enabled() bool { return os.Getenv("VC_GRAPHRAG") == "1" }

// maxScan bounds the candidate nodes read per query.
const maxScan = 2000

// What the WhatsApp bot asks for on every turn (graphrag_retrieve_v1).
const (
	BotK       = 4
	BotTimeout = 3 * time.Second
)

// BotSnippets is exactly the graph retrieval the bot prepends to its RAG
// (same k and timeout); graph-ingest -query calls it to reproduce a bot turn.
func BotSnippets(ctx context.Context, restaurantID int, text string) ([]Snippet, error) {
	gctx, cancel := context.WithTimeout(ctx, BotTimeout)
	defer cancel()
	return Search(gctx, restaurantID, text, BotK)
}

// Query is Search without the error: any failure yields nil so callers fall
// back to the existing RAG.
func Query(ctx context.Context, restaurantID int, text string, k int) []Snippet {
	out, _ := Search(ctx, restaurantID, text, k)
	return out
}

// Field weights: tags (kind aliases, wine type, menu section) > title > body.
const (
	wTags  = 4.0
	wTitle = 3.0
	wBody  = 1.0
)

// Search returns up to k snippets of restaurantID's graph ranked by weighted
// term overlap (tags > title > body, IDF over the restaurant's nodes).
// restaurantID must come from the authenticated context. Rows of another
// restaurant are dropped (defence in depth; the instance is per restaurant).
func Search(ctx context.Context, restaurantID int, text string, k int) ([]Snippet, error) {
	if !Enabled() {
		return nil, errors.New("VC_GRAPHRAG is not 1")
	}
	if restaurantID <= 0 || k <= 0 {
		return nil, errors.New("restaurant id and k required")
	}
	terms := Terms(text)
	if len(terms) == 0 {
		return nil, nil
	}
	in, err := Lookup(RegistryPath(), restaurantID)
	if err != nil {
		return nil, err
	}
	res, err := in.Query(ctx, "bot", Statement{
		Cypher: "MATCH (n:Doc {restaurant_id: $r}) RETURN n.uid, n.kind, n.title, n.body, n.restaurant_id, n.tags LIMIT " + itoa(maxScan),
		Params: map[string]any{"r": restaurantID},
	})
	if err != nil {
		return nil, err
	}
	type doc struct {
		s                 Snippet
		tags, title, body string
	}
	var docs []doc
	df := map[string]int{}
	for _, row := range res.Rows {
		if len(row) < 5 || toInt(row[4]) != restaurantID {
			continue
		}
		d := doc{s: Snippet{UID: str(row[0]), Kind: str(row[1]), Title: str(row[2]), Body: str(row[3])}}
		if len(row) > 5 {
			d.tags = field(str(row[5]))
		}
		d.title, d.body = field(d.s.Title), field(d.s.Body)
		for _, t := range terms {
			if has(d.tags, t) || has(d.title, t) || has(d.body, t) {
				df[t]++
			}
		}
		docs = append(docs, d)
	}
	var out []Snippet
	for _, d := range docs {
		for _, t := range terms {
			w := 0.0
			switch {
			case has(d.tags, t):
				w = wTags
			case has(d.title, t):
				w = wTitle
			case has(d.body, t):
				w = wBody
			}
			if w > 0 {
				d.s.Score += w * math.Log(1+float64(len(docs))/float64(df[t]))
			}
		}
		if d.s.Score > 0 {
			out = append(out, d.s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > k {
		out = out[:k]
	}
	return out, nil
}

// field is the normalised, space-delimited term string of s.
func field(s string) string { return " " + strings.Join(Terms(s), " ") + " " }

// has: some word of the field starts with term t (prefix match).
func has(field, t string) bool { return strings.Contains(field, " "+t) }

// stop holds post-stem forms of words >= 3 letters that carry no meaning.
var stop = map[string]bool{
	"los": true, "las": true, "una": true, "con": true, "sin": true, "que": true, "por": true, "para": true, "del": true,
	"the": true, "and": true, "hay": true, "tenei": true, "tienen": true, "tiene": true, "tenemo": true, "cual": true,
	"cuale": true, "como": true, "quiero": true, "querria": true, "puedo": true, "podria": true, "hola": true,
	"buena": true, "bueno": true, "gracia": true, "algun": true, "alguna": true, "alguno": true, "nos": true,
	"vuestro": true, "vuestra": true, "son": true, "esta": true, "este": true, "eso": true, "esto": true, "favor": true,
}

// Terms lowercases, strips accents, splits into words of >= 3 letters (or
// 2-digit numbers, e.g. a day of the month) and
// applies a light Spanish plural stem (arroces->arroz, jamones->jamon,
// postres->postre), so query and graph text normalise the same way.
func Terms(s string) []string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	s, _, _ = transform.String(t, strings.ToLower(s))
	var out []string
	for _, w := range strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		w = stem(w)
		if (len([]rune(w)) >= 3 || (len(w) == 2 && unicode.IsDigit(rune(w[0])) && unicode.IsDigit(rune(w[1])))) && !stop[w] {
			out = append(out, w)
		}
	}
	return out
}

func stem(w string) string {
	n := len(w)
	switch {
	case n > 5 && strings.HasSuffix(w, "ces") && strings.ContainsRune("aeiou", rune(w[n-4])):
		return w[:n-3] + "z"
	case n > 5 && strings.HasSuffix(w, "ones"):
		return w[:n-2]
	case n > 3 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss"):
		return w[:n-1]
	}
	return w
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
