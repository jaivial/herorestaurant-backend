-- Coordination id: guest_count_flags_v1
--
-- Per-special-date toggles for the guest-count questions in the public
-- booking wizard: "preguntar nº adultos", "preguntar nº tronas" and
-- "preguntar nº carros de bebé". All default to 1 so existing dates keep
-- asking (current behaviour unchanged).
--
-- When a counter is not asked, the booking stores NULL in the matching
-- column (children / highChairs / babyStrollers) instead of 0, so the
-- confirmation and reminder messages can tell "not asked" apart from
-- "asked and answered 0".

-- --- special_dates guest-count toggles ---
SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'special_dates'
    AND COLUMN_NAME = 'ask_adults'
);
SET @ddl := IF(@col_exists = 0,
  'ALTER TABLE `special_dates`
     ADD COLUMN `ask_adults` TINYINT(1) NOT NULL DEFAULT 1 AFTER `allow_customer_modification`,
     ADD COLUMN `ask_high_chairs` TINYINT(1) NOT NULL DEFAULT 1 AFTER `ask_adults`,
     ADD COLUMN `ask_baby_strollers` TINYINT(1) NOT NULL DEFAULT 1 AFTER `ask_high_chairs`',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- --- bookings counters become nullable ---
SET @bookings_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bookings'
);

SET @col_nullable := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bookings'
    AND COLUMN_NAME = 'children' AND IS_NULLABLE = 'YES'
);
SET @ddl := IF(@bookings_exists = 1 AND @col_nullable = 0,
  'ALTER TABLE `bookings` MODIFY COLUMN `children` INT NULL',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_nullable := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bookings'
    AND COLUMN_NAME = 'highChairs' AND IS_NULLABLE = 'YES'
);
SET @ddl := IF(@bookings_exists = 1 AND @col_nullable = 0,
  'ALTER TABLE `bookings` MODIFY COLUMN `highChairs` INT NULL',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_nullable := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bookings'
    AND COLUMN_NAME = 'babyStrollers' AND IS_NULLABLE = 'YES'
);
SET @ddl := IF(@bookings_exists = 1 AND @col_nullable = 0,
  'ALTER TABLE `bookings` MODIFY COLUMN `babyStrollers` INT NULL',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;
