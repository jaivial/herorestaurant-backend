package api

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
)

// Scheduled booking reconfirmation (reminder) WhatsApp worker.
// Coordination id: bkg-wa-notif. Mirrors the pre-shift reminder and stock
// digest workers: a one-minute ticker, a premium + paired-gateway gate, and a
// durable idempotency claim so each booking gets at most one reminder.

const (
	bookingReminderScanEvery    = time.Minute
	bookingReminderQueryTimeout = 15 * time.Second
	bookingReminderSource       = "booking_reconfirmation"
)

type dueBookingReminder struct {
	ID              int64
	CustomerName    string
	PhoneCC         sql.NullString
	Phone           sql.NullString
	ReservationDate string
	ReservationTime string
	PartySize       int
	ArrozType       sql.NullString
	ArrozServings   sql.NullString
	HighChairs      sql.NullInt64
	BabyStrollers   sql.NullInt64
	PreferredFloor  sql.NullInt64
	SalonName       sql.NullString
}

func bookingReminderDeliveryKey(restaurantID int, bookingID int64) string {
	return fmt.Sprintf("booking_reconfirmation:%d:%d", restaurantID, bookingID)
}

func (s *Server) runBookingReminderLoop(ctx context.Context) {
	ticker := time.NewTicker(bookingReminderScanEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.runBookingRemindersOnce(ctx); err != nil {
				log.Printf("%s.reminder.error %v", bookingNotifCoordinationID, err)
			}
		}
	}
}

func (s *Server) runBookingRemindersOnce(ctx context.Context) error {
	scanCtx, cancel := context.WithTimeout(ctx, bookingReminderQueryTimeout)
	rows, err := s.db.QueryContext(scanCtx, `
		SELECT restaurant_id, reconfirmation_days_before
		FROM booking_notification_settings
		WHERE send_reconfirmation = 1
	`)
	if err != nil {
		cancel()
		return err
	}
	type target struct {
		restaurantID int
		days         int
	}
	var targets []target
	for rows.Next() {
		var t target
		if err = rows.Scan(&t.restaurantID, &t.days); err != nil {
			rows.Close()
			cancel()
			return err
		}
		t.days = clampReconfirmationDays(t.days)
		targets = append(targets, t)
	}
	err = rows.Err()
	rows.Close()
	cancel()
	if err != nil {
		return err
	}

	for _, t := range targets {
		if e := s.deliverBookingReminders(ctx, t.restaurantID, t.days); e != nil {
			log.Printf("%s.reminder.failed restaurant=%d %v", bookingNotifCoordinationID, t.restaurantID, e)
		}
	}
	return nil
}

func (s *Server) deliverBookingReminders(ctx context.Context, restaurantID int, daysBefore int) error {
	entitled, err := s.hasActiveRecurringFeature(ctx, restaurantID, boPremiumWhatsAppFeatureKey)
	if err != nil || !entitled {
		return err
	}
	gw, connected := s.botGatewayFor(ctx, restaurantID)
	if !connected {
		return nil
	}

	now := time.Now()
	bookings, err := s.dueBookingReminders(ctx, restaurantID, now, now.AddDate(0, 0, daysBefore))
	if err != nil {
		return err
	}
	if len(bookings) == 0 {
		return nil
	}
	log.Printf("%s.reminder.scan restaurant=%d days=%d due=%d", bookingNotifCoordinationID, restaurantID, daysBefore, len(bookings))

	brandName := restaurantBrandNameOrDefault(ctx, s, restaurantID)
	baseURL := resolveRestaurantPublicBaseURL(ctx, s, restaurantID)

	for _, b := range bookings {
		phone, ok := normalizePhoneForReminder(b.PhoneCC.String, b.Phone.String)
		if !ok {
			continue
		}
		key := bookingReminderDeliveryKey(restaurantID, b.ID)
		claimed, e := s.claimBookingReminder(ctx, restaurantID, b.ID, key)
		if e != nil || !claimed {
			continue
		}
		log.Printf("%s.reminder.claimed restaurant=%d booking=%d", bookingNotifCoordinationID, restaurantID, b.ID)

		extras := bookingReminderExtras{
			ArrozLine:     bookingReminderArrozLine(b.ArrozType, b.ArrozServings),
			HighChairs:    int(b.HighChairs.Int64),
			BabyStrollers: int(b.BabyStrollers.Int64),
		}
		msg := buildBookingReminderPayload(brandName, b.CustomerName, formatBookingDateDisplay(b.ReservationDate), formatHHMM(b.ReservationTime),
			b.PartySize, bookingReminderFloorDisplay(b.PreferredFloor), strings.TrimSpace(b.SalonName.String), extras, b.ID, baseURL)

		if sendErr := s.sendWhatsAppMenuTracked(ctx, restaurantID, gw, phone, msg.Text, msg.Choices, bookingReminderSource); sendErr != nil {
			s.markBookingReminderFailed(ctx, key, sendErr)
			log.Printf("%s.reminder.failed restaurant=%d booking=%d %v", bookingNotifCoordinationID, restaurantID, b.ID, sendErr)
			continue
		}
		s.markBookingReminderSent(ctx, restaurantID, b.ID, key)
		log.Printf("%s.reminder.sent restaurant=%d booking=%d", bookingNotifCoordinationID, restaurantID, b.ID)
	}
	return nil
}

