-- Add special menu image column to menusDeGrupos
-- Idempotent: only runs if table exists
SET @table_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menusDeGrupos');
SET @col_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menusDeGrupos' AND COLUMN_NAME = 'special_menu_image_url');
SET @ddl := IF(@table_exists = 1 AND @col_exists = 0, 'ALTER TABLE `menusDeGrupos` ADD COLUMN `special_menu_image_url` VARCHAR(512) NULL AFTER `editor_version`', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;
