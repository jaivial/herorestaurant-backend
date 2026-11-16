package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"

	"preactvillacarmen/internal/sqldb"
)

// Coordination id: stripe_event_processing_v2
//
// Exactly-one processing of a Stripe event without repurposing any visible
// column (v1 re-stamped created_at as a lease; reverted - created_at stays
// the first receipt the payments list shows):
//
//  1. A per-event session advisory lock (MySQL GET_LOCK / PG
//     pg_try_advisory_lock) on ONE pinned connection (sqldb.TryNamedLockConn;
//     release discards the physical connection via driver.ErrBadConn when the
//     unlock is not confirmed). No DB transaction is open across external
//     calls; the lock is a session lock, not a long tx.
//  2. On that same connection: insert the event row with processed_at NULL
//     (duplicate = no-op, no error, so no PG aborted-tx/savepoint concern:
//     autocommit), then read processed_at. Set -> already done.
//  3. Fencing: the DB effects tx and the processed mark run ON THE PINNED
//     CONNECTION. If that session died (lock lost, another worker may now
//     hold it), those statements fail instead of completing a stale claim.
//
// processed_at NULL after the handler returns is the durable "not done"
// state: retryable (503, Stripe retries) or REVIEW (200 to stop Stripe, row
// left unprocessed for manual reconciliation - never silently terminal).
// Rows the source wrote with processed_at = NOW() before processing keep
// that value (cannot be told apart).
type stripeEventState int

const (
	stripeEventClaimed stripeEventState = iota
	stripeEventDone
	stripeEventBusy
)

type stripeEventClaim struct {
	conn    *sql.Conn
	release func()
	eventID string
}

func stripeEventLockName(eventID string) string {
	sum := sha256.Sum256([]byte(eventID))
	return "se:" + hex.EncodeToString(sum[:])[:40] // <= 64 chars (GET_LOCK limit)
}

func (s *Server) claimStripeEvent(ctx context.Context, eventID, eventType, payload string) (*stripeEventClaim, stripeEventState, error) {
	conn, release, ok, err := sqldb.TryNamedLockConn(ctx, sqlDialect, s.db, stripeEventLockName(eventID))
	if err != nil {
		return nil, stripeEventBusy, err
	}
	if !ok {
		return nil, stripeEventBusy, nil
	}
	if _, err := conn.ExecContext(ctx, nativeSQL(
		`INSERT INTO stripe_events (event_id, type, payload, processed_at) VALUES (?, ?, ?, NULL) ON DUPLICATE KEY UPDATE event_id = event_id`,
		`INSERT INTO stripe_events (event_id, type, payload, processed_at) VALUES (CAST(? AS varchar(128)), CAST(? AS varchar(128)), CAST(? AS jsonb), NULL) ON CONFLICT (app.norm_ascii(event_id)) DO NOTHING`),
		eventID, eventType, payload); err != nil {
		release()
		return nil, stripeEventBusy, err
	}
	var processed sql.NullTime
	if err := conn.QueryRowContext(ctx, nativeSQL(`SELECT processed_at FROM stripe_events WHERE event_id = ?`, `SELECT processed_at FROM stripe_events WHERE app.norm_ascii(event_id) = app.norm_ascii(?)`), eventID).Scan(&processed); err != nil {
		release()
		return nil, stripeEventBusy, err
	}
	if processed.Valid {
		release()
		return nil, stripeEventDone, nil
	}
	return &stripeEventClaim{conn: conn, release: release, eventID: eventID}, stripeEventClaimed, nil
}

// markProcessed runs on the pinned (lock-holding) connection: a lost session
// cannot mark. Exactly one row must change.
func (c *stripeEventClaim) markProcessed(ctx context.Context) error {
	res, err := c.conn.ExecContext(ctx, nativeSQL(
		`UPDATE stripe_events SET processed_at = NOW() WHERE event_id = ? AND processed_at IS NULL`,
		`UPDATE stripe_events SET processed_at = date_trunc('second', statement_timestamp()) WHERE app.norm_ascii(event_id) = app.norm_ascii(?) AND processed_at IS NULL`),
		c.eventID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return errors.New("stripe event already processed: claim was not exclusive")
	}
	return nil
}

func (c *stripeEventClaim) close() { c.release() }

// stripeOutcome is what an event handler concluded.
type stripeOutcome int

const (
	stripeOutcomeDone   stripeOutcome = iota // mark processed, 200
	stripeOutcomeReview                      // 200, left unprocessed for manual review
)
