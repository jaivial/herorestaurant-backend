package api

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"preactvillacarmen/internal/httpx"
)

// Coordination id: camp_bounce_sync_v1
//
// Titan delivery failures become suppressions automatically:
//   - hard bounce (permanent bad address, 5.1.x / user unknown): invalid for
//     the e-mail channel only. Never touches WhatsApp or an existing baja.
//   - anything else that failed (4.x.x, mailbox full, policy/spam/auth, no
//     status): blocked as pending_verify, never marked invalid.
//   - delayed/delivered notices are ignored.
//
// Evidence is recorded in campaign_bounce_events (idempotent by NDR
// Message-ID). An SMTP acceptance is not a delivery confirmation, and an
// SMTP rejection at send time is a different evidence than an IMAP NDR.

type campaignBounceEvent struct {
	Key     string
	Address string
	Status  string
	Action  string
	Diag    string
	Class   string // hard | soft | ignore
}

var (
	campaignNDRStatusRe = regexp.MustCompile(`(?im)^status:\s*([245]\.\d{1,3}\.\d{1,3})`)
	campaignNDRActionRe = regexp.MustCompile(`(?im)^action:\s*([a-z]+)`)
	campaignNDRDiagRe   = regexp.MustCompile(`(?im)^diagnostic-code:\s*(.+)$`)
	campaignNDRMsgIDRe  = regexp.MustCompile(`(?im)^message-id:\s*(<[^>\r\n]+>)`)
	campaignNDRFinalRe  = regexp.MustCompile(`(?im)^(?:final|original)-recipient:\s*rfc822;\s*<?([a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,})`)
	campaignNDRLooseRe  = regexp.MustCompile(`\b([45]\.\d{1,3}\.\d{1,3})\b`)
	campaignHardTextRe  = regexp.MustCompile(`(?i)user unknown|unknown user|no such user|does not exist|mailbox not found|mailbox unavailable|invalid recipient|recipient address rejected|address rejected|no mailbox here|account (?:is )?disabled|unrouteable address`)
)

// campaignClassifyBounce decides the class of one notice. Conservative: only
// a clear permanent bad-address signal is "hard".
func campaignClassifyBounce(status, action, diag string) string {
	action = strings.ToLower(strings.TrimSpace(action))
	switch action {
	case "delayed", "delivered", "relayed", "expanded":
		return "ignore"
	}
	if strings.HasPrefix(status, "5.1.") || status == "5.2.1" {
		return "hard"
	}
	if strings.HasPrefix(status, "5.") && campaignHardTextRe.MatchString(diag) && !strings.HasPrefix(status, "5.7.") && status != "5.2.2" {
		return "hard"
	}
	return "soft"
}

// campaignParseNDR extracts one event from a raw notice, or ok=false when no
// recipient can be found.
func campaignParseNDR(raw, ownMailbox string) (campaignBounceEvent, bool) {
	ev := campaignBounceEvent{}
	own := strings.ToLower(ownMailbox)
	for _, m := range campaignBounceAddrRe.FindAllStringSubmatch(raw, 12) {
		a := strings.ToLower(m[1])
		if a != own && !strings.HasPrefix(a, "mailer-daemon@") && !strings.HasPrefix(a, "postmaster@") {
			ev.Address = a
			break
		}
	}
	if ev.Address == "" || len(ev.Address) > 190 {
		return ev, false
	}
	// Only the DSN Final-Recipient field proves whose address failed. An
	// address picked from free text may be a stranger, so it can never be hard.
	viaFinal := false
	if m := campaignNDRFinalRe.FindStringSubmatch(raw); m != nil {
		if a := strings.ToLower(m[1]); a != own && len(a) <= 190 {
			ev.Address = a
			viaFinal = true
		}
	}
	if m := campaignNDRStatusRe.FindStringSubmatch(raw); m != nil {
		ev.Status = m[1]
	} else if m := campaignNDRLooseRe.FindStringSubmatch(raw); m != nil {
		ev.Status = m[1]
	}
	if m := campaignNDRActionRe.FindStringSubmatch(raw); m != nil {
		ev.Action = strings.ToLower(m[1])
	}
	if m := campaignNDRDiagRe.FindStringSubmatch(raw); m != nil {
		ev.Diag = strings.TrimSpace(m[1])
		if len(ev.Diag) > 300 {
			ev.Diag = ev.Diag[:300]
		}
	}
	if m := campaignNDRMsgIDRe.FindStringSubmatch(raw); m != nil {
		ev.Key = m[1]
	} else {
		sum := sha256.Sum256([]byte(raw))
		ev.Key = "sha256:" + hex.EncodeToString(sum[:16])
	}
	if len(ev.Key) > 190 {
		sum := sha256.Sum256([]byte(ev.Key))
		ev.Key = "sha256:" + hex.EncodeToString(sum[:16])
	}
	ev.Class = campaignClassifyBounce(ev.Status, ev.Action, ev.Diag)
	if !viaFinal && ev.Class == "hard" {
		ev.Class = "soft"
	}
	return ev, true
}

