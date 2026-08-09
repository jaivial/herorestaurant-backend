-- Unified comida category catalogue, scoped per restaurant and food type.
--
-- Platos and bebidas already have their own legacy tables
-- (comida_plato_categories, comida_bebida_categories) and comida_items.category_id
-- has a FK into the platos one, so those are left untouched. This table is purely
-- additive: it adds categories for the food types that never had any (vinos, cafes,
-- postres) and introduces the notion of a "global" category shared by every type.
--
-- food_type = '' is the sentinel for a global category. A NULL is deliberately NOT
-- used: MySQL treats NULLs as distinct inside a UNIQUE index, so nullable food_type
-- would let duplicate global categories through.

CREATE TABLE IF NOT EXISTS `comida_categories` (
  `id` INT NOT NULL AUTO_INCREMENT,
  `restaurant_id` INT NOT NULL,
  `food_type` VARCHAR(16) NOT NULL DEFAULT '',
  `name` VARCHAR(120) NOT NULL,
  `slug` VARCHAR(160) NOT NULL,
  `active` TINYINT(1) NOT NULL DEFAULT 1,
  `created_at` TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uniq_comida_categories_restaurant_type_slug` (`restaurant_id`, `food_type`, `slug`),
  KEY `idx_comida_categories_restaurant_active` (`restaurant_id`, `active`),
  KEY `idx_comida_categories_restaurant_type` (`restaurant_id`, `food_type`, `active`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
