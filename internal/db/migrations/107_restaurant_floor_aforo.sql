-- Floor aforo (capacity) + per-date occupancy ledger for salons and floors.
--
-- 1) restaurant_floors.max_aforo: optional max headcount for a floor. NULL =
--    no floor-wide cap. Global value stored on the global (specific_date IS
--    NULL) row; a date-scoped floor carries its own value on its own row.
--
-- 2) restaurant_floor_aforo_overrides: per-date override of max_aforo for a
--    floor, mirroring restaurant_floor_overrides (restaurant_id + date + floor
--    id). Absence of a row = inherit the floor's effective max_aforo.
--
-- 3) reservation_location_occupancy: running ledger of live headcount per
--    (restaurant, date, scope, target). scope='salon' targets restaurant_salons.id,
--    scope='floor' targets restaurant_floors.id. `count` is the sum of party_size
--    of live bookings that selected that location on that date. It is
--    incrementally maintained on booking insert / cancel / modify, and seeded
--    here from existing live bookings (backfill).
--
-- All guarded / idempotent (mirrors 105/106 patterns).

SET @col1 = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_floors' AND COLUMN_NAME = 'max_aforo');
SET @ddl1 := IF(@col1 = 0, 'ALTER TABLE `restaurant_floors` ADD COLUMN `max_aforo` INT NULL DEFAULT NULL AFTER `scope_key`', 'SELECT 1');
PREPARE stmt1 FROM @ddl1; EXECUTE stmt1; DEALLOCATE PREPARE stmt1;

CREATE TABLE IF NOT EXISTS `restaurant_floor_aforo_overrides` (
  `restaurant_id` INT NOT NULL,
  `date` DATE NOT NULL,
  `floor_id` INT NOT NULL,
  `max_aforo` INT NULL DEFAULT NULL,
  `created_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`restaurant_id`, `date`, `floor_id`),
  KEY `idx_restaurant_floor_aforo_overrides_rest_date` (`restaurant_id`, `date`),
  KEY `idx_restaurant_floor_aforo_overrides_floor` (`floor_id`),
  CONSTRAINT `fk_restaurant_floor_aforo_overrides_floor` FOREIGN KEY (`floor_id`) REFERENCES `restaurant_floors`(`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `reservation_location_occupancy` (
  `restaurant_id` INT NOT NULL,
  `date` DATE NOT NULL,
  `scope` ENUM('salon','floor') NOT NULL,
  `target_id` INT NOT NULL,
  `count` INT NOT NULL DEFAULT 0,
  `created_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`restaurant_id`, `date`, `scope`, `target_id`),
  KEY `idx_reservation_location_occupancy_scope_target_date` (`scope`, `target_id`, `date`),
  KEY `idx_reservation_location_occupancy_rest_date_scope` (`restaurant_id`, `date`, `scope`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Backfill salons: sum party_size of live bookings that chose a salon, keyed
-- by the salon itself (only counts when preferred_salon_id is set).
INSERT INTO `reservation_location_occupancy` (`restaurant_id`, `date`, `scope`, `target_id`, `count`)
SELECT b.restaurant_id, b.reservation_date, 'salon', b.preferred_salon_id, SUM(b.party_size)
FROM bookings b
WHERE b.preferred_salon_id IS NOT NULL
  AND COALESCE(b.status, '') <> 'cancelled'
GROUP BY b.restaurant_id, b.reservation_date, b.preferred_salon_id
ON DUPLICATE KEY UPDATE `count` = VALUES(`count`);

-- Backfill floors: sum party_size of live bookings that chose a floor, mapped
-- to the floor id via the global floor row (specific_date IS NULL). A booking
-- picks a floor_number; the effective floor row on a date may be date-scoped,
-- but for the ledger we keep a single canonical target per floor_number (the
-- global row id) so backfill + runtime stay consistent.
INSERT INTO `reservation_location_occupancy` (`restaurant_id`, `date`, `scope`, `target_id`, `count`)
SELECT b.restaurant_id, b.reservation_date, 'floor', f.id, SUM(b.party_size)
FROM bookings b
JOIN restaurant_floors f
  ON f.restaurant_id = b.restaurant_id
 AND f.floor_number = b.preferred_floor_number
 AND f.specific_date IS NULL
WHERE b.preferred_floor_number IS NOT NULL
  AND COALESCE(b.status, '') <> 'cancelled'
GROUP BY b.restaurant_id, b.reservation_date, f.id
ON DUPLICATE KEY UPDATE `count` = VALUES(`count`);
