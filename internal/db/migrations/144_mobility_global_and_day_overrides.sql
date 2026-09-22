-- Coordination id: mobility_day_override_v1
--
-- "Problemas de movilidad" question for ALL days, not just special dates:
--
--   restaurant_reservation_defaults.mobility_enabled  -> global toggle
--   mobility_day_override.mobility_enabled            -> per-day override
--                                                       (NULL = inherit global)
--
-- Every existing special date freezes its current explicit choice as a day
-- override, preserving today's behaviour. All idempotent.

-- --- restaurant_reservation_defaults.mobility_enabled (guarded ALTER) ---
SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_reservation_defaults'
    AND COLUMN_NAME = 'mobility_enabled'
);
SET @ddl := IF(@col_exists = 0,
  'ALTER TABLE `restaurant_reservation_defaults` ADD COLUMN `mobility_enabled` TINYINT(1) NOT NULL DEFAULT 0',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- --- per-day override (mirrors location_booking_override, migration 105) ---
CREATE TABLE IF NOT EXISTS `mobility_day_override` (
  `id` INT NOT NULL AUTO_INCREMENT,
  `restaurant_id` INT NOT NULL,
  `reservationDate` DATE NOT NULL,
  `mobility_enabled` TINYINT(1) NULL DEFAULT NULL,
  `created_at` TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  `updated_at` TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uniq_mobility_day_override_rest_date` (`restaurant_id`, `reservationDate`),
  KEY `idx_mobility_day_override_restaurant` (`restaurant_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Seed: freeze each special date's current explicit choice as a day override.
INSERT INTO mobility_day_override (restaurant_id, reservationDate, mobility_enabled)
SELECT restaurant_id, date, mobility_enabled
FROM special_dates
ON DUPLICATE KEY UPDATE mobility_enabled = VALUES(mobility_enabled);
