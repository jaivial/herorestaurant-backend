package api

import (
	"regexp"
	"strings"
	"testing"
)

func TestVerificationDigestIsDeterministicAndBoundToInputs(t *testing.T) {
	got := verificationDigest(12, "34612345678", "123456")
	if got != verificationDigest(12, "+34 612 345 678", "123456") {
		t.Fatal("digest should normalize phone")
	}
	if len(got) != 64 || !regexp.MustCompile(`^[0-9a-f]+$`).MatchString(got) {
		t.Fatalf("invalid sha256 digest %q", got)
	}
	for _, in := range []struct {
		id          int
		phone, code string
	}{{13, "34612345678", "123456"}, {12, "34612345679", "123456"}, {12, "34612345678", "123457"}} {
		if got == verificationDigest(in.id, in.phone, in.code) {
			t.Fatalf("digest not bound to input %v", in)
		}
	}
}

func TestNewWhatsAppVerificationCode(t *testing.T) {
	for i := 0; i < 100; i++ {
		code, err := newWhatsAppVerificationCode()
		if err != nil {
			t.Fatal(err)
		}
		if len(code) != 6 {
			t.Fatalf("code length %q", code)
		}
		if strings.Trim(code, "0123456789") != "" {
			t.Fatalf("non numeric code %q", code)
		}
	}
}
