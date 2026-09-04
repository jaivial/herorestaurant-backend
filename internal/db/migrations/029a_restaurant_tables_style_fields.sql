-- Table style fields for backoffice reservas map editor.
SET @table_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_tables'
);

SET @col_shape := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_tables' AND COLUMN_NAME = 'shape'
);
SET @ddl_shape := IF(
  @table_exists = 1 AND @col_shape = 0,
  "ALTER TABLE `restaurant_tables` ADD COLUMN `shape` VARCHAR(16) NOT NULL DEFAULT 'round'",
  'SELECT 1'
);
PREPARE stmt_shape FROM @ddl_shape;
EXECUTE stmt_shape;
DEALLOCATE PREPARE stmt_shape;

SET @col_fill := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_tables' AND COLUMN_NAME = 'fill_color'
);
SET @ddl_fill := IF(
  @table_exists = 1 AND @col_fill = 0,
  "ALTER TABLE `restaurant_tables` ADD COLUMN `fill_color` VARCHAR(32) NULL",
  'SELECT 1'
);
PREPARE stmt_fill FROM @ddl_fill;
EXECUTE stmt_fill;
DEALLOCATE PREPARE stmt_fill;

SET @col_outline := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_tables' AND COLUMN_NAME = 'outline_color'
);
SET @ddl_outline := IF(
  @table_exists = 1 AND @col_outline = 0,
  "ALTER TABLE `restaurant_tables` ADD COLUMN `outline_color` VARCHAR(32) NULL",
  'SELECT 1'
);
PREPARE stmt_outline FROM @ddl_outline;
EXECUTE stmt_outline;
DEALLOCATE PREPARE stmt_outline;

SET @col_preset := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_tables' AND COLUMN_NAME = 'style_preset'
);
SET @ddl_preset := IF(
  @table_exists = 1 AND @col_preset = 0,
  "ALTER TABLE `restaurant_tables` ADD COLUMN `style_preset` VARCHAR(32) NULL",
  'SELECT 1'
);
PREPARE stmt_preset FROM @ddl_preset;
EXECUTE stmt_preset;
DEALLOCATE PREPARE stmt_preset;

SET @col_texture := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_tables' AND COLUMN_NAME = 'texture_image_url'
);
SET @ddl_texture := IF(
  @table_exists = 1 AND @col_texture = 0,
  "ALTER TABLE `restaurant_tables` ADD COLUMN `texture_image_url` VARCHAR(512) NULL",
  'SELECT 1'
);
PREPARE stmt_texture FROM @ddl_texture;
EXECUTE stmt_texture;
DEALLOCATE PREPARE stmt_texture;