var campaignSMTPCodeRe = regexp.MustCompile(`\b([245]\d\d)[ -]([245]\.\d{1,3}\.\d{1,3})`)

// campaignSMTPErrorClass classifies an SMTP rejection received while sending.
// "hard" only for a permanent bad-address reply; everything else is "soft".
func campaignSMTPErrorClass(msg string) string {
	status := ""
	if m := campaignSMTPCodeRe.FindStringSubmatch(msg); m != nil {
		status = m[2]
	}
	return campaignClassifyBounce(status, "failed", msg)
}

// campaignReadBounceEvents reads recent notices from the sender mailbox
// (minimal IMAP over TLS, read-only: BODY.PEEK).
func campaignReadBounceEvents(host, user, pass string, sinceDays int) ([]campaignBounceEvent, error) {
	host = strings.TrimSpace(host)
	if host == "" || user == "" || pass == "" {
		return nil, fmt.Errorf("sin credenciales IMAP")
	}
	imapHost := strings.Replace(host, "smtp", "imap", 1)
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp", imapHost+":993", &tls.Config{ServerName: imapHost})
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(90 * time.Second))
	rd := bufio.NewReader(conn)
	if _, err := rd.ReadString('\n'); err != nil {
		return nil, err
	}
	n := 0
	run := func(cmd string) ([]string, error) {
		n++
		tag := fmt.Sprintf("b%d", n)
		if _, err := fmt.Fprintf(conn, "%s %s\r\n", tag, cmd); err != nil {
			return nil, err
		}
		lines := []string{}
		for {
			line, err := rd.ReadString('\n')
			if err != nil {
				return lines, err
			}
			if strings.HasPrefix(line, tag+" ") {
				if !strings.HasPrefix(line, tag+" OK") {
					return lines, fmt.Errorf("imap: %s", strings.TrimSpace(line))
				}
				return lines, nil
			}
			if i := strings.LastIndex(line, "{"); i >= 0 && strings.HasSuffix(strings.TrimSpace(line), "}") {
				var sz int
				fmt.Sscanf(line[i:], "{%d}", &sz)
				buf := make([]byte, sz)
				if _, err := ioReadFull(rd, buf); err != nil {
					return lines, err
				}
				lines = append(lines, string(buf))
				continue
			}
			lines = append(lines, line)
		}
	}
	q := func(s string) string { return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) }
	if _, err := run(fmt.Sprintf(`LOGIN "%s" "%s"`, q(user), q(pass))); err != nil {
		return nil, fmt.Errorf("imap login")
	}
	if _, err := run(`EXAMINE INBOX`); err != nil {
		return nil, err
	}
	if sinceDays < 1 {
		sinceDays = 1
	}
	since := time.Now().UTC().AddDate(0, 0, -sinceDays).Format("02-Jan-2006")
	res, err := run(fmt.Sprintf(`SEARCH SINCE %s OR FROM "MAILER-DAEMON" FROM "postmaster"`, since))
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, l := range res {
		if strings.HasPrefix(l, "* SEARCH") {
			ids = strings.Fields(strings.TrimPrefix(strings.TrimSpace(l), "* SEARCH"))
		}
	}
	if len(ids) > 400 {
		ids = ids[len(ids)-400:]
	}
	out := []campaignBounceEvent{}
	for _, id := range ids {
		parts, err := run("FETCH " + id + " BODY.PEEK[]<0.30000>")
		if err != nil {
			continue
		}
		if ev, ok := campaignParseNDR(strings.Join(parts, "\n"), user); ok {
			out = append(out, ev)
		}
	}
	_, _ = run("LOGOUT")
	return out, nil
}

type campaignBounceSyncResult struct {
	Scanned   int `json:"scanned"`
	Known     int `json:"already_known"`
	FreshHard int `json:"fresh_hard"`
	FreshSoft int `json:"fresh_soft"`
	Ignored   int `json:"ignored"`
}

