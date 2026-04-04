-- Beverage categories table (mirrors comida_plato_categories for bebidas)

CREATE TABLE IF NOT EXISTS `comida_bebida_categories` (
  `id` INT NOT NULL AUTO_INCREMENT,
  `restaurant_id` INT NOT NULL,
  `name` VARCHAR(120) NOT NULL,
  `slug` VARCHAR(160) NOT NULL,
  `source` VARCHAR(16) NOT NULL DEFAULT 'custom',
  `active` TINYINT(1) NOT NULL DEFAULT 1,
  `created_at` TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uniq_comida_bebida_categories_restaurant_slug` (`restaurant_id`, `slug`),
  KEY `idx_comida_bebida_categories_restaurant_active` (`restaurant_id`, `active`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
