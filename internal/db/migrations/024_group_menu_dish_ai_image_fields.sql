-- Add AI image generation tracking fields for group_menu_section_dishes_v2.

SET @table_exists = (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'group_menu_section_dishes_v2'
);

SET @col_exists = (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'group_menu_section_dishes_v2'
    AND COLUMN_NAME = 'ai_requested_img'
);

SET @ddl := IF(
  @table_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `group_menu_section_dishes_v2` ADD COLUMN `ai_requested_img` TINYINT(1) NOT NULL DEFAULT 0 AFTER `foto_path`',
  'SELECT 1'
);

PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @col_exists = (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'group_menu_section_dishes_v2'
    AND COLUMN_NAME = 'ai_generating_img'
);

SET @ddl := IF(
  @table_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `group_menu_section_dishes_v2` ADD COLUMN `ai_generating_img` TINYINT(1) NOT NULL DEFAULT 0 AFTER `ai_requested_img`',
  'SELECT 1'
);

PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @col_exists = (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'group_menu_section_dishes_v2'
    AND COLUMN_NAME = 'ai_generated_img'
);

SET @ddl := IF(
  @table_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `group_menu_section_dishes_v2` ADD COLUMN `ai_generated_img` VARCHAR(512) NULL AFTER `ai_generating_img`',
  'SELECT 1'
);

PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
