-- Add price column to group_menu_section_dishes_v2 for a la carte menus
-- Idempotent: only runs if table exists
SET @table_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'group_menu_section_dishes_v2');
SET @col_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'group_menu_section_dishes_v2' AND COLUMN_NAME = 'price');
SET @ddl := IF(@table_exists = 1 AND @col_exists = 0, 'ALTER TABLE `group_menu_section_dishes_v2` ADD COLUMN `price` DECIMAL(10,2) NULL AFTER `supplement_price`', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;
