-- Customer self-service modification gate for special dates.
--
-- When a guest's contact details already match a booking on the SAME day, the
-- public duplicate guard offers "modify your booking instead of creating a new
-- one". For active special dates that escape hatch must be opted into per
-- concrete date, because the pre-reserva menus / adelanto make edits risky.
--
-- Coordination id: reservation_self_modification_v1
--   settings (special_dates.allow_customer_modification)
--     -> guard  (POST /reservations/contact-lookup)
--     -> route  (/reservas/modificar)
--     -> write  (POST /reservations/modify)

SET @col_exists := (
    SELECT COUNT(*)
    FROM information_schema.COLUMNS
    WHERE TABLE_SCHEMA = DATABASE()
      AND TABLE_NAME = 'special_dates'
      AND COLUMN_NAME = 'allow_customer_modification'
);
SET @ddl := IF(
    @col_exists = 0,
    'ALTER TABLE special_dates ADD COLUMN allow_customer_modification TINYINT(1) NOT NULL DEFAULT 0 AFTER mobility_enabled',
    'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
