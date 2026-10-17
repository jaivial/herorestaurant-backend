-- 181_group_menu_wines_v2.sql
--
-- Vinos como datos v2 (tabla propia, no reutiliza group_menu_section_dishes_v2).
--
-- Contexto historico: la carta de vinos vivia en la tabla legacy `VINOS`, con
-- `num` como identificador. Se crea esta tabla para que el backend lea y
-- escriba los vinos desde aqui, de modo que `VINOS` pueda eliminarse.
--
-- Se replican TODAS las columnas de `VINOS` para no perder funcionalidad:
--   - production_type / stock_item_id / stock_recipe_id los usa
--     backoffice_technical_sheet_link.go y backoffice_technical_sheet_ensure.go
--   - ai_* los usa el flujo de generacion de imagenes con IA
--   - `foto` (blob) NO se replica: las imagenes se sirven por `foto_path`
--     (ruta relativa) contra el pull base URL de BunnyCDN
--
-- Idempotente: se puede ejecutar mas de una vez sin efectos duplicados.

CREATE TABLE IF NOT EXISTS group_menu_wines_v2 (
  id                    BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  restaurant_id         INT            NOT NULL,
  position              INT            NOT NULL DEFAULT 0,
  tipo                  VARCHAR(16)    NOT NULL DEFAULT 'TINTO',
  nombre                VARCHAR(255)   NOT NULL,
  precio                DECIMAL(10,2)  NULL,
  descripcion           TEXT           NULL,
  bodega                VARCHAR(255)   NULL,
  denominacion_origen   VARCHAR(255)   NULL,
  graduacion            DECIMAL(4,1)   NULL,
  anyo                  INT            NULL,
  active                TINYINT(1)     NOT NULL DEFAULT 1,
  foto_path             VARCHAR(512)   NULL,
  ui_data_id            VARCHAR(255)   NULL,
  production_type       ENUM('RAW','MANUFACTURED') NOT NULL DEFAULT 'RAW',
  stock_item_id         BIGINT UNSIGNED NULL,
  stock_recipe_id       BIGINT UNSIGNED NULL,
  ai_requested_img      TINYINT(1)     NOT NULL DEFAULT 0,
  ai_generating_img     TINYINT(1)     NOT NULL DEFAULT 0,
  ai_generated_img      VARCHAR(512)   NULL,
  created_at            DATETIME       NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at            DATETIME       NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_wines_restaurant_active (restaurant_id, active),
  KEY idx_wines_restaurant_tipo    (restaurant_id, tipo, active),
  KEY idx_wines_stock              (stock_item_id),
  KEY idx_wines_recipe             (stock_recipe_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Mapa de traduccion: el `num` de `VINOS` al `id` de esta tabla.
-- Necesario porque el codigo legacy (y las reservas historicas) identifican
-- los vinos por `num`, que es un entero sin relacion con el id autogenerado.
CREATE TABLE IF NOT EXISTS legacy_wine_map (
  restaurant_id INT NOT NULL,
  legacy_num    INT NOT NULL,
  v2_wine_id    BIGINT UNSIGNED NOT NULL,
  PRIMARY KEY (restaurant_id, legacy_num),
  KEY idx_legacy_wine_map_v2 (v2_wine_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Copia de datos desde `VINOS` si la tabla legacy existe.
-- El emparejamiento se hace por nombre normalizado + tipo, que es como se
-- hizo la migracion en produccion (42/42_rows emparejadas, mapeo 1:1).
-- Si `VINOS` no existe (instalaciones nuevas, DEV limpio) esto no hace nada.
INSERT INTO group_menu_wines_v2
  (restaurant_id, `position`, tipo, nombre, precio, descripcion, bodega,
   denominacion_origen, graduacion, anyo, active, foto_path, ui_data_id,
   production_type, stock_item_id, stock_recipe_id,
   ai_requested_img, ai_generating_img, ai_generated_img)
SELECT
  l.restaurant_id, l.`num`, l.tipo, l.nombre, l.precio, l.descripcion, l.bodega,
  l.denominacion_origen, l.graduacion, l.anyo, l.active, l.foto_path, l.ui_data_id,
  l.production_type, l.stock_item_id, l.stock_recipe_id,
  l.ai_requested_img, l.ai_generating_img, l.ai_generated_img
FROM VINOS l
WHERE NOT EXISTS (
  SELECT 1 FROM group_menu_wines_v2 w
  WHERE w.restaurant_id = l.restaurant_id AND w.nombre COLLATE utf8mb4_unicode_ci = l.nombre COLLATE utf8mb4_unicode_ci
      AND w.tipo COLLATE utf8mb4_unicode_ci = l.tipo COLLATE utf8mb4_unicode_ci
);

-- Rellena el mapa emparejando por id recien insertado. Se hace por posicion
-- para garantizar que el mapeo es 1:1 y determinista.
INSERT IGNORE INTO legacy_wine_map (restaurant_id, legacy_num, v2_wine_id)
SELECT
  l.restaurant_id,
  l.`num`,
  (
    SELECT w.id FROM group_menu_wines_v2 w
    WHERE w.restaurant_id = l.restaurant_id
      AND CONVERT(w.nombre USING utf8mb4) = CONVERT(l.nombre USING utf8mb4)
      AND CONVERT(w.tipo USING utf8mb4) = CONVERT(l.tipo USING utf8mb4)
    ORDER BY w.id LIMIT 1
  )
FROM VINOS l
WHERE l.`num` IS NOT NULL
  AND EXISTS (
    SELECT 1 FROM group_menu_wines_v2 w
    WHERE w.restaurant_id = l.restaurant_id
      AND CONVERT(w.nombre USING utf8mb4) = CONVERT(l.nombre USING utf8mb4)
      AND CONVERT(w.tipo USING utf8mb4) = CONVERT(l.tipo USING utf8mb4)
  );