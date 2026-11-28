package api

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Shared do-not-contact normalization (repair-ignore1). campaign_suppressions
// declares target varchar(190) / reason varchar(255) (utf8mb4 and PG varchar
// both count CHARACTERS). MySQL INSERT IGNORE silently cut an over-wide target
// to 190 characters, i.e. stored a DIFFERENT recipient while the original
// stayed contactable. Explicit technical fix on both dialects: a target that
// cannot be stored exactly is never truncated; writers skip it and the
// send-side filter treats it as suppressed (fail closed).
const (
	campaignSuppressionTargetMaxChars = 190
	campaignSuppressionReasonMaxChars = 255
)

// campaignSuppressionKey returns the canonical stored key and whether it fits
// the column losslessly (valid UTF-8, no NUL, <= 190 characters).
func campaignSuppressionKey(channel, target string) (string, bool) {
	key := campaignCanonicalTarget(channel, target)
	if key == "" || !utf8.ValidString(key) || strings.IndexByte(key, 0) >= 0 ||
		utf8.RuneCountInString(key) > campaignSuppressionTargetMaxChars {
		return key, false
	}
	return key, true
}

// campaignSuppressionReason makes a free-text reason storable on both
// dialects: invalid bytes -> U+FFFD, NUL dropped, cut to 255 characters (the
// character truncation MySQL IGNORE applied to valid text; PG would 22001).
func campaignSuppressionReason(reason string) string {
	reason = strings.ReplaceAll(strings.ToValidUTF8(reason, "\uFFFD"), "\x00", "")
	return utf8RuneCut(reason, campaignSuppressionReasonMaxChars)
}

// utf8RuneCut keeps at most n characters.
func utf8RuneCut(s string, n int) string {
	i := 0
	for pos := range s {
		if i == n {
			return s[:pos]
		}
		i++
	}
	return s
}

// utf8BytePrefix keeps at most n bytes without splitting a character; bytes
// that were already invalid before the cut point are left as they were.
func utf8BytePrefix(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && cut > n-utf8.UTFMax && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// campaignProbe is one (channel, target) a send path is about to contact.
type campaignProbe struct{ channel, target string }

// campaignSuppressionProbeChunk bounds one collation lookup statement.
const campaignSuppressionProbeChunk = 200

// loadCampaignSuppressionFilterFor is loadCampaignSuppressionFilter plus the
// collation lookup for the probes (repair-suppression2). uq_suppression
// compares target under the column collation (MySQL utf8mb4_unicode_ci, PG
// the old nondeterministic collation): an insert for "jose@x" is a no-op when "josé@x" is stored,
// while the Go map only knows the exact bytes. Each probe is therefore also
// compared IN THE DB with the column's own collation, so exactly the targets
// the unique key already treats as the same contact are blocked - no Go
// approximation, no wider match. Any lookup error is returned: callers do not
// send on error (fail closed).
func (s *Server) loadCampaignSuppressionFilterFor(ctx context.Context, restaurantID int, probes []campaignProbe) (campaignSuppressionFilter, error) {
	f, err := s.loadCampaignSuppressionFilter(ctx, restaurantID)
	if err != nil {
		return f, err
	}
	byChannel := map[string][]string{}
	for _, pr := range probes {
		key, ok := campaignSuppressionKey(pr.channel, pr.target)
		if !ok || f.blocked(pr.channel, key) {
			continue // already blocked (exact or unstorable)
		}
		byChannel[pr.channel] = append(byChannel[pr.channel], key)
	}
	for channel, keys := range byChannel {
		for start := 0; start < len(keys); start += campaignSuppressionProbeChunk {
			end := start + campaignSuppressionProbeChunk
			if end > len(keys) {
				end = len(keys)
			}
			if err := s.campaignSuppressionCollationHits(ctx, restaurantID, channel, keys[start:end], f); err != nil {
				return f, err
			}
		}
	}
	return f, nil
}

func (s *Server) campaignSuppressionCollationHits(ctx context.Context, restaurantID int, channel string, keys []string, f campaignSuppressionFilter) error {
	var my, pg strings.Builder
	args := make([]any, 0, len(keys)+2)
	for i, k := range keys {
		if i > 0 {
			my.WriteString(" UNION ALL ")
			pg.WriteString(" UNION ALL ")
		}
		// Index literals are integers generated here, never user text.
		fmt.Fprintf(&my, "SELECT %d AS i, ? AS probe", i)
		fmt.Fprintf(&pg, "SELECT %d AS i, CAST(? AS text) AS probe", i)
		args = append(args, k)
	}
	args = append(args, restaurantID, channel)
	rows, err := s.db.QueryContext(ctx, nativeSQL(
		"SELECT p.i FROM ("+my.String()+") p WHERE EXISTS (SELECT 1 FROM campaign_suppressions s WHERE s.restaurant_id = ? AND s.channel = ? AND s.target = CONVERT(p.probe USING utf8mb4) COLLATE utf8mb4_unicode_ci)",
		"SELECT p.i FROM ("+pg.String()+") p WHERE EXISTS (SELECT 1 FROM campaign_suppressions s WHERE s.restaurant_id = ? AND app.norm(s.channel) = app.norm(?) AND app.norm(s.target) = app.norm(p.probe))"), args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var i int
		if err := rows.Scan(&i); err != nil {
			return err
		}
		if i < 0 || i >= len(keys) {
			return fmt.Errorf("suppression lookup: index out of range")
		}
		if channel == "whatsapp" {
			f.phones[keys[i]] = true
		} else {
			f.emails[keys[i]] = true
		}
	}
	return rows.Err()
}

// campaignBounceDiagnosticForStore keeps a valid diagnostic as is. Invalid
// UTF-8 (raw IMAP bytes) is stored losslessly behind an explicit flag, cut so
// the result fits varchar(300) characters and is always valid UTF-8:
//   - no NUL: "[raw-latin1]" + Latin-1 reading (byte -> U+00xx, bijective),
//     first 288 bytes;
//   - with NUL (PG text cannot hold U+0000): "[raw-b64]" + standard base64 of
//     the first 216 bytes.
//
// Not MySQL-equivalent: what MySQL IGNORE kept is unproven (repair-ignore2).
func campaignBounceDiagnosticForStore(diag string) string {
	if utf8.ValidString(diag) && strings.IndexByte(diag, 0) < 0 {
		return diag
	}
	b := []byte(diag)
	if strings.IndexByte(diag, 0) >= 0 {
		if len(b) > 216 {
			b = b[:216]
		}
		return "[raw-b64]" + base64.StdEncoding.EncodeToString(b)
	}
	const flag = "[raw-latin1]"
	if max := 300 - len(flag); len(b) > max {
		b = b[:max]
	}
	var sb strings.Builder
	sb.WriteString(flag)
	for _, c := range b {
		sb.WriteRune(rune(c))
	}
	return sb.String()
}
