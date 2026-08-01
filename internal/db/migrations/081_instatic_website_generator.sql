-- Instatic website generator: app settings, instance registry, Stripe/payment
-- plumbing for domain purchase. All additive + idempotent.

-- App-wide settings (key/value). Seed the app base URL; used to build
-- subdomains `*.{app_base_url}` and as PUBLIC_ORIGIN for instatic instances.
CREATE TABLE IF NOT EXISTS app_settings (
  id INT NOT NULL AUTO_INCREMENT,
  k VARCHAR(64) NOT NULL,
  v TEXT NOT NULL,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uniq_app_settings_k (k)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT IGNORE INTO app_settings (k, v) VALUES ('app_base_url', 'backoffice-dev.menustudioai.com');

-- One instatic instance per restaurant. Go supervises the process + owns data dir.
CREATE TABLE IF NOT EXISTS instatic_instances (
  id INT NOT NULL AUTO_INCREMENT,
  restaurant_id INT NOT NULL,
  port INT NOT NULL,
  status ENUM('provisioning','running','stopped','error') NOT NULL DEFAULT 'provisioning',
  data_dir VARCHAR(500) DEFAULT NULL,
  sqlite_path VARCHAR(500) DEFAULT NULL,
  uploads_dir VARCHAR(500) DEFAULT NULL,
  public_origin VARCHAR(255) DEFAULT NULL,
  instatic_session_token VARCHAR(255) DEFAULT NULL,
  last_health_at TIMESTAMP NULL DEFAULT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uniq_instatic_instances_restaurant (restaurant_id),
  UNIQUE KEY uniq_instatic_instances_port (port),
  CONSTRAINT fk_instatic_instances_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Stripe webhook events (idempotency).
CREATE TABLE IF NOT EXISTS stripe_events (
  id BIGINT NOT NULL AUTO_INCREMENT,
  event_id VARCHAR(128) NOT NULL,
  type VARCHAR(128) NOT NULL,
  payload JSON NOT NULL,
  processed_at TIMESTAMP NULL DEFAULT NULL,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uniq_stripe_events_event_id (event_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Additive payment/registrar columns to restaurant_domains.
SET @col_exists = (SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_domains' AND COLUMN_NAME = 'registration_cost');
SET @sql = IF(@col_exists = 0, 'ALTER TABLE restaurant_domains ADD COLUMN registration_cost DECIMAL(10,2) DEFAULT NULL', 'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists = (SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_domains' AND COLUMN_NAME = 'currency');
SET @sql = IF(@col_exists = 0, 'ALTER TABLE restaurant_domains ADD COLUMN currency VARCHAR(8) DEFAULT ''EUR''', 'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists = (SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_domains' AND COLUMN_NAME = 'stripe_checkout_session_id');
SET @sql = IF(@col_exists = 0, 'ALTER TABLE restaurant_domains ADD COLUMN stripe_checkout_session_id VARCHAR(255) DEFAULT NULL', 'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists = (SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_domains' AND COLUMN_NAME = 'stripe_payment_status');
SET @sql = IF(@col_exists = 0, 'ALTER TABLE restaurant_domains ADD COLUMN stripe_payment_status ENUM(''pending'',''paid'',''failed'') NOT NULL DEFAULT ''pending''', 'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists = (SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_domains' AND COLUMN_NAME = 'auto_renew');
SET @sql = IF(@col_exists = 0, 'ALTER TABLE restaurant_domains ADD COLUMN auto_renew TINYINT(1) NOT NULL DEFAULT 1', 'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- site_builder_sites: link to instatic instance + shell-generation flag.
SET @col_exists = (SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'site_builder_sites' AND COLUMN_NAME = 'instatic_instance_id');
SET @sql = IF(@col_exists = 0, 'ALTER TABLE site_builder_sites ADD COLUMN instatic_instance_id INT DEFAULT NULL', 'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists = (SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'site_builder_sites' AND COLUMN_NAME = 'generated_shell');
SET @sql = IF(@col_exists = 0, 'ALTER TABLE site_builder_sites ADD COLUMN generated_shell TINYINT(1) NOT NULL DEFAULT 0', 'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- site_builder_publish_queue: allow seed/verify_domain actions.
ALTER TABLE site_builder_publish_queue MODIFY COLUMN action ENUM('publish','unpublish','rollback','seed','verify_domain') NOT NULL DEFAULT 'publish';
