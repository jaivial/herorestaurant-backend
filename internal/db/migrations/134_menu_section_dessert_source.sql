-- Dessert source for menu sections: where a "postres" section takes its dishes from.
-- Coordination id: dessert_section_source_v1
-- (backoffice add-section modal -> DB group_menu_sections_v2.dessert_source
--  -> backoffice/public REST section loaders -> preactvillacarmen)
--
-- 'general' - the section mirrors the restaurant's general desserts carta (the
--             POSTRES carrier menu). Read-only everywhere but /app/comida/postres.
-- 'custom'  - the section owns its own fully editable dish list (default, and the
--             only meaningful value for non-dessert sections).

SET @sections_exists := (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'group_menu_sections_v2');

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'group_menu_sections_v2'
    AND COLUMN_NAME = 'dessert_source'
);

SET @ddl := IF(
  @sections_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `group_menu_sections_v2` ADD COLUMN `dessert_source` VARCHAR(32) NOT NULL DEFAULT ''custom'' AFTER `section_kind`',
  'SELECT 1'
);

PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- The carrier POSTRES menu section IS the general carta, so it always owns its
-- dishes; never let it point at itself.
SET @ddl := IF(
  @sections_exists = 1,
  'UPDATE group_menu_sections_v2 sec
     JOIN menus m ON m.id = sec.menu_id
      SET sec.dessert_source = ''custom''
    WHERE UPPER(COALESCE(m.legacy_source_table, '''')) = ''POSTRES''',
  'SELECT 1'
);

PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
