-- Salons (dining rooms) per restaurant floor: backoffice config v3.
-- A salon belongs to exactly one floor; deleting the floor deletes its salons.

CREATE TABLE IF NOT EXISTS restaurant_salons (
  id INT NOT NULL AUTO_INCREMENT,
  restaurant_id INT NOT NULL,
  floor_id INT NOT NULL,
  name VARCHAR(120) NOT NULL,
  has_capacity_limit TINYINT(1) NOT NULL DEFAULT 0,
  capacity_limit INT NOT NULL DEFAULT 45,
  is_active TINYINT(1) NOT NULL DEFAULT 1,
  display_order INT NOT NULL DEFAULT 0,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uniq_restaurant_salons_restaurant_floor_name (restaurant_id, floor_id, name),
  KEY idx_restaurant_salons_restaurant (restaurant_id, is_active, display_order),
  KEY idx_restaurant_salons_floor (floor_id),
  CONSTRAINT fk_restaurant_salons_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
  CONSTRAINT fk_restaurant_salons_floor FOREIGN KEY (floor_id) REFERENCES restaurant_floors(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Per-date salon status override (mirrors restaurant_floor_overrides).
-- Absence of a row means "use the salon's global default is_active".
CREATE TABLE IF NOT EXISTS restaurant_salons_overrides (
  restaurant_id INT NOT NULL,
  `date` DATE NOT NULL,
  salon_id INT NOT NULL,
  is_active TINYINT(1) NOT NULL DEFAULT 1,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (restaurant_id, `date`, salon_id),
  KEY idx_restaurant_salons_overrides_restaurant_date (restaurant_id, `date`),
  CONSTRAINT fk_restaurant_salons_overrides_salon FOREIGN KEY (salon_id) REFERENCES restaurant_salons(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
