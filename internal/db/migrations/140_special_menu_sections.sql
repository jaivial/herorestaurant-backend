-- Special menu image sections + per-menu public visibility.
--
-- Two concerns ship together because they belong to the same feature
-- (special menus / menús especiales):
--   1. `special_menu_sections` lets one special menu hold several image
--      sections, each with an optional title rendered above the image.
--      Operator edits them from /app/comida/menus/crear?menuId=… step 4.
--   2. `menus.web_placement` and `menus.menu_public_active` carry the
--      placement / active flags for menu_type = 'special', the same shape
--      the food-type visibility settings already use on
--      restaurant_page_visibility. We keep this in the menus table because
--      the visibility belongs to one menu, not the whole restaurant.
--
-- Coordination id: special_menu_sections_v1 + special_menu_visibility_v1

-- --- menus.web_placement (per-menu public placement, mirrors section schema) ---
SET @menus_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menus'
);
SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menus'
    AND COLUMN_NAME = 'web_placement'
);
SET @ddl := IF(@menus_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `menus` ADD COLUMN `web_placement` VARCHAR(64) NOT NULL DEFAULT ''inside_menus'' AFTER `show_menu_preview_image`',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- --- menus.menu_public_active (per-menu visibility on/off) ---
SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menus'
    AND COLUMN_NAME = 'menu_public_active'
);
SET @ddl := IF(@menus_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `menus` ADD COLUMN `menu_public_active` TINYINT(1) NOT NULL DEFAULT 1 AFTER `web_placement`',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- --- special_menu_sections ---
CREATE TABLE IF NOT EXISTS `special_menu_sections` (
  `id` BIGINT NOT NULL AUTO_INCREMENT,
  `restaurant_id` INT NOT NULL,
  `menu_id` INT NOT NULL,
  `title` VARCHAR(255) NOT NULL DEFAULT '''',
  `image_path` VARCHAR(512) NULL,
  `position` INT NOT NULL DEFAULT 0,
  `created_at` TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_special_menu_sections_menu` (`restaurant_id`, `menu_id`, `position`),
  KEY `idx_special_menu_sections_image` (`restaurant_id`, `menu_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- FK to `menus` only when that table exists.
SET @fk_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'special_menu_sections'
    AND CONSTRAINT_NAME = 'fk_special_menu_sections_menu'
);
SET @ddl := IF(@menus_exists = 1 AND @fk_exists = 0,
  'ALTER TABLE `special_menu_sections` ADD CONSTRAINT `fk_special_menu_sections_menu` FOREIGN KEY (`menu_id`) REFERENCES `menus`(`id`) ON DELETE CASCADE',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;
