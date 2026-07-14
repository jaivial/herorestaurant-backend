-- AI translations for dish/menu text fields.
-- Generic table: one row per (restaurant, entity, field, language).
-- entity_type examples: comida_items, POSTRES, VINOS, DIA, FINDE, menus,
-- group_menu_sections_v2, group_menu_section_dishes_v2, menu_dishes_catalog.

CREATE TABLE IF NOT EXISTS dish_translations (
  id BIGINT NOT NULL AUTO_INCREMENT,
  restaurant_id INT NOT NULL,
  entity_type VARCHAR(48) NOT NULL,
  entity_id BIGINT NOT NULL,
  field_name VARCHAR(64) NOT NULL,
  lang CHAR(2) NOT NULL DEFAULT 'en',
  source_hash CHAR(64) NOT NULL,
  translated_text TEXT NOT NULL,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uniq_dish_translation (restaurant_id, entity_type, entity_id, field_name, lang),
  KEY idx_dish_translation_lookup (restaurant_id, entity_type, entity_id, lang)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
