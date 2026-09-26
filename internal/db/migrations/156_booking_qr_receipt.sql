-- Special-date booking QR + payment receipt CDN urls.
-- Coordination id: special_booking_qr_v1
--   bookings.qr_url      -> BunnyCDN PNG of the QR that opens the backoffice
--                           booking page (/app/reservas/especial/reserva).
--   bookings.receipt_url -> BunnyCDN PDF of the Stripe adelanto receipt.
SET @col_exists := (
    SELECT COUNT(*) FROM information_schema.COLUMNS
    WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bookings' AND COLUMN_NAME = 'qr_url'
);
SET @ddl := IF(@col_exists = 0,
  'ALTER TABLE bookings ADD COLUMN qr_url VARCHAR(1024) NULL, ADD COLUMN receipt_url VARCHAR(1024) NULL',
  'SELECT 1');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
