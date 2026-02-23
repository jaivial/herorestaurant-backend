CREATE TABLE IF NOT EXISTS restaurant_table_layouts (
  id BIGINT NOT NULL AUTO_INCREMENT,
  restaurant_id INT NOT NULL,
  layout_date DATE NOT NULL,
  floor_number INT NOT NULL DEFAULT 0,
  data_json JSON NULL,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uniq_restaurant_table_layout (restaurant_id, layout_date, floor_number),
  KEY idx_restaurant_table_layout_restaurant_date (restaurant_id, layout_date),
  CONSTRAINT fk_restaurant_table_layout_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
