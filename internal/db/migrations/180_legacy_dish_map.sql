-- 180_legacy_dish_map.sql
-- Mapa de correspondencia entre las tablas legacy de carta (DIA, FINDE) y el
-- esquema v2 (group_menu_section_dishes_v2).
--
-- Necesario porque special_booking_snapshot.scanDishName() resolvia el nombre
-- de un plato a partir de su NUM legacy, y las referencias historicas de las
-- reservas siguen usando esos NUM. Con DIA y FINDE ya migradas, este mapa es
-- lo que permite seguir mostrando el nombre correcto del plato.
--
-- La clave primaria es (restaurant_id, legacy_table, legacy_num) porque DIA y
-- FINDE numeran desde 1 y se solapan: el mismo numero existe en ambas tablas
-- para platos distintos.

CREATE TABLE IF NOT EXISTS `legacy_dish_map` (
  `restaurant_id` INT NOT NULL,
  `legacy_table`  VARCHAR(16) CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci NOT NULL,
  `legacy_num`    INT NOT NULL,
  `legacy_desc`   VARCHAR(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci NULL,
  `v2_dish_id`    BIGINT UNSIGNED NULL,
  `migrated_at`   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`restaurant_id`, `legacy_table`, `legacy_num`),
  KEY `idx_legacy_v2` (`v2_dish_id`),
  KEY `idx_legacy_rest` (`restaurant_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Backfill paso 1: una fila por plato de DIA y FINDE, sin resolver v2_dish_id.
-- TIPO='PRECIO' no es un plato sino el precio del menu, asi que se excluye.
INSERT INTO `legacy_dish_map` (restaurant_id, legacy_table, legacy_num, legacy_desc)
SELECT restaurant_id, legacy_table, legacy_num, LEFT(legacy_desc, 255)
FROM (
  SELECT restaurant_id, 'DIA'   AS legacy_table, NUM AS legacy_num, DESCRIPCION AS legacy_desc
  FROM DIA WHERE TIPO IS NOT NULL AND TIPO <> 'PRECIO'
  UNION ALL
  SELECT restaurant_id, 'FINDE', NUM, DESCRIPCION
  FROM FINDE WHERE TIPO IS NOT NULL AND TIPO <> 'PRECIO'
) l
ON DUPLICATE KEY UPDATE legacy_desc = VALUES(legacy_desc);

-- Backfill paso 2: resolver el id de v2 por titulo normalizado (sin acentos,
-- sin el sufijo de recargo entre parentesis y sin puntuacion), porque un mismo
-- plato puede haberse escrito con redaccion distinta al migrarlo a v2.
-- COLLATE explicito porque REGEXP_REPLACE devuelve la collation del servidor.
DROP TEMPORARY TABLE IF EXISTS `_v2_norm`;
CREATE TEMPORARY TABLE `_v2_norm` (
  `id` BIGINT UNSIGNED NOT NULL,
  `k`  VARCHAR(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci NOT NULL,
  KEY `k` (`k`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT INTO `_v2_norm` (id, k)
SELECT d.id,
       LOWER(TRIM(REGEXP_REPLACE(REGEXP_REPLACE(REGEXP_REPLACE(REGEXP_REPLACE(
         CONVERT(IFNULL(d.title_snapshot,'') USING utf8mb4),
         '\\([^)]*\\)', ''), '[aáàäâ]', 'a'), '[eéèëê]', 'e'), '[^a-zA-Z0-9]+', ' ')))
       COLLATE utf8mb4_unicode_ci
FROM group_menu_section_dishes_v2 d
WHERE d.active = 1 AND d.menu_id IN (41, 42);

UPDATE `legacy_dish_map` m
JOIN `_v2_norm` v
  ON v.k = LOWER(TRIM(REGEXP_REPLACE(REGEXP_REPLACE(REGEXP_REPLACE(REGEXP_REPLACE(
       CONVERT(IFNULL(m.legacy_desc,'') USING utf8mb4),
       '\\([^)]*\\)', ''), '[aáàäâ]', 'a'), '[eéèëê]', 'e'), '[^a-zA-Z0-9]+', ' ')))
       COLLATE utf8mb4_unicode_ci
SET m.v2_dish_id = v.id
WHERE m.v2_dish_id IS NULL;

DROP TEMPORARY TABLE `_v2_norm`;
