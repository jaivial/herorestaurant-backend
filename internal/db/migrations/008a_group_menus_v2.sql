-- Group menus v2 - idempotent
-- Only creates new tables if menusDeGrupos exists (for FK), otherwise creates tables without FK

SET @menus_table_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menusDeGrupos');

-- menusDeGrupos columns (idempotent)
SET @col_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menusDeGrupos' AND COLUMN_NAME = 'menu_type');
SET @ddl := IF(@menus_table_exists = 1 AND @col_exists = 0, 'ALTER TABLE `menusDeGrupos` ADD COLUMN `menu_type` VARCHAR(64) NOT NULL DEFAULT ''closed_conventional''', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menusDeGrupos' AND COLUMN_NAME = 'is_draft');
SET @ddl := IF(@menus_table_exists = 1 AND @col_exists = 0, 'ALTER TABLE `menusDeGrupos` ADD COLUMN `is_draft` TINYINT(1) NOT NULL DEFAULT 0', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menusDeGrupos' AND COLUMN_NAME = 'editor_version');
SET @ddl := IF(@menus_table_exists = 1 AND @col_exists = 0, 'ALTER TABLE `menusDeGrupos` ADD COLUMN `editor_version` INT NOT NULL DEFAULT 1', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- menu_dishes_catalog (always create, no FK)
CREATE TABLE IF NOT EXISTS `menu_dishes_catalog` (
  `id` BIGINT NOT NULL AUTO_INCREMENT,
  `restaurant_id` INT NOT NULL,
  `title` VARCHAR(255) NOT NULL,
  `description` TEXT NULL,
  `allergens_json` JSON NULL,
  `default_supplement_enabled` TINYINT(1) NOT NULL DEFAULT 0,
  `default_supplement_price` DECIMAL(10,2) NULL,
  `created_at` TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_menu_dishes_catalog_restaurant_title` (`restaurant_id`, `title`),
  KEY `idx_menu_dishes_catalog_restaurant_updated` (`restaurant_id`, `updated_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- group_menu_sections_v2
CREATE TABLE IF NOT EXISTS `group_menu_sections_v2` (
  `id` BIGINT NOT NULL AUTO_INCREMENT,
  `restaurant_id` INT NOT NULL,
  `menu_id` INT NOT NULL,
  `title` VARCHAR(255) NOT NULL,
  `section_kind` VARCHAR(64) NOT NULL DEFAULT 'custom',
  `position` INT NOT NULL DEFAULT 0,
  `created_at` TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_group_menu_sections_v2_menu_position` (`menu_id`, `position`),
  KEY `idx_group_menu_sections_v2_restaurant_menu` (`restaurant_id`, `menu_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Add FK to menusDeGrupos only if it exists
SET @fk_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'group_menu_sections_v2' AND CONSTRAINT_NAME = 'fk_group_menu_sections_v2_menu');
SET @ddl := IF(@menus_table_exists = 1 AND @fk_exists = 0, 'ALTER TABLE `group_menu_sections_v2` ADD CONSTRAINT `fk_group_menu_sections_v2_menu` FOREIGN KEY (`menu_id`) REFERENCES `menusDeGrupos`(`id`) ON DELETE CASCADE', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- group_menu_section_dishes_v2
CREATE TABLE IF NOT EXISTS `group_menu_section_dishes_v2` (
  `id` BIGINT NOT NULL AUTO_INCREMENT,
  `restaurant_id` INT NOT NULL,
  `menu_id` INT NOT NULL,
  `section_id` BIGINT NOT NULL,
  `catalog_dish_id` BIGINT NULL,
  `title_snapshot` VARCHAR(255) NOT NULL,
  `description_snapshot` TEXT NULL,
  `allergens_json` JSON NULL,
  `supplement_enabled` TINYINT(1) NOT NULL DEFAULT 0,
  `supplement_price` DECIMAL(10,2) NULL,
  `active` TINYINT(1) NOT NULL DEFAULT 1,
  `position` INT NOT NULL DEFAULT 0,
  `created_at` TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_group_menu_section_dishes_v2_section_position` (`section_id`, `position`),
  KEY `idx_group_menu_section_dishes_v2_menu` (`menu_id`),
  KEY `idx_group_menu_section_dishes_v2_restaurant` (`restaurant_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Add FKs
SET @fk_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'group_menu_section_dishes_v2' AND CONSTRAINT_NAME = 'fk_group_menu_section_dishes_v2_section');
SET @ddl := IF(@fk_exists = 0, 'ALTER TABLE `group_menu_section_dishes_v2` ADD CONSTRAINT `fk_group_menu_section_dishes_v2_section` FOREIGN KEY (`section_id`) REFERENCES `group_menu_sections_v2`(`id`) ON DELETE CASCADE', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @fk_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'group_menu_section_dishes_v2' AND CONSTRAINT_NAME = 'fk_group_menu_section_dishes_v2_catalog');
SET @ddl := IF(@fk_exists = 0, 'ALTER TABLE `group_menu_section_dishes_v2` ADD CONSTRAINT `fk_group_menu_section_dishes_v2_catalog` FOREIGN KEY (`catalog_dish_id`) REFERENCES `menu_dishes_catalog`(`id`) ON DELETE SET NULL', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;
