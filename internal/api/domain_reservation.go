package api

import (
	"context"
	"database/sql"
	"errors"
)

// Coordination id: domain_reservation_v1
//
// A restaurant claims a domain row BEFORE any external purchase or Stripe
// checkout. restaurant_domains.domain is UNIQUE: the claim is one INSERT whose
// conflict branch writes nothing (MySQL: no-op ODKU; PG: ON CONFLICT DO
// NOTHING), so the first committed inserter owns the row on both engines and
// no missing-row lock is relied on. The owner is then re-read; a foreign
// owner is errDomainUnavailable. Registration / checkout progress uses
// conditional UPDATEs of the OWN row from an expected state, so only one
// request can move a state forward (same-owner concurrent retries lose the
// transition and do not purchase or open a second checkout).
//
// registration_status (varchar(32)): pending (claimed, nothing bought) ->
// registering (purchase in flight) -> registered | register_uncertain
// (provider call failed or answered ambiguously: NOT retried automatically,
// reconcile manually with the registrar). Existing values kept: paid (Stripe
// webhook), provisioned (instatic subdomain), active (DNS provisioned).
// A claimed row is never deleted here: an external outcome may be uncertain.

var errDomainUnavailable = errors.New("domain owned by another restaurant")

// errDomainNotCanonical: padding_domain_identity_v1. restaurant_domains.domain
// is a GLOBAL unique key; MySQL utf8mb4_unicode_ci (PAD SPACE) treats
// "x.com " as "x.com", PG old-nondeterministic-collation does not, so a non-canonical claim could
// create a second owner row on PG. Every claim must use the existing
// normalizePremiumDomain identity (lower, trim, scheme/path strip, format
// check); anything else is refused, never normalized here silently.
var errDomainNotCanonical = errors.New("domain is not in canonical form")

type ownedDomainRow struct {
	RegistrationStatus  string
	StripePaymentStatus string
	CheckoutSessionID   sql.NullString
	RegistrationCost    sql.NullString // numeric(10,2) text
}

// claimOwnedDomain claims (or confirms) the domain row for restaurantID.
func (s *Server) claimOwnedDomain(ctx context.Context, restaurantID int, domain string) (ownedDomainRow, error) {
	var row ownedDomainRow
	if domain == "" || normalizePremiumDomain(domain) != domain {
		return row, errDomainNotCanonical
	}
	claimArgs := []any{restaurantID, domain}
	var err error
	if _, err := s.db.ExecContext(ctx, nativeSQL(`
		INSERT INTO restaurant_domains (restaurant_id, domain, is_primary, registration_status)
		VALUES (?, ?, 0, 'pending')
		ON DUPLICATE KEY UPDATE registration_status = registration_status
	`, `
		INSERT INTO restaurant_domains (restaurant_id, domain, is_primary, registration_status)
		VALUES (?, ?, 0, 'pending')
		ON CONFLICT (app.norm_ascii(domain)) DO NOTHING
	`), claimArgs...); err != nil {
		return row, err
	}
	var owner int
	err = s.db.QueryRowContext(ctx, nativeSQL(
		`SELECT restaurant_id, registration_status, stripe_payment_status, stripe_checkout_session_id, registration_cost FROM restaurant_domains WHERE domain = ?`,
		`SELECT restaurant_id, registration_status, CAST(stripe_payment_status AS text), stripe_checkout_session_id, CAST(registration_cost AS text) FROM restaurant_domains WHERE app.norm_ascii(domain) = app.norm_ascii(?)`),
		domain).Scan(&owner, &row.RegistrationStatus, &row.StripePaymentStatus, &row.CheckoutSessionID, &row.RegistrationCost)
	if err != nil {
		return row, err
	}
	if owner != restaurantID {
		return row, errDomainUnavailable
	}
	return row, nil
}
