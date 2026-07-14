package api

import "testing"

func TestNormalizeWhatsAppNumberForMemberPhone(t *testing.T) {
	for input, want := range map[string]string{
		"612 345 678":     "34612345678",
		"+34 612 345 678": "34612345678",
		"123":             "",
	} {
		if got := normalizeWhatsAppNumber(input); got != want {
			t.Fatalf("normalizeWhatsAppNumber(%q) = %q, want %q", input, got, want)
		}
	}
}
