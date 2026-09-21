-- Coordination id: mobility_issues_v1
--
-- "Problemas de movilidad" for special dates. The operator enables the
-- question per special date; when enabled, the public wizard and the
-- backoffice booking editor ask whether any guests have mobility issues and
-- how many of the party they are, so the floor can seat them away from a
-- first floor with no lift.
--
--   special_dates.mobility_enabled  -> the per-date toggle (settings)
--   bookings.has_mobility_issues    -> answer on the booking
--   bookings.mobility_people        -> how many of party_size are affected

-- --- special_dates.mobility_enabled ---
SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'special_dates'
    AND COLUMN_NAME = 'mobility_enabled'
);
SET @ddl := IF(@col_exists = 0,
  'ALTER TABLE `special_dates` ADD COLUMN `mobility_enabled` TINYINT(1) NOT NULL DEFAULT 0 AFTER `max_per_table`',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- --- bookings.has_mobility_issues / bookings.mobility_people ---
-- Guarded on the table existing, matching migration 141.
SET @bookings_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bookings'
);

SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bookings'
    AND COLUMN_NAME = 'has_mobility_issues'
);
SET @ddl := IF(@bookings_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `bookings` ADD COLUMN `has_mobility_issues` TINYINT(1) NOT NULL DEFAULT 0',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bookings'
    AND COLUMN_NAME = 'mobility_people'
);
SET @ddl := IF(@bookings_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `bookings` ADD COLUMN `mobility_people` INT NOT NULL DEFAULT 0 AFTER `has_mobility_issues`',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;
