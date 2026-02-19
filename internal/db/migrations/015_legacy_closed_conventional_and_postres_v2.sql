-- 015_legacy_closed_conventional_and_postres_v2.sql
-- Non-destructive migration:
-- 1) Keep DIA/FINDE/POSTRES as source-of-truth during transition.
-- 2) Copy data into menusDeGrupos + group_menu_sections_v2 + group_menu_section_dishes_v2.
-- 3) Populate menu_dishes_catalog with legacy traceability.

-- ---------------------------------------------------------------------
-- Add traceability columns/indexes (idempotent)
-- ---------------------------------------------------------------------

SET @col_exists = (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'menusDeGrupos'
    AND COLUMN_NAME = 'legacy_source_table'
);
SET @ddl = IF(
  @col_exists = 0,
  'ALTER TABLE `menusDeGrupos` ADD COLUMN `legacy_source_table` VARCHAR(16) NULL AFTER `editor_version`',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @idx_exists = (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'menusDeGrupos'
    AND INDEX_NAME = 'uniq_menusDeGrupos_restaurant_legacy_source'
);
SET @ddl = IF(
  @idx_exists = 0,
  'ALTER TABLE `menusDeGrupos` ADD UNIQUE KEY `uniq_menusDeGrupos_restaurant_legacy_source` (`restaurant_id`, `legacy_source_table`)',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists = (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'group_menu_section_dishes_v2'
    AND COLUMN_NAME = 'legacy_source_table'
);
SET @ddl = IF(
  @col_exists = 0,
  'ALTER TABLE `group_menu_section_dishes_v2` ADD COLUMN `legacy_source_table` VARCHAR(16) NULL AFTER `position`',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists = (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'group_menu_section_dishes_v2'
    AND COLUMN_NAME = 'legacy_source_num'
);
SET @ddl = IF(
  @col_exists = 0,
  'ALTER TABLE `group_menu_section_dishes_v2` ADD COLUMN `legacy_source_num` INT NULL AFTER `legacy_source_table`',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists = (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'group_menu_section_dishes_v2'
    AND COLUMN_NAME = 'legacy_source_tipo'
);
SET @ddl = IF(
  @col_exists = 0,
  'ALTER TABLE `group_menu_section_dishes_v2` ADD COLUMN `legacy_source_tipo` VARCHAR(32) NULL AFTER `legacy_source_num`',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @idx_exists = (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'group_menu_section_dishes_v2'
    AND INDEX_NAME = 'uniq_group_menu_section_dishes_v2_legacy_source'
);
SET @ddl = IF(
  @idx_exists = 0,
  'ALTER TABLE `group_menu_section_dishes_v2` ADD UNIQUE KEY `uniq_group_menu_section_dishes_v2_legacy_source` (`restaurant_id`, `menu_id`, `legacy_source_table`, `legacy_source_num`)',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists = (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'menu_dishes_catalog'
    AND COLUMN_NAME = 'legacy_source_table'
);
SET @ddl = IF(
  @col_exists = 0,
  'ALTER TABLE `menu_dishes_catalog` ADD COLUMN `legacy_source_table` VARCHAR(16) NULL AFTER `default_supplement_price`',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists = (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'menu_dishes_catalog'
    AND COLUMN_NAME = 'legacy_source_num'
);
SET @ddl = IF(
  @col_exists = 0,
  'ALTER TABLE `menu_dishes_catalog` ADD COLUMN `legacy_source_num` INT NULL AFTER `legacy_source_table`',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists = (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'menu_dishes_catalog'
    AND COLUMN_NAME = 'legacy_source_tipo'
);
SET @ddl = IF(
  @col_exists = 0,
  'ALTER TABLE `menu_dishes_catalog` ADD COLUMN `legacy_source_tipo` VARCHAR(32) NULL AFTER `legacy_source_num`',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists = (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'menu_dishes_catalog'
    AND COLUMN_NAME = 'legacy_active'
);
SET @ddl = IF(
  @col_exists = 0,
  'ALTER TABLE `menu_dishes_catalog` ADD COLUMN `legacy_active` TINYINT(1) NULL AFTER `legacy_source_tipo`',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @idx_exists = (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'menu_dishes_catalog'
    AND INDEX_NAME = 'uniq_menu_dishes_catalog_legacy_source'
);
SET @ddl = IF(
  @idx_exists = 0,
  'ALTER TABLE `menu_dishes_catalog` ADD UNIQUE KEY `uniq_menu_dishes_catalog_legacy_source` (`restaurant_id`, `legacy_source_table`, `legacy_source_num`)',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- ---------------------------------------------------------------------
-- Create one closed_conventional menu per source table (DIA / FINDE)
-- ---------------------------------------------------------------------

INSERT INTO menusDeGrupos
  (restaurant_id, menu_title, price, included_coffee, active, menu_type, is_draft, editor_version,
   legacy_source_table, menu_subtitle, entrantes, principales, postre, beverage, comments,
   min_party_size, main_dishes_limit, main_dishes_limit_number)
SELECT
  src.restaurant_id,
  'Menu del Dia',
  COALESCE(
    (
      SELECT CAST(NULLIF(REGEXP_REPLACE(REPLACE(d2.DESCRIPCION, ',', '.'), '[^0-9.]', ''), '') AS DECIMAL(10,2))
      FROM DIA d2
      WHERE d2.restaurant_id = src.restaurant_id
        AND UPPER(TRIM(d2.TIPO)) = 'PRECIO'
      ORDER BY COALESCE(d2.active, 1) DESC, d2.NUM DESC
      LIMIT 1
    ),
    0.00
  ),
  0,
  CASE
    WHEN EXISTS (
      SELECT 1
      FROM DIA d3
      WHERE d3.restaurant_id = src.restaurant_id
        AND UPPER(TRIM(d3.TIPO)) <> 'PRECIO'
        AND COALESCE(d3.active, 1) = 1
    ) THEN 1 ELSE 0
  END,
  'closed_conventional',
  0,
  2,
  'DIA',
  '[]',
  '[]',
  '{"titulo_principales":"Principal a elegir","items":[]}',
  '[]',
  '{"type":"no_incluida","price_per_person":null,"has_supplement":false,"supplement_price":null}',
  '["legacy_migration:015","legacy_source:DIA"]',
  1,
  0,
  1
FROM (
  SELECT DISTINCT restaurant_id
  FROM DIA
) src
ON DUPLICATE KEY UPDATE
  menu_type = VALUES(menu_type),
  is_draft = VALUES(is_draft),
  editor_version = VALUES(editor_version),
  price = VALUES(price),
  active = VALUES(active);

INSERT INTO menusDeGrupos
  (restaurant_id, menu_title, price, included_coffee, active, menu_type, is_draft, editor_version,
   legacy_source_table, menu_subtitle, entrantes, principales, postre, beverage, comments,
   min_party_size, main_dishes_limit, main_dishes_limit_number)
SELECT
  src.restaurant_id,
  'Menu Fin de Semana',
  COALESCE(
    (
      SELECT CAST(NULLIF(REGEXP_REPLACE(REPLACE(f2.DESCRIPCION, ',', '.'), '[^0-9.]', ''), '') AS DECIMAL(10,2))
      FROM FINDE f2
      WHERE f2.restaurant_id = src.restaurant_id
        AND UPPER(TRIM(f2.TIPO)) = 'PRECIO'
      ORDER BY COALESCE(f2.active, 1) DESC, f2.NUM DESC
      LIMIT 1
    ),
    0.00
  ),
  0,
  CASE
    WHEN EXISTS (
      SELECT 1
      FROM FINDE f3
      WHERE f3.restaurant_id = src.restaurant_id
        AND UPPER(TRIM(f3.TIPO)) <> 'PRECIO'
        AND COALESCE(f3.active, 1) = 1
    ) THEN 1 ELSE 0
  END,
  'closed_conventional',
  0,
  2,
  'FINDE',
  '[]',
  '[]',
  '{"titulo_principales":"Principal a elegir","items":[]}',
  '[]',
  '{"type":"no_incluida","price_per_person":null,"has_supplement":false,"supplement_price":null}',
  '["legacy_migration:015","legacy_source:FINDE"]',
  1,
  0,
  1
FROM (
  SELECT DISTINCT restaurant_id
  FROM FINDE
) src
ON DUPLICATE KEY UPDATE
  menu_type = VALUES(menu_type),
  is_draft = VALUES(is_draft),
  editor_version = VALUES(editor_version),
  price = VALUES(price),
  active = VALUES(active);

-- ---------------------------------------------------------------------
-- Ensure baseline sections for migrated menus
-- ---------------------------------------------------------------------

INSERT INTO group_menu_sections_v2 (restaurant_id, menu_id, title, section_kind, position)
SELECT
  m.restaurant_id,
  m.id,
  sec.title,
  sec.section_kind,
  sec.position
FROM menusDeGrupos m
JOIN (
  SELECT 'Entrantes' AS title, 'entrantes' AS section_kind, 0 AS position
  UNION ALL
  SELECT 'Principales' AS title, 'principales' AS section_kind, 1 AS position
  UNION ALL
  SELECT 'Arroces' AS title, 'principales' AS section_kind, 2 AS position
  UNION ALL
  SELECT 'Postres' AS title, 'postres' AS section_kind, 3 AS position
) sec
WHERE m.legacy_source_table IN ('DIA', 'FINDE')
  AND NOT EXISTS (
    SELECT 1
    FROM group_menu_sections_v2 s
    WHERE s.restaurant_id = m.restaurant_id
      AND s.menu_id = m.id
      AND s.title = sec.title
  );

-- ---------------------------------------------------------------------
-- Populate catalog with legacy dishes (traceable by source table + NUM)
-- ---------------------------------------------------------------------

INSERT INTO menu_dishes_catalog
  (restaurant_id, title, description, allergens_json, default_supplement_enabled, default_supplement_price,
   legacy_source_table, legacy_source_num, legacy_source_tipo, legacy_active)
SELECT
  d.restaurant_id,
  LEFT(TRIM(d.DESCRIPCION), 255),
  CASE
    WHEN CHAR_LENGTH(TRIM(d.DESCRIPCION)) > 255 THEN TRIM(d.DESCRIPCION)
    ELSE ''
  END,
  CASE
    WHEN d.alergenos IS NULL OR TRIM(d.alergenos) = '' THEN JSON_ARRAY()
    ELSE d.alergenos
  END,
  0,
  NULL,
  'DIA',
  d.NUM,
  UPPER(TRIM(d.TIPO)),
  COALESCE(d.active, 1)
FROM DIA d
WHERE TRIM(COALESCE(d.DESCRIPCION, '')) <> ''
ON DUPLICATE KEY UPDATE
  title = VALUES(title),
  description = VALUES(description),
  allergens_json = VALUES(allergens_json),
  legacy_source_tipo = VALUES(legacy_source_tipo),
  legacy_active = VALUES(legacy_active);

INSERT INTO menu_dishes_catalog
  (restaurant_id, title, description, allergens_json, default_supplement_enabled, default_supplement_price,
   legacy_source_table, legacy_source_num, legacy_source_tipo, legacy_active)
SELECT
  f.restaurant_id,
  LEFT(TRIM(f.DESCRIPCION), 255),
  CASE
    WHEN CHAR_LENGTH(TRIM(f.DESCRIPCION)) > 255 THEN TRIM(f.DESCRIPCION)
    ELSE ''
  END,
  CASE
    WHEN f.alergenos IS NULL OR TRIM(f.alergenos) = '' THEN JSON_ARRAY()
    ELSE f.alergenos
  END,
  0,
  NULL,
  'FINDE',
  f.NUM,
  UPPER(TRIM(f.TIPO)),
  COALESCE(f.active, 1)
FROM FINDE f
WHERE TRIM(COALESCE(f.DESCRIPCION, '')) <> ''
ON DUPLICATE KEY UPDATE
  title = VALUES(title),
  description = VALUES(description),
  allergens_json = VALUES(allergens_json),
  legacy_source_tipo = VALUES(legacy_source_tipo),
  legacy_active = VALUES(legacy_active);

INSERT INTO menu_dishes_catalog
  (restaurant_id, title, description, allergens_json, default_supplement_enabled, default_supplement_price,
   legacy_source_table, legacy_source_num, legacy_source_tipo, legacy_active)
SELECT
  p.restaurant_id,
  LEFT(TRIM(p.DESCRIPCION), 255),
  CASE
    WHEN CHAR_LENGTH(TRIM(p.DESCRIPCION)) > 255 THEN TRIM(p.DESCRIPCION)
    ELSE ''
  END,
  CASE
    WHEN p.alergenos IS NULL OR TRIM(p.alergenos) = '' THEN JSON_ARRAY()
    ELSE p.alergenos
  END,
  0,
  NULL,
  'POSTRES',
  p.NUM,
  'POSTRE',
  COALESCE(p.active, 1)
FROM POSTRES p
WHERE TRIM(COALESCE(p.DESCRIPCION, '')) <> ''
ON DUPLICATE KEY UPDATE
  title = VALUES(title),
  description = VALUES(description),
  allergens_json = VALUES(allergens_json),
  legacy_source_tipo = VALUES(legacy_source_tipo),
  legacy_active = VALUES(legacy_active);

-- ---------------------------------------------------------------------
-- Copy DIA and FINDE dishes into migrated menus
-- ---------------------------------------------------------------------

INSERT INTO group_menu_section_dishes_v2
  (restaurant_id, menu_id, section_id, catalog_dish_id, title_snapshot, description_snapshot, allergens_json,
   supplement_enabled, supplement_price, price, active, position,
   legacy_source_table, legacy_source_num, legacy_source_tipo)
SELECT
  d.restaurant_id,
  m.id,
  s.id,
  c.id,
  LEFT(TRIM(d.DESCRIPCION), 255),
  CASE
    WHEN CHAR_LENGTH(TRIM(d.DESCRIPCION)) > 255 THEN TRIM(d.DESCRIPCION)
    ELSE ''
  END,
  CASE
    WHEN d.alergenos IS NULL OR TRIM(d.alergenos) = '' THEN JSON_ARRAY()
    ELSE d.alergenos
  END,
  0,
  NULL,
  NULL,
  COALESCE(d.active, 1),
  d.NUM,
  'DIA',
  d.NUM,
  UPPER(TRIM(d.TIPO))
FROM DIA d
JOIN menusDeGrupos m
  ON m.restaurant_id = d.restaurant_id
 AND m.legacy_source_table = 'DIA'
JOIN group_menu_sections_v2 s
  ON s.restaurant_id = m.restaurant_id
 AND s.menu_id = m.id
 AND s.title = CASE
   WHEN UPPER(TRIM(d.TIPO)) = 'ENTRANTE' THEN 'Entrantes'
   WHEN UPPER(TRIM(d.TIPO)) = 'PRINCIPAL' THEN 'Principales'
   WHEN UPPER(TRIM(d.TIPO)) = 'ARROZ' THEN 'Arroces'
   ELSE ''
 END
LEFT JOIN menu_dishes_catalog c
  ON c.restaurant_id = d.restaurant_id
 AND c.legacy_source_table = 'DIA'
 AND c.legacy_source_num = d.NUM
WHERE UPPER(TRIM(d.TIPO)) IN ('ENTRANTE', 'PRINCIPAL', 'ARROZ')
  AND TRIM(COALESCE(d.DESCRIPCION, '')) <> ''
ON DUPLICATE KEY UPDATE
  section_id = VALUES(section_id),
  catalog_dish_id = VALUES(catalog_dish_id),
  title_snapshot = VALUES(title_snapshot),
  description_snapshot = VALUES(description_snapshot),
  allergens_json = VALUES(allergens_json),
  active = VALUES(active),
  position = VALUES(position),
  legacy_source_tipo = VALUES(legacy_source_tipo);

INSERT INTO group_menu_section_dishes_v2
  (restaurant_id, menu_id, section_id, catalog_dish_id, title_snapshot, description_snapshot, allergens_json,
   supplement_enabled, supplement_price, price, active, position,
   legacy_source_table, legacy_source_num, legacy_source_tipo)
SELECT
  f.restaurant_id,
  m.id,
  s.id,
  c.id,
  LEFT(TRIM(f.DESCRIPCION), 255),
  CASE
    WHEN CHAR_LENGTH(TRIM(f.DESCRIPCION)) > 255 THEN TRIM(f.DESCRIPCION)
    ELSE ''
  END,
  CASE
    WHEN f.alergenos IS NULL OR TRIM(f.alergenos) = '' THEN JSON_ARRAY()
    ELSE f.alergenos
  END,
  0,
  NULL,
  NULL,
  COALESCE(f.active, 1),
  f.NUM,
  'FINDE',
  f.NUM,
  UPPER(TRIM(f.TIPO))
FROM FINDE f
JOIN menusDeGrupos m
  ON m.restaurant_id = f.restaurant_id
 AND m.legacy_source_table = 'FINDE'
JOIN group_menu_sections_v2 s
  ON s.restaurant_id = m.restaurant_id
 AND s.menu_id = m.id
 AND s.title = CASE
   WHEN UPPER(TRIM(f.TIPO)) = 'ENTRANTE' THEN 'Entrantes'
   WHEN UPPER(TRIM(f.TIPO)) = 'PRINCIPAL' THEN 'Principales'
   WHEN UPPER(TRIM(f.TIPO)) = 'ARROZ' THEN 'Arroces'
   ELSE ''
 END
LEFT JOIN menu_dishes_catalog c
  ON c.restaurant_id = f.restaurant_id
 AND c.legacy_source_table = 'FINDE'
 AND c.legacy_source_num = f.NUM
WHERE UPPER(TRIM(f.TIPO)) IN ('ENTRANTE', 'PRINCIPAL', 'ARROZ')
  AND TRIM(COALESCE(f.DESCRIPCION, '')) <> ''
ON DUPLICATE KEY UPDATE
  section_id = VALUES(section_id),
  catalog_dish_id = VALUES(catalog_dish_id),
  title_snapshot = VALUES(title_snapshot),
  description_snapshot = VALUES(description_snapshot),
  allergens_json = VALUES(allergens_json),
  active = VALUES(active),
  position = VALUES(position),
  legacy_source_tipo = VALUES(legacy_source_tipo);

-- ---------------------------------------------------------------------
-- Copy POSTRES into the "Postres" section of both migrated menus
-- ---------------------------------------------------------------------

INSERT INTO group_menu_section_dishes_v2
  (restaurant_id, menu_id, section_id, catalog_dish_id, title_snapshot, description_snapshot, allergens_json,
   supplement_enabled, supplement_price, price, active, position,
   legacy_source_table, legacy_source_num, legacy_source_tipo)
SELECT
  p.restaurant_id,
  m.id,
  s.id,
  c.id,
  LEFT(TRIM(p.DESCRIPCION), 255),
  CASE
    WHEN CHAR_LENGTH(TRIM(p.DESCRIPCION)) > 255 THEN TRIM(p.DESCRIPCION)
    ELSE ''
  END,
  CASE
    WHEN p.alergenos IS NULL OR TRIM(p.alergenos) = '' THEN JSON_ARRAY()
    ELSE p.alergenos
  END,
  0,
  NULL,
  NULL,
  COALESCE(p.active, 1),
  p.NUM,
  'POSTRES',
  p.NUM,
  'POSTRE'
FROM POSTRES p
JOIN menusDeGrupos m
  ON m.restaurant_id = p.restaurant_id
 AND m.legacy_source_table IN ('DIA', 'FINDE')
JOIN group_menu_sections_v2 s
  ON s.restaurant_id = m.restaurant_id
 AND s.menu_id = m.id
 AND s.title = 'Postres'
LEFT JOIN menu_dishes_catalog c
  ON c.restaurant_id = p.restaurant_id
 AND c.legacy_source_table = 'POSTRES'
 AND c.legacy_source_num = p.NUM
WHERE TRIM(COALESCE(p.DESCRIPCION, '')) <> ''
ON DUPLICATE KEY UPDATE
  section_id = VALUES(section_id),
  catalog_dish_id = VALUES(catalog_dish_id),
  title_snapshot = VALUES(title_snapshot),
  description_snapshot = VALUES(description_snapshot),
  allergens_json = VALUES(allergens_json),
  active = VALUES(active),
  position = VALUES(position),
  legacy_source_tipo = VALUES(legacy_source_tipo);

-- ---------------------------------------------------------------------
-- Keep migrated menu price and snapshots in sync with legacy sources
-- ---------------------------------------------------------------------

UPDATE menusDeGrupos m
SET
  m.price = CASE
    WHEN m.legacy_source_table = 'DIA' THEN COALESCE(
      (
        SELECT CAST(NULLIF(REGEXP_REPLACE(REPLACE(d.DESCRIPCION, ',', '.'), '[^0-9.]', ''), '') AS DECIMAL(10,2))
        FROM DIA d
        WHERE d.restaurant_id = m.restaurant_id
          AND UPPER(TRIM(d.TIPO)) = 'PRECIO'
        ORDER BY COALESCE(d.active, 1) DESC, d.NUM DESC
        LIMIT 1
      ),
      0.00
    )
    WHEN m.legacy_source_table = 'FINDE' THEN COALESCE(
      (
        SELECT CAST(NULLIF(REGEXP_REPLACE(REPLACE(f.DESCRIPCION, ',', '.'), '[^0-9.]', ''), '') AS DECIMAL(10,2))
        FROM FINDE f
        WHERE f.restaurant_id = m.restaurant_id
          AND UPPER(TRIM(f.TIPO)) = 'PRECIO'
        ORDER BY COALESCE(f.active, 1) DESC, f.NUM DESC
        LIMIT 1
      ),
      0.00
    )
    ELSE m.price
  END,
  m.active = CASE
    WHEN m.legacy_source_table = 'DIA' THEN
      CASE WHEN EXISTS (
        SELECT 1
        FROM DIA d
        WHERE d.restaurant_id = m.restaurant_id
          AND UPPER(TRIM(d.TIPO)) <> 'PRECIO'
          AND COALESCE(d.active, 1) = 1
      ) THEN 1 ELSE 0 END
    WHEN m.legacy_source_table = 'FINDE' THEN
      CASE WHEN EXISTS (
        SELECT 1
        FROM FINDE f
        WHERE f.restaurant_id = m.restaurant_id
          AND UPPER(TRIM(f.TIPO)) <> 'PRECIO'
          AND COALESCE(f.active, 1) = 1
      ) THEN 1 ELSE 0 END
    ELSE m.active
  END
WHERE m.legacy_source_table IN ('DIA', 'FINDE');

UPDATE menusDeGrupos m
SET
  m.entrantes = COALESCE(
    (
      SELECT JSON_ARRAYAGG(x.val)
      FROM (
        SELECT
          CASE
            WHEN d.description_snapshot IS NOT NULL AND TRIM(d.description_snapshot) <> '' THEN d.description_snapshot
            ELSE d.title_snapshot
          END AS val
        FROM group_menu_section_dishes_v2 d
        JOIN group_menu_sections_v2 s
          ON s.id = d.section_id
         AND s.menu_id = m.id
         AND s.restaurant_id = m.restaurant_id
        WHERE d.menu_id = m.id
          AND d.restaurant_id = m.restaurant_id
          AND d.active = 1
          AND s.title = 'Entrantes'
        ORDER BY d.position, d.id
      ) x
    ),
    JSON_ARRAY()
  ),
  m.principales = JSON_OBJECT(
    'titulo_principales', 'Principal a elegir',
    'items', COALESCE(
      (
        SELECT JSON_ARRAYAGG(x.val)
        FROM (
          SELECT
            CASE
              WHEN d.description_snapshot IS NOT NULL AND TRIM(d.description_snapshot) <> '' THEN d.description_snapshot
              ELSE d.title_snapshot
            END AS val
          FROM group_menu_section_dishes_v2 d
          JOIN group_menu_sections_v2 s
            ON s.id = d.section_id
           AND s.menu_id = m.id
           AND s.restaurant_id = m.restaurant_id
          WHERE d.menu_id = m.id
            AND d.restaurant_id = m.restaurant_id
            AND d.active = 1
            AND s.title IN ('Principales', 'Arroces')
          ORDER BY d.position, d.id
        ) x
      ),
      JSON_ARRAY()
    )
  ),
  m.postre = COALESCE(
    (
      SELECT JSON_ARRAYAGG(x.val)
      FROM (
        SELECT
          CASE
            WHEN d.description_snapshot IS NOT NULL AND TRIM(d.description_snapshot) <> '' THEN d.description_snapshot
            ELSE d.title_snapshot
          END AS val
        FROM group_menu_section_dishes_v2 d
        JOIN group_menu_sections_v2 s
          ON s.id = d.section_id
         AND s.menu_id = m.id
         AND s.restaurant_id = m.restaurant_id
        WHERE d.menu_id = m.id
          AND d.restaurant_id = m.restaurant_id
          AND d.active = 1
          AND s.title = 'Postres'
        ORDER BY d.position, d.id
      ) x
    ),
    JSON_ARRAY()
  ),
  m.menu_type = 'closed_conventional',
  m.is_draft = 0,
  m.editor_version = 2
WHERE m.legacy_source_table IN ('DIA', 'FINDE');
