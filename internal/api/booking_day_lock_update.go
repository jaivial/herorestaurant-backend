package api

import (
	"context"
	"database/sql"
	"errors"
)

// lockRestaurantCapacityConfig takes the restaurant row FOR UPDATE (both
// dialects) for writers of the restaurant-wide default limit: it excludes
// every day writer (MySQL: same row; PG: they hold it FOR SHARE first).
func lockRestaurantCapacityConfig(ctx context.Context, tx *sql.Tx, restaurantID int) error {
	var id int
	return tx.QueryRowContext(ctx, portableSQL(`SELECT id FROM restaurants WHERE id = ? FOR UPDATE`), restaurantID).Scan(&id)
}

var errBookingMovedConcurrently = errors.New("la reserva cambió mientras se guardaba; vuelve a intentarlo")

// execBookingUpdateDayLocked runs the caller's booking UPDATE (update(ex)) in a tx
// holding the day lock of the booking's current day and of newDate
// (ascending, restaurant first). The current day is read, then re-read under
// the lock; a concurrent move fails closed (errBookingMovedConcurrently,
// nothing written). An absent booking keeps the source behaviour: the plain
// UPDATE that matches 0 rows. booking_day_lock_v1.
func (s *Server) execBookingUpdateDayLocked(ctx context.Context, restaurantID int, bookingID int64, newDate string, update func(ex execQueryer) (sql.Result, error)) (sql.Result, error) {
	var oldDate string
	err := s.db.QueryRowContext(ctx, nativeSQL(`SELECT DATE_FORMAT(reservation_date, '%Y-%m-%d') FROM bookings WHERE id = ? AND restaurant_id = ?`, `SELECT to_char(reservation_date, 'YYYY-MM-DD') FROM bookings WHERE id = ? AND restaurant_id = ?`), bookingID, restaurantID).Scan(&oldDate)
	if errors.Is(err, sql.ErrNoRows) {
		return update(s.db)
	}
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockBookingDays(ctx, tx, restaurantID, oldDate, newDate); err != nil {
		return nil, err
	}
	var lockedDate string
	if err := tx.QueryRowContext(ctx, nativeSQL(`SELECT DATE_FORMAT(reservation_date, '%Y-%m-%d') FROM bookings WHERE id = ? AND restaurant_id = ?`, `SELECT to_char(reservation_date, 'YYYY-MM-DD') FROM bookings WHERE id = ? AND restaurant_id = ?`), bookingID, restaurantID).Scan(&lockedDate); err != nil {
		return nil, err
	}
	if lockedDate != oldDate {
		return nil, errBookingMovedConcurrently
	}
	res, err := update(tx)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return res, nil
}
