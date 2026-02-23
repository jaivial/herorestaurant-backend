-- Add section-level annotations for Group Menus V2.
-- Stored as JSON array to preserve ordering and multi-line editing from backoffice.

SET @sections_table_exists = (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'group_menu_sections_v2'
);

SET @col_exists = (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'group_menu_sections_v2'
    AND COLUMN_NAME = 'annotations_json'
);

SET @ddl := IF(
  @sections_table_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `group_menu_sections_v2` ADD COLUMN `annotations_json` LONGTEXT NULL AFTER `position`',
  'SELECT 1'
);

PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
