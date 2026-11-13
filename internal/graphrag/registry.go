// Package graphrag is the per-restaurant knowledge graph client
// (coordination ids: graphrag_registry_v1, graphrag_retrieve_v1).
// Isolation model: one neo4j-rust process per restaurant, loopback only, its
// own data dir and 0600 token. The restaurant -> (port, token file) mapping
// lives ONLY in the server-side registry (~/graphrag/registry.tsv, 0600);
// callers pass the restaurant id of the authenticated context, never a port,
// graph name or token. Tokens are never logged or returned.
package graphrag

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Host is the only host ever contacted.
const Host = "127.0.0.1"

// Instance is one restaurant's graph endpoint (token kept unexported).
type Instance struct {
	RestaurantID int
	Port         int
	TokenFile    string
	token        string
}

// RegistryPath: VC_GRAPHRAG_REGISTRY or ~/graphrag/registry.tsv.
func RegistryPath() string {
	if p := strings.TrimSpace(os.Getenv("VC_GRAPHRAG_REGISTRY")); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "graphrag", "registry.tsv")
}

func checkPrivate(p, what string) error {
	st, err := os.Stat(p)
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if !st.Mode().IsRegular() || st.Mode().Perm() != 0o600 {
		return fmt.Errorf("%s must be a regular 0600 file (is %v)", what, st.Mode().Perm())
	}
	return nil
}

// Lookup resolves EXACTLY one restaurant from the registry (columns:
// restaurant_id, http_port, bolt_port, token_file) and loads its token.
// Registry and token file must be 0600; a duplicated restaurant id or a port
// shared by two restaurants is refused (an instance is never shared).
func Lookup(registry string, rid int) (*Instance, error) {
	if rid <= 0 {
		return nil, fmt.Errorf("restaurant id required")
	}
	if err := checkPrivate(registry, "registry"); err != nil {
		return nil, err
	}
	f, err := os.Open(registry)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var hit *Instance
	ports := map[int]int{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		c := strings.Split(strings.TrimSpace(sc.Text()), "\t")
		if len(c) < 4 || strings.HasPrefix(c[0], "#") {
			continue
		}
		r, e1 := strconv.Atoi(c[0])
		port, e2 := strconv.Atoi(c[1])
		if e1 != nil || e2 != nil || port < 1024 || port > 65535 {
			return nil, fmt.Errorf("registry: malformed line")
		}
		if o, dup := ports[port]; dup && o != r {
			return nil, fmt.Errorf("registry: port %d shared by two restaurants", port)
		}
		ports[port] = r
		if r == rid {
			if hit != nil {
				return nil, fmt.Errorf("registry: restaurant %d listed twice", rid)
			}
			hit = &Instance{RestaurantID: r, Port: port, TokenFile: c[3]}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if hit == nil {
		return nil, fmt.Errorf("restaurant %d not registered", rid)
	}
	if err := checkPrivate(hit.TokenFile, "token file"); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(hit.TokenFile)
	if err != nil {
		return nil, err
	}
	if hit.token = strings.TrimSpace(string(b)); hit.token == "" {
		return nil, fmt.Errorf("token file empty")
	}
	return hit, nil
}

// LoopbackOnly reports how the instance port is bound, from /proc/net/tcp
// (IPv4) and /proc/net/tcp6: "loopback", "not-listening" or an error when
// it listens on any non-loopback address.
func (in *Instance) LoopbackOnly() (string, error) {
	state := "not-listening"
	for _, p := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for _, ln := range strings.Split(string(b), "\n")[1:] {
			f := strings.Fields(ln)
			if len(f) < 4 || f[3] != "0A" { // LISTEN
				continue
			}
			i := strings.LastIndex(f[1], ":")
			if i < 0 {
				continue
			}
			port, err := strconv.ParseInt(f[1][i+1:], 16, 32)
			if err != nil || int(port) != in.Port {
				continue
			}
			addr := f[1][:i]
			if addr == "0100007F" || addr == "00000000000000000000000001000000" {
				state = "loopback"
				continue
			}
			return "", fmt.Errorf("port %d listens on a non-loopback address", in.Port)
		}
	}
	return state, nil
}

func (in *Instance) base() string { return "http://" + Host + ":" + strconv.Itoa(in.Port) }

// Statement is one parameterised Cypher statement (fixed templates only).
type Statement struct {
	Cypher string         `json:"cypher"`
	Params map[string]any `json:"params"`
}

// Result is the /query response shape.
type Result struct {
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
}

var httpc = &http.Client{Timeout: 20 * time.Second}

func (in *Instance) post(ctx context.Context, path, source string, body any) ([]byte, error) {
	j, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, in.base()+path, bytes.NewReader(j))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+in.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Source", source+":"+strconv.Itoa(in.RestaurantID))
	res, err := httpc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("graph request failed") // never echo URL/headers
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if res.StatusCode/100 != 2 {
		msg := string(out)
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return nil, fmt.Errorf("graph status %d: %s", res.StatusCode, msg)
	}
	return out, nil
}

// Query runs one statement (autocommit).
func (in *Instance) Query(ctx context.Context, source string, st Statement) (*Result, error) {
	b, err := in.post(ctx, "/query", source, st)
	if err != nil {
		return nil, err
	}
	var r Result
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// Tx runs statements all-or-nothing in one /tx batch.
func (in *Instance) Tx(ctx context.Context, source string, sts []Statement) error {
	if len(sts) == 0 {
		return nil
	}
	_, err := in.post(ctx, "/tx", source, map[string]any{"statements": sts, "commit": true})
	return err
}
