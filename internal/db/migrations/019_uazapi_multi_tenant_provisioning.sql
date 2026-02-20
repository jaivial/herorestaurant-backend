-- UAZAPI multi-tenant server pool + per-restaurant provisioned instances.

CREATE TABLE IF NOT EXISTS uazapi_servers (
  id BIGINT NOT NULL AUTO_INCREMENT,
  name VARCHAR(128) NOT NULL,
  base_url VARCHAR(512) NOT NULL,
  admin_token VARCHAR(255) NOT NULL,
  capacity INT NOT NULL DEFAULT 100,
  used_count INT NOT NULL DEFAULT 0,
  priority INT NOT NULL DEFAULT 100,
  is_active TINYINT(1) NOT NULL DEFAULT 1,
  metadata_json JSON DEFAULT NULL,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uniq_uazapi_servers_base_url (base_url),
  KEY idx_uazapi_servers_active_priority (is_active, priority, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS restaurant_uazapi_instances (
  id BIGINT NOT NULL AUTO_INCREMENT,
  restaurant_id INT NOT NULL,
  server_id BIGINT NOT NULL,
  instance_name VARCHAR(191) NOT NULL,
  instance_token VARCHAR(255) NOT NULL,
  provider_instance_id VARCHAR(128) DEFAULT NULL,
  connected_phone VARCHAR(32) DEFAULT NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'pending',
  qr_payload MEDIUMTEXT DEFAULT NULL,
  pair_code VARCHAR(64) DEFAULT NULL,
  last_seen_at DATETIME DEFAULT NULL,
  connected_at DATETIME DEFAULT NULL,
  is_active TINYINT(1) NOT NULL DEFAULT 1,
  metadata_json JSON DEFAULT NULL,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uniq_restaurant_uazapi_instances_restaurant (restaurant_id),
  UNIQUE KEY uniq_restaurant_uazapi_instances_name (instance_name),
  KEY idx_restaurant_uazapi_instances_server (server_id, is_active),
  KEY idx_restaurant_uazapi_instances_status (status, updated_at),
  CONSTRAINT fk_restaurant_uazapi_instances_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
  CONSTRAINT fk_restaurant_uazapi_instances_server FOREIGN KEY (server_id) REFERENCES uazapi_servers(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Keep used_count aligned with currently active instances.
UPDATE uazapi_servers s
LEFT JOIN (
  SELECT server_id, COUNT(*) AS used_count
  FROM restaurant_uazapi_instances
  WHERE is_active = 1
  GROUP BY server_id
) t ON t.server_id = s.id
SET s.used_count = IFNULL(t.used_count, 0),
    s.updated_at = NOW();
