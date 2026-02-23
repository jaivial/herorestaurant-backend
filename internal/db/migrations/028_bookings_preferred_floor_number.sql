-- Add preferred floor selection for reservations.
SET @table_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bookings'
);
SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bookings' AND COLUMN_NAME = 'preferred_floor_number'
);
SET @ddl := IF(
  @table_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `bookings` ADD COLUMN `preferred_floor_number` INT NULL AFTER `table_number`',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
