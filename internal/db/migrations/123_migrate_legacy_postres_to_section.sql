-- Migrate the legacy standalone POSTRES list into a group_menu_sections_v2
-- section, so the public desserts page is driven by the same section machinery
-- as every other menu section.
-- Coordination id: menu_section_public_placement_v1
--
-- Shape: one menu per restaurant (menu_type 'special', legacy_source_table
-- 'POSTRES') holding a single 'postres' section flagged public_page_active with
-- web_placement 'independent_section' - which reproduces today's standalone
-- /postres nav entry.
--
-- Idempotent: every INSERT is guarded by a NOT EXISTS on its natural key, so
-- re-running adds nothing. The legacy POSTRES table is left untouched.

SET @postres_exists := (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'POSTRES');
SET @sections_ready := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'group_menu_sections_v2' AND COLUMN_NAME = 'public_page_active'
);

-- 1. Carrier menu, one per restaurant that has legacy desserts.
SET @ddl := IF(@postres_exists = 1 AND @sections_ready = 1,
  'INSERT INTO menus (restaurant_id, menu_title, price, menu_type, active, is_draft, legacy_source_table)
     SELECT DISTINCT p.restaurant_id, ''Postres'', 0, ''special'', 1, 0, ''POSTRES''
       FROM POSTRES p
      WHERE NOT EXISTS (
            SELECT 1 FROM menus m
             WHERE m.restaurant_id = p.restaurant_id
               AND UPPER(COALESCE(m.legacy_source_table, '''')) = ''POSTRES'')',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- 2. The 'postres' section inside that menu, publicly visible and standalone.
SET @ddl := IF(@postres_exists = 1 AND @sections_ready = 1,
  'INSERT INTO group_menu_sections_v2 (restaurant_id, menu_id, title, section_kind, position, public_page_active, web_placement)
     SELECT m.restaurant_id, m.id, ''Postres'', ''postres'', 0, 1, ''independent_section''
       FROM menus m
      WHERE UPPER(COALESCE(m.legacy_source_table, '''')) = ''POSTRES''
        AND NOT EXISTS (
            SELECT 1 FROM group_menu_sections_v2 sec
             WHERE sec.menu_id = m.id AND sec.section_kind = ''postres'')',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- 3. Catalog entries for each legacy dessert, matched later by title.
SET @ddl := IF(@postres_exists = 1 AND @sections_ready = 1,
  'INSERT INTO menu_dishes_catalog (restaurant_id, title, description, allergens_json)
     SELECT p.restaurant_id, TRIM(p.DESCRIPCION), NULL,
            CASE WHEN JSON_VALID(p.alergenos) THEN CAST(p.alergenos AS JSON) ELSE NULL END
       FROM POSTRES p
      WHERE TRIM(COALESCE(p.DESCRIPCION, '''')) <> ''''
        AND NOT EXISTS (
            SELECT 1 FROM menu_dishes_catalog c
             WHERE c.restaurant_id = p.restaurant_id AND c.title = TRIM(p.DESCRIPCION))',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- 4. Dessert rows as section dishes, preserving NUM ordering and active flag.
SET @ddl := IF(@postres_exists = 1 AND @sections_ready = 1,
  'INSERT INTO group_menu_section_dishes_v2
     (restaurant_id, menu_id, section_id, catalog_dish_id, title_snapshot, description_snapshot, allergens_json, price, foto_path, ui_data_id, active, position)
     SELECT p.restaurant_id, sec.menu_id, sec.id, c.id, TRIM(p.DESCRIPCION), NULL,
            CASE WHEN JSON_VALID(p.alergenos) THEN CAST(p.alergenos AS JSON) ELSE NULL END,
            NULL, p.foto_url, p.ui_data_id,
            COALESCE(p.active, 1),
            (SELECT COUNT(*) FROM POSTRES prev
              WHERE prev.restaurant_id = p.restaurant_id AND prev.NUM < p.NUM)
       FROM POSTRES p
       JOIN menus m ON m.restaurant_id = p.restaurant_id AND UPPER(COALESCE(m.legacy_source_table, '''')) = ''POSTRES''
       JOIN group_menu_sections_v2 sec ON sec.menu_id = m.id AND sec.section_kind = ''postres''
       LEFT JOIN menu_dishes_catalog c ON c.restaurant_id = p.restaurant_id AND c.title = TRIM(p.DESCRIPCION)
      WHERE TRIM(COALESCE(p.DESCRIPCION, '''')) <> ''''
        AND NOT EXISTS (
            SELECT 1 FROM group_menu_section_dishes_v2 d
             WHERE d.section_id = sec.id AND d.title_snapshot = TRIM(p.DESCRIPCION))',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;
