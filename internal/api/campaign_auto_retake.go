package api

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"strings"
)

// Coordination id: camp_auto_retake_v1
//
// Bounce freeze with AUTOMATIC retake: a bounce burst (or a policy signal)
// freezes the e-mail rows (paused) instead of stopping the campaign forever.
// After CAMPAIGN_BOUNCE_FREEZE_HOURS (default 6) with NO new bounce notice the
// freeze expires by itself: the frozen rows are re-queued — re-checking the
// do-not-contact list first — the breaker re-baselines and the drain restarts,
// so the operator never has to resume by hand.
//
// The same pass restarts ORPHANED drains: a campaign in 'sending' whose runner
// died (redeploy/restart) with rows still 'pending' gets its runner back at
// boot and on every bounce-loop tick.
//
// Safety:
//   - the quiescence window keys on campaign_bounce_events.created_at (any
//     fresh notice extends the prevention window, no schema change needed);
//   - every state transition claims rows by status (idempotent under the
//     boot + tick overlap and under concurrent runners);
//   - re-queued targets are re-checked against the suppression list, so a
//     target that unsubscribed during the freeze becomes 'skipped', never
//     pending;
//   - only campaigns in 'draft'/'sending' are ever touched: a campaign already
//     reported 'sent' is never resurrected;
//   - CAMPAIGN_BOUNCE_FREEZE_HOURS <= 0 disables the automatic retake
//     (frozen rows then wait for an operator, the pre-existing behaviour).

const campaignBounceFreezeDefaultHours = 6

// campaignBounceFrozenClause matches every "frozen by the bounce breaker"
// row: the current marker and the legacy 'pausa bounce HH:MM' rows written by
// earlier builds, which must retake too instead of staying stranded.
const campaignBounceFrozenClause = `(error = 'corte por rebotes' OR error LIKE 'pausa bounce%')`

// campaignBounceFreezeHours is the prevention window: how long the freeze
// lasts WITHOUT new bounce notices before the campaign retakes by itself.
func campaignBounceFreezeHours() int {
	h := campaignBounceFreezeDefaultHours
	if v := strings.TrimSpace(os.Getenv("CAMPAIGN_BOUNCE_FREEZE_HOURS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			h = n
		}
	}
	return h
}

// runCampaignAutoRetake is safe to call repeatedly (boot + every bounce-loop
// tick). It never sends anything itself: it re-queues eligible rows and starts
// the regular runner, which applies the pacing and the per-delivery checks.
func (s *Server) runCampaignAutoRetake(ctx context.Context) {
	hours := campaignBounceFreezeHours()

	// ---- B: expired bounce freezes -> pending (quiescence per restaurant) ----
	frozen, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT r.campaign_id, r.restaurant_id, c.coord_id
		FROM campaign_recipients r
		JOIN campaigns c ON c.id = r.campaign_id
		WHERE r.status = 'paused' AND `+campaignBounceFrozenClause+`
		  AND c.status IN ('draft', 'sending')`)
	if err != nil {
		slog.Default().Warn("campaign.auto_retake.scan_failed", "coord_id", "camp_auto_retake_v1", "err", err.Error())
		return
	}
	type frozenCampaign struct {
		id    int64
		rid   int
		coord string
	}
	cands := []frozenCampaign{}
	for frozen.Next() {
		var f frozenCampaign
		if frozen.Scan(&f.id, &f.rid, &f.coord) == nil {
			cands = append(cands, f)
		}
	}
	frozen.Close()
	if hours <= 0 {
		// Retake disabled by configuration: only report what is being held.
		if len(cands) > 0 {
			slog.Default().Info("campaign.auto_retake.disabled", "coord_id", "camp_auto_retake_v1", "campaigns", len(cands))
		}
	} else if len(cands) > 0 {
		// One quiescence check per restaurant: any notice inside the window
		// keeps the whole freeze of that restaurant in prevention.
		quiescent := map[int]bool{}
		for _, f := range cands {
			if _, seen := quiescent[f.rid]; seen {
				continue
			}
			var fresh int
			_ = s.db.QueryRowContext(ctx, `
				SELECT COUNT(*) FROM campaign_bounce_events
				WHERE restaurant_id = ? AND created_at >= DATE_SUB(NOW(), INTERVAL ? HOUR)`,
				f.rid, hours).Scan(&fresh)
			quiescent[f.rid] = fresh == 0
		}
		for _, f := range cands {
			if !quiescent[f.rid] {
				slog.Default().Info("campaign.auto_retake.holding", "coord_id", f.coord,
					"campaign_id", f.id, "restaurant_id", f.rid, "freeze_hours", hours)
				continue
			}
			s.requeueFrozenCampaign(ctx, f.id, f.rid, f.coord)
		}
	}

	// ---- A: orphaned drains -> runner back ('sending' + pending + no runner) ----
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, restaurant_id, coord_id FROM campaigns
		WHERE status = 'sending'
		  AND EXISTS (SELECT 1 FROM campaign_recipients r
		              WHERE r.campaign_id = campaigns.id AND r.status = 'pending')`)
	if err != nil {
		return
	}
	type orphan struct {
		id    int64
		rid   int
		coord string
	}
	orphans := []orphan{}
	for rows.Next() {
		var o orphan
		if rows.Scan(&o.id, &o.rid, &o.coord) == nil {
			orphans = append(orphans, o)
		}
	}
	rows.Close()
	for _, o := range orphans {
		if campaignRunners.running(o.id) {
			continue
		}
		if !campaignRunners.start(o.id) {
			continue
		}
		slog.Default().Info("campaign.auto_retake.runner_started", "coord_id", o.coord,
			"campaign_id", o.id, "restaurant_id", o.rid, "reason", "orphaned pending drain")
		campaignID, restID := o.id, o.rid
		go func() {
			defer campaignRunners.stop(campaignID)
			s.runCampaignSend(ctx, restID, campaignID)
		}()
	}
}

