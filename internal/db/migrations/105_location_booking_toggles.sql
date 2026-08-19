-- Location booking toggles ("Permitir reserva de planta" / "Permitir reserva
-- de salón").
--
-- 1) Restaurant defaults: two boolean flags on `restaurant_reservation_defaults`
--    (same home as hour_split_enabled; that table has a clean single-row
--    per-restaurant upsert path — see backoffice_config.go
--    upsertReservationDefaults).
--
-- 2) Per-date override: dedicated table with NULLable flag columns, mirroring
--    the hour_split_override pattern. NULL = inherit the restaurant default;
--    a row exists only when at least one flag is explicitly overridden.
--
-- 3) Front bookings: optional preferred salon, mirroring preferred_floor_number.
--
-- All idempotent.

SET @col1 = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_reservation_defaults' AND COLUMN_NAME = 'allow_floor_reservation');
SET @ddl1 := IF(@col1 = 0, 'ALTER TABLE `restaurant_reservation_defaults` ADD COLUMN `allow_floor_reservation` TINYINT(1) NOT NULL DEFAULT 0', 'SELECT 1');
PREPARE stmt1 FROM @ddl1; EXECUTE stmt1; DEALLOCATE PREPARE stmt1;

SET @col2 = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_reservation_defaults' AND COLUMN_NAME = 'allow_salon_reservation');
SET @ddl2 := IF(@col2 = 0, 'ALTER TABLE `restaurant_reservation_defaults` ADD COLUMN `allow_salon_reservation` TINYINT(1) NOT NULL DEFAULT 0', 'SELECT 1');
PREPARE stmt2 FROM @ddl2; EXECUTE stmt2; DEALLOCATE PREPARE stmt2;

CREATE TABLE IF NOT EXISTS `location_booking_override` (
  `id` INT NOT NULL AUTO_INCREMENT,
  `restaurant_id` INT NOT NULL,
  `reservationDate` DATE NOT NULL,
  `allow_floor_reservation` TINYINT(1) NULL DEFAULT NULL,
  `allow_salon_reservation` TINYINT(1) NULL DEFAULT NULL,
  `created_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uniq_location_booking_override_rest_date` (`restaurant_id`, `reservationDate`),
  KEY `idx_location_booking_override_restaurant` (`restaurant_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

SET @col3 = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bookings' AND COLUMN_NAME = 'preferred_salon_id');
SET @ddl3 := IF(@col3 = 0, 'ALTER TABLE `bookings` ADD COLUMN `preferred_salon_id` INT NULL DEFAULT NULL AFTER `preferred_floor_number`', 'SELECT 1');
PREPARE stmt3 FROM @ddl3; EXECUTE stmt3; DEALLOCATE PREPARE stmt3;
