package api

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	emailverifier "github.com/AfterShip/email-verifier"
	"log/slog"
	"net"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Coordination id: camp_email_guard_v1
//
// Titan (the restaurant SMTP) suspends the mailbox after a handful of
// bounces per hour/day, so a campaign must never send to addresses that are
// likely dead, and must stop by itself when bounces start to arrive.
//   - campaignEmailLooksDeliverable: syntax, typo domains, disposable domains
//     and a cached MX lookup (a domain without MX/A can never accept mail).
//   - campaignBounceBreaker: reads the sender mailbox over IMAP, adds every
//     bounced recipient to invalid_emails and trips after N new bounces so
//     the runner pauses the e-mail part of the campaign.

var campaignEmailSyntax = regexp.MustCompile(`^[a-z0-9][a-z0-9._%+\-]{0,63}@[a-z0-9]([a-z0-9\-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9\-]*[a-z0-9])?)+$`)

// Typos of the big providers: nobody owns these mailboxes.
var campaignTypoDomains = map[string]bool{
	"gmial.com": true, "gmai.com": true, "gamil.com": true, "gmail.con": true, "gmail.cm": true, "gmail.om": true,
	"gnail.com": true, "gmaill.com": true, "gmeil.com": true, "gmal.com": true, "gmail.comm": true, "gmail.vom": true, "gmail.xom": true, "gmail.cim": true,
	"hotmial.com": true, "hotmai.com": true, "hotmail.con": true, "hotmal.com": true, "hotnail.com": true, "hotmil.com": true, "hotamil.com": true, "hotmail.cm": true, "hotmail.om": true, "hotmaill.com": true,
	"hotmail.ex": true, "hotmail.ec": true, "outlok.com": true, "outlook.con": true, "outlool.com": true,
	"yaho.com": true, "yahho.com": true, "yahoo.con": true, "yaoo.com": true, "yahoo.ed": true, "yahoo.e": true,
	"icloud.con": true, "iclou.com": true, "icoud.com": true,
}

var campaignDisposableDomains = map[string]bool{
	"mailinator.com": true, "guerrillamail.com": true, "10minutemail.com": true, "tempmail.com": true, "temp-mail.org": true,
	"yopmail.com": true, "trashmail.com": true, "sharklasers.com": true, "getnada.com": true, "dispostable.com": true,
	"maildrop.cc": true, "throwawaymail.com": true, "fakeinbox.com": true, "mailnesia.com": true,
}

type campaignMXEntry struct {
	ok bool
	at time.Time
}

var (
	campaignMXCache   = map[string]campaignMXEntry{}
	campaignMXCacheMu sync.Mutex
)

