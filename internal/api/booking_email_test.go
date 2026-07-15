package api

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// smtpCall records the arguments a single smtpSend invocation was made with.
type smtpCall struct {
	host       string
	port       int
	username   string
	password   string
	fromName   string
	fromAddr   string
	to         string
	subject    string
	encryption string
}

// withRecordingSMTP swaps the package-level smtpSend for a recorder and returns
// the slice that will collect calls plus a restore func.
func withRecordingSMTP(t *testing.T) (*[]smtpCall, func()) {
	t.Helper()
	orig := smtpSend
	var calls []smtpCall
	smtpSend = func(ctx context.Context, host string, port int, username, password, fromName, fromAddr, to, subject, htmlBody, encryption string) error {
		calls = append(calls, smtpCall{host, port, username, password, fromName, fromAddr, to, subject, encryption})
		return nil
	}
	return &calls, func() { smtpSend = orig }
}

// seedRestaurant inserts a throwaway restaurant and returns its id + cleanup.
// Deleting the restaurant cascades to email_provider_config (FK ON DELETE CASCADE).
func seedRestaurant(t *testing.T, db *sql.DB, slug string) (int, func()) {
	t.Helper()
	res, err := db.Exec(`INSERT INTO restaurants (slug, name) VALUES (?, ?)`, slug, "Test "+slug)
	if err != nil {
		t.Fatalf("insert restaurant: %v", err)
	}
	id64, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}
	id := int(id64)
	return id, func() { _, _ = db.Exec(`DELETE FROM restaurants WHERE id = ?`, id) }
}

func seedEmailProvider(t *testing.T, db *sql.DB, restaurantID int, active bool) {
	t.Helper()
	activeInt := 0
	if active {
		activeInt = 1
	}
	_, err := db.Exec(`
		INSERT INTO email_provider_config
			(restaurant_id, provider, smtp_host, smtp_port, smtp_username, smtp_password, smtp_encryption, smtp_from_email, is_active)
		VALUES (?, 'smtp', 'smtp.titan.email', 587, 'reservas@example.com', 'secret-pass', 'tls', 'reservas@example.com', ?)
	`, restaurantID, activeInt)
	if err != nil {
		t.Fatalf("insert email_provider_config: %v", err)
	}
}

func sampleBooking() map[string]any {
	return map[string]any{
		"reservation_date": "2026-08-15",
		"reservation_time": "14:00",
		"party_size":       "4",
		"customer_name":    "Test Booking",
		"contact_email":    "customer@example.com",
	}
}

