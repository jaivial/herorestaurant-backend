-- Per-menu "Información importante" block.
--
-- The public templates used to hardcode this copy (minimum consumption, no kids
-- menu, takeaway containers). It is now authored per menu from the backoffice
-- Configuracion tab and rendered from the public API, so it needs its own
-- column mirroring the existing `comments` JSON string array.
--
-- The backfill below copies the exact copy that used to be hardcoded so every
-- existing menu keeps showing the same information until an editor changes it.
-- Both steps are idempotent: the column guard is INFORMATION_SCHEMA based and
-- the backfill only touches menus whose important_info is still empty.
--
-- Coordination id: menu_important_info_v1

SET @menus_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menus'
);

SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menus' AND COLUMN_NAME = 'important_info'
);
SET @ddl := IF(@menus_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `menus` ADD COLUMN `important_info` LONGTEXT NULL',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- Seed the matching English translations before the backfill, while the empty
-- guard still identifies the menus that never had their own copy. SHA2 matches
-- hashText() in the translator, so an unchanged Spanish line skips retranslation.
INSERT INTO dish_translations
  (restaurant_id, entity_type, entity_id, field_name, lang, source_hash, translated_text)
SELECT restaurant_id, 'menus', id, 'important_info.0', 'en',
       SHA2('Consumo mínimo: 1 menú por plaza reservada en la mesa, independientemente de la edad de los comensales.', 256),
       'Minimum consumption: 1 menu per reserved seat at the table, regardless of guests’ age.'
FROM menus
WHERE important_info IS NULL OR TRIM(important_info) = '' OR TRIM(important_info) = '[]'
ON DUPLICATE KEY UPDATE translated_text = VALUES(translated_text), source_hash = VALUES(source_hash);

INSERT INTO dish_translations
  (restaurant_id, entity_type, entity_id, field_name, lang, source_hash, translated_text)
SELECT restaurant_id, 'menus', id, 'important_info.1', 'en',
       SHA2('No hay menú infantil.', 256),
       'No kids menu.'
FROM menus
WHERE important_info IS NULL OR TRIM(important_info) = '' OR TRIM(important_info) = '[]'
ON DUPLICATE KEY UPDATE translated_text = VALUES(translated_text), source_hash = VALUES(source_hash);

INSERT INTO dish_translations
  (restaurant_id, entity_type, entity_id, field_name, lang, source_hash, translated_text)
SELECT restaurant_id, 'menus', id, 'important_info.2', 'en',
       SHA2('Envases para llevar: 1€ (Cobro obligatorio por Ley de Residuos 7/2020).', 256),
       'Takeaway containers: 1€ (Mandatory charge under Waste Law 7/2020).'
FROM menus
WHERE important_info IS NULL OR TRIM(important_info) = '' OR TRIM(important_info) = '[]'
ON DUPLICATE KEY UPDATE translated_text = VALUES(translated_text), source_hash = VALUES(source_hash);

-- Backfill the previous hardcoded copy for menus that never had their own
-- important info. Guarded so re-running never clobbers edited content.
UPDATE menus
SET important_info = JSON_ARRAY(
  'Consumo mínimo: 1 menú por plaza reservada en la mesa, independientemente de la edad de los comensales.',
  'No hay menú infantil.',
  'Envases para llevar: 1€ (Cobro obligatorio por Ley de Residuos 7/2020).'
)
WHERE important_info IS NULL OR TRIM(important_info) = '' OR TRIM(important_info) = '[]';
