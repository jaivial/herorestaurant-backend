package api

import (
	"context"
	"strings"
)

// provisionRestaurantDefaults seeds the per-restaurant satellite rows a fresh
// tenant needs so a newly created restaurant works end-to-end with no manual
// setup (branding, reservation defaults, info, bot config, public page
// visibility). Idempotent: every seed is an INSERT IGNORE keyed by
// restaurant_id, so re-running it never clobbers tenant data.
func (s *Server) provisionRestaurantDefaults(ctx context.Context, restaurantID int, name string) error {
	if s.db == nil || restaurantID <= 0 {
		return nil
	}
	seeds := []struct {
		query string
		args  []any
	}{
		// repair-ignore3: duplicate-only no-op on both dialects (MySQL ODKU
		// no-op, PG ON CONFLICT on the table's actual key). The source IGNORE
		// (and the PG EXISTS guard) also skipped an FK failure silently, so a
		// tenant could be reported provisioned without its satellite rows; an
		// FK error now fails provisioning. brand_name comes from
		// restaurants.name varchar(255) = brand_name varchar(255): no cut.
		{nativeSQL(`INSERT INTO restaurant_branding (restaurant_id, brand_name) VALUES (?, ?) ON DUPLICATE KEY UPDATE restaurant_id = restaurant_id`,
			`INSERT INTO restaurant_branding (restaurant_id, brand_name) VALUES (CAST(? AS integer), CAST(? AS text)) ON CONFLICT (restaurant_id) DO NOTHING`), []any{restaurantID, strings.TrimSpace(name)}},
		{nativeSQL(`INSERT INTO restaurant_reservation_defaults (restaurant_id) VALUES (?) ON DUPLICATE KEY UPDATE restaurant_id = restaurant_id`,
			`INSERT INTO restaurant_reservation_defaults (restaurant_id) VALUES (?) ON CONFLICT (restaurant_id) DO NOTHING`), []any{restaurantID}}, // no FK
		{nativeSQL(`INSERT INTO restaurant_info (restaurant_id) VALUES (?) ON DUPLICATE KEY UPDATE restaurant_id = restaurant_id`,
			`INSERT INTO restaurant_info (restaurant_id) VALUES (?) ON CONFLICT (restaurant_id) DO NOTHING`), []any{restaurantID}},
		{nativeSQL(`INSERT INTO restaurant_bot_ai_config (restaurant_id) VALUES (?) ON DUPLICATE KEY UPDATE restaurant_id = restaurant_id`,
			`INSERT INTO restaurant_bot_ai_config (restaurant_id) VALUES (?) ON CONFLICT (restaurant_id) DO NOTHING`), []any{restaurantID}},
		{nativeSQL(`INSERT INTO restaurant_page_visibility (restaurant_id) VALUES (?) ON DUPLICATE KEY UPDATE restaurant_id = restaurant_id`,
			`INSERT INTO restaurant_page_visibility (restaurant_id) VALUES (?) ON CONFLICT (restaurant_id) DO NOTHING`), []any{restaurantID}}, // no FK; uniq restaurant_id
	}
	for _, seed := range seeds {
		if _, err := s.db.ExecContext(ctx, selectedSQL(seed.query), seed.args...); err != nil {
			return err
		}
	}
	return nil
}

// provisionRestaurantOwner grants the creating user owner membership on the
// new restaurant so it is immediately visible and administrable without a
// separate assignment step.
func (s *Server) provisionRestaurantOwner(ctx context.Context, restaurantID, userID int) error {
	if s.db == nil || restaurantID <= 0 || userID <= 0 {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		// Duplicate (user_id, restaurant_id) = existing membership kept (its
		// role is not changed); an FK failure now errors (was skipped).
		nativeSQL(`INSERT INTO bo_user_restaurants (user_id, restaurant_id, role) VALUES (?, ?, 'owner') ON DUPLICATE KEY UPDATE user_id = user_id`, `INSERT INTO bo_user_restaurants (user_id, restaurant_id, role) VALUES (?, ?, 'owner') ON CONFLICT (user_id, restaurant_id) DO NOTHING`),
		userID, restaurantID)
	return err
}
