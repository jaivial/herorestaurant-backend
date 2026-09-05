-- Section display fields: per-section display title, subtitle, and tab label.
-- Coordination id: menu_section_display_fields_v1
-- display_title  - shown in preactvillacarmen as the section heading (required).
-- subtitle       - short italic line shown below the heading (optional).
-- tab_label      - label used when the menu renders sections as sticky tabs
--                  (only used when menus.show_section_tabs = 1).

SET @table_exists := (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'group_menu_sections_v2');

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'group_menu_sections_v2'
    AND COLUMN_NAME = 'display_title'
);

SET @ddl := IF(
  @table_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `group_menu_sections_v2` ADD COLUMN `display_title` VARCHAR(255) NOT NULL DEFAULT '''' AFTER `title`',
  'SELECT 1'
);

PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'group_menu_sections_v2'
    AND COLUMN_NAME = 'subtitle'
);

SET @ddl := IF(
  @table_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `group_menu_sections_v2` ADD COLUMN `subtitle` VARCHAR(500) NOT NULL DEFAULT '''' AFTER `display_title`',
  'SELECT 1'
);

PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'group_menu_sections_v2'
    AND COLUMN_NAME = 'tab_label'
);

SET @ddl := IF(
  @table_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `group_menu_sections_v2` ADD COLUMN `tab_label` VARCHAR(255) NOT NULL DEFAULT '''' AFTER `subtitle`',
  'SELECT 1'
);

PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;