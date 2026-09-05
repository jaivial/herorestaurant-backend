-- Section-level public visibility for menu sections (e.g. "Postres").
-- Scoped per section row, which already carries restaurant_id + menu_id (multi-tenant).
-- Coordination id: menu_section_public_placement_v1

SET @sections_exists := (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'group_menu_sections_v2');

SET @public_active_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'group_menu_sections_v2'
    AND COLUMN_NAME = 'public_page_active'
);

SET @ddl := IF(
  @sections_exists = 1 AND @public_active_exists = 0,
  'ALTER TABLE `group_menu_sections_v2` ADD COLUMN `public_page_active` TINYINT(1) NOT NULL DEFAULT 0 AFTER `section_kind`',
  'SELECT 1'
);

PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @placement_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'group_menu_sections_v2'
    AND COLUMN_NAME = 'web_placement'
);

SET @ddl := IF(
  @sections_exists = 1 AND @placement_exists = 0,
  'ALTER TABLE `group_menu_sections_v2` ADD COLUMN `web_placement` VARCHAR(64) NOT NULL DEFAULT ''inside_menus'' AFTER `public_page_active`',
  'SELECT 1'
);

PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
