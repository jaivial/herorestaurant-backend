-- Special dates: per-section adelanto for special-type menus.
-- Coordination id: special_date_section_menus_v1
--   A special-type menu (menus.menu_type = 'special') is priced per image
--   section (special_menu_sections.price), so on a special date its adelanto
--   is also set per section instead of per menu.
--     backoffice /app/reservas/especial (adelanto per section row)
--       -> special_date_menu_sections
--       -> public GET /reservations/special-date (menus[].sections[])
--       -> preactvillacarmen reservas step 2 + booking snapshot

CREATE TABLE IF NOT EXISTS `special_date_menu_sections` (
  `id` BIGINT NOT NULL AUTO_INCREMENT,
  `restaurant_id` INT NOT NULL,
  `special_date_menu_id` BIGINT NOT NULL,
  `section_id` BIGINT NOT NULL,
  `adelanto_amount` DECIMAL(10,2) NULL,
  `created_at` TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_special_date_menu_section` (`special_date_menu_id`, `section_id`),
  KEY `idx_special_date_menu_sections_restaurant` (`restaurant_id`, `special_date_menu_id`),
  CONSTRAINT `fk_sdms_special_date_menu` FOREIGN KEY (`special_date_menu_id`) REFERENCES `special_date_menus` (`id`) ON DELETE CASCADE,
  CONSTRAINT `fk_sdms_section` FOREIGN KEY (`section_id`) REFERENCES `special_menu_sections` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
