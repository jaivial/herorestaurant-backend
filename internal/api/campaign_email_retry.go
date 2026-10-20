package api

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"
)

// Coordination id: camp_email_retry_v1
//
// A temporary Titan refusal (421/450/451/452/454) means the message was not
// accepted, so it is safe to try again later. Anything else (permanent 5xx,
// timeout or a dropped connection, where acceptance is unknown) is NEVER
// retried: it stays 'failed' so a message can never be delivered twice.
// Retries are bounded (3 attempts, 30 min / 2 h / 6 h), at most
// campaignRetryPerTick per tick, honour the do-not-contact list and the
// bounce breaker, and are visible in campaign_recipients.error.

const (
	campaignRetryMax      = 3
	campaignRetryPerTick  = 5
	campaignRetryTickCapN = 20
)

var campaignRetryBackoff = []time.Duration{30 * time.Minute, 2 * time.Hour, 6 * time.Hour}

var campaignSMTPTempRe = regexp.MustCompile(`\b(421|450|451|452|454)[ -]4\.\d{1,3}\.\d{1,3}\b|\b(421|450|451|452|454)\b[ -]`)

// campaignSMTPTemporary is true only for an explicit temporary SMTP reply.
func campaignSMTPTemporary(msg string) bool {
	return campaignSMTPTempRe.MatchString(msg) && campaignSMTPErrorClass(msg) != "hard"
}

