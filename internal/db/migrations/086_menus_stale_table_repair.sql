-- Repair databases where migration 036 was marked applied before the legacy
-- menus table was imported. This migration must have a new version because
-- previously applied migrations are intentionally skipped by the runner.

SET @legacy_exists := (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menusDeGrupos');
SET @menus_exists := (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menus');
SET @ddl := IF(@legacy_exists = 1 AND @menus_exists = 0, 'RENAME TABLE `menusDeGrupos` TO `menus`', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @menus_exists := (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menus');
SET @col_exists := (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menus' AND COLUMN_NAME = 'menu_type');
SET @ddl := IF(@menus_exists = 1 AND @col_exists = 0, 'ALTER TABLE `menus` ADD COLUMN `menu_type` VARCHAR(64) NOT NULL DEFAULT ''closed_conventional''', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists := (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menus' AND COLUMN_NAME = 'is_draft');
SET @ddl := IF(@menus_exists = 1 AND @col_exists = 0, 'ALTER TABLE `menus` ADD COLUMN `is_draft` TINYINT(1) NOT NULL DEFAULT 0', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists := (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menus' AND COLUMN_NAME = 'editor_version');
SET @ddl := IF(@menus_exists = 1 AND @col_exists = 0, 'ALTER TABLE `menus` ADD COLUMN `editor_version` INT NOT NULL DEFAULT 1', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;
