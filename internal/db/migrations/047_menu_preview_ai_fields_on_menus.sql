-- Add menu-level preview AI state columns to the `menus` table.
-- Migration 026 targeted the legacy `menusDeGrupos` table name; after 036 renamed
-- it to `menus`, these columns were never created on DBs where 026 was a no-op.
-- The Go handler (handleBOGroupMenusV2Get) SELECTs these columns, so their absence
-- caused "Error cargando menu" (HTTP 500). This migration is idempotent.

SET @menus_table_exists = (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'menus'
);

SET @col_exists = (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'menus'
    AND COLUMN_NAME = 'menu_preview_image_path'
);

SET @ddl := IF(
  @menus_table_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `menus` ADD COLUMN `menu_preview_image_path` VARCHAR(512) NULL AFTER `special_menu_image_url`',
  'SELECT 1'
);

PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @col_exists = (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'menus'
    AND COLUMN_NAME = 'menu_preview_ai_requested'
);

SET @ddl := IF(
  @menus_table_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `menus` ADD COLUMN `menu_preview_ai_requested` TINYINT(1) NOT NULL DEFAULT 0 AFTER `menu_preview_image_path`',
  'SELECT 1'
);

PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @col_exists = (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'menus'
    AND COLUMN_NAME = 'menu_preview_ai_generating'
);

SET @ddl := IF(
  @menus_table_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `menus` ADD COLUMN `menu_preview_ai_generating` TINYINT(1) NOT NULL DEFAULT 0 AFTER `menu_preview_ai_requested`',
  'SELECT 1'
);

PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- ---------------------------------------------------------------------------
-- group_menu_section_dishes_v2: ensure columns expected by the V2 handler.
-- Some DBs were seeded with a legacy dish schema (ai_generating / ai_image_url,
-- no price), so migrations 012/024 were no-ops. The handler SELECTs
-- price, ai_requested_img, ai_generating_img, ai_generated_img → add them and
-- backfill from legacy columns when present. Idempotent.
-- ---------------------------------------------------------------------------

SET @dishes_table_exists = (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'group_menu_section_dishes_v2'
);

-- price
SET @col_exists = (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'group_menu_section_dishes_v2' AND COLUMN_NAME = 'price'
);
SET @ddl := IF(@dishes_table_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `group_menu_section_dishes_v2` ADD COLUMN `price` DECIMAL(10,2) NULL AFTER `supplement_price`',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- ai_requested_img
SET @col_exists = (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'group_menu_section_dishes_v2' AND COLUMN_NAME = 'ai_requested_img'
);
SET @ddl := IF(@dishes_table_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `group_menu_section_dishes_v2` ADD COLUMN `ai_requested_img` TINYINT(1) NOT NULL DEFAULT 0 AFTER `foto_path`',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- ai_generating_img
SET @col_exists = (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'group_menu_section_dishes_v2' AND COLUMN_NAME = 'ai_generating_img'
);
SET @ddl := IF(@dishes_table_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `group_menu_section_dishes_v2` ADD COLUMN `ai_generating_img` TINYINT(1) NOT NULL DEFAULT 0 AFTER `ai_requested_img`',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- ai_generated_img
SET @col_exists = (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'group_menu_section_dishes_v2' AND COLUMN_NAME = 'ai_generated_img'
);
SET @ddl := IF(@dishes_table_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `group_menu_section_dishes_v2` ADD COLUMN `ai_generated_img` VARCHAR(512) NULL AFTER `ai_generating_img`',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- Backfill from legacy columns if they exist.
SET @legacy_gen = (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'group_menu_section_dishes_v2' AND COLUMN_NAME = 'ai_generating'
);
SET @ddl := IF(@dishes_table_exists = 1 AND @legacy_gen = 1,
  'UPDATE `group_menu_section_dishes_v2` SET `ai_generating_img` = `ai_generating`',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @legacy_url = (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'group_menu_section_dishes_v2' AND COLUMN_NAME = 'ai_image_url'
);
SET @ddl := IF(@dishes_table_exists = 1 AND @legacy_url = 1,
  'UPDATE `group_menu_section_dishes_v2` SET `ai_generated_img` = `ai_image_url` WHERE `ai_image_url` IS NOT NULL AND `ai_image_url` <> ''''',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;
