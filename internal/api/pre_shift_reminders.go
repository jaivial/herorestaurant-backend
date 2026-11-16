package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"preactvillacarmen/internal/reminders"
	"preactvillacarmen/internal/sqldb"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type sqlPreShiftStore struct{ db *sql.DB }

func (s sqlPreShiftStore) DueShifts(ctx context.Context, now time.Time, lead time.Duration) ([]reminders.Shift, error) {
	end := now.Add(lead)
	rows, e := s.db.QueryContext(ctx, nativeSQL(`SELECT w.id,w.restaurant_id,w.restaurant_member_id,TRIM(CONCAT(COALESCE(m.first_name,''),' ',COALESCE(m.last_name,''))),m.whatsapp_number, TIMESTAMP(w.work_date,w.start_time) FROM member_work_schedules w JOIN restaurant_members m ON m.id=w.restaurant_member_id AND m.restaurant_id=w.restaurant_id WHERE m.whatsapp_verified_at IS NOT NULL AND m.whatsapp_number IS NOT NULL AND m.whatsapp_number<>'' AND TIMESTAMP(w.work_date,w.start_time)>=? AND TIMESTAMP(w.work_date,w.start_time)<?`, `SELECT w.id,w.restaurant_id,w.restaurant_member_id,TRIM(CONCAT(COALESCE(m.first_name,''),' ',COALESCE(m.last_name,''))),m.whatsapp_number, (w.work_date + w.start_time) FROM member_work_schedules w JOIN restaurant_members m ON m.id=w.restaurant_member_id AND m.restaurant_id=w.restaurant_id WHERE m.whatsapp_verified_at IS NOT NULL AND m.whatsapp_number IS NOT NULL AND m.whatsapp_number<>'' AND (w.work_date + w.start_time)>=? AND (w.work_date + w.start_time)<?`), sqldb.NaiveUTCArg(sqlDialect, now), sqldb.NaiveUTCArg(sqlDialect, end))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []reminders.Shift
	for rows.Next() {
		var x reminders.Shift
		if e = rows.Scan(&x.ID, &x.RestaurantID, &x.MemberID, &x.MemberName, &x.Phone, &x.StartAt); e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
func (s sqlPreShiftStore) Claim(ctx context.Context, key string) (bool, error) { // event is the durable idempotency key
	payload, _ := json.Marshal(map[string]string{"key": key})
	parts := strings.Split(key, ":")
	restaurantID := 0
	if len(parts) > 1 {
		restaurantID, _ = strconv.Atoi(parts[1])
	}
	if restaurantID <= 0 {
		return false, fmt.Errorf("invalid reminder key")
	}
	// repair-ignore3: key is both event (varchar(64)) and delivery_key (191).
	// A cut event made Release (WHERE event = key) miss its row. Over-width
	// keys are refused; only the delivery_key duplicate is a no-op. Any other
	// insert error is RETURNED: the source fallback answered "claimed" when no
	// row existed, i.e. it could send without a durable claim.
	if utf8.RuneCountInString(key) > 64 {
		return false, fmt.Errorf("pre-shift reminder key exceeds 64 characters")
	}
	r, e := s.db.ExecContext(ctx, nativeSQL(`INSERT INTO message_deliveries (restaurant_id,channel,event,delivery_key,recipient,payload_json,status) VALUES (?,'whatsapp',?,?,'pre-shift',?,'pending') ON DUPLICATE KEY UPDATE delivery_key = delivery_key`, `INSERT INTO message_deliveries (restaurant_id,channel,event,delivery_key,recipient,payload_json,status) VALUES (?,'whatsapp',CAST(? AS text),CAST(? AS text),'pre-shift',CAST(? AS jsonb),'pending') ON CONFLICT (app.norm_ascii(delivery_key)) DO NOTHING`), restaurantID, key, key, payload)
	if e != nil {
		return false, e
	}
	n, _ := r.RowsAffected()
	return n == 1, nil
}
func (s sqlPreShiftStore) Release(ctx context.Context, key string) error {
	_, e := s.db.ExecContext(ctx, portableSQL(`DELETE FROM message_deliveries WHERE event=? AND status='pending'`), key)
	return e
}

type preShiftSender struct{ s *Server }

func (x preShiftSender) Send(ctx context.Context, rid int, to, text string) error {
	g, ok := x.s.botGatewayFor(ctx, rid)
	if !ok {
		return fmt.Errorf("whatsapp not connected")
	}
	return x.s.sendWhatsAppTextTracked(ctx, rid, g, to, text, "pre_shift_reminder")
}

type preShiftGate struct{ s *Server }

func (x preShiftGate) Allowed(ctx context.Context, rid int, _ reminders.Shift) (bool, error) {
	ok, e := x.s.hasActiveRecurringFeature(ctx, rid, boPremiumWhatsAppFeatureKey)
	if e != nil || !ok {
		return false, e
	}
	_, connected := x.s.botGatewayFor(ctx, rid)
	return connected, nil
}
func (s *Server) runPreShiftReminderLoop(ctx context.Context) {
	w := &reminders.Worker{Store: sqlPreShiftStore{s.db}, Sender: preShiftSender{s}, Gate: preShiftGate{s}, Config: reminders.Config{Lead: time.Duration(s.cfg.PreShiftReminderMinutes) * time.Minute}}
	w.Run(ctx)
}
