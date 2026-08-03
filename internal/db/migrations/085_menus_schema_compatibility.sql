-- Ensure the renamed menus table has columns introduced before the rename.
-- Some installations were created with `menus` already present, causing the
-- pre-rename migration 008 to no-op and leaving the backoffice menu queries
-- without their required columns.

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
