// Command graph-ingest upserts inventoried bot prompt/markdown sources into ONE
// restaurant's neo4j-rust instance (graphrag_ingest_v1). Dry-run by default.
// The port and token file come only from the server-side registry; the host is
// always 127.0.0.1. Tokens are never printed.
package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type source struct{ Kind, Scope, Path string }
type chunk struct{ Title, Body string }
type stmt struct {
	Cypher string         `json:"cypher"`
	Params map[string]any `json:"params"`
}

func main() {
	home, _ := os.UserHomeDir()
	rid := flag.Int("restaurant-id", 0, "restaurant id (required)")
	reg := flag.String("registry", filepath.Join(home, "graphrag", "registry.tsv"), "server-side registry")
	inv := flag.String("inventory", "docs/graphrag/sources.tsv", "kind<TAB>scope<TAB>path")
	apply := flag.Bool("apply", false, "write to the graph (default dry-run)")
	flag.Parse()
	if *rid <= 0 {
		fail("refusing: -restaurant-id is required")
	}
	srcs, err := readInventory(*inv)
	if err != nil {
		fail("inventory: " + err.Error())
	}
	base, token := "", ""
	if *apply {
		port, tokFile, err := lookup(*reg, *rid)
		if err != nil {
			fail("registry: " + err.Error())
		}
		base = "http://127.0.0.1:" + strconv.Itoa(port)
		if u, _ := url.Parse(base); u == nil || u.Hostname() != "127.0.0.1" {
			fail("refusing: non-loopback host")
		}
		if token, err = readToken(tokFile); err != nil {
			fail("token: " + err.Error())
		}
	}
	cl := &http.Client{Timeout: 30 * time.Second}
	var nSrc, nChunk, nSkip int
	for _, s := range srcs {
		raw, err := os.ReadFile(s.Path)
		if err != nil {
			fail("read " + s.Path + ": " + err.Error())
		}
		sum := sha256.Sum256(raw)
		hash := hex.EncodeToString(sum[:])
		chunks := split(string(raw))
		key := fmt.Sprintf("%d:%s", *rid, s.Path)
		fmt.Printf("source kind=%s scope=%s path=%s bytes=%d chunks=%d sha256=%s\n", s.Kind, s.Scope, s.Path, len(raw), len(chunks), hash[:12])
		nSrc++
		nChunk += len(chunks)
		if !*apply {
			continue
		}
		cur, err := post(cl, base, token, *rid, "/query", map[string]any{"cypher": "MATCH (s:Source {source_key: $k, restaurant_id: $r}) RETURN s.content_hash", "params": map[string]any{"k": key, "r": *rid}})
		if err != nil {
			fail("query " + s.Path + ": " + err.Error())
		}
		if bytes.Contains(cur, []byte(hash)) {
			nSkip++
			continue
		}
		p := map[string]any{"k": key, "r": *rid}
		st := []stmt{
			{"MATCH (s:Source {source_key: $k, restaurant_id: $r})-[h:HAS_CHUNK]->(c:Chunk) DELETE h, c", p},
			{"MATCH (s:Source {source_key: $k, restaurant_id: $r}) DELETE s", p},
			{"CREATE (s:Source {source_key: $k, restaurant_id: $r, kind: $kind, scope: $scope, path: $path, content_hash: $h, ingested_at: $t})",
				map[string]any{"k": key, "r": *rid, "kind": s.Kind, "scope": s.Scope, "path": s.Path, "h": hash, "t": time.Now().UTC().Format(time.RFC3339)}},
		}
		for i, c := range chunks {
			cs := sha256.Sum256([]byte(c.Body))
			st = append(st, stmt{"MATCH (s:Source {source_key: $k, restaurant_id: $r}) CREATE (s)-[:HAS_CHUNK]->(:Chunk:" + label(s.Kind) + " {restaurant_id: $r, ord: $o, title: $ti, body: $b, content_hash: $ch})",
				map[string]any{"k": key, "r": *rid, "o": i, "ti": c.Title, "b": c.Body, "ch": hex.EncodeToString(cs[:])}})
		}
		if _, err := post(cl, base, token, *rid, "/tx", map[string]any{"statements": st, "commit": true}); err != nil {
			fail("tx " + s.Path + ": " + err.Error())
		}
	}
	fmt.Printf("restaurant=%d apply=%v sources=%d chunks=%d unchanged=%d\n", *rid, *apply, nSrc, nChunk, nSkip)
}

// label maps an inventory kind to a fixed node label (allowlist, never user text).
func label(kind string) string {
	switch kind {
	case "prompt":
		return "Prompt"
	case "policy":
		return "Policy"
	case "edgecase":
		return "EdgeCase"
	case "faq":
		return "Faq"
	case "menu":
		return "Menu"
	}
	return "Rule"
}

// split chunks markdown by headings; non-markdown sources become one chunk.
func split(s string) []chunk {
	var out []chunk
	cur := chunk{Title: "(top)"}
	for _, ln := range strings.Split(s, "\n") {
		if strings.HasPrefix(ln, "#") {
			if strings.TrimSpace(cur.Body) != "" {
				out = append(out, cur)
			}
			cur = chunk{Title: strings.TrimSpace(strings.TrimLeft(ln, "#"))}
			continue
		}
		cur.Body += ln + "\n"
	}
	if strings.TrimSpace(cur.Body) != "" {
		out = append(out, cur)
	}
	return out
}

func readInventory(p string) ([]source, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []source
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		ln := sc.Text()
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		c := strings.Split(ln, "\t")
		if len(c) < 3 {
			return nil, fmt.Errorf("bad line %q", ln)
		}
		out = append(out, source{c[0], c[1], c[2]})
	}
	return out, sc.Err()
}

// lookup returns (http port, token file) for exactly one restaurant.
func lookup(p string, rid int) (int, string, error) {
	st, err := os.Stat(p)
	if err != nil {
		return 0, "", err
	}
	if st.Mode().Perm()&0o077 != 0 {
		return 0, "", fmt.Errorf("registry must be 0600")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return 0, "", err
	}
	for _, ln := range strings.Split(string(b), "\n") {
		c := strings.Split(ln, "\t")
		if len(c) >= 4 && c[0] == strconv.Itoa(rid) {
			port, err := strconv.Atoi(c[1])
			if err != nil || port < 1024 || port > 65535 {
				return 0, "", fmt.Errorf("bad port")
			}
			return port, c[3], nil
		}
	}
	return 0, "", fmt.Errorf("restaurant %d not registered", rid)
}

func readToken(p string) (string, error) {
	st, err := os.Stat(p)
	if err != nil {
		return "", err
	}
	if st.Mode().Perm() != 0o600 {
		return "", fmt.Errorf("token file must be 0600")
	}
	b, err := os.ReadFile(p)
	return strings.TrimSpace(string(b)), err
}

func post(cl *http.Client, base, token string, rid int, path string, body any) ([]byte, error) {
	j, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", base+path, bytes.NewReader(j))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Source", fmt.Sprintf("graph-ingest:%d", rid))
	res, err := cl.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode/100 != 2 {
		return nil, fmt.Errorf("status %d", res.StatusCode)
	}
	return out, nil
}

func fail(m string) { fmt.Fprintln(os.Stderr, m); os.Exit(1) }
