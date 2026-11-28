package api

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"preactvillacarmen/internal/httpx"
)

// Coordination id: camp_email_validation_v1
//
// POST /campanas/email-validation[?apply=1] analyses every distinct booking
// e-mail of the restaurant WITHOUT sending anything. Only DEFINITIVE failures
// (bad syntax, typo domain, disposable domain, domain that does not exist) are
// classed invalid; they go to the e-mail blacklist (campaign_suppressions,
// channel email, source invalid, reason "email no valido: ...") and never block
// WhatsApp. A syntactically fine address on a domain with mail servers is
// "no verificado": the mailbox may or may not exist and it is never labelled
// invalid (catch-all and unknown stay sendable).
func (s *Server) handleBOCampaignEmailValidation(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	apply := r.URL.Query().Get("apply") == "1"
	// GROUP BY the expression inherits the old-nondeterministic-collation collation: CI/accent folding
	// picks the group and an arbitrary representative on both (CI/PAD SPACE gap).
	rows, err := s.db.QueryContext(r.Context(), portableSQL(`
		SELECT LOWER(TRIM(contact_email)) e, MAX(id)
		FROM bookings
		WHERE restaurant_id = ? AND TRIM(COALESCE(contact_email, '')) <> ''
		GROUP BY e`), a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteJSON(w, 500, map[string]any{"success": false, "message": "No se pudieron leer los emails"})
		return
	}
	type item struct {
		email   string
		booking int64
	}
	items := []item{}
	for rows.Next() {
		var it item
		if rows.Scan(&it.email, &it.booking) == nil {
			items = append(items, it)
		}
	}
	rows.Close()

	probes := make([]campaignProbe, 0, len(items))
	for _, it := range items {
		probes = append(probes, campaignProbe{"email", it.email})
	}
	// Report-only classification + suppression inserts (no send): a read
	// error leaves the filter partial exactly as the source ignored it.
	sup, _ := s.loadCampaignSuppressionFilterFor(r.Context(), a.ActiveRestaurantID, probes)
	type verdict struct {
		it     item
		ok     bool
		reason string
	}
	out := make([]verdict, len(items))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 16)
	for i := range items {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			ok, reason := campaignEmailLooksDeliverable(items[i].email)
			out[i] = verdict{it: items[i], ok: ok, reason: reason}
		}(i)
	}
	wg.Wait()

	byReason := map[string]int{}
	invalid, alreadyBlocked, unverified, stored := 0, 0, 0, 0
	samples := []string{}
	for _, v := range out {
		if v.ok {
			if sup.blocked("email", v.it.email) {
				alreadyBlocked++
			} else {
				unverified++
			}
			continue
		}
		byReason[v.reason]++
		if sup.blocked("email", v.it.email) {
			alreadyBlocked++
			continue
		}
		invalid++
		if len(samples) < 15 {
			samples = append(samples, v.it.email+" ("+v.reason+")")
		}
		if _, fits := campaignSuppressionKey("email", v.it.email); apply && fits {
			// A suspected typo or a disposable domain is never proven
			// invalid: it stays blocked as pending verification.
			source, why := "invalid", "email no valido: "+v.reason
			if v.reason == "dominio con errata" {
				source, why = "pending_verify", "pendiente de verificar (bloqueado hasta decision): posible errata de dominio"
			} else if v.reason == "dominio desechable" {
				source, why = "pending_verify", "pendiente de verificar (bloqueado hasta decision): dominio desechable"
			}
			// INSERT IGNORE breadth: only the uq_suppression duplicate (target
			// <= 190 bytes checked above, short constant reasons, no FK). As in
			// MySQL a duplicate returns no error and still counts as stored.
			if _, err := s.db.ExecContext(r.Context(), nativeSQL(`
				INSERT IGNORE INTO campaign_suppressions (restaurant_id, channel, target, booking_id, reason, source)
				VALUES (?, 'email', ?, NULLIF(?, 0), ?, ?)`,
				`
				INSERT INTO campaign_suppressions (restaurant_id, channel, target, booking_id, reason, source)
				VALUES (?, 'email', ?, NULLIF(CAST(? AS bigint), 0), ?, ?)
				ON CONFLICT (restaurant_id, app.norm(channel), app.norm(target)) DO NOTHING`), a.ActiveRestaurantID, v.it.email, v.it.booking, why, source); err == nil {
				stored++
			}
		}
	}
	reasons := []string{}
	for k := range byReason {
		reasons = append(reasons, k)
	}
	sort.Strings(reasons)
	httpx.WriteJSON(w, 200, map[string]any{
		"success":            true,
		"applied":            apply,
		"analyzed":           len(items),
		"new_invalid":        invalid,
		"stored":             stored,
		"already_blocked":    alreadyBlocked,
		"unverified_mailbox": unverified,
		"invalid_by_reason":  byReason,
		"sample_new_invalid": samples,
		"note":               strings.Join([]string{"invalid = fallo definitivo (sintaxis, typo, desechable, dominio inexistente)", "unverified_mailbox = sintaxis y MX correctos, buzon sin comprobar (NO invalido)"}, "; "),
	})
}
