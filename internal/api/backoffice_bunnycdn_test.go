package api

import "testing"

func TestBunnySecretMaskDoesNotExposeShortSecrets(t *testing.T) {
	if got := bunnySecretMask("short"); got != "••••••••" {
		t.Fatalf("mask=%q, want hidden mask", got)
	}
}

func TestBunnySecretMaskKeepsOnlyEdges(t *testing.T) {
	if got := bunnySecretMask("abcdefghijkl"); got != "abcd••••ijkl" {
		t.Fatalf("mask=%q, want edge mask", got)
	}
}

func TestBunnyProfileCompletenessRequiresAllFields(t *testing.T) {
	cfg := bunnyCDNConfig{PublicPullBaseURL: "https://media.example", PublicStorageZone: "zone", PublicStorageAccessKey: "key"}
	if !bunnyPublicConfigured(cfg) {
		t.Fatal("complete public profile should be configured")
	}
	cfg.PublicStorageAccessKey = ""
	if bunnyPublicConfigured(cfg) {
		t.Fatal("public profile without access key should be incomplete")
	}
}
