CREATE TABLE IF NOT EXISTS assistant_memory_nodes (
  id BIGINT NOT NULL AUTO_INCREMENT,
  restaurant_id INT NOT NULL,
  user_id INT NOT NULL DEFAULT 0,
  kind VARCHAR(24) NOT NULL DEFAULT 'fact',
  label VARCHAR(190) NOT NULL,
  detail TEXT NULL,
  weight DOUBLE NOT NULL DEFAULT 1,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uq_memory_scope_label (restaurant_id, user_id, label),
  KEY ix_memory_scope (restaurant_id, user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS assistant_memory_edges (
  id BIGINT NOT NULL AUTO_INCREMENT,
  restaurant_id INT NOT NULL,
  from_node BIGINT NOT NULL,
  to_node BIGINT NOT NULL,
  rel VARCHAR(64) NOT NULL,
  weight DOUBLE NOT NULL DEFAULT 1,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uq_edge (from_node, to_node, rel),
  KEY ix_edge_rest (restaurant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
