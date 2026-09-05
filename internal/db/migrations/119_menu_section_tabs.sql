-- Menu-level toggle: render public menu sections as sticky tabs.
-- Scoped per menu row, which already carries restaurant_id (multi-tenant).
-- Coordination id: menu_section_tabs_flag

SET @menus_exists := (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menus');

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'menus'
    AND COLUMN_NAME = 'show_section_tabs'
);

SET @ddl := IF(
  @menus_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `menus` ADD COLUMN `show_section_tabs` TINYINT(1) NOT NULL DEFAULT 0 AFTER `show_dish_images`',
  'SELECT 1'
);

PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
