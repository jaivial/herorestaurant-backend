package api

import (
	"context"
	"database/sql"
	"errors"
)

// Coordination id: domain_owner_isolation_v1
//
// restaurant_domains.domain is UNIQUE and is the host -> tenant routing key.
// Invariant enforced by every writer (premium register, registrar register,
// Stripe domain webhook, instatic subdomain provisioning): an INSERT that hits
// an existing domain row only updates it when the row already belongs to the
// inserting restaurant (MySQL: ON DUPLICATE KEY UPDATE col = IF(restaurant_id =
// VALUES(restaurant_id), new, col); PG: ON CONFLICT (domain) DO UPDATE ...
// WHERE restaurant_domains.restaurant_id = EXCLUDED.restaurant_id). No writer
// assigns restaurant_id on conflict, so the owner of a row is whoever inserted
// it first and never changes. After the upsert the caller re-reads the owner:
// a foreign owner means "domain unavailable" and nothing of the foreign row
// was modified (the unique index makes the concurrent first insert win
// exactly once on both engines; the losing INSERT takes the conflict path).
// This deliberately departs from the source MySQL ODKU, which reassigned or
// rewrote another tenant's row (reviewed tenant-isolation bug fix).

type domainRowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// restaurantDomainOwner returns the owning restaurant of a domain row.
// lock=true reads it FOR UPDATE (use inside the writing transaction).
func restaurantDomainOwner(ctx context.Context, q domainRowQuerier, domain string, lock bool) (int, bool, error) {
	query := nativeSQL(`SELECT restaurant_id FROM restaurant_domains WHERE domain = ?`, `SELECT restaurant_id FROM restaurant_domains WHERE app.norm_ascii(domain) = app.norm_ascii(?)`)
	if lock {
		query = nativeSQL(`SELECT restaurant_id FROM restaurant_domains WHERE domain = ? FOR UPDATE`, `SELECT restaurant_id FROM restaurant_domains WHERE app.norm_ascii(domain) = app.norm_ascii(?) FOR UPDATE`)
	}
	var owner int
	err := q.QueryRowContext(ctx, selectedSQL(query), domain).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return owner, true, nil
}

// domainOwnedByOther reports a row of the domain owned by another restaurant.
func domainOwnedByOther(ctx context.Context, q domainRowQuerier, domain string, restaurantID int, lock bool) (bool, error) {
	owner, found, err := restaurantDomainOwner(ctx, q, domain, lock)
	if err != nil {
		return false, err
	}
	return found && owner != restaurantID, nil
}