func (s *Server) dueBookingReminders(ctx context.Context, restaurantID int, from, to time.Time) ([]dueBookingReminder, error) {
	queryCtx, cancel := context.WithTimeout(ctx, bookingReminderQueryTimeout)
	defer cancel()
	rows, err := s.db.QueryContext(queryCtx, `
		SELECT b.id, b.customer_name, b.contact_phone_country_code, b.contact_phone,
		       DATE_FORMAT(b.reservation_date, '%Y-%m-%d') AS reservation_date,
		       TIME_FORMAT(b.reservation_time, '%H:%i:%s') AS reservation_time,
		       b.party_size, b.arroz_type, b.arroz_servings, b.highChairs, b.babyStrollers,
		       b.preferred_floor_number, sal.name
		FROM bookings b
		LEFT JOIN restaurant_salons sal ON sal.id = b.preferred_salon_id AND sal.restaurant_id = b.restaurant_id
		LEFT JOIN booking_reminder_deliveries d
		       ON d.booking_id = b.id AND d.restaurant_id = b.restaurant_id AND d.status IN ('pending','sent')
		WHERE b.restaurant_id = ?
		  AND d.id IS NULL
		  AND (b.status IN ('pending','confirmed') OR b.status IS NULL OR b.status = '')
		  AND TIMESTAMP(b.reservation_date, COALESCE(NULLIF(b.reservation_time, ''), '00:00:00')) BETWEEN ? AND ?
		ORDER BY b.reservation_date, b.reservation_time
	`, restaurantID, from.Format("2006-01-02 15:04:05"), to.Format("2006-01-02 15:04:05"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []dueBookingReminder
	for rows.Next() {
		var b dueBookingReminder
		if err = rows.Scan(&b.ID, &b.CustomerName, &b.PhoneCC, &b.Phone, &b.ReservationDate, &b.ReservationTime,
			&b.PartySize, &b.ArrozType, &b.ArrozServings, &b.HighChairs, &b.BabyStrollers, &b.PreferredFloor, &b.SalonName); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// claimBookingReminder reserves the delivery slot. The unique delivery_key makes
// the claim safe against concurrent instances and process restarts.
func (s *Server) claimBookingReminder(ctx context.Context, restaurantID int, bookingID int64, key string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT IGNORE INTO booking_reminder_deliveries (restaurant_id, booking_id, delivery_key, status)
		VALUES (?, ?, ?, 'pending')
	`, restaurantID, bookingID, key)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// markBookingReminderFailed frees the claim so the next scan retries.
func (s *Server) markBookingReminderFailed(ctx context.Context, key string, cause error) {
	_, _ = s.db.ExecContext(ctx, `
		DELETE FROM booking_reminder_deliveries WHERE delivery_key = ? AND status = 'pending'
	`, key)
	_ = cause
}

func (s *Server) markBookingReminderSent(ctx context.Context, restaurantID int, bookingID int64, key string) {
	_, _ = s.db.ExecContext(ctx, `
		UPDATE booking_reminder_deliveries SET status = 'sent', sent_at = NOW() WHERE delivery_key = ?
	`, key)
	// Legacy flag kept in sync for the n8n reminder path; tolerated if absent.
	_, _ = s.db.ExecContext(ctx, `UPDATE bookings SET reminder_sent = 1 WHERE restaurant_id = ? AND id = ?`, restaurantID, bookingID)
}

func bookingReminderFloorDisplay(floor sql.NullInt64) string {
	if !floor.Valid || floor.Int64 < 0 {
		return ""
	}
	return "Planta " + strconv.FormatInt(floor.Int64, 10)
}

func formatBookingDateDisplay(raw string) string {
	if t, err := time.Parse("2006-01-02", raw); err == nil {
		return t.Format("02/01/2006")
	}
	return raw
}
