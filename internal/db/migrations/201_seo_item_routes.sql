-- 201_seo_item_routes.sql
-- Registro de rutas SEO por plato y por vino.
-- Coordination id: seo_item_routes_v1
--
-- Una fila por entidad con ruta publica propia: /platos/<slug> para los
-- platos de carta y /vinos/<slug> para los vinos de la carta.
--
-- La clave (restaurant_id, entity_type, entity_id) es la que detecta si esa
-- entidad YA tiene ruta: el slug se genera una sola vez y se conserva aunque
-- el plato se renombre, de modo que la URL (y el SEO que ya tiene) no se
-- rompen. El titulo y la descripcion editables en el backoffice se guardan
-- aqui; si estan vacios, la pagina usa los del propio plato o vino.
--
-- active = 1 significa "la entidad sigue activa en la carta". Cuando un plato
-- se desactiva o se retira de su menu, la fila pasa a active = 0 y la ruta
-- desaparece del sitemap.

CREATE TABLE IF NOT EXISTS `seo_item_routes` (
  `id`              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `restaurant_id`   INT            NOT NULL,
  `entity_type`     VARCHAR(16)    NOT NULL,
  `entity_id`       BIGINT         NOT NULL,
  `slug`            VARCHAR(160)   NOT NULL,
  `seo_title`       VARCHAR(255)   NULL,
  `seo_description` VARCHAR(320)   NULL,
  `active`          TINYINT(1)     NOT NULL DEFAULT 1,
  `created_at`      DATETIME       NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at`      DATETIME       NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uniq_seo_item_route` (`restaurant_id`, `entity_type`, `entity_id`),
  UNIQUE KEY `uniq_seo_item_slug` (`restaurant_id`, `entity_type`, `slug`),
  KEY `idx_seo_item_active` (`restaurant_id`, `entity_type`, `active`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
