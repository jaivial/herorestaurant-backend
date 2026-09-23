-- Special menus: optional main courses (principales) per image section.
-- Coordination id: special_menu_principales_v1
--   backoffice configuracion ("Anadir platos principales" toggle + per-section
--   dish picker from comida_items platos)
--     -> menus.special_principales_enabled + special_menu_section_principales
--     -> public menu payload (special_menu_sections[].principales)
--     -> preactvillacarmen reservas wizard step 2 (principales per section)
--     -> booking snapshot validation (special_json.menus[].items)

SET @col_exists := (
    SELECT COUNT(*) FROM information_schema.COLUMNS
    WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menus' AND COLUMN_NAME = 'special_principales_enabled'
);
SET @ddl := IF(@col_exists = 0, 'ALTER TABLE menus ADD COLUMN special_principales_enabled TINYINT(1) NOT NULL DEFAULT 0', 'SELECT 1');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

CREATE TABLE IF NOT EXISTS `special_menu_section_principales` (
  `id` BIGINT NOT NULL AUTO_INCREMENT,
  `restaurant_id` INT NOT NULL,
  `menu_id` INT NOT NULL,
  `section_id` BIGINT NOT NULL,
  `dish_id` INT NOT NULL,
  `position` INT NOT NULL DEFAULT 0,
  `created_at` TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_special_section_principal` (`section_id`, `dish_id`),
  KEY `idx_special_section_principales_menu` (`restaurant_id`, `menu_id`, `section_id`, `position`),
  CONSTRAINT `fk_special_section_principales_section` FOREIGN KEY (`section_id`) REFERENCES `special_menu_sections` (`id`) ON DELETE CASCADE,
  CONSTRAINT `fk_special_section_principales_dish` FOREIGN KEY (`dish_id`) REFERENCES `comida_items` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
