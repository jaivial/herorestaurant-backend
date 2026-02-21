-- Persist menu-level preview style for dish cards:
-- 0 = text-only cards, 1 = cards with image area.

SET @menus_table_exists = (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'menusDeGrupos'
);

SET @col_exists = (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'menusDeGrupos'
    AND COLUMN_NAME = 'show_dish_images'
);

SET @ddl := IF(
  @menus_table_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `menusDeGrupos` ADD COLUMN `show_dish_images` TINYINT(1) NOT NULL DEFAULT 0 AFTER `menu_subtitle`',
  'SELECT 1'
);

PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
