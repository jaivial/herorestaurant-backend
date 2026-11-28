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
	campaignNDRFinalRe  = regexp.MustCompile(`(?im)^final-recipient:\s*rfc822;\s*<?([a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,})`)
	campaignNDROrigRe   = regexp.MustCompile(`(?im)^original-recipient:\s*rfc822;\s*<?([a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,})`)
	campaignNDRLooseRe  = regexp.MustCompile(`\b([45]\.\d{1,3}\.\d{1,3})\b`)
	campaignHardTextRe  = regexp.MustCompile(`(?i)user unknown|unknown user|no such user|does not exist|mailbox not found|invalid recipient|recipient address rejected|address rejected|no mailbox here|account (?:is )?disabled|unrouteable address`)
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

// campaignBounceKey builds the idempotency key: NDR Message-ID plus the
// failed address, so a DSN that lists several recipients keeps one event per
// address and a repeated Message-ID never hides another address.
func campaignBounceKey(msgID, address, raw string) string {
	base := msgID
	if base == "" {
		sum := sha256.Sum256([]byte(raw))
		base = "sha256:" + hex.EncodeToString(sum[:16])
	}
	key := base + "|" + address
	if len(key) > 190 {
		sum := sha256.Sum256([]byte(key))
		key = "sha256:" + hex.EncodeToString(sum[:16])
	}
	return key
}

// campaignParseNDR extracts one event per failed recipient of a raw notice.
// Only a DSN Final-Recipient field proves whose address failed; an address
// picked from free text may belong to a stranger, so it can never be hard.
func campaignParseNDR(raw, ownMailbox string) []campaignBounceEvent {
	own := strings.ToLower(ownMailbox)
	msgID := ""
	if m := campaignNDRMsgIDRe.FindStringSubmatch(raw); m != nil {
		msgID = m[1]
	}
	build := func(addr, seg string, proven bool) campaignBounceEvent {
		ev := campaignBounceEvent{Address: addr}
		if m := campaignNDRStatusRe.FindStringSubmatch(seg); m != nil {
			ev.Status = m[1]
		} else if m := campaignNDRLooseRe.FindStringSubmatch(seg); m != nil {
			ev.Status = m[1]
		}
		if m := campaignNDRActionRe.FindStringSubmatch(seg); m != nil {
			ev.Action = strings.ToLower(m[1])
		}
		if m := campaignNDRDiagRe.FindStringSubmatch(seg); m != nil {
			ev.Diag = strings.TrimSpace(m[1])
			if len(ev.Diag) > 300 {
				// 300-byte cut kept, never mid-character (a split rune made PG
				// reject the event and skip its suppression; repair-ignore1).
				ev.Diag = utf8BytePrefix(ev.Diag, 300)
			}
		}
		ev.Key = campaignBounceKey(msgID, addr, raw)
		ev.Class = campaignClassifyBounce(ev.Status, ev.Action, ev.Diag)
		if !proven && ev.Class == "hard" {
			ev.Class = "soft"
		}
		return ev
	}
	out := []campaignBounceEvent{}
	seen := map[string]bool{}
	// Segment per Final-Recipient only: Original-Recipient sits right after it in
	// the same DSN block and used to cut the segment before Status/Diagnostic.
	idx := campaignNDRFinalRe.FindAllStringSubmatchIndex(raw, 12)
	if len(idx) == 0 {
		idx = campaignNDROrigRe.FindAllStringSubmatchIndex(raw, 12)
	}
	for i, loc := range idx {
		addr := strings.ToLower(raw[loc[2]:loc[3]])
		if addr == own || len(addr) > 190 || seen[addr] || strings.HasPrefix(addr, "mailer-daemon@") || strings.HasPrefix(addr, "postmaster@") {
			continue
		}
		end := len(raw)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		seen[addr] = true
		out = append(out, build(addr, raw[loc[0]:end], true))
	}
	if len(out) > 0 {
		return out
	}
	for _, m := range campaignBounceAddrRe.FindAllStringSubmatch(raw, 12) {
		a := strings.ToLower(m[1])
		if a != own && len(a) <= 190 && !strings.HasPrefix(a, "mailer-daemon@") && !strings.HasPrefix(a, "postmaster@") {
			return []campaignBounceEvent{build(a, raw, false)}
		}
	}
	return nil
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
		out = append(out, campaignParseNDR(strings.Join(parts, "\n"), user)...)
	}
	_, _ = run("LOGOUT")
	return out, nil
}

