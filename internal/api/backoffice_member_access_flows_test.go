package api

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// seedEmailProviderGmail inserts an active Gmail provider row for a restaurant.
func seedEmailProviderGmail(t *testing.T, db *sql.DB, restaurantID int) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO email_provider_config
			(restaurant_id, provider, gmail_from_email, gmail_app_password, is_active)
		VALUES (?, 'gmail', 'reservas@gmail.com', 'gmail-app-pass', 1)
	`, restaurantID)
	if err != nil {
		t.Fatalf("insert gmail email_provider_config: %v", err)
	}
}

func emailAttempt(attempts []boDeliveryAttempt) (boDeliveryAttempt, bool) {
	for _, a := range attempts {
		if a.Channel == "email" {
			return a, true
		}
	}
	return boDeliveryAttempt{}, false
}

func TestSendMemberInvitation_UsesDBSMTPConfig(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	if err := db.Ping(); err != nil {
		t.Skipf("test DB unreachable: %v", err)
	}
	srv := newTestServer(t, db)
	ctx := context.Background()

	id, cleanup := seedRestaurant(t, db, "inv-smtp")
	defer cleanup()
	seedEmailProvider(t, db, id, true)

	calls, restore := withRecordingSMTP(t)
	defer restore()

	attempts := srv.sendMemberInvitation(ctx, id, "user@example.com", "", "https://bo.example/invitacion/tok123")

	if len(*calls) != 1 {
		t.Fatalf("expected 1 smtp call, got %d", len(*calls))
	}
	c := (*calls)[0]
	if c.host != "smtp.titan.email" {
		t.Errorf("expected DB host smtp.titan.email, got %q", c.host)
	}
	if c.port != 587 {
		t.Errorf("expected DB port 587, got %d", c.port)
	}
	if c.username != "reservas@example.com" {
		t.Errorf("expected DB username, got %q", c.username)
	}
	if c.password != "secret-pass" {
		t.Errorf("expected DB password, got %q", c.password)
	}
	if c.encryption != "tls" {
		t.Errorf("expected DB encryption tls, got %q", c.encryption)
	}
	if c.to != "user@example.com" {
		t.Errorf("expected recipient user@example.com, got %q", c.to)
	}
	if c.fromName != "Restaurant Backoffice" {
		t.Errorf("expected app fromName 'Restaurant Backoffice', got %q", c.fromName)
	}
	att, ok := emailAttempt(attempts)
	if !ok || !att.Sent {
		t.Fatalf("expected email delivery attempt sent=true, got %+v", attempts)
	}
}

func TestSendMemberInvitation_UsesGmailProvider(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	if err := db.Ping(); err != nil {
		t.Skipf("test DB unreachable: %v", err)
	}
	srv := newTestServer(t, db)
	ctx := context.Background()

	id, cleanup := seedRestaurant(t, db, "inv-gmail")
	defer cleanup()
	seedEmailProviderGmail(t, db, id)

	calls, restore := withRecordingSMTP(t)
	defer restore()

	attempts := srv.sendMemberInvitation(ctx, id, "user@example.com", "", "https://bo.example/invitacion/tok123")

	if len(*calls) != 1 {
		t.Fatalf("expected 1 smtp call, got %d", len(*calls))
	}
	c := (*calls)[0]
	if c.host != "smtp.gmail.com" {
		t.Errorf("expected gmail host smtp.gmail.com, got %q", c.host)
	}
	if c.port != 587 {
		t.Errorf("expected gmail port 587, got %d", c.port)
	}
	if c.encryption != "tls" {
		t.Errorf("expected gmail encryption tls, got %q", c.encryption)
	}
	if c.username != "reservas@gmail.com" {
		t.Errorf("expected gmail username = gmailFromEmail, got %q", c.username)
	}
	if c.password != "gmail-app-pass" {
		t.Errorf("expected gmail app password, got %q", c.password)
	}
	if att, ok := emailAttempt(attempts); !ok || !att.Sent {
		t.Fatalf("expected email delivery attempt sent=true, got %+v", attempts)
	}
}

func TestSendMemberInvitation_NoConfig_RecordsError(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	if err := db.Ping(); err != nil {
		t.Skipf("test DB unreachable: %v", err)
	}
	srv := newTestServer(t, db)
	ctx := context.Background()

	t.Run("missing provider row", func(t *testing.T) {
		id, cleanup := seedRestaurant(t, db, "inv-noconfig")
		defer cleanup()
		calls, restore := withRecordingSMTP(t)
		defer restore()

		attempts := srv.sendMemberInvitation(ctx, id, "user@example.com", "", "https://bo.example/invitacion/tok")
		if len(*calls) != 0 {
			t.Fatalf("expected 0 smtp calls, got %d", len(*calls))
		}
		att, ok := emailAttempt(attempts)
		if !ok || att.Sent {
			t.Fatalf("expected failed email attempt, got %+v", attempts)
		}
		if !strings.Contains(att.Error, "email provider not configured") {
			t.Errorf("expected 'email provider not configured' error, got %q", att.Error)
		}
	})

	t.Run("inactive provider row", func(t *testing.T) {
		id, cleanup := seedRestaurant(t, db, "inv-inactive")
		defer cleanup()
		seedEmailProvider(t, db, id, false)
		calls, restore := withRecordingSMTP(t)
		defer restore()

		attempts := srv.sendMemberInvitation(ctx, id, "user@example.com", "", "https://bo.example/invitacion/tok")
		if len(*calls) != 0 {
			t.Fatalf("expected 0 smtp calls, got %d", len(*calls))
		}
		att, _ := emailAttempt(attempts)
		if att.Sent || !strings.Contains(att.Error, "email provider not configured") {
			t.Errorf("expected not-configured error, got %+v", att)
		}
	})
}

func TestSendMemberPasswordReset_UsesDBConfig(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	if err := db.Ping(); err != nil {
		t.Skipf("test DB unreachable: %v", err)
	}
	srv := newTestServer(t, db)
	ctx := context.Background()

	t.Run("active smtp sends via DB config", func(t *testing.T) {
		id, cleanup := seedRestaurant(t, db, "rst-smtp")
		defer cleanup()
		seedEmailProvider(t, db, id, true)
		calls, restore := withRecordingSMTP(t)
		defer restore()

		attempts := srv.sendMemberPasswordReset(ctx, id, "user@example.com", "", "https://bo.example/reset-password/tok")
		if len(*calls) != 1 {
			t.Fatalf("expected 1 smtp call, got %d", len(*calls))
		}
		if (*calls)[0].host != "smtp.titan.email" {
			t.Errorf("expected DB host, got %q", (*calls)[0].host)
		}
		if (*calls)[0].fromName != "Restaurant Backoffice" {
			t.Errorf("expected app fromName, got %q", (*calls)[0].fromName)
		}
		if att, ok := emailAttempt(attempts); !ok || !att.Sent {
			t.Fatalf("expected sent email attempt, got %+v", attempts)
		}
	})

	t.Run("gmail sends via gmail transport", func(t *testing.T) {
		id, cleanup := seedRestaurant(t, db, "rst-gmail")
		defer cleanup()
		seedEmailProviderGmail(t, db, id)
		calls, restore := withRecordingSMTP(t)
		defer restore()

		srv.sendMemberPasswordReset(ctx, id, "user@example.com", "", "https://bo.example/reset-password/tok")
		if len(*calls) != 1 || (*calls)[0].host != "smtp.gmail.com" {
			t.Fatalf("expected 1 gmail call, got %+v", *calls)
		}
	})

	t.Run("no config records error", func(t *testing.T) {
		id, cleanup := seedRestaurant(t, db, "rst-noconfig")
		defer cleanup()
		calls, restore := withRecordingSMTP(t)
		defer restore()

		attempts := srv.sendMemberPasswordReset(ctx, id, "user@example.com", "", "https://bo.example/reset-password/tok")
		if len(*calls) != 0 {
			t.Fatalf("expected 0 smtp calls, got %d", len(*calls))
		}
		att, _ := emailAttempt(attempts)
		if att.Sent || !strings.Contains(att.Error, "email provider not configured") {
			t.Errorf("expected not-configured error, got %+v", att)
		}
	})
}

func TestBackofficeInvitationEmailHTML_IsDarkAndAppBranded(t *testing.T) {
	html := buildBackofficeInvitationEmailHTML("Alqueria Villa Carmen", "https://bo.example/invitacion/abc123")

	mustContain := []string{
		"Restaurant Backoffice",                // app brand, not restaurant brand
		backofficeEmailLogoURL,                 // app logo in header
		"https://bo.example/invitacion/abc123", // action URL
		`name="color-scheme" content="dark"`,   // dark meta
		"prefers-color-scheme",                 // client-scheme override
		"data-ogsc",                            // gmail/outlook dark override hook
		boEmailBg,                              // dark background hex
		"Completar mi alta",                    // CTA label
	}
	for _, s := range mustContain {
		if !strings.Contains(html, s) {
			t.Errorf("invitation email HTML missing %q", s)
		}
	}
}

func TestBackofficePasswordResetEmailHTML_IsDarkAndAppBranded(t *testing.T) {
	html := buildBackofficePasswordResetEmailHTML("Alqueria Villa Carmen", "https://bo.example/reset-password/abc123")
	for _, s := range []string{"Restaurant Backoffice", "https://bo.example/reset-password/abc123", "prefers-color-scheme", "data-ogsc", "Restablecer contrasena"} {
		if !strings.Contains(html, s) {
			t.Errorf("reset email HTML missing %q", s)
		}
	}
}

func TestBackofficeEmailHTML_EscapesRestaurantName(t *testing.T) {
	html := buildBackofficeInvitationEmailHTML(`<script>alert(1)</script>`, "https://bo.example/invitacion/x")
	if strings.Contains(html, "<script>alert(1)</script>") {
		t.Errorf("restaurant name was not HTML-escaped")
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Errorf("expected escaped restaurant name in body")
	}
}

func TestResolveEmailFromAddr(t *testing.T) {
	// Branding override wins.
	got := resolveEmailFromAddr(restaurantBrandingCfg{EmailFromAddress: "brand@x.com"}, boEmailProviderConfig{Provider: "smtp", SMTPFromEmail: "smtp@x.com"})
	if got != "brand@x.com" {
		t.Errorf("expected branding override, got %q", got)
	}
	// SMTP provider falls back to smtp from.
	got = resolveEmailFromAddr(restaurantBrandingCfg{}, boEmailProviderConfig{Provider: "smtp", SMTPFromEmail: "smtp@x.com"})
	if got != "smtp@x.com" {
		t.Errorf("expected smtp from, got %q", got)
	}
	// Gmail provider falls back to gmail from.
	got = resolveEmailFromAddr(restaurantBrandingCfg{}, boEmailProviderConfig{Provider: "gmail", GmailFromEmail: "gm@gmail.com"})
	if got != "gm@gmail.com" {
		t.Errorf("expected gmail from, got %q", got)
	}
}