// scheduleCampaignEmailRetry registers the first retry of a failed row and
// writes the visible state. Returns true when a retry was scheduled.
func (s *Server) scheduleCampaignEmailRetry(ctx context.Context, recipientID int64, sendErr string) bool {
	if !campaignSMTPTemporary(sendErr) {
		return false
	}
	msg := truncate(sendErr, 250)
	if _, err := s.db.ExecContext(ctx, `
		INSERT IGNORE INTO campaign_email_retries (recipient_id, attempts, next_at, done, last_error)
		VALUES (?, 1, DATE_ADD(NOW(), INTERVAL ? MINUTE), 0, ?)`,
		recipientID, int(campaignRetryBackoff[0]/time.Minute), msg); err != nil {
		return false
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE campaign_recipients SET error = ? WHERE id = ? AND status = 'failed'`,
		truncate(fmt.Sprintf("temporal Titan, reintento 1/%d en 30 min: %s", campaignRetryMax, msg), 500), recipientID)
	return true
}

// runCampaignEmailRetries sends the due retries. Called by the background loop.
func (s *Server) runCampaignEmailRetries(ctx context.Context) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT x.recipient_id, x.attempts, r.restaurant_id, r.campaign_id, r.target, COALESCE(r.name, ''), COALESCE(r.booking_id, 0)
		FROM campaign_email_retries x
		JOIN campaign_recipients r ON r.id = x.recipient_id
		JOIN campaigns c ON c.id = r.campaign_id
		WHERE x.done = 0 AND x.next_at <= NOW() AND r.status = 'failed' AND r.channel = 'email'
		  AND c.status IN ('sending', 'sent')
		ORDER BY x.next_at LIMIT ?`, campaignRetryTickCapN)
	if err != nil {
		return
	}
	type job struct {
		id, attempts, campaignID, bookingID int64
		restaurantID                        int
		target, name                        string
	}
	jobs := []job{}
	for rows.Next() {
		var j job
		if rows.Scan(&j.id, &j.attempts, &j.restaurantID, &j.campaignID, &j.target, &j.name, &j.bookingID) == nil {
			jobs = append(jobs, j)
		}
	}
	rows.Close()
	done := 0
	for _, j := range jobs {
		if done >= campaignRetryPerTick || ctx.Err() != nil {
			return
		}
		// Claim atomically: a row left claimed by an interrupted worker is
		// ambiguous and is never retried again.
		res, err := s.db.ExecContext(ctx, `UPDATE campaign_email_retries SET done = 2 WHERE recipient_id = ? AND done = 0`, j.id)
		if err != nil {
			continue
		}
		if n, _ := res.RowsAffected(); n != 1 {
			continue
		}
		finish := func(status, errText string) {
			_, _ = s.db.ExecContext(ctx, `UPDATE campaign_email_retries SET done = 1, last_error = NULLIF(?, '') WHERE recipient_id = ?`, truncate(errText, 300), j.id)
			if status == "sent" {
				_, _ = s.db.ExecContext(ctx, `UPDATE campaign_recipients SET status = 'sent', error = NULL, sent_at = NOW() WHERE id = ? AND status = 'failed'`, j.id)
			} else if status == "skipped" {
				_, _ = s.db.ExecContext(ctx, `UPDATE campaign_recipients SET status = 'skipped', error = ? WHERE id = ? AND status = 'failed'`, truncate(errText, 500), j.id)
			} else {
				_, _ = s.db.ExecContext(ctx, `UPDATE campaign_recipients SET error = ? WHERE id = ? AND status = 'failed'`, truncate(errText, 500), j.id)
			}
		}
		if st := campaignBounceFor(j.campaignID); func() bool { st.mu.Lock(); defer st.mu.Unlock(); return st.tripped }() {
			// Breaker open: put it back to waiting, retry after it is resolved.
			_, _ = s.db.ExecContext(ctx, `UPDATE campaign_email_retries SET done = 0, next_at = DATE_ADD(NOW(), INTERVAL 30 MINUTE) WHERE recipient_id = ?`, j.id)
			continue
		}
		if sup, supErr := s.loadCampaignSuppressionFilter(ctx, j.restaurantID); supErr != nil {
			_, _ = s.db.ExecContext(ctx, `UPDATE campaign_email_retries SET done = 0, next_at = DATE_ADD(NOW(), INTERVAL 30 MINUTE) WHERE recipient_id = ?`, j.id)
			continue
		} else if sup.blocked("email", j.target) {
			finish("skipped", "baja o direccion bloqueada antes del reintento")
			continue
		}
		c, err := s.loadBOCampaign(ctx, j.restaurantID, j.campaignID)
		if err != nil {
			_, _ = s.db.ExecContext(ctx, `UPDATE campaign_email_retries SET done = 0, next_at = DATE_ADD(NOW(), INTERVAL 30 MINUTE) WHERE recipient_id = ?`, j.id)
			continue
		}
		sendErr := s.deliverCampaignTo(ctx, j.restaurantID, c, campaignTarget{Channel: "email", Target: j.target, Name: j.name, BookingID: j.bookingID})
		done++
		if sendErr == nil {
			finish("sent", "")
			slog.Default().Info("campaign.email.retry_sent", "campaign_id", j.campaignID, "recipient_id", j.id, "attempt", j.attempts+1)
		} else if cls := campaignSMTPErrorClass(sendErr.Error()); cls == "hard" {
			s.campaignSuppressBounce(ctx, j.restaurantID, campaignBounceEvent{Address: strings.ToLower(strings.TrimSpace(j.target)), Status: "5.1.1", Class: "hard"})
			finish("failed", "rechazo permanente en reintento: "+truncate(sendErr.Error(), 250))
		} else if campaignSMTPTemporary(sendErr.Error()) && j.attempts+1 <= campaignRetryMax {
			wait := campaignRetryBackoff[min(int(j.attempts), len(campaignRetryBackoff)-1)]
			_, _ = s.db.ExecContext(ctx, `UPDATE campaign_email_retries SET done = 0, attempts = attempts + 1, next_at = DATE_ADD(NOW(), INTERVAL ? MINUTE), last_error = ? WHERE recipient_id = ?`,
				int(wait/time.Minute), truncate(sendErr.Error(), 300), j.id)
			_, _ = s.db.ExecContext(ctx, `UPDATE campaign_recipients SET error = ? WHERE id = ? AND status = 'failed'`,
				truncate(fmt.Sprintf("temporal Titan, reintento %d/%d en %d min: %s", j.attempts+1, campaignRetryMax, int(wait/time.Minute), truncate(sendErr.Error(), 250)), 500), j.id)
		} else {
			finish("failed", "reintentos agotados o respuesta ambigua, no se reenvia: "+truncate(sendErr.Error(), 250))
		}
		time.Sleep(campaignChannelPause("email", c.EmailPerMinute))
	}
}
