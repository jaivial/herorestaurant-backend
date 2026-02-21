-- Add foto_path storage for V2 group menu dish images.
SET @table_exists = (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'group_menu_section_dishes_v2'
);

SET @col_exists = (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'group_menu_section_dishes_v2'
    AND COLUMN_NAME = 'foto_path'
);

SET @ddl := IF(
  @table_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `group_menu_section_dishes_v2` ADD COLUMN `foto_path` VARCHAR(512) NULL AFTER `price`',
  'SELECT 1'
);

PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
