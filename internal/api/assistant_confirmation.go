package api

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// confirmationStore issues short-lived, single-use confirmation tokens. When a
// database is available the tokens are persisted (survive restarts and work
// across replicas); otherwise an in-memory map is used (unit tests, nil db).
type confirmationStore struct {
	mu      sync.Mutex
	entries map[string]confirmationEntry
	db      *sql.DB
}
type confirmationEntry struct {
	digest, user, restaurant, tool, args, session string
	expires                                       time.Time
	used                                          bool
}

func newConfirmationStore(db *sql.DB) *confirmationStore {
	return &confirmationStore{entries: make(map[string]confirmationEntry), db: db}
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
	k := hex.EncodeToString(d[:])
	if s.db != nil {
		// Opportunistic purge of expired tokens keeps the table bounded.
		_, _ = s.db.Exec(`DELETE FROM forky_confirmation_tokens WHERE expires_at <= NOW()`)
		_, err := s.db.Exec(`
			INSERT INTO forky_confirmation_tokens (token_hash, user_id, restaurant_id, tool, args_hash, session_key, expires_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`, k, user, restaurant, tool, sha256Hex(args), session, time.Now().Add(ttl))
		if err != nil {
			return "", err
		}
		return tok, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[k] = confirmationEntry{digest: k, user: user, restaurant: restaurant, tool: tool, args: args, session: session, expires: time.Now().Add(ttl)}
	return tok, nil
}
func (s *confirmationStore) Consume(tok, user, restaurant, tool, args, session string) error {
	d := sha256.Sum256([]byte(tok))
	k := hex.EncodeToString(d[:])
	if s.db != nil {
		// Atomic single-use: the row is deleted on consume, so a replay (or an
		// expired/mismatched token) affects zero rows.
		res, err := s.db.Exec(`
			DELETE FROM forky_confirmation_tokens
			WHERE token_hash = ? AND user_id = ? AND restaurant_id = ? AND tool = ? AND args_hash = ? AND session_key = ? AND expires_at > NOW()
		`, k, user, restaurant, tool, sha256Hex(args), session)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return s.rejectReason(k, restaurant, tool, sha256Hex(args), session)
		}
		return nil
	}
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

// rejectReason explains, without consuming or revealing anything, why a token did
// not match. The text goes back to the model (so it can act on it instead of
// retrying blind) and the reason class alone is logged: never args or the token.
func (s *confirmationStore) rejectReason(hash, restaurant, tool, argsHash, session string) error {
	const base = "confirmation token expired or invalid"
	var rowTool, rowArgs, rowRest, rowSess string
	var live bool
	err := s.db.QueryRow(`SELECT tool, args_hash, restaurant_id, session_key, expires_at > NOW() FROM forky_confirmation_tokens WHERE token_hash = ?`, hash).Scan(&rowTool, &rowArgs, &rowRest, &rowSess, &live)
	reason, hint := "unknown", ""
	switch {
	case err == sql.ErrNoRows:
		reason, hint = "no_token", "el token no existe, ya se usó o caducó. Si ya lo habías usado, la operación PUEDE estar aplicada: NO la repitas, comprueba el estado con una lectura. Si no, pide una vista previa nueva con confirmed=false"
	case err != nil:
		reason = "lookup_error"
	case !live:
		reason, hint = "expired", "el token caducó: pide una vista previa nueva con confirmed=false"
	case rowTool != tool || rowRest != restaurant || rowSess != session:
		reason, hint = "other_operation", "el token es de otra operación: pide una vista previa nueva para esta"
	case rowArgs != argsHash:
		reason, hint = "args_mismatch", "los argumentos no coinciden con los de la vista previa: repite la llamada con EXACTAMENTE los mismos name, path_params, query y body, solo añadiendo confirmed=true y el token, o pide otra vista previa"
	}
	botCheckpoint("wa_confirm_reject_v1", "tool=%s reason=%s", tool, reason)
	if hint == "" {
		return fmt.Errorf(base)
	}
	return fmt.Errorf("%s: %s", base, hint)
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
