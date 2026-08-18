// Package vault encrypts and decrypts secrets (e.g. API keys) at rest using
// AES-256-GCM keyed by a token supplied via environment (VAULT_TOKEN).
//
// The plaintext token is never stored; the encryption key is a SHA-256 digest
// of the token, so any token length is accepted (16+ bytes recommended).
// Each encryption draws a fresh random 12-byte nonce; the stored payload is
// base64("nonce||ciphertext||authTag").
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

var (
	// ErrInvalidToken means the VAULT_TOKEN is missing/too weak to derive a key.
	ErrInvalidToken = errors.New("vault: invalid token")
	// ErrDecrypt means the ciphertext could not be decrypted (wrong token or payload tampered).
	ErrDecrypt = errors.New("vault: cannot decrypt secret")
)

// deriveKey hashes the token once with SHA-256 to obtain a 32-byte AES key.
func deriveKey(token string) ([32]byte, error) {
	var key [32]byte
	t := strings.TrimSpace(token)
	if len(t) < 16 {
		return key, fmt.Errorf("%w: token must be at least 16 bytes", ErrInvalidToken)
	}
	key = sha256.Sum256([]byte(t))
	return key, nil
}

// Encrypt seals plaintext with AES-256-GCM. Returns base64("nonce||ciphertext||tag").
func Encrypt(token, plaintext string) (string, error) {
	key, err := deriveKey(token)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", fmt.Errorf("vault: aes new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("vault: gcm: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("vault: read nonce: %w", err)
	}
	sealed := gcm.Seal(nil, nonce, []byte(plaintext), nil)
	payload := append(nonce, sealed...)
	return base64.StdEncoding.EncodeToString(payload), nil
}

// Decrypt opens a payload produced by Encrypt. Any error (malformed base64,
// wrong token, tampered data) is collapsed into ErrDecrypt so callers do not
// leak key material or nonce details.
func Decrypt(token, encoded string) (string, error) {
	key, err := deriveKey(token)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", fmt.Errorf("vault: aes new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("vault: gcm: %w", err)
	}

	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return "", fmt.Errorf("%w: base64: %v", ErrDecrypt, err)
	}
	if len(raw) < gcm.NonceSize() {
		return "", fmt.Errorf("%w: payload too short", ErrDecrypt)
	}
	nonce, ciphertext := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrDecrypt, err)
	}
	return string(plain), nil
}