func TestSendBookingConfirmationEmails(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	if err := db.Ping(); err != nil {
		t.Skipf("test DB unreachable: %v", err)
	}
	srv := newTestServer(t, db)
	ctx := context.Background()

	t.Run("missing config row returns error", func(t *testing.T) {
		id, cleanup := seedRestaurant(t, db, "epc-missing")
		defer cleanup()
		calls, restore := withRecordingSMTP(t)
		defer restore()

		customerSent, restaurantSent, err := sendBookingConfirmationEmails(ctx, srv, id, sampleBooking(), 1001)
		if err == nil || !strings.Contains(err.Error(), "email provider not configured") {
			t.Fatalf("expected 'email provider not configured' error, got %v", err)
		}
		if customerSent || restaurantSent {
			t.Fatalf("expected no sends, got customer=%v restaurant=%v", customerSent, restaurantSent)
		}
		if len(*calls) != 0 {
			t.Fatalf("expected 0 smtp calls, got %d", len(*calls))
		}
	})

	t.Run("inactive config row returns error", func(t *testing.T) {
		id, cleanup := seedRestaurant(t, db, "epc-inactive")
		defer cleanup()
		seedEmailProvider(t, db, id, false)
		calls, restore := withRecordingSMTP(t)
		defer restore()

		customerSent, restaurantSent, err := sendBookingConfirmationEmails(ctx, srv, id, sampleBooking(), 1002)
		if err == nil || !strings.Contains(err.Error(), "email provider not configured") {
			t.Fatalf("expected 'email provider not configured' error, got %v", err)
		}
		if customerSent || restaurantSent {
			t.Fatalf("expected no sends, got customer=%v restaurant=%v", customerSent, restaurantSent)
		}
		if len(*calls) != 0 {
			t.Fatalf("expected 0 smtp calls, got %d", len(*calls))
		}
	})

	t.Run("active config row sends with DB values", func(t *testing.T) {
		id, cleanup := seedRestaurant(t, db, "epc-active")
		defer cleanup()
		seedEmailProvider(t, db, id, true)
		calls, restore := withRecordingSMTP(t)
		defer restore()

		customerSent, restaurantSent, err := sendBookingConfirmationEmails(ctx, srv, id, sampleBooking(), 1003)
		if err != nil {
			t.Fatalf("expected success, got error %v", err)
		}
		if !customerSent || !restaurantSent {
			t.Fatalf("expected both sends, got customer=%v restaurant=%v", customerSent, restaurantSent)
		}
		if len(*calls) != 2 {
			t.Fatalf("expected 2 smtp calls (customer + restaurant), got %d", len(*calls))
		}
		for _, c := range *calls {
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
		}
		if got := (*calls)[0].to; got != "customer@example.com" {
			t.Errorf("expected first send to customer, got %q", got)
		}
		if got := (*calls)[1].to; got != "reservas@example.com" {
			t.Errorf("expected second send to restaurant from-addr, got %q", got)
		}
	})
}

// TestSMTPSendEncryption verifies the encryption argument routing without a real
// network call: "none"/"tls" dial plaintext (StartTLS only for tls), "ssl" dials
// implicit TLS. We only assert argument-driven dial failures are shaped correctly
// against an unreachable host so no live SMTP server is required.
func TestSMTPSendEncryption(t *testing.T) {
	// Use a port that refuses connections quickly (localhost:1 is unassigned).
	const deadHost = "127.0.0.1"
	const deadPort = 1

	cases := []struct {
		name       string
		encryption string
		wantPrefix string
	}{
		{"tls dials plaintext then StartTLS", "tls", "SMTP Dial:"},
		{"none dials plaintext", "none", "SMTP Dial:"},
		{"ssl dials implicit TLS", "ssl", "SMTP TLS Dial:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := smtpSendReal(context.Background(), deadHost, deadPort, "u", "p", "From", "from@example.com", "to@example.com", "Sub", "<p>hi</p>", tc.encryption)
			if err == nil {
				t.Fatalf("expected dial error against dead host")
			}
			if !strings.HasPrefix(err.Error(), tc.wantPrefix) {
				t.Fatalf("encryption %q: expected error prefix %q, got %q", tc.encryption, tc.wantPrefix, err.Error())
			}
		})
	}

	// Sanity: empty host is rejected before any dial.
	if err := smtpSendReal(context.Background(), "", 587, "u", "p", "", "f@x.com", "t@x.com", "s", "b", "tls"); err == nil || !strings.Contains(err.Error(), "SMTP no configurado") {
		t.Fatalf("expected 'SMTP no configurado' for empty host, got %v", err)
	}
}

func TestMimeEncodeSubject(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		expect string
	}{
		{"ascii passthrough", "Reservation confirmed", "Reservation confirmed"},
		{"spanish accents", "Confirmación de reserva · Alquería Villa Carmen", "=?UTF-8?B?Q29uZmlybWFjacOzbiBkZSByZXNlcnZhIMK3IEFscXVlcsOtYSBWaWxsYSBDYXJtZW4=?="},
		{"emoji", "Reserva 🦐", "=?UTF-8?B?UmVzZXJ2YSDwn6aQ?="},
		{"empty falls through as ascii", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mimeEncodeSubject(tc.input)
			if got != tc.expect {
				t.Fatalf("input %q: expected %q, got %q", tc.input, tc.expect, got)
			}
		})
	}
}
