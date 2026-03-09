-- Migration: Rename menusDeGrupos table to menus
-- This renames the main menus table to a cleaner name

-- Rename the table if it exists with the old name
SET @table_exists := (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menusDeGrupos');
SET @new_table_exists := (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menus');

-- Only rename if old table exists and new table doesn't exist
SET @sql := IF(@table_exists = 1 AND @new_table_exists = 0,
    'RENAME TABLE `menusDeGrupos` TO `menus`',
    'SELECT 1'
);

PREPARE stmt FROM @sql;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
