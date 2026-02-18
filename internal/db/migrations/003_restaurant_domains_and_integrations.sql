-- Tenant resolution by domain + restaurant-level branding/integrations + delivery logs.

CREATE TABLE IF NOT EXISTS restaurant_domains (
  id INT NOT NULL AUTO_INCREMENT,
  restaurant_id INT NOT NULL,
  domain VARCHAR(255) NOT NULL,
  is_primary TINYINT(1) NOT NULL DEFAULT 0,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uniq_restaurant_domains_domain (domain),
  KEY idx_restaurant_domains_restaurant (restaurant_id),
  CONSTRAINT fk_restaurant_domains_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Seed common dev + VillaCarmen domains (idempotent).
INSERT IGNORE INTO restaurant_domains (restaurant_id, domain, is_primary) VALUES
  (1, 'localhost', 1),
  (1, '127.0.0.1', 0),
  (1, 'alqueriavillacarmen.com', 0),
  (1, 'www.alqueriavillacarmen.com', 0);

CREATE TABLE IF NOT EXISTS restaurant_branding (
  restaurant_id INT NOT NULL,
  brand_name VARCHAR(255) DEFAULT NULL,
  logo_url VARCHAR(1024) DEFAULT NULL,
  primary_color VARCHAR(32) DEFAULT NULL,
  accent_color VARCHAR(32) DEFAULT NULL,
  email_from_name VARCHAR(255) DEFAULT NULL,
  email_from_address VARCHAR(255) DEFAULT NULL,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (restaurant_id),
  CONSTRAINT fk_restaurant_branding_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS restaurant_integrations (
  restaurant_id INT NOT NULL,
  n8n_webhook_url VARCHAR(1024) DEFAULT NULL,
  enabled_events_json JSON DEFAULT NULL,
  uazapi_url VARCHAR(1024) DEFAULT NULL,
  uazapi_token VARCHAR(255) DEFAULT NULL,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (restaurant_id),
  CONSTRAINT fk_restaurant_integrations_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS audit_log (
  id BIGINT NOT NULL AUTO_INCREMENT,
  restaurant_id INT NOT NULL,
  actor_user_id INT DEFAULT NULL,
  action VARCHAR(64) NOT NULL,
  entity VARCHAR(64) NOT NULL,
  entity_id VARCHAR(64) DEFAULT NULL,
  before_json JSON DEFAULT NULL,
  after_json JSON DEFAULT NULL,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_audit_log_restaurant_created (restaurant_id, created_at),
  KEY idx_audit_log_actor_created (actor_user_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS message_deliveries (
  id BIGINT NOT NULL AUTO_INCREMENT,
  restaurant_id INT NOT NULL,
  channel VARCHAR(16) NOT NULL,
  event VARCHAR(64) NOT NULL,
  recipient VARCHAR(255) NOT NULL,
  payload_json JSON DEFAULT NULL,
  status VARCHAR(16) NOT NULL DEFAULT 'pending',
  provider_message_id VARCHAR(255) DEFAULT NULL,
  error TEXT DEFAULT NULL,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  sent_at DATETIME DEFAULT NULL,
  PRIMARY KEY (id),
  KEY idx_message_deliveries_restaurant_created (restaurant_id, created_at),
  KEY idx_message_deliveries_restaurant_status (restaurant_id, status),
  KEY idx_message_deliveries_event_created (event, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

