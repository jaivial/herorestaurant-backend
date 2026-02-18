package api

import (
	"context"
	"sync"
)

var (
	ensureModificationHistoryOnce sync.Once
	ensureModificationHistoryErr  error
)

func (s *Server) ensureModificationHistoryTable(ctx context.Context) error {
	ensureModificationHistoryOnce.Do(func() {
		_, ensureModificationHistoryErr = s.db.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS modification_history (
				id INT AUTO_INCREMENT PRIMARY KEY,
				restaurant_id INT NOT NULL DEFAULT 1,
				booking_id INT NOT NULL,
				customer_phone VARCHAR(20),
				field_modified VARCHAR(50) NOT NULL,
				old_value TEXT,
				new_value TEXT,
				modification_date TIMESTAMP DEFAULT CURRENT_TIMESTAMP,

				INDEX idx_booking (booking_id),
				INDEX idx_restaurant_booking (restaurant_id, booking_id),
				INDEX idx_date (modification_date),
				INDEX idx_phone (customer_phone),

				FOREIGN KEY (booking_id) REFERENCES bookings(id) ON DELETE CASCADE
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
		`)
		if ensureModificationHistoryErr != nil {
			return
		}

		// Backward compatibility: if the table existed without multitenant columns, add them best-effort.
		_, _ = s.db.ExecContext(ctx, "ALTER TABLE modification_history ADD COLUMN restaurant_id INT NOT NULL DEFAULT 1")
		_, _ = s.db.ExecContext(ctx, "ALTER TABLE modification_history ADD INDEX idx_restaurant_booking (restaurant_id, booking_id)")

		// Best-effort: table comment (non-critical).
		_, _ = s.db.ExecContext(ctx, "ALTER TABLE modification_history COMMENT = 'Historial de todas las modificaciones de reservas realizadas por clientes'")
	})

	return ensureModificationHistoryErr
}
