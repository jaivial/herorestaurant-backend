-- Restaurant page visibility toggles (cafe/bebidas public pages).

CREATE TABLE IF NOT EXISTS `restaurant_page_visibility` (
  `id` INT NOT NULL AUTO_INCREMENT,
  `restaurant_id` INT NOT NULL,
  `cafe_page_active` TINYINT(1) NOT NULL DEFAULT 1,
  `bebidas_page_active` TINYINT(1) NOT NULL DEFAULT 1,
  `postres_page_active` TINYINT(1) NOT NULL DEFAULT 1,
  `postres_web_placement` VARCHAR(64) NOT NULL DEFAULT 'inside_menus',
  `created_at` TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uniq_rest_page_vis_restaurant` (`restaurant_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Seed existing restaurants with both pages visible by default
INSERT INTO `restaurant_page_visibility` (`restaurant_id`, `cafe_page_active`, `bebidas_page_active`)
SELECT `id`, 1, 1 FROM `restaurants`
WHERE `id` NOT IN (SELECT `restaurant_id` FROM `restaurant_page_visibility`)
ON DUPLICATE KEY UPDATE `restaurant_id` = `restaurant_id`;
