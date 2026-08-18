-- Backfill dish allergens lost during the 2026-08-16 restaurant-1 snapshot restore.
--
-- That restore rebuilt `menu_dishes_catalog` and `group_menu_section_dishes_v2`
-- from the JSON blob columns on `menus` (`entrantes`, `principales.items`,
-- `postre`). Those blobs hold only dish description strings, so both inserts
-- hardcoded `JSON_ARRAY()` and every dish ended up with no allergens. The legacy
-- `DIA`/`FINDE`/`POSTRES` tables were copied over verbatim and still hold the
-- original `alergenos` values, so they can be matched back by exact title.
--
-- A few titles exist in more than one legacy row with differing allergen sets.
-- Those are resolved to the union of all sets: for allergens, over-reporting is
-- safe and under-reporting is not.
--
-- Only rows that are currently empty are touched, so any allergens edited in the
-- backoffice since the restore are preserved.

DROP TEMPORARY TABLE IF EXISTS tmp_legacy_allergen_rows;
CREATE TEMPORARY TABLE tmp_legacy_allergen_rows (
  restaurant_id INT NOT NULL,
  title VARCHAR(900) NOT NULL,
  allergens JSON NOT NULL
);

SET @sql = IF(
  (SELECT COUNT(*) FROM information_schema.TABLES
   WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'DIA') = 1,
  'INSERT INTO tmp_legacy_allergen_rows (restaurant_id, title, allergens)
     SELECT restaurant_id, DESCRIPCION, CAST(alergenos AS JSON) FROM DIA
     WHERE alergenos IS NOT NULL AND JSON_VALID(alergenos) AND JSON_LENGTH(alergenos) > 0',
  'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @sql = IF(
  (SELECT COUNT(*) FROM information_schema.TABLES
   WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'FINDE') = 1,
  'INSERT INTO tmp_legacy_allergen_rows (restaurant_id, title, allergens)
     SELECT restaurant_id, DESCRIPCION, CAST(alergenos AS JSON) FROM FINDE
     WHERE alergenos IS NOT NULL AND JSON_VALID(alergenos) AND JSON_LENGTH(alergenos) > 0',
  'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @sql = IF(
  (SELECT COUNT(*) FROM information_schema.TABLES
   WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'POSTRES') = 1,
  'INSERT INTO tmp_legacy_allergen_rows (restaurant_id, title, allergens)
     SELECT restaurant_id, DESCRIPCION, CAST(alergenos AS JSON) FROM POSTRES
     WHERE alergenos IS NOT NULL AND JSON_VALID(alergenos) AND JSON_LENGTH(alergenos) > 0',
  'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- Explode to one row per (title, allergen) so duplicate titles merge into a
-- de-duplicated union instead of one arbitrary set winning.
DROP TEMPORARY TABLE IF EXISTS tmp_legacy_allergen_items;
CREATE TEMPORARY TABLE tmp_legacy_allergen_items (
  restaurant_id INT NOT NULL,
  title VARCHAR(900) NOT NULL,
  position INT NOT NULL,
  allergen VARCHAR(190) NOT NULL,
  PRIMARY KEY (restaurant_id, title(255), allergen)
);

INSERT IGNORE INTO tmp_legacy_allergen_items (restaurant_id, title, position, allergen)
SELECT r.restaurant_id, r.title, j.position, j.allergen
FROM tmp_legacy_allergen_rows r
JOIN JSON_TABLE(r.allergens, '$[*]' COLUMNS (
  position FOR ORDINALITY,
  allergen VARCHAR(190) PATH '$'
)) j
WHERE j.allergen IS NOT NULL AND j.allergen <> '';

DROP TEMPORARY TABLE IF EXISTS tmp_legacy_allergens;
CREATE TEMPORARY TABLE tmp_legacy_allergens (
  restaurant_id INT NOT NULL,
  title VARCHAR(900) NOT NULL,
  allergens JSON NOT NULL,
  PRIMARY KEY (restaurant_id, title(255))
);

INSERT INTO tmp_legacy_allergens (restaurant_id, title, allergens)
SELECT restaurant_id, title, JSON_ARRAYAGG(allergen)
FROM (
  SELECT restaurant_id, title, allergen
  FROM tmp_legacy_allergen_items
  ORDER BY restaurant_id, title, MIN(position) OVER (PARTITION BY restaurant_id, title, allergen), allergen
) ordered
GROUP BY restaurant_id, title;

UPDATE group_menu_section_dishes_v2 d
JOIN tmp_legacy_allergens l
  ON l.restaurant_id = d.restaurant_id
 AND l.title = d.title_snapshot
SET d.allergens_json = l.allergens
WHERE d.allergens_json IS NULL OR JSON_LENGTH(d.allergens_json) = 0;

UPDATE menu_dishes_catalog c
JOIN tmp_legacy_allergens l
  ON l.restaurant_id = c.restaurant_id
 AND l.title = c.title
SET c.allergens_json = l.allergens
WHERE c.allergens_json IS NULL OR JSON_LENGTH(c.allergens_json) = 0;

DROP TEMPORARY TABLE IF EXISTS tmp_legacy_allergen_rows;
DROP TEMPORARY TABLE IF EXISTS tmp_legacy_allergen_items;
DROP TEMPORARY TABLE IF EXISTS tmp_legacy_allergens;