type campaignBounceSyncResult struct {
	Scanned   int `json:"scanned"`
	Known     int `json:"already_known"`
	FreshHard int `json:"fresh_hard"`
	// EventStoreFailed: evidence row not stored, suppression still applied.
	EventStoreFailed int `json:"event_store_failed"`
	// SuppressFailed: do-not-contact write failed (logged, no address).
	SuppressFailed int `json:"suppress_failed"`
	FreshSoft      int `json:"fresh_soft"`
	Ignored        int `json:"ignored"`
}

// campaignSuppressBounce records one fresh hard/soft event in the legacy
// invalid_emails list (hard only) and in campaign_suppressions. INSERT IGNORE
// on the unique key keeps an existing baja or earlier verdict untouched; a hard bounce only upgrades an earlier pending_verify row to invalid.
func (s *Server) campaignSuppressBounce(ctx context.Context, restaurantID int, ev campaignBounceEvent) error {
	// Never truncate the address into another recipient: an unstorable target
	// is already blocked by campaignSuppressionFilter (fail closed).
	if _, ok := campaignSuppressionKey("email", ev.Address); !ok {
		slog.Default().Warn("campaign.bounce.target_unstorable", "restaurant_id", restaurantID, "bytes", len(ev.Address))
		return nil
	}
	if ev.Class == "hard" {
		// Source quirk kept: an aggregate without GROUP BY always yields one
		// row, so the WHERE-level NOT EXISTS does not stop the insert (id_booking
		// 0 when no booking matches). Same on PG. Select-list params are typed.
		_, _ = s.db.ExecContext(ctx, nativeSQL(`
			INSERT INTO invalid_emails (id_booking, email, reason)
			SELECT COALESCE(MAX(id), 0), ?, 'rebote Titan (direccion inexistente)' FROM bookings WHERE restaurant_id = ? AND LOWER(TRIM(contact_email)) = ?
			  AND NOT EXISTS (SELECT 1 FROM invalid_emails WHERE LOWER(TRIM(email)) = ?)
		`, `
			INSERT INTO invalid_emails (id_booking, email, reason)
			SELECT COALESCE(MAX(id), 0), CAST(? AS text), 'rebote Titan (direccion inexistente)' FROM bookings WHERE restaurant_id = ? AND LOWER(TRIM(contact_email)) = ?
			  AND NOT EXISTS (SELECT 1 FROM invalid_emails WHERE LOWER(TRIM(email)) = ?)
		`), ev.Address, restaurantID, ev.Address, ev.Address)
		// ODKU -> ON CONFLICT on uq_suppression (the only reachable unique key;
		// id is generated). MySQL assigns reason first while source still holds
		// the old value, so evaluating both CASEs on the old row is identical.
		_, err := s.db.ExecContext(ctx, nativeSQL(`
			INSERT INTO campaign_suppressions (restaurant_id, channel, target, booking_id, reason, source)
			SELECT ?, 'email', ?, MAX(id), CONCAT('email no valido: rebote Titan ', ?), 'bounce' FROM bookings WHERE restaurant_id = ? AND LOWER(TRIM(contact_email)) = ?
			ON DUPLICATE KEY UPDATE
				reason = IF(source = 'pending_verify', VALUES(reason), reason),
				source = IF(source = 'pending_verify', 'bounce', source)
		`, `
			INSERT INTO campaign_suppressions (restaurant_id, channel, target, booking_id, reason, source)
			SELECT CAST(? AS integer), 'email', CAST(? AS text), MAX(id), 'email no valido: rebote Titan ' || CAST(? AS text), 'bounce' FROM bookings WHERE restaurant_id = ? AND LOWER(TRIM(contact_email)) = ?
			ON CONFLICT (restaurant_id, app.norm(channel), app.norm(target)) DO UPDATE SET
				reason = CASE WHEN campaign_suppressions.source = 'pending_verify' THEN EXCLUDED.reason ELSE campaign_suppressions.reason END,
				source = CASE WHEN campaign_suppressions.source = 'pending_verify' THEN 'bounce' ELSE campaign_suppressions.source END
		`), restaurantID, ev.Address, ev.Status, restaurantID, ev.Address)
		return err
	}
	// INSERT IGNORE breadth: uq_suppression duplicate + over-width target
	// (190) / reason (255) truncation; no FK. PG mirrors exactly those.
	_, err := s.db.ExecContext(ctx, nativeSQL(`
		INSERT IGNORE INTO campaign_suppressions (restaurant_id, channel, target, booking_id, reason, source)
		SELECT ?, 'email', ?, MAX(id), CONCAT('pendiente de verificar (bloqueado hasta decision): rebote no concluyente ', ?), 'pending_verify' FROM bookings WHERE restaurant_id = ? AND LOWER(TRIM(contact_email)) = ?
	`, `
		INSERT INTO campaign_suppressions (restaurant_id, channel, target, booking_id, reason, source)
		SELECT CAST(? AS integer), 'email', CAST(? AS text), MAX(id), LEFT('pendiente de verificar (bloqueado hasta decision): rebote no concluyente ' || CAST(? AS text), 255), 'pending_verify' FROM bookings WHERE restaurant_id = ? AND LOWER(TRIM(contact_email)) = ?
		ON CONFLICT (restaurant_id, app.norm(channel), app.norm(target)) DO NOTHING
	`), restaurantID, ev.Address, ev.Status, restaurantID, ev.Address)
	return err
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
		// INSERT IGNORE breadth: uq_bounce_event (restaurant_id, event_key)
		// duplicate (RowsAffected 0 -> "known") + varchar truncation of the
		// IMAP-derived strings; no FK. PG: ON CONFLICT on that key + LEFT to
		// each declared width after NULLIF (MySQL order). OPEN: IMAP text cut
		// at 300 BYTES can be invalid UTF-8 - MySQL IGNORE downgrades that to
		// a warning, PG rejects (22021) and the event + its suppression are
		// skipped. Not reproduced here (MySQL's stored value unproven).
		r, err := s.db.ExecContext(ctx, nativeSQL(`
			INSERT IGNORE INTO campaign_bounce_events (restaurant_id, event_key, address, status_code, class, diagnostic)
			VALUES (?, ?, ?, NULLIF(?, ''), ?, NULLIF(?, ''))`, `
			INSERT INTO campaign_bounce_events (restaurant_id, event_key, address, status_code, class, diagnostic)
			VALUES (?, LEFT(CAST(? AS text), 190), LEFT(CAST(? AS text), 190), LEFT(NULLIF(CAST(? AS text), ''), 16), LEFT(CAST(? AS text), 8), LEFT(NULLIF(CAST(? AS text), ''), 300))
			ON CONFLICT (restaurant_id, app.norm_ascii(event_key)) DO NOTHING`),
			restaurantID, ev.Key, ev.Address, ev.Status, ev.Class, campaignBounceDiagnosticForStore(ev.Diag))
		// repair-suppression2: the evidence insert no longer gates the
		// do-not-contact write. Autocommit statements on s.db (no shared tx,
		// so no aborted-transaction/savepoint concern). Only a proven
		// duplicate (RowsAffected 0) skips, as before.
		if err != nil {
			res.EventStoreFailed++
			slog.Default().Warn("campaign.bounce.event_store_failed", "restaurant_id", restaurantID, "class", ev.Class, "err", err.Error())
		} else if n, _ := r.RowsAffected(); n == 0 {
			res.Known++
			continue
		}
		if err := s.campaignSuppressBounce(ctx, restaurantID, ev); err != nil {
			res.SuppressFailed++
			slog.Default().Warn("campaign.bounce.suppress_failed", "restaurant_id", restaurantID, "class", ev.Class, "err", err.Error())
		}
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
	// camp_auto_retake_v1: at boot the frozen/orphaned campaigns retake
	// immediately — the prevention window is measured against bounce notices,
	// not against process restarts.
	s.runCampaignAutoRetake(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		// smtp_host <> '' : PAD SPACE makes an all-blank host equal '' on MySQL only (PAD SPACE gap).
		rows, err := s.db.QueryContext(ctx, portableSQL(`SELECT restaurant_id FROM email_provider_config WHERE is_active = 1 AND provider <> 'gmail' AND smtp_host <> ''`))
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
		s.runCampaignEmailRetries(ctx)
		// camp_auto_retake_v1: after the sync, so a fresh notice inside the
		// window keeps the freeze held for another round.
		s.runCampaignAutoRetake(ctx)
	}
}
