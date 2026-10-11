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
		{`INSERT IGNORE INTO restaurant_branding (restaurant_id, brand_name) VALUES (?, ?)`, []any{restaurantID, strings.TrimSpace(name)}},
		{`INSERT IGNORE INTO restaurant_reservation_defaults (restaurant_id) VALUES (?)`, []any{restaurantID}},
		{`INSERT IGNORE INTO restaurant_info (restaurant_id) VALUES (?)`, []any{restaurantID}},
		{`INSERT IGNORE INTO restaurant_bot_ai_config (restaurant_id) VALUES (?)`, []any{restaurantID}},
		{`INSERT IGNORE INTO restaurant_page_visibility (restaurant_id) VALUES (?)`, []any{restaurantID}},
	}
	for _, seed := range seeds {
		if _, err := s.db.ExecContext(ctx, seed.query, seed.args...); err != nil {
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
		`INSERT IGNORE INTO bo_user_restaurants (user_id, restaurant_id, role) VALUES (?, ?, 'owner')`,
		userID, restaurantID)
	return err
}
