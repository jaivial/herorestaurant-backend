-- Data migration: DIA/FINDE -> comida_items
-- Populates the unified comida_items table from legacy DIA and FINDE tables.
-- All dishes are classified as source_type = 'platos'.
-- Date: 2026-07-08

SET NAMES utf8mb4;

-- Step 1: Seed base plato categories for restaurant 1 (idempotent)
INSERT IGNORE INTO comida_plato_categories (restaurant_id, name, slug, source, active)
VALUES
  (1, 'Entrantes', 'entrantes', 'base', 1),
  (1, 'Principal', 'principal', 'base', 1),
  (1, 'Arroz', 'arroz', 'base', 1),
  (1, 'Postre', 'postre', 'base', 1);

-- Step 2: Migrate DIA dishes to comida_items
-- Uses a derived table to compute cleaned values once.
INSERT INTO comida_items
  (restaurant_id, source_type, nombre, tipo, categoria, category_id,
   precio, suplemento, descripcion, alergenos_json, active,
   created_at, updated_at)
SELECT
  restaurant_id,
  'platos',
  LEFT(cleaned, 255),          -- nombre: VARCHAR(255), truncate if needed
  TIPO,
  NULL,                         -- categoria
  NULL,                         -- category_id
  precio_extracted,
  suplemento_extracted,
  cleaned,                      -- descripcion: TEXT, full text
  alergenos_fixed,
  active,
  NOW(),
  NOW()
FROM (
  SELECT
    restaurant_id,
    TIPO,
    active,
    TRIM(REGEXP_REPLACE(
      REGEXP_REPLACE(DESCRIPCION, '\\s*\\(\\+?\\d+(?:[.,]\\d{1,2})?€\\)\\.?\\s*$', ''),
      '\\s*\\(\\+?\\d+(?:[.,]\\d{1,2})?€\\)\\.?', ''
    )) AS cleaned,
    COALESCE(CAST(NULLIF(REGEXP_REPLACE(
      REGEXP_SUBSTR(DESCRIPCION, '\\(\\d+(?:[.,]\\d{1,2})?€\\)'),
      '[^0-9.,]', ''), '') AS DECIMAL(10,2)), 0.00) AS precio_extracted,
    COALESCE(CAST(NULLIF(REGEXP_REPLACE(
      REGEXP_SUBSTR(DESCRIPCION, '\\(\\+\\d+(?:[.,]\\d{1,2})?€\\)'),
      '[^0-9.,]', ''), '') AS DECIMAL(10,2)), 0.00) AS suplemento_extracted,
    CASE
      WHEN alergenos IS NULL OR TRIM(alergenos) = '' OR TRIM(alergenos) = 'null'
      THEN JSON_ARRAY()
      ELSE CAST(alergenos AS JSON)
    END AS alergenos_fixed
  FROM DIA
  WHERE TIPO != 'PRECIO'
) AS src
WHERE NOT EXISTS (
  SELECT 1 FROM comida_items ci
  WHERE ci.restaurant_id = src.restaurant_id
    AND ci.source_type = 'platos'
    AND ci.nombre = LEFT(src.cleaned, 255)
    AND ci.tipo = src.TIPO
);

-- Step 3: Migrate FINDE dishes to comida_items
INSERT INTO comida_items
  (restaurant_id, source_type, nombre, tipo, categoria, category_id,
   precio, suplemento, descripcion, alergenos_json, active,
   created_at, updated_at)
SELECT
  restaurant_id,
  'platos',
  LEFT(cleaned, 255),
  TIPO,
  NULL,
  NULL,
  precio_extracted,
  suplemento_extracted,
  cleaned,
  alergenos_fixed,
  active,
  NOW(),
  NOW()
FROM (
  SELECT
    restaurant_id,
    TIPO,
    active,
    TRIM(REGEXP_REPLACE(
      REGEXP_REPLACE(DESCRIPCION, '\\s*\\(\\+?\\d+(?:[.,]\\d{1,2})?€\\)\\.?\\s*$', ''),
      '\\s*\\(\\+?\\d+(?:[.,]\\d{1,2})?€\\)\\.?', ''
    )) AS cleaned,
    COALESCE(CAST(NULLIF(REGEXP_REPLACE(
      REGEXP_SUBSTR(DESCRIPCION, '\\(\\d+(?:[.,]\\d{1,2})?€\\)'),
      '[^0-9.,]', ''), '') AS DECIMAL(10,2)), 0.00) AS precio_extracted,
    COALESCE(CAST(NULLIF(REGEXP_REPLACE(
      REGEXP_SUBSTR(DESCRIPCION, '\\(\\+\\d+(?:[.,]\\d{1,2})?€\\)'),
      '[^0-9.,]', ''), '') AS DECIMAL(10,2)), 0.00) AS suplemento_extracted,
    CASE
      WHEN alergenos IS NULL OR TRIM(alergenos) = '' OR TRIM(alergenos) = 'null'
      THEN JSON_ARRAY()
      ELSE CAST(alergenos AS JSON)
    END AS alergenos_fixed
  FROM FINDE
  WHERE TIPO != 'PRECIO'
) AS src
WHERE NOT EXISTS (
  SELECT 1 FROM comida_items ci
  WHERE ci.restaurant_id = src.restaurant_id
    AND ci.source_type = 'platos'
    AND ci.nombre = LEFT(src.cleaned, 255)
    AND ci.tipo = src.TIPO
);

-- Step 4: Verify counts
SELECT 'comida_items total' AS label, COUNT(*) AS count FROM comida_items;
SELECT 'by source_type' AS label, source_type, COUNT(*) AS count FROM comida_items GROUP BY source_type;
SELECT 'by tipo' AS label, tipo, COUNT(*) AS count FROM comida_items WHERE source_type = 'platos' GROUP BY tipo ORDER BY count DESC;
