-- Comida module tables + VINOS denominacion_origen (idempotent)

-- Ensure VINOS has denominacion_origen used by current handlers.
SET @table_exists = (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'VINOS'
);
SET @col_exists = (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'VINOS' AND COLUMN_NAME = 'denominacion_origen'
);
SET @ddl := IF(
  @table_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `VINOS` ADD COLUMN `denominacion_origen` VARCHAR(120) NULL AFTER `bodega`',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- Categories for platos (base + custom per restaurant).
CREATE TABLE IF NOT EXISTS `comida_plato_categories` (
  `id` INT NOT NULL AUTO_INCREMENT,
  `restaurant_id` INT NOT NULL,
  `name` VARCHAR(120) NOT NULL,
  `slug` VARCHAR(140) NOT NULL,
  `source` VARCHAR(16) NOT NULL DEFAULT 'custom',
  `active` TINYINT(1) NOT NULL DEFAULT 1,
  `created_at` TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uniq_comida_plato_categories_restaurant_slug` (`restaurant_id`, `slug`),
  KEY `idx_comida_plato_categories_restaurant_active` (`restaurant_id`, `active`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Generic comida items for platos/bebidas/cafes.
CREATE TABLE IF NOT EXISTS `comida_items` (
  `id` INT NOT NULL AUTO_INCREMENT,
  `restaurant_id` INT NOT NULL,
  `source_type` VARCHAR(16) NOT NULL,
  `nombre` VARCHAR(255) NOT NULL,
  `tipo` VARCHAR(64) NULL,
  `categoria` VARCHAR(140) NULL,
  `category_id` INT NULL,
  `titulo` VARCHAR(255) NULL,
  `precio` DECIMAL(10,2) NOT NULL DEFAULT 0.00,
  `suplemento` DECIMAL(10,2) NULL,
  `descripcion` TEXT NULL,
  `alergenos_json` JSON NULL,
  `active` TINYINT(1) NOT NULL DEFAULT 1,
  `foto_path` VARCHAR(512) NULL,
  `foto` LONGBLOB NULL,
  `created_at` TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_comida_items_restaurant_source_active` (`restaurant_id`, `source_type`, `active`),
  KEY `idx_comida_items_restaurant_source_tipo` (`restaurant_id`, `source_type`, `tipo`),
  KEY `idx_comida_items_restaurant_source_category` (`restaurant_id`, `source_type`, `category_id`),
  KEY `idx_comida_items_restaurant_updated` (`restaurant_id`, `updated_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

SET @fk_exists = (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'comida_items'
    AND CONSTRAINT_NAME = 'fk_comida_items_category'
);
SET @ddl := IF(
  @fk_exists = 0,
  'ALTER TABLE `comida_items` ADD CONSTRAINT `fk_comida_items_category` FOREIGN KEY (`category_id`) REFERENCES `comida_plato_categories`(`id`) ON DELETE SET NULL',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;