// requeueFrozenCampaign moves the expired frozen rows of one campaign back to
// pending (or to 'skipped' when the target is now suppressed) and restarts the
// drain if it moved anything.
func (s *Server) requeueFrozenCampaign(ctx context.Context, campaignID int64, restaurantID int, coordID string) {
	sup, err := s.loadCampaignSuppressionFilter(ctx, restaurantID)
	if err != nil {
		slog.Default().Warn("campaign.auto_retake.suppression_read_failed", "coord_id", coordID,
			"campaign_id", campaignID, "err", err.Error())
		return
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, channel, target FROM campaign_recipients
		WHERE campaign_id = ? AND status = 'paused' AND `+campaignBounceFrozenClause, campaignID)
	if err != nil {
		return
	}
	type frozenRow struct {
		id      int64
		channel string
		target  string
	}
	list := []frozenRow{}
	for rows.Next() {
		var r frozenRow
		if rows.Scan(&r.id, &r.channel, &r.target) == nil {
			list = append(list, r)
		}
	}
	rows.Close()
	requeued, skipped := 0, 0
	for _, r := range list {
		// Exact counters: RowsAffected, never err == nil (a concurrent update
		// can leave a statement succeeding on zero rows).
		if sup.blocked(r.channel, r.target) {
			res, err := s.db.ExecContext(ctx, `
				UPDATE campaign_recipients SET status = 'skipped', error = 'baja comprobada al retomar automatico'
				WHERE id = ? AND status = 'paused'`, r.id)
			if err == nil {
				if n, _ := res.RowsAffected(); n > 0 {
					skipped++
				}
			}
			continue
		}
		res, err := s.db.ExecContext(ctx, `
			UPDATE campaign_recipients SET status = 'pending', error = NULL
			WHERE id = ? AND status = 'paused'`, r.id)
		if err == nil {
			if n, _ := res.RowsAffected(); n > 0 {
				requeued++
			}
		}
	}
	if requeued == 0 && skipped == 0 {
		return
	}
	// The prevention window elapsed in this same process too: re-baseline the
	// breaker so the next poll only trips on notices newer than now.
	campaignBounceFor(campaignID).reset()
	slog.Default().Info("campaign.auto_retake.requeued", "coord_id", coordID,
		"campaign_id", campaignID, "restaurant_id", restaurantID,
		"requeued", requeued, "skipped", skipped, "freeze_hours", campaignBounceFreezeHours())
}
