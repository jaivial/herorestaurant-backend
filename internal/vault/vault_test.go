package vault

import "testing"

func TestEncryptDecryptRoundTrip(t *testing.T) {
	token := "0123456789abcdefghijklmnop"
	secrets := []string{"sk-ant-minimax-1234567890", "", "with spaces  ñ 漢字"}
	for _, s := range secrets {
		enc, err := Encrypt(token, s)
		if err != nil {
			t.Fatalf("encrypt %q: %v", s, err)
		}
		got, err := Decrypt(token, enc)
		if err != nil {
			t.Fatalf("decrypt %q: %v", s, err)
		}
		if got != s {
			t.Fatalf("roundtrip mismatch: got %q want %q", got, s)
		}
	}
}

func TestEncryptProducesRandomCiphertexts(t *testing.T) {
	token := "0123456789abcdefghijklmnop"
	a, err := Encrypt(token, "same plaintext")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Encrypt(token, "same plaintext")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("two encryptions of same plaintext produced identical ciphertext (nonce reuse?)")
	}
}

func TestDecryptWrongTokenFails(t *testing.T) {
	token := "0123456789abcdefghijklmnop"
	enc, err := Encrypt(token, "secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decrypt("0987654321fedcba987654321", enc); err == nil {
		t.Fatal("expected error decrypting with wrong token")
	}
}

func TestDecryptTamperedPayloadFails(t *testing.T) {
	token := "0123456789abcdefghijklmnop"
	enc, err := Encrypt(token, "secret")
	if err != nil {
		t.Fatal(err)
	}
	tampered := enc[:len(enc)-2] + "AA"
	if _, err := Decrypt(token, tampered); err == nil {
		t.Fatal("expected error decrypting tampered payload")
	}
}

func TestTokenTooShort(t *testing.T) {
	if _, err := Encrypt("short", "x"); err == nil {
		t.Fatal("expected error with short token")
	}
	if _, err := Decrypt("short", "x"); err == nil {
		t.Fatal("expected error with short token")
	}
}
