SET @table_exists := (
  SELECT COUNT(*)
  FROM information_schema.tables
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_tables'
);

SET @col_exists := (
  SELECT COUNT(*)
  FROM information_schema.columns
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_tables' AND COLUMN_NAME = 'status'
);
SET @ddl := IF(
  @table_exists = 1 AND @col_exists = 0,
  "ALTER TABLE `restaurant_tables` ADD COLUMN `status` VARCHAR(32) NOT NULL DEFAULT 'available' AFTER `capacity`",
  "SELECT 1"
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM information_schema.columns
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_tables' AND COLUMN_NAME = 'x_pos'
);
SET @ddl := IF(
  @table_exists = 1 AND @col_exists = 0,
  "ALTER TABLE `restaurant_tables` ADD COLUMN `x_pos` INT NOT NULL DEFAULT 0 AFTER `texture_image_url`",
  "SELECT 1"
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM information_schema.columns
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_tables' AND COLUMN_NAME = 'y_pos'
);
SET @ddl := IF(
  @table_exists = 1 AND @col_exists = 0,
  "ALTER TABLE `restaurant_tables` ADD COLUMN `y_pos` INT NOT NULL DEFAULT 0 AFTER `x_pos`",
  "SELECT 1"
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM information_schema.columns
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_tables' AND COLUMN_NAME = 'metadata_json'
);
SET @ddl := IF(
  @table_exists = 1 AND @col_exists = 0,
  "ALTER TABLE `restaurant_tables` ADD COLUMN `metadata_json` JSON NULL AFTER `is_active`",
  "SELECT 1"
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
