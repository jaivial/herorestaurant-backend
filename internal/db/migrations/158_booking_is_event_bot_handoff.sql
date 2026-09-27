-- Coordination id: booking_is_event_v1 - staff flags a booking as an event /
-- big-negotiation booking in /app/reservas/anadir. The WhatsApp bot never
-- negotiates these and always hands over to restaurant management.
SET @col_exists := (
    SELECT COUNT(*) FROM information_schema.COLUMNS
    WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bookings' AND COLUMN_NAME = 'is_event'
);
SET @ddl := IF(@col_exists = 0,
  'ALTER TABLE bookings ADD COLUMN is_event TINYINT(1) NOT NULL DEFAULT 0',
  'SELECT 1');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
