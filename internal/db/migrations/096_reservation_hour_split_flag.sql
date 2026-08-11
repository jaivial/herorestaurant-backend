-- By-hour client split feature.
--
-- 1) Restaurant defaults: enable/disable the per-hour client split AND store a
--    default percentages template applied to new dates.
--    `restaurant_reservation_defaults` already has a clean single-row-per-restaurant
--    upsert path (see backoffice_config.go upsertReservationDefaults), so the flag
--    and template live here safely.
--
-- 2) Per-date override: a dedicated table is used (NOT a column on reservation_manager)
--    because reservation_manager lacks a unique key and the daily-limit writer uses a
--    DELETE+INSERT pattern that would wipe any added column.
--
-- The canonical per-hour percentages remain in the existing `hours_percentage` table;
-- `hour_configuration.hourData.Percentage` is deprecated as a percentage source.
-- All idempotent.

SET @col1 = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_reservation_defaults' AND COLUMN_NAME = 'hour_split_enabled');
SET @ddl1 := IF(@col1 = 0, 'ALTER TABLE `restaurant_reservation_defaults` ADD COLUMN `hour_split_enabled` TINYINT(1) NOT NULL DEFAULT 1', 'SELECT 1');
PREPARE stmt1 FROM @ddl1; EXECUTE stmt1; DEALLOCATE PREPARE stmt1;

SET @col2 = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_reservation_defaults' AND COLUMN_NAME = 'default_hour_percentages_json');
SET @ddl2 := IF(@col2 = 0, 'ALTER TABLE `restaurant_reservation_defaults` ADD COLUMN `default_hour_percentages_json` LONGTEXT NULL', 'SELECT 1');
PREPARE stmt2 FROM @ddl2; EXECUTE stmt2; DEALLOCATE PREPARE stmt2;

CREATE TABLE IF NOT EXISTS `hour_split_override` (
  `id` INT NOT NULL AUTO_INCREMENT,
  `restaurant_id` INT NOT NULL,
  `reservationDate` DATE NOT NULL,
  `enabled` TINYINT(1) NOT NULL DEFAULT 1,
  `created_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uniq_hour_split_override_rest_date` (`restaurant_id`, `reservationDate`),
  KEY `idx_hour_split_override_restaurant` (`restaurant_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
