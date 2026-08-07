package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// confirmationStore issues short-lived, single-use confirmation tokens. Tokens
// are opaque and stored only as hashes, preventing replay and disclosure.
type confirmationStore struct {
	mu      sync.Mutex
	entries map[string]confirmationEntry
}
type confirmationEntry struct {
	digest, user, restaurant, tool, args, session string
	expires                                       time.Time
	used                                          bool
}

func newConfirmationStore() *confirmationStore {
	return &confirmationStore{entries: make(map[string]confirmationEntry)}
}
func (s *confirmationStore) Issue(user, restaurant, tool, args, session string, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = 2 * time.Minute
	}
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	tok := base64.RawURLEncoding.EncodeToString(b)
	d := sha256.Sum256([]byte(tok))
	s.mu.Lock()
	s.entries[hex.EncodeToString(d[:])] = confirmationEntry{digest: hex.EncodeToString(d[:]), user: user, restaurant: restaurant, tool: tool, args: args, session: session, expires: time.Now().Add(ttl)}
	s.mu.Unlock()
	return tok, nil
}
func (s *confirmationStore) Consume(tok, user, restaurant, tool, args, session string) error {
	d := sha256.Sum256([]byte(tok))
	k := hex.EncodeToString(d[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[k]
	if !ok || e.used || time.Now().After(e.expires) {
		return fmt.Errorf("confirmation token expired or invalid")
	}
	if e.user != user || e.restaurant != restaurant || e.tool != tool || e.args != args || e.session != session {
		return fmt.Errorf("confirmation token does not match operation")
	}
	e.used = true
	s.entries[k] = e
	delete(s.entries, k)
	return nil
}

// confirmationArguments canonicalizes model input while excluding the mutable
// confirmation fields, binding the token to the actual operation arguments.
func confirmationArguments(raw []byte) string {
	var v map[string]any
	if json.Unmarshal(raw, &v) != nil {
		return string(raw)
	}
	delete(v, "confirmed")
	delete(v, "confirmation_token")
	b, _ := json.Marshal(v)
	return string(b)
}
