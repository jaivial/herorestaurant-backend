package api

import "testing"

// The backoffice booking editor lists every canonical payment method of the
// SPEC ÃÂ¢ÃÃ2 enum, so an operator can record a deposit collected by card, bizum,
// transferencia, efectivo or stripe even when the special date only advertises
// a subset. The snapshot validator must therefore accept the whole enum for
// backoffice edits while the public booking form stays restricted to the
// methods configured on the date.
func TestAdelantoAcceptedMethodsBackofficeAcceptsFullEnum(t *testing.T) {
	settings := &specialDateSettings{AdelantoPaymentMethods: []string{"card"}}

	got := adelantoAcceptedMethods(settings, false)

	for _, m := range []string{"card", "bizum", "transferencia", "efectivo", "stripe"} {
		if !got[m] {
			t.Fatalf("backoffice edit: method %q should be accepted, got %v", m, got)
		}
	}
}

func TestAdelantoAcceptedMethodsOnlineRestrictedToConfigured(t *testing.T) {
	settings := &specialDateSettings{AdelantoPaymentMethods: []string{"card", " bizum "}}

	got := adelantoAcceptedMethods(settings, true)

	if len(got) != 2 || !got["card"] || !got["bizum"] {
		t.Fatalf("online booking: expected only the configured methods, got %v", got)
	}
	if got["efectivo"] {
		t.Fatalf("online booking must not accept unconfigured methods, got %v", got)
	}
}

func TestAdelantoAcceptedMethodsNilSettings(t *testing.T) {
	if got := adelantoAcceptedMethods(nil, true); len(got) != 0 {
		t.Fatalf("nil settings should yield no accepted methods, got %v", got)
	}
	if got := adelantoAcceptedMethods(nil, false); len(got) != 0 {
		t.Fatalf("nil settings should yield no accepted methods, got %v", got)
	}
}
