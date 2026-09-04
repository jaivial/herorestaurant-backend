-- Restaurant-scoped beverage choices used by group-menu editors.
CREATE TABLE IF NOT EXISTS restaurant_beverage_options (
  id BIGINT NOT NULL AUTO_INCREMENT,
  restaurant_id INT NOT NULL,
  slug VARCHAR(120) NOT NULL,
  name VARCHAR(255) NOT NULL,
  is_custom TINYINT(1) NOT NULL DEFAULT 0,
  active TINYINT(1) NOT NULL DEFAULT 1,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uq_restaurant_beverage_option_slug (restaurant_id, slug),
  KEY idx_restaurant_beverage_options_restaurant (restaurant_id, active),
  CONSTRAINT fk_restaurant_beverage_options_restaurant
    FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS menu_beverage_options (
  menu_id INT NOT NULL,
  restaurant_id INT NOT NULL,
  beverage_option_id BIGINT NOT NULL,
  selected TINYINT(1) NOT NULL DEFAULT 1,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (menu_id, beverage_option_id),
  KEY idx_menu_beverage_options_restaurant (restaurant_id, menu_id),
  CONSTRAINT fk_menu_beverage_options_menu FOREIGN KEY (menu_id) REFERENCES menus(id) ON DELETE CASCADE,
  CONSTRAINT fk_menu_beverage_options_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
  CONSTRAINT fk_menu_beverage_options_option FOREIGN KEY (beverage_option_id) REFERENCES restaurant_beverage_options(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Seed the default beverage options for every restaurant. Wrapped in a
-- derived table (`AS new`) because MySQL 8.0.20+ rejects VALUES() in
-- ON DUPLICATE KEY UPDATE clauses of INSERT ... SELECT statements
-- (Error 1064 near 'KEY UPDATE name = VALUES(name), ...'). The alias
-- form is portable across 8.x versions that the runner is deployed on.
INSERT INTO restaurant_beverage_options (restaurant_id, slug, name, is_custom, active)
SELECT restaurant_id, slug, name, is_custom, active FROM (
  SELECT r.id AS restaurant_id, defaults.slug AS slug, defaults.name AS name,
         0 AS is_custom, 1 AS active
  FROM restaurants r
  CROSS JOIN (
    SELECT 'agua' AS slug, 'Agua' AS name
    UNION ALL SELECT 'refrescos', 'Refrescos'
    UNION ALL SELECT 'vino', 'Vino'
    UNION ALL SELECT 'cerveza-de-barril', 'Cerveza de barril'
    UNION ALL SELECT 'sangria', 'Sangria'
    UNION ALL SELECT 'cerveza-de-tercio', 'Cerveza de tercio'
    UNION ALL SELECT 'martini', 'Martini'
    UNION ALL SELECT 'combinados', 'Combinados (gin tonic)'
  ) defaults
) AS new (restaurant_id, slug, name, is_custom, active)
ON DUPLICATE KEY UPDATE name = new.name, active = new.active;
