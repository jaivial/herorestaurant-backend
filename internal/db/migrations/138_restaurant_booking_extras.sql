-- Per-restaurant booking extras catalog + per-booking selected extras.
--
-- Extras are the non-group-menu add-ons a booking can carry (e.g. "Café
-- incluido", "Bebida ilimitada"). They mirror restaurant_beverage_options:
-- a restaurant-scoped catalog with custom entries created from the extras
-- modal, plus a snapshot stored on each booking so confirmations, reminders
-- and the WhatsApp bot can render them without joins.
--
-- Coordination id: booking_extras_v1
-- (extras modal -> restaurant_booking_extras -> bookings.extras_json -> bot/notifications)

CREATE TABLE IF NOT EXISTS `restaurant_booking_extras` (
  `id` BIGINT NOT NULL AUTO_INCREMENT,
  `restaurant_id` INT NOT NULL,
  `slug` VARCHAR(140) NOT NULL,
  `name` VARCHAR(255) NOT NULL,
  `is_custom` TINYINT(1) NOT NULL DEFAULT 0,
  `active` TINYINT(1) NOT NULL DEFAULT 1,
  `created_at` TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_restaurant_booking_extras_slug` (`restaurant_id`, `slug`),
  KEY `idx_restaurant_booking_extras_restaurant` (`restaurant_id`, `active`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

SET @restaurants_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurants'
);
SET @fk_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_booking_extras'
    AND CONSTRAINT_NAME = 'fk_restaurant_booking_extras_restaurant'
);
SET @ddl := IF(@restaurants_exists = 1 AND @fk_exists = 0,
  'ALTER TABLE `restaurant_booking_extras` ADD CONSTRAINT `fk_restaurant_booking_extras_restaurant` FOREIGN KEY (`restaurant_id`) REFERENCES `restaurants`(`id`) ON DELETE CASCADE',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- Seed the default extras for every restaurant. Derived-table alias keeps the
-- ON DUPLICATE KEY UPDATE portable across MySQL 8.0.x (see migration 117).
SET @restaurants_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurants'
);
SET @seed := IF(@restaurants_exists = 1,
  'INSERT INTO restaurant_booking_extras (restaurant_id, slug, name, is_custom, active)
   SELECT restaurant_id, slug, name, 0, 1 FROM (
     SELECT r.id AS restaurant_id, defaults.slug AS slug, defaults.name AS name
     FROM restaurants r
     CROSS JOIN (
       SELECT ''cafe-incluido'' AS slug, ''Café incluido'' AS name
       UNION ALL SELECT ''bebida-ilimitada'', ''Bebida ilimitada''
       UNION ALL SELECT ''botella-cava'', ''Botella de cava''
       UNION ALL SELECT ''tarta'', ''Tarta''
     ) defaults
   ) AS new
   ON DUPLICATE KEY UPDATE name = new.name, active = 1',
  'SELECT 1');
PREPARE stmt FROM @seed; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- Snapshot of the selected extras on the booking (JSON array of {id, slug, name}).
SET @bookings_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bookings'
);
SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bookings' AND COLUMN_NAME = 'extras_json'
);
SET @ddl := IF(@bookings_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `bookings` ADD COLUMN `extras_json` LONGTEXT NULL',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;
