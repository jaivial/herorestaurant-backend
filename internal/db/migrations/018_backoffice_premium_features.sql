-- Backoffice premium features schema.

CREATE TABLE IF NOT EXISTS restaurant_websites (
  id BIGINT NOT NULL AUTO_INCREMENT,
  restaurant_id INT NOT NULL,
  template_id VARCHAR(64) NULL,
  custom_html MEDIUMTEXT NULL,
  domain VARCHAR(255) NULL,
  domain_status VARCHAR(32) NOT NULL DEFAULT 'pending',
  is_published TINYINT(1) NOT NULL DEFAULT 0,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uniq_restaurant_websites_restaurant (restaurant_id),
  KEY idx_restaurant_websites_domain (domain),
  KEY idx_restaurant_websites_status (domain_status, is_published),
  CONSTRAINT fk_restaurant_websites_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS restaurant_areas (
  id BIGINT NOT NULL AUTO_INCREMENT,
  restaurant_id INT NOT NULL,
  name VARCHAR(120) NOT NULL,
  display_order INT NOT NULL DEFAULT 0,
  is_active TINYINT(1) NOT NULL DEFAULT 1,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uniq_restaurant_areas_restaurant_name (restaurant_id, name),
  KEY idx_restaurant_areas_restaurant (restaurant_id, is_active, display_order),
  CONSTRAINT fk_restaurant_areas_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS restaurant_tables (
  id BIGINT NOT NULL AUTO_INCREMENT,
  restaurant_id INT NOT NULL,
  area_id BIGINT NULL,
  name VARCHAR(120) NOT NULL,
  capacity INT NOT NULL DEFAULT 0,
  display_order INT NOT NULL DEFAULT 0,
  is_active TINYINT(1) NOT NULL DEFAULT 1,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uniq_restaurant_tables_restaurant_name (restaurant_id, name),
  KEY idx_restaurant_tables_restaurant (restaurant_id, is_active, display_order),
  KEY idx_restaurant_tables_area (area_id),
  CONSTRAINT fk_restaurant_tables_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
  CONSTRAINT fk_restaurant_tables_area FOREIGN KEY (area_id) REFERENCES restaurant_areas(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS recurring_invoices (
  id BIGINT NOT NULL AUTO_INCREMENT,
  restaurant_id INT NOT NULL,
  feature_key VARCHAR(64) NOT NULL,
  concept VARCHAR(255) NOT NULL,
  amount DECIMAL(12,2) NOT NULL DEFAULT 0.00,
  currency CHAR(3) NOT NULL DEFAULT 'EUR',
  frequency VARCHAR(32) NOT NULL,
  interval_count INT NOT NULL DEFAULT 1,
  next_run_at DATETIME NOT NULL,
  is_active TINYINT(1) NOT NULL DEFAULT 1,
  metadata_json JSON NULL,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_recurring_invoices_restaurant_next (restaurant_id, is_active, next_run_at),
  KEY idx_recurring_invoices_feature (feature_key),
  CONSTRAINT fk_recurring_invoices_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Ensure restaurant_websites required columns/indexes exist.
SET @restaurant_websites_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_websites'
);

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_websites' AND COLUMN_NAME = 'restaurant_id'
);
SET @ddl := IF(
  @restaurant_websites_exists = 1 AND @col_exists = 0,
  'ALTER TABLE restaurant_websites ADD COLUMN restaurant_id INT NULL',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_websites' AND COLUMN_NAME = 'template_id'
);
SET @ddl := IF(
  @restaurant_websites_exists = 1 AND @col_exists = 0,
  'ALTER TABLE restaurant_websites ADD COLUMN template_id VARCHAR(64) NULL',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_websites' AND COLUMN_NAME = 'custom_html'
);
SET @ddl := IF(
  @restaurant_websites_exists = 1 AND @col_exists = 0,
  'ALTER TABLE restaurant_websites ADD COLUMN custom_html MEDIUMTEXT NULL',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_websites' AND COLUMN_NAME = 'domain'
);
SET @ddl := IF(
  @restaurant_websites_exists = 1 AND @col_exists = 0,
  'ALTER TABLE restaurant_websites ADD COLUMN domain VARCHAR(255) NULL',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_websites' AND COLUMN_NAME = 'domain_status'
);
SET @ddl := IF(
  @restaurant_websites_exists = 1 AND @col_exists = 0,
  'ALTER TABLE restaurant_websites ADD COLUMN domain_status VARCHAR(32) NOT NULL DEFAULT ''pending''',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_websites' AND COLUMN_NAME = 'is_published'
);
SET @ddl := IF(
  @restaurant_websites_exists = 1 AND @col_exists = 0,
  'ALTER TABLE restaurant_websites ADD COLUMN is_published TINYINT(1) NOT NULL DEFAULT 0',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_websites' AND COLUMN_NAME = 'created_at'
);
SET @ddl := IF(
  @restaurant_websites_exists = 1 AND @col_exists = 0,
  'ALTER TABLE restaurant_websites ADD COLUMN created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_websites' AND COLUMN_NAME = 'updated_at'
);
SET @ddl := IF(
  @restaurant_websites_exists = 1 AND @col_exists = 0,
  'ALTER TABLE restaurant_websites ADD COLUMN updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @idx_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_websites' AND INDEX_NAME = 'uniq_restaurant_websites_restaurant'
);
SET @ddl := IF(
  @restaurant_websites_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE restaurant_websites ADD UNIQUE KEY uniq_restaurant_websites_restaurant (restaurant_id)',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @idx_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_websites' AND INDEX_NAME = 'idx_restaurant_websites_domain'
);
SET @ddl := IF(
  @restaurant_websites_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE restaurant_websites ADD KEY idx_restaurant_websites_domain (domain)',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @idx_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_websites' AND INDEX_NAME = 'idx_restaurant_websites_status'
);
SET @ddl := IF(
  @restaurant_websites_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE restaurant_websites ADD KEY idx_restaurant_websites_status (domain_status, is_published)',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- Ensure restaurant_areas required columns/indexes exist.
SET @restaurant_areas_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_areas'
);

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_areas' AND COLUMN_NAME = 'restaurant_id'
);
SET @ddl := IF(
  @restaurant_areas_exists = 1 AND @col_exists = 0,
  'ALTER TABLE restaurant_areas ADD COLUMN restaurant_id INT NULL',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_areas' AND COLUMN_NAME = 'name'
);
SET @ddl := IF(
  @restaurant_areas_exists = 1 AND @col_exists = 0,
  'ALTER TABLE restaurant_areas ADD COLUMN name VARCHAR(120) NULL',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_areas' AND COLUMN_NAME = 'display_order'
);
SET @ddl := IF(
  @restaurant_areas_exists = 1 AND @col_exists = 0,
  'ALTER TABLE restaurant_areas ADD COLUMN display_order INT NOT NULL DEFAULT 0',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_areas' AND COLUMN_NAME = 'is_active'
);
SET @ddl := IF(
  @restaurant_areas_exists = 1 AND @col_exists = 0,
  'ALTER TABLE restaurant_areas ADD COLUMN is_active TINYINT(1) NOT NULL DEFAULT 1',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_areas' AND COLUMN_NAME = 'created_at'
);
SET @ddl := IF(
  @restaurant_areas_exists = 1 AND @col_exists = 0,
  'ALTER TABLE restaurant_areas ADD COLUMN created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_areas' AND COLUMN_NAME = 'updated_at'
);
SET @ddl := IF(
  @restaurant_areas_exists = 1 AND @col_exists = 0,
  'ALTER TABLE restaurant_areas ADD COLUMN updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @idx_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_areas' AND INDEX_NAME = 'uniq_restaurant_areas_restaurant_name'
);
SET @ddl := IF(
  @restaurant_areas_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE restaurant_areas ADD UNIQUE KEY uniq_restaurant_areas_restaurant_name (restaurant_id, name)',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @idx_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_areas' AND INDEX_NAME = 'idx_restaurant_areas_restaurant'
);
SET @ddl := IF(
  @restaurant_areas_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE restaurant_areas ADD KEY idx_restaurant_areas_restaurant (restaurant_id, is_active, display_order)',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- Ensure restaurant_tables required columns/indexes exist.
SET @restaurant_tables_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_tables'
);

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_tables' AND COLUMN_NAME = 'restaurant_id'
);
SET @ddl := IF(
  @restaurant_tables_exists = 1 AND @col_exists = 0,
  'ALTER TABLE restaurant_tables ADD COLUMN restaurant_id INT NULL',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_tables' AND COLUMN_NAME = 'area_id'
);
SET @ddl := IF(
  @restaurant_tables_exists = 1 AND @col_exists = 0,
  'ALTER TABLE restaurant_tables ADD COLUMN area_id BIGINT NULL',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_tables' AND COLUMN_NAME = 'name'
);
SET @ddl := IF(
  @restaurant_tables_exists = 1 AND @col_exists = 0,
  'ALTER TABLE restaurant_tables ADD COLUMN name VARCHAR(120) NULL',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_tables' AND COLUMN_NAME = 'capacity'
);
SET @ddl := IF(
  @restaurant_tables_exists = 1 AND @col_exists = 0,
  'ALTER TABLE restaurant_tables ADD COLUMN capacity INT NOT NULL DEFAULT 0',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_tables' AND COLUMN_NAME = 'display_order'
);
SET @ddl := IF(
  @restaurant_tables_exists = 1 AND @col_exists = 0,
  'ALTER TABLE restaurant_tables ADD COLUMN display_order INT NOT NULL DEFAULT 0',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_tables' AND COLUMN_NAME = 'is_active'
);
SET @ddl := IF(
  @restaurant_tables_exists = 1 AND @col_exists = 0,
  'ALTER TABLE restaurant_tables ADD COLUMN is_active TINYINT(1) NOT NULL DEFAULT 1',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_tables' AND COLUMN_NAME = 'created_at'
);
SET @ddl := IF(
  @restaurant_tables_exists = 1 AND @col_exists = 0,
  'ALTER TABLE restaurant_tables ADD COLUMN created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_tables' AND COLUMN_NAME = 'updated_at'
);
SET @ddl := IF(
  @restaurant_tables_exists = 1 AND @col_exists = 0,
  'ALTER TABLE restaurant_tables ADD COLUMN updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @idx_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_tables' AND INDEX_NAME = 'uniq_restaurant_tables_restaurant_name'
);
SET @ddl := IF(
  @restaurant_tables_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE restaurant_tables ADD UNIQUE KEY uniq_restaurant_tables_restaurant_name (restaurant_id, name)',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @idx_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_tables' AND INDEX_NAME = 'idx_restaurant_tables_restaurant'
);
SET @ddl := IF(
  @restaurant_tables_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE restaurant_tables ADD KEY idx_restaurant_tables_restaurant (restaurant_id, is_active, display_order)',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @idx_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_tables' AND INDEX_NAME = 'idx_restaurant_tables_area'
);
SET @ddl := IF(
  @restaurant_tables_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE restaurant_tables ADD KEY idx_restaurant_tables_area (area_id)',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- Ensure recurring_invoices required columns/indexes exist.
SET @recurring_invoices_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'recurring_invoices'
);

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'recurring_invoices' AND COLUMN_NAME = 'restaurant_id'
);
SET @ddl := IF(
  @recurring_invoices_exists = 1 AND @col_exists = 0,
  'ALTER TABLE recurring_invoices ADD COLUMN restaurant_id INT NULL',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'recurring_invoices' AND COLUMN_NAME = 'feature_key'
);
SET @ddl := IF(
  @recurring_invoices_exists = 1 AND @col_exists = 0,
  'ALTER TABLE recurring_invoices ADD COLUMN feature_key VARCHAR(64) NULL',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'recurring_invoices' AND COLUMN_NAME = 'concept'
);
SET @ddl := IF(
  @recurring_invoices_exists = 1 AND @col_exists = 0,
  'ALTER TABLE recurring_invoices ADD COLUMN concept VARCHAR(255) NULL',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'recurring_invoices' AND COLUMN_NAME = 'amount'
);
SET @ddl := IF(
  @recurring_invoices_exists = 1 AND @col_exists = 0,
  'ALTER TABLE recurring_invoices ADD COLUMN amount DECIMAL(12,2) NOT NULL DEFAULT 0.00',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'recurring_invoices' AND COLUMN_NAME = 'currency'
);
SET @ddl := IF(
  @recurring_invoices_exists = 1 AND @col_exists = 0,
  'ALTER TABLE recurring_invoices ADD COLUMN currency CHAR(3) NOT NULL DEFAULT ''EUR''',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'recurring_invoices' AND COLUMN_NAME = 'frequency'
);
SET @ddl := IF(
  @recurring_invoices_exists = 1 AND @col_exists = 0,
  'ALTER TABLE recurring_invoices ADD COLUMN frequency VARCHAR(32) NULL',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'recurring_invoices' AND COLUMN_NAME = 'interval_count'
);
SET @ddl := IF(
  @recurring_invoices_exists = 1 AND @col_exists = 0,
  'ALTER TABLE recurring_invoices ADD COLUMN interval_count INT NOT NULL DEFAULT 1',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'recurring_invoices' AND COLUMN_NAME = 'next_run_at'
);
SET @ddl := IF(
  @recurring_invoices_exists = 1 AND @col_exists = 0,
  'ALTER TABLE recurring_invoices ADD COLUMN next_run_at DATETIME NULL',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'recurring_invoices' AND COLUMN_NAME = 'is_active'
);
SET @ddl := IF(
  @recurring_invoices_exists = 1 AND @col_exists = 0,
  'ALTER TABLE recurring_invoices ADD COLUMN is_active TINYINT(1) NOT NULL DEFAULT 1',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'recurring_invoices' AND COLUMN_NAME = 'metadata_json'
);
SET @ddl := IF(
  @recurring_invoices_exists = 1 AND @col_exists = 0,
  'ALTER TABLE recurring_invoices ADD COLUMN metadata_json JSON NULL',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'recurring_invoices' AND COLUMN_NAME = 'created_at'
);
SET @ddl := IF(
  @recurring_invoices_exists = 1 AND @col_exists = 0,
  'ALTER TABLE recurring_invoices ADD COLUMN created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'recurring_invoices' AND COLUMN_NAME = 'updated_at'
);
SET @ddl := IF(
  @recurring_invoices_exists = 1 AND @col_exists = 0,
  'ALTER TABLE recurring_invoices ADD COLUMN updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @idx_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'recurring_invoices' AND INDEX_NAME = 'idx_recurring_invoices_restaurant_next'
);
SET @ddl := IF(
  @recurring_invoices_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE recurring_invoices ADD KEY idx_recurring_invoices_restaurant_next (restaurant_id, is_active, next_run_at)',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @idx_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'recurring_invoices' AND INDEX_NAME = 'idx_recurring_invoices_feature'
);
SET @ddl := IF(
  @recurring_invoices_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE recurring_invoices ADD KEY idx_recurring_invoices_feature (feature_key)',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- restaurant_members.whatsapp_number
SET @restaurant_members_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_members'
);

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_members' AND COLUMN_NAME = 'whatsapp_number'
);
SET @ddl := IF(
  @restaurant_members_exists = 1 AND @col_exists = 0,
  'ALTER TABLE restaurant_members ADD COLUMN whatsapp_number VARCHAR(32) NULL',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- restaurant_domains Cloudflare metadata columns/indexes.
SET @restaurant_domains_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_domains'
);

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_domains' AND COLUMN_NAME = 'cf_domain_id'
);
SET @ddl := IF(
  @restaurant_domains_exists = 1 AND @col_exists = 0,
  'ALTER TABLE restaurant_domains ADD COLUMN cf_domain_id VARCHAR(128) NULL',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_domains' AND COLUMN_NAME = 'cf_zone_id'
);
SET @ddl := IF(
  @restaurant_domains_exists = 1 AND @col_exists = 0,
  'ALTER TABLE restaurant_domains ADD COLUMN cf_zone_id VARCHAR(128) NULL',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_domains' AND COLUMN_NAME = 'registration_status'
);
SET @ddl := IF(
  @restaurant_domains_exists = 1 AND @col_exists = 0,
  'ALTER TABLE restaurant_domains ADD COLUMN registration_status VARCHAR(32) NOT NULL DEFAULT ''pending''',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @idx_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_domains' AND INDEX_NAME = 'idx_restaurant_domains_cf_domain_id'
);
SET @ddl := IF(
  @restaurant_domains_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE restaurant_domains ADD KEY idx_restaurant_domains_cf_domain_id (cf_domain_id)',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @idx_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_domains' AND INDEX_NAME = 'idx_restaurant_domains_cf_zone_id'
);
SET @ddl := IF(
  @restaurant_domains_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE restaurant_domains ADD KEY idx_restaurant_domains_cf_zone_id (cf_zone_id)',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @idx_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_domains' AND INDEX_NAME = 'idx_restaurant_domains_registration_status'
);
SET @ddl := IF(
  @restaurant_domains_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE restaurant_domains ADD KEY idx_restaurant_domains_registration_status (registration_status)',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;
