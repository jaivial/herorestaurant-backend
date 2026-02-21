CREATE TABLE IF NOT EXISTS restaurant_menu_templates (
  restaurant_id INT NOT NULL,
  menu_type VARCHAR(32) NOT NULL,
  theme_id VARCHAR(64) NOT NULL,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (restaurant_id, menu_type),
  KEY idx_restaurant_menu_templates_theme (theme_id),
  CONSTRAINT fk_restaurant_menu_templates_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
