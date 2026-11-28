-- Coordination id: menu_reorder_settings_v1
-- Per (restaurant, menu) switches for the menu editor reorder controls.
-- No row = both enabled. PG twin: docs/pgnative/209_menu_reorder_settings.pg.sql
CREATE TABLE IF NOT EXISTS menu_reorder_settings (
  restaurant_id INT NOT NULL,
  menu_id BIGINT NOT NULL,
  sections_reorder_enabled TINYINT(1) NOT NULL DEFAULT 1,
  dishes_reorder_enabled TINYINT(1) NOT NULL DEFAULT 1,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (restaurant_id, menu_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