// campaignSuppressBounce records one fresh hard/soft event in the legacy
// invalid_emails list (hard only) and in campaign_suppressions. INSERT IGNORE
// on the unique key keeps an existing baja or earlier verdict untouched.
func (s *Server) campaignSuppressBounce(ctx context.Context, restaurantID int, ev campaignBounceEvent) {
	if ev.Class == "hard" {
		_, _ = s.db.ExecContext(ctx, `
			INSERT INTO invalid_emails (id_booking, email, reason)
			SELECT COALESCE(MAX(id), 0), ?, 'rebote Titan (direccion inexistente)' FROM bookings WHERE restaurant_id = ? AND LOWER(TRIM(contact_email)) = ?
			  AND NOT EXISTS (SELECT 1 FROM invalid_emails WHERE LOWER(TRIM(email)) = ?)
		`, ev.Address, restaurantID, ev.Address, ev.Address)
		_, _ = s.db.ExecContext(ctx, `
			INSERT IGNORE INTO campaign_suppressions (restaurant_id, channel, target, booking_id, reason, source)
			SELECT ?, 'email', ?, MAX(id), CONCAT('email no valido: rebote Titan ', ?), 'bounce' FROM bookings WHERE restaurant_id = ? AND LOWER(TRIM(contact_email)) = ?
		`, restaurantID, ev.Address, ev.Status, restaurantID, ev.Address)
		return
	}
	_, _ = s.db.ExecContext(ctx, `
		INSERT IGNORE INTO campaign_suppressions (restaurant_id, channel, target, booking_id, reason, source)
		SELECT ?, 'email', ?, MAX(id), CONCAT('pendiente de verificar (bloqueado hasta decision): rebote no concluyente ', ?), 'pending_verify' FROM bookings WHERE restaurant_id = ? AND LOWER(TRIM(contact_email)) = ?
	`, restaurantID, ev.Address, ev.Status, restaurantID, ev.Address)
}

// syncCampaignBounces reads the notices and applies the fresh ones. Safe to
// call repeatedly from the breaker, the endpoint and the background loop.
func (s *Server) syncCampaignBounces(ctx context.Context, restaurantID int, sinceDays int) (campaignBounceSyncResult, error) {
	res := campaignBounceSyncResult{}
	cfg, err := s.loadEmailProviderConfig(ctx, restaurantID)
	if err != nil {
		return res, err
	}
	if strings.EqualFold(cfg.Provider, "gmail") || cfg.SMTPHost == "" {
		return res, fmt.Errorf("sin buzon SMTP/IMAP configurado")
	}
	events, err := campaignReadBounceEvents(cfg.SMTPHost, cfg.SMTPUsername, cfg.SMTPPassword, sinceDays)
	if err != nil {
		return res, err
	}
	for _, ev := range events {
		res.Scanned++
		if ev.Class == "ignore" {
			res.Ignored++
			continue
		}
		r, err := s.db.ExecContext(ctx, `
			INSERT IGNORE INTO campaign_bounce_events (restaurant_id, event_key, address, status_code, class, diagnostic)
			VALUES (?, ?, ?, NULLIF(?, ''), ?, NULLIF(?, ''))`,
			restaurantID, ev.Key, ev.Address, ev.Status, ev.Class, ev.Diag)
		if err != nil {
			continue
		}
		if n, _ := r.RowsAffected(); n == 0 {
			res.Known++
			continue
		}
		s.campaignSuppressBounce(ctx, restaurantID, ev)
		if ev.Class == "hard" {
			res.FreshHard++
		} else {
			res.FreshSoft++
		}
	}
	if res.FreshHard+res.FreshSoft > 0 {
		slog.Default().Warn("campaign.bounce.sync", "restaurant_id", restaurantID, "hard", res.FreshHard, "soft", res.FreshSoft)
	}
	return res, nil
}

// handleBOCampaignBounceSync POST /campanas/bounces/sync[?days=N]: reads the
// sender mailbox now and applies new notices. No e-mail is sent.
func (s *Server) handleBOCampaignBounceSync(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, 401, "Unauthorized")
		return
	}
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days < 1 || days > 30 {
		days = 3
	}
	res, err := s.syncCampaignBounces(r.Context(), a.ActiveRestaurantID, days)
	if err != nil {
		httpx.WriteJSON(w, 200, map[string]any{"success": false, "message": err.Error()})
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "days": days, "result": res})
}

// runCampaignBounceLoop keeps the lists current without a campaign running.
// CAMPAIGN_BOUNCE_SYNC_MINUTES overrides the 15 min default; 0 disables.
func (s *Server) runCampaignBounceLoop(ctx context.Context) {
	minutes := 15
	if v := strings.TrimSpace(os.Getenv("CAMPAIGN_BOUNCE_SYNC_MINUTES")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			minutes = n
		}
	}
	if minutes <= 0 {
		return
	}
	t := time.NewTicker(time.Duration(minutes) * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		rows, err := s.db.QueryContext(ctx, `SELECT restaurant_id FROM email_provider_config WHERE is_active = 1 AND provider <> 'gmail' AND smtp_host <> ''`)
		if err != nil {
			continue
		}
		ids := []int{}
		for rows.Next() {
			var id int
			if rows.Scan(&id) == nil {
				ids = append(ids, id)
			}
		}
		rows.Close()
		for _, id := range ids {
			_, _ = s.syncCampaignBounces(ctx, id, 2)
		}
	}
}
