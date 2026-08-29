package api

import "testing"

func TestParseSupportedBOAppVersionRejectsUnknownValues(t *testing.T) {
	for _, raw := range []string{"", "0.3", "garbage", "1.0"} {
		if got, ok := parseSupportedBOAppVersion(raw); ok || got != "" {
			t.Fatalf("parseSupportedBOAppVersion(%q) = %q, %v; want empty, false", raw, got, ok)
		}
	}
}

func TestParseSupportedBOAppVersionAcceptsKnownValues(t *testing.T) {
	for _, want := range []string{boAppVersion001, boAppVersion01, boAppVersion02} {
		got, ok := parseSupportedBOAppVersion("  " + want + "  ")
		if !ok || got != want {
			t.Fatalf("parseSupportedBOAppVersion(%q) = %q, %v; want %q, true", want, got, ok, want)
		}
	}
}

func TestAppVersionAtLeastSupportsPatchVersions(t *testing.T) {
	if !appVersionAtLeast("0.1", "0.0.1") || !appVersionAtLeast("0.0.1", "0.0.1") {
		t.Fatal("0.0.1 feature must be available to 0.0.1 and later versions")
	}
	if appVersionAtLeast("0.0.1", "0.1") {
		t.Fatal("0.0.1 must remain below 0.1")
	}
}

func TestAppCapabilityAllowedUsesCentralMinimumVersion(t *testing.T) {
	if appCapabilityAllowed(boCapabilityAds, boAppVersion01) {
		t.Fatal("ads must be blocked for v0.1")
	}
	if !appCapabilityAllowed(boCapabilityAds, boAppVersion02) {
		t.Fatal("ads must be allowed for v0.2")
	}
	if appCapabilityAllowed(boCapabilityStock, boAppVersion01) {
		t.Fatal("stock must be blocked for v0.1")
	}
}
