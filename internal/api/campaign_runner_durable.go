package api

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"time"
)

// Campaign runner durability.
//
// The send loop is an in-process goroutine, so a container recreate used to
// kill it silently and leave the campaign in 'sending' forever. What this file
// adds, without ever changing who gets contacted:
//
//   - graceful shutdown: on SIGTERM the runner finishes the delivery it is in
//     and stops before the next one (and wakes from its pacing sleep), leaving
//     the pending rows untouched and the campaign in 'sending';
//   - startup detection: campaigns in 'sending' with pending rows and no live
//     runner are logged as interrupted;
//   - OPT-IN automatic resume (CAMPAIGN_AUTO_RESUME=1, off by default): it
//     resumes only those campaigns and only their rows already pending. It does
//     not recompute the audience, does not insert rows, and never touches
//     'paused', 'sent', 'failed' or 'skipped' rows. Every delivery still goes
//     through the do-not-contact re-check in runCampaignSend.

var (
	campaignShutdownOnce sync.Once
	campaignShutdownCh   = make(chan struct{})
	campaignRunnersWG    sync.WaitGroup
)

// BeginCampaignShutdown tells every campaign runner to stop after the delivery
// in flight. Safe to call more than once.
func BeginCampaignShutdown() {
	campaignShutdownOnce.Do(func() { close(campaignShutdownCh) })
}

func campaignShuttingDown() bool {
	select {
	case <-campaignShutdownCh:
		return true
	default:
		return false
	}
}

// campaignPause sleeps d and returns false when shutdown started meanwhile.
func campaignPause(d time.Duration) bool {
	if d <= 0 {
		return !campaignShuttingDown()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-campaignShutdownCh:
		return false
	}
}

// WaitCampaignRunners blocks until every runner stopped or the timeout passed.
func WaitCampaignRunners(timeout time.Duration) bool {
	done := make(chan struct{})
	go func() { campaignRunnersWG.Wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// RecoverInterruptedCampaigns logs campaigns a restart interrupted and, only
// when CAMPAIGN_AUTO_RESUME=1, resumes their already-pending rows.
func (s *Server) RecoverInterruptedCampaigns(ctx context.Context) {
	rows, err := s.db.QueryContext(ctx, `SELECT c.id, c.restaurant_id,
			(SELECT COUNT(*) FROM campaign_recipients r WHERE r.campaign_id = c.id AND r.status = 'pending')
		FROM campaigns c
		WHERE c.status = 'sending'
		  AND EXISTS (SELECT 1 FROM campaign_recipients r WHERE r.campaign_id = c.id AND r.status = 'pending')`)
	if err != nil {
		slog.Default().Warn("campaign.recover.query_failed", "err", err.Error())
		return
	}
	type interrupted struct {
		id      int64
		rid     int
		pending int
	}
	var list []interrupted
	for rows.Next() {
		var it interrupted
		if rows.Scan(&it.id, &it.rid, &it.pending) == nil {
			list = append(list, it)
		}
	}
	rows.Close()
	auto := os.Getenv("CAMPAIGN_AUTO_RESUME") == "1"
	for _, it := range list {
		if campaignRunners.running(it.id) {
			continue
		}
		slog.Default().Warn("campaign.recover.interrupted", "campaign_id", it.id, "restaurant_id", it.rid, "pending", it.pending, "auto_resume", auto)
		if !auto || !campaignRunners.start(it.id) {
			continue
		}
		campaignRunnersWG.Add(1)
		go func(it interrupted) {
			defer campaignRunnersWG.Done()
			defer campaignRunners.stop(it.id)
			s.runCampaignSend(context.WithoutCancel(ctx), it.rid, it.id)
		}(it)
	}
}
