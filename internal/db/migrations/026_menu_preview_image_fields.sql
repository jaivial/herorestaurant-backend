-- Add menu-level preview image fields for V2 editor/public rendering.
-- Keeps one active preview image per menu and AI generation state.

SET @menus_table_exists = (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'menusDeGrupos'
);

SET @col_exists = (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'menusDeGrupos'
    AND COLUMN_NAME = 'show_menu_preview_image'
);

SET @ddl := IF(
  @menus_table_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `menusDeGrupos` ADD COLUMN `show_menu_preview_image` TINYINT(1) NOT NULL DEFAULT 0 AFTER `show_dish_images`',
  'SELECT 1'
);

PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @col_exists = (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'menusDeGrupos'
    AND COLUMN_NAME = 'menu_preview_image_path'
);

SET @ddl := IF(
  @menus_table_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `menusDeGrupos` ADD COLUMN `menu_preview_image_path` VARCHAR(512) NULL AFTER `special_menu_image_url`',
  'SELECT 1'
);

PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @col_exists = (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'menusDeGrupos'
    AND COLUMN_NAME = 'menu_preview_ai_requested'
);

SET @ddl := IF(
  @menus_table_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `menusDeGrupos` ADD COLUMN `menu_preview_ai_requested` TINYINT(1) NOT NULL DEFAULT 0 AFTER `menu_preview_image_path`',
  'SELECT 1'
);

PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @col_exists = (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'menusDeGrupos'
    AND COLUMN_NAME = 'menu_preview_ai_generating'
);

SET @ddl := IF(
  @menus_table_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `menusDeGrupos` ADD COLUMN `menu_preview_ai_generating` TINYINT(1) NOT NULL DEFAULT 0 AFTER `menu_preview_ai_requested`',
  'SELECT 1'
);

PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