func campaignDomainHasMail(domain string) bool {
	campaignMXCacheMu.Lock()
	e, hit := campaignMXCache[domain]
	campaignMXCacheMu.Unlock()
	if hit && time.Since(e.at) < 24*time.Hour {
		return e.ok
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	ok := true
	mx, err := net.DefaultResolver.LookupMX(ctx, domain)
	// RFC 7505 Null MX ("." target): the domain states it accepts no mail.
	if err == nil && len(mx) == 1 && strings.TrimSuffix(mx[0].Host, ".") == "" {
		ok = false
	} else if err != nil || len(mx) == 0 {
		// Only a definitive "no such domain / no records" counts as dead; a
		// timeout or SERVFAIL must not drop a good customer.
		if dnsErr, isDNS := err.(*net.DNSError); isDNS && (dnsErr.IsNotFound) {
			if _, aerr := net.DefaultResolver.LookupHost(ctx, domain); aerr != nil {
				if adns, ok2 := aerr.(*net.DNSError); ok2 && adns.IsNotFound {
					ok = false
				}
			}
		}
	}
	campaignMXCacheMu.Lock()
	campaignMXCache[domain] = campaignMXEntry{ok: ok, at: time.Now()}
	campaignMXCacheMu.Unlock()
	return ok
}

// campaignEmailVerifier is AfterShip/email-verifier (MIT) used ONLY for its
// local checks: address syntax, disposable-domain list and domain typo
// suggestion. SMTP verification, Gravatar and auto-update are deliberately
// never enabled: no connection to any recipient mail server is ever made
// (outbound port 25 is closed and mailbox probing is not approved), and no
// address leaves the process.
var campaignEmailVerifier = emailverifier.NewVerifier().EnableDomainSuggest()

// campaignEmailLooksDeliverable returns false (with a short reason) for
// addresses that would almost certainly bounce. Reason "dominio con errata"
// means a suspected typo: callers keep those blocked as pending verification,
// never as proven invalid and never auto-corrected.
func campaignEmailLooksDeliverable(addr string) (bool, string) {
	addr = strings.ToLower(strings.TrimSpace(addr))
	if !campaignEmailSyntax.MatchString(addr) || strings.Contains(addr, "..") {
		return false, "sintaxis"
	}
	syn := campaignEmailVerifier.ParseAddress(addr)
	if !syn.Valid {
		return false, "sintaxis"
	}
	domain := addr[strings.LastIndex(addr, "@")+1:]
	if campaignTypoDomains[strings.ToLower(domain)] {
		return false, "dominio con errata"
	}
	if campaignDisposableDomains[domain] || campaignEmailVerifier.IsDisposable(domain) {
		return false, "dominio desechable"
	}
	if !campaignDomainHasMail(domain) {
		// A dead domain that is one edit away from a known provider is a
		// probable typo (pending), otherwise it simply cannot receive mail.
		if sug := campaignEmailVerifier.SuggestDomain(domain); sug != "" && sug != domain {
			return false, "dominio con errata"
		}
		return false, "dominio sin correo (MX)"
	}
	return true, ""
}

// ---- bounce breaker ---------------------------------------------------------

const (
	campaignBounceTripAfter = 3                // 3 new bounces in one run pause the e-mail part; a single hard bounce only marks that address invalid and the run continues
	campaignBouncePoll      = 45 * time.Second // IMAP poll spacing
)

type campaignBounceState struct {
	mu       sync.Mutex
	baseline map[string]bool
	seen     map[string]bool
	lastPoll time.Time
	tripped  bool
	started  bool
	baseID   int64
}

var campaignBounceStates = struct {
	mu sync.Mutex
	m  map[int64]*campaignBounceState
}{m: map[int64]*campaignBounceState{}}

func campaignBounceFor(campaignID int64) *campaignBounceState {
	campaignBounceStates.mu.Lock()
	defer campaignBounceStates.mu.Unlock()
	st := campaignBounceStates.m[campaignID]
	if st == nil {
		st = &campaignBounceState{baseline: map[string]bool{}, seen: map[string]bool{}}
		campaignBounceStates.m[campaignID] = st
	}
	return st
}

var campaignBounceAddrRe = regexp.MustCompile(`(?i)(?:final-recipient: rfc822;|original-recipient: rfc822;|<)\s*([a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,})`)

// campaignReadBounces lists the recipients of MAILER-DAEMON messages received
// today in the sender mailbox (minimal IMAP over TLS, read-only).
func campaignReadBounces(host, user, pass string) (map[string]bool, error) {
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
	_ = conn.SetDeadline(time.Now().Add(40 * time.Second))
	rd := bufio.NewReader(conn)
	if _, err := rd.ReadString('\n'); err != nil {
		return nil, err
	}
	n := 0
	run := func(cmd string) ([]string, error) {
		n++
		tag := fmt.Sprintf("a%d", n)
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
			// literal {N}
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
	since := time.Now().UTC().AddDate(0, 0, -1).Format("02-Jan-2006")
	res, err := run(fmt.Sprintf(`SEARCH SINCE %s FROM "MAILER-DAEMON"`, since))
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, l := range res {
		if strings.HasPrefix(l, "* SEARCH") {
			ids = strings.Fields(strings.TrimPrefix(strings.TrimSpace(l), "* SEARCH"))
		}
	}
	if len(ids) > 300 {
		ids = ids[len(ids)-300:]
	}
	out := map[string]bool{}
	own := strings.ToLower(user)
	for _, id := range ids {
		parts, err := run("FETCH " + id + " BODY.PEEK[TEXT]")
		if err != nil {
			continue
		}
		body := strings.Join(parts, "\n")
		for _, m := range campaignBounceAddrRe.FindAllStringSubmatch(body, 6) {
			a := strings.ToLower(m[1])
			if a != own && !strings.HasPrefix(a, "mailer-daemon@") && !strings.HasPrefix(a, "postmaster@") {
				out[a] = true
				break
			}
		}
	}
	_, _ = run("LOGOUT")
	return out, nil
}

func ioReadFull(r *bufio.Reader, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := r.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// campaignEmailBreakerTripped polls the bounce mailbox at most every
// campaignBouncePoll, records new bounced recipients in invalid_emails and
// reports whether the campaign must stop sending e-mail.
func (s *Server) campaignEmailBreakerTripped(ctx context.Context, restaurantID int, campaignID int64, coordID string) bool {
	st := campaignBounceFor(campaignID)
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.tripped {
		return true
	}
	if time.Since(st.lastPoll) < campaignBouncePoll {
		return false
	}
	st.lastPoll = time.Now()
	cfg, err := s.loadEmailProviderConfig(ctx, restaurantID)
	if err != nil || strings.EqualFold(cfg.Provider, "gmail") {
		return false
	}
	res, err := s.syncCampaignBounces(ctx, restaurantID, 1)
	if err != nil {
		slog.Default().Warn("campaign.bounce.read_failed", "coord_id", coordID, "campaign_id", campaignID, "err", err.Error())
		return false
	}
	if !st.started {
		// First poll of this run: notices already in the mailbox were applied by
		// the sync (idempotent). Only events recorded after this point trip it,
		// whoever (this breaker, the endpoint or the background loop) applied them.
		st.started = true
		_ = s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) FROM campaign_bounce_events WHERE restaurant_id = ?`, restaurantID).Scan(&st.baseID)
		return false
	}
	newTotal := 0
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM campaign_bounce_events WHERE restaurant_id = ? AND id > ?`, restaurantID, st.baseID).Scan(&newTotal)
	if newTotal > 0 {
		slog.Default().Warn("campaign.bounce.new", "coord_id", coordID, "campaign_id", campaignID, "events", newTotal, "hard", res.FreshHard, "soft", res.FreshSoft)
	}
	// An isolated bad address (one hard or one generic permanent reply) is marked
	// by the sync and the run continues. The send stops on a series of notices or
	// on any sign of a policy / auth / rate / reputation problem, which would hurt
	// the sending account.
	systemic := 0
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM campaign_bounce_events WHERE restaurant_id = ? AND id > ? AND (
		status_code LIKE '4.%' OR status_code LIKE '5.3.%' OR status_code LIKE '5.4.%' OR status_code LIKE '5.7.%'
		OR diagnostic REGEXP 'rate limit|throttl|too many|spam|block|reputation|policy|authenticat|denied|quota|try again|temporar')`, restaurantID, st.baseID).Scan(&systemic)
	if systemic > 0 || newTotal >= campaignBounceTripAfter {
		st.tripped = true
		_, _ = s.db.ExecContext(ctx, `UPDATE campaign_recipients SET status = 'paused', error = 'corte por rebotes' WHERE campaign_id = ? AND channel = 'email' AND status = 'pending'`, campaignID)
		slog.Default().Error("campaign.bounce.tripped", "coord_id", coordID, "campaign_id", campaignID, "new_bounces", newTotal, "systemic", systemic)
		return true
	}
	return false
}
