CREATE TABLE IF NOT EXISTS pos_visit_merges (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  restaurant_id INT NOT NULL,
  target_visit_id BIGINT UNSIGNED NOT NULL,
  idempotency_key VARCHAR(120) NOT NULL,
  source_visit_ids_json JSON NOT NULL,
  created_by INT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uq_pos_visit_merges_restaurant_key (restaurant_id, idempotency_key),
  KEY idx_pos_visit_merges_target (restaurant_id, target_visit_id),
  CONSTRAINT fk_pos_visit_merges_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
  CONSTRAINT fk_pos_visit_merges_target FOREIGN KEY (restaurant_id, target_visit_id) REFERENCES pos_visits(restaurant_id, id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
