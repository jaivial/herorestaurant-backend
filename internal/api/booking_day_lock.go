package api

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"preactvillacarmen/internal/sqldb"
)

// Coordination id: booking_day_lock_v1
//
// lockBookingDays serialises booking writers of one restaurant day inside the
// CALLER'S transaction; the lock is released by that transaction's commit or
// rollback (never a pooled session lock):
//
//   - PostgreSQL: pg_advisory_xact_lock(hashtextextended('booking_day:<rid>:<date>', 0))
//     per distinct date, taken in ascending date order (deadlock-free between
//     multi-day writers such as a date move).
//   - MySQL: SELECT ... FOR UPDATE on the restaurant's EXISTING restaurants row
//     (no missing-row lock): a per-restaurant superset of the day key, taken
//     once. MySQL has no transaction-scoped named lock.
//
// Granularity follows the actual capacity rule: dailyLimit
// (reservation_manager per restaurant+reservationDate, else the restaurant
// default) vs SUM(bookings.party_size) for that restaurant+date.
// Callers must take it BEFORE any consistent read of the day in the tx (MySQL
// RR snapshot starts at the first non-locking read).
func lockBookingDays(ctx context.Context, tx *sql.Tx, restaurantID int, dates ...string) error {
	if sqlDialect != sqldb.Postgres {
		var id int
		return tx.QueryRowContext(ctx, nativeSQL(`SELECT id FROM restaurants WHERE id = ? FOR UPDATE`, `SELECT pg_advisory_xact_lock(hashtextextended(CAST(? AS text), 0))`), restaurantID).Scan(&id)
	}
	// Order: restaurant (shared) THEN days. The capacity-config writer
	// (default limit) takes the same row FOR UPDATE, so a default change and
	// any day writer are serialised without lock-order inversion.
	var rid int
	if err := tx.QueryRowContext(ctx, pgOnlySQL(`SELECT id FROM restaurants WHERE id = ? FOR SHARE`), restaurantID).Scan(&rid); err != nil {
		return err
	}
	seen := map[string]bool{}
	keys := []string{}
	for _, d := range dates {
		d = strings.TrimSpace(d)
		if len(d) > 10 {
			d = d[:10] // "YYYY-MM-DD..." (RFC3339 DATE rendering) -> day
		}
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		keys = append(keys, d)
	}
	sort.Strings(keys)
	for _, d := range keys {
		if _, err := tx.ExecContext(ctx, nativeSQL(`SELECT id FROM restaurants WHERE id = ? FOR UPDATE`, `SELECT pg_advisory_xact_lock(hashtextextended(CAST(? AS text), 0))`), fmt.Sprintf("booking_day:%d:%s", restaurantID, d)); err != nil {
			return err
		}
	}
	return nil
}
