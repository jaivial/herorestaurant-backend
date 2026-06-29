-- Manual migration bundle: legacy dump -> current backend schema.
-- Date: 2026-02-16
-- Target DB: villacarmen (MySQL 8+)
-- Properties: idempotent for legacy and partially migrated states.

SET NAMES utf8mb4;

-- -----------------------------------------------------------------------------
-- 0) Migration registry
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS schema_migrations (
  id VARCHAR(255) NOT NULL,
  applied_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- -----------------------------------------------------------------------------
-- 1) Backoffice auth + restaurants
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS restaurants (
  id INT NOT NULL AUTO_INCREMENT,
  slug VARCHAR(64) NOT NULL,
  name VARCHAR(255) NOT NULL,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uniq_restaurants_slug (slug)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT INTO restaurants (id, slug, name)
VALUES (1, 'villacarmen', 'Alqueria Villa Carmen')
ON DUPLICATE KEY UPDATE
  slug = VALUES(slug),
  name = VALUES(name);

CREATE TABLE IF NOT EXISTS bo_users (
  id INT NOT NULL AUTO_INCREMENT,
  email VARCHAR(255) NOT NULL,
  name VARCHAR(255) NOT NULL,
  password_hash VARCHAR(255) NOT NULL,
  is_superadmin TINYINT(1) NOT NULL DEFAULT 0,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uniq_bo_users_email (email)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS bo_user_restaurants (
  user_id INT NOT NULL,
  restaurant_id INT NOT NULL,
  role VARCHAR(32) NOT NULL DEFAULT 'admin',
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (user_id, restaurant_id),
  KEY idx_bo_user_restaurants_restaurant (restaurant_id),
  CONSTRAINT fk_bo_user_restaurants_user FOREIGN KEY (user_id) REFERENCES bo_users(id) ON DELETE CASCADE,
  CONSTRAINT fk_bo_user_restaurants_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS bo_sessions (
  id BIGINT NOT NULL AUTO_INCREMENT,
  token_sha256 CHAR(64) NOT NULL,
  user_id INT NOT NULL,
  active_restaurant_id INT NOT NULL,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  last_seen_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at DATETIME NOT NULL,
  ip VARCHAR(64) DEFAULT NULL,
  user_agent VARCHAR(255) DEFAULT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uniq_bo_sessions_token (token_sha256),
  KEY idx_bo_sessions_user (user_id),
  KEY idx_bo_sessions_expires (expires_at),
  CONSTRAINT fk_bo_sessions_user FOREIGN KEY (user_id) REFERENCES bo_users(id) ON DELETE CASCADE,
  CONSTRAINT fk_bo_sessions_active_restaurant FOREIGN KEY (active_restaurant_id) REFERENCES restaurants(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- -----------------------------------------------------------------------------
-- 2) Multitenant restaurant_id + indexes on legacy tables
-- -----------------------------------------------------------------------------

-- DIA
SET @tbl_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'DIA'
);
SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'DIA' AND COLUMN_NAME = 'restaurant_id'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `DIA` ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @ddl := IF(
  @tbl_exists = 1,
  'UPDATE `DIA` SET `restaurant_id` = 1 WHERE `restaurant_id` IS NULL OR `restaurant_id` <= 0',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @idx_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'DIA' AND INDEX_NAME = 'idx_DIA_restaurant_tipo_active'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE `DIA` ADD KEY `idx_DIA_restaurant_tipo_active` (`restaurant_id`, `TIPO`, `active`)',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- FINDE
SET @tbl_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'FINDE'
);
SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'FINDE' AND COLUMN_NAME = 'restaurant_id'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `FINDE` ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @ddl := IF(
  @tbl_exists = 1,
  'UPDATE `FINDE` SET `restaurant_id` = 1 WHERE `restaurant_id` IS NULL OR `restaurant_id` <= 0',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @idx_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'FINDE' AND INDEX_NAME = 'idx_FINDE_restaurant_tipo_active'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE `FINDE` ADD KEY `idx_FINDE_restaurant_tipo_active` (`restaurant_id`, `TIPO`, `active`)',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- POSTRES
SET @tbl_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'POSTRES'
);
SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'POSTRES' AND COLUMN_NAME = 'restaurant_id'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `POSTRES` ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @ddl := IF(
  @tbl_exists = 1,
  'UPDATE `POSTRES` SET `restaurant_id` = 1 WHERE `restaurant_id` IS NULL OR `restaurant_id` <= 0',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @idx_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'POSTRES' AND INDEX_NAME = 'idx_POSTRES_restaurant_active'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE `POSTRES` ADD KEY `idx_POSTRES_restaurant_active` (`restaurant_id`, `active`)',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- VINOS
SET @tbl_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'VINOS'
);
SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'VINOS' AND COLUMN_NAME = 'restaurant_id'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `VINOS` ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @ddl := IF(
  @tbl_exists = 1,
  'UPDATE `VINOS` SET `restaurant_id` = 1 WHERE `restaurant_id` IS NULL OR `restaurant_id` <= 0',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @idx_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'VINOS' AND INDEX_NAME = 'idx_VINOS_restaurant_tipo_active'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE `VINOS` ADD KEY `idx_VINOS_restaurant_tipo_active` (`restaurant_id`, `tipo`, `active`)',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- bookings
SET @tbl_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bookings'
);
SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bookings' AND COLUMN_NAME = 'restaurant_id'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `bookings` ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @ddl := IF(
  @tbl_exists = 1,
  'UPDATE `bookings` SET `restaurant_id` = 1 WHERE `restaurant_id` IS NULL OR `restaurant_id` <= 0',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @idx_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bookings' AND INDEX_NAME = 'idx_bookings_restaurant_date_status'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE `bookings` ADD KEY `idx_bookings_restaurant_date_status` (`restaurant_id`, `reservation_date`, `status`)',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- cancelled_bookings
SET @tbl_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'cancelled_bookings'
);
SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'cancelled_bookings' AND COLUMN_NAME = 'restaurant_id'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `cancelled_bookings` ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @ddl := IF(
  @tbl_exists = 1,
  'UPDATE `cancelled_bookings` SET `restaurant_id` = 1 WHERE `restaurant_id` IS NULL OR `restaurant_id` <= 0',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @idx_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'cancelled_bookings' AND INDEX_NAME = 'idx_cancelled_restaurant_reservation_date'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE `cancelled_bookings` ADD KEY `idx_cancelled_restaurant_reservation_date` (`restaurant_id`, `reservation_date`)',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- menu_visibility
SET @tbl_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menu_visibility'
);
SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menu_visibility' AND COLUMN_NAME = 'restaurant_id'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `menu_visibility` ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @ddl := IF(
  @tbl_exists = 1,
  'UPDATE `menu_visibility` SET `restaurant_id` = 1 WHERE `restaurant_id` IS NULL OR `restaurant_id` <= 0',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @old_idx_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menu_visibility' AND INDEX_NAME = 'menu_key'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @old_idx_exists > 0,
  'ALTER TABLE `menu_visibility` DROP INDEX `menu_key`',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @idx_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menu_visibility' AND INDEX_NAME = 'uniq_menu_visibility_restaurant_key'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE `menu_visibility` ADD UNIQUE KEY `uniq_menu_visibility_restaurant_key` (`restaurant_id`, `menu_key`)',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- menusDeGrupos
SET @tbl_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menusDeGrupos'
);
SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menusDeGrupos' AND COLUMN_NAME = 'restaurant_id'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `menusDeGrupos` ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @ddl := IF(
  @tbl_exists = 1,
  'UPDATE `menusDeGrupos` SET `restaurant_id` = 1 WHERE `restaurant_id` IS NULL OR `restaurant_id` <= 0',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @idx_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menusDeGrupos' AND INDEX_NAME = 'idx_menusDeGrupos_restaurant_active'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE `menusDeGrupos` ADD KEY `idx_menusDeGrupos_restaurant_active` (`restaurant_id`, `active`)',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- daily_limits
SET @tbl_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'daily_limits'
);
SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'daily_limits' AND COLUMN_NAME = 'restaurant_id'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `daily_limits` ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @ddl := IF(
  @tbl_exists = 1,
  'UPDATE `daily_limits` SET `restaurant_id` = 1 WHERE `restaurant_id` IS NULL OR `restaurant_id` <= 0',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @old_idx_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'daily_limits' AND INDEX_NAME = 'date'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @old_idx_exists > 0,
  'ALTER TABLE `daily_limits` DROP INDEX `date`',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @idx_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'daily_limits' AND INDEX_NAME = 'uniq_daily_limits_restaurant_date'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE `daily_limits` ADD UNIQUE KEY `uniq_daily_limits_restaurant_date` (`restaurant_id`, `date`)',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- hour_configuration
SET @tbl_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'hour_configuration'
);
SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'hour_configuration' AND COLUMN_NAME = 'restaurant_id'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `hour_configuration` ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @ddl := IF(
  @tbl_exists = 1,
  'UPDATE `hour_configuration` SET `restaurant_id` = 1 WHERE `restaurant_id` IS NULL OR `restaurant_id` <= 0',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @old_idx_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'hour_configuration' AND INDEX_NAME = 'date'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @old_idx_exists > 0,
  'ALTER TABLE `hour_configuration` DROP INDEX `date`',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @idx_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'hour_configuration' AND INDEX_NAME = 'uniq_hour_configuration_restaurant_date'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE `hour_configuration` ADD UNIQUE KEY `uniq_hour_configuration_restaurant_date` (`restaurant_id`, `date`)',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @idx_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'hour_configuration' AND INDEX_NAME = 'idx_hour_configuration_restaurant_date'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE `hour_configuration` ADD KEY `idx_hour_configuration_restaurant_date` (`restaurant_id`, `date`)',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- hours_percentage
SET @tbl_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'hours_percentage'
);
SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'hours_percentage' AND COLUMN_NAME = 'restaurant_id'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `hours_percentage` ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @ddl := IF(
  @tbl_exists = 1,
  'UPDATE `hours_percentage` SET `restaurant_id` = 1 WHERE `restaurant_id` IS NULL OR `restaurant_id` <= 0',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @old_idx_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'hours_percentage' AND INDEX_NAME = 'reservationDate'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @old_idx_exists > 0,
  'ALTER TABLE `hours_percentage` DROP INDEX `reservationDate`',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @idx_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'hours_percentage' AND INDEX_NAME = 'uniq_hours_percentage_restaurant_date'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE `hours_percentage` ADD UNIQUE KEY `uniq_hours_percentage_restaurant_date` (`restaurant_id`, `reservationDate`)',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @idx_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'hours_percentage' AND INDEX_NAME = 'idx_hours_percentage_restaurant_date'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE `hours_percentage` ADD KEY `idx_hours_percentage_restaurant_date` (`restaurant_id`, `reservationDate`)',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- mesas_de_dos
SET @tbl_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'mesas_de_dos'
);
SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'mesas_de_dos' AND COLUMN_NAME = 'restaurant_id'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `mesas_de_dos` ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @ddl := IF(
  @tbl_exists = 1,
  'UPDATE `mesas_de_dos` SET `restaurant_id` = 1 WHERE `restaurant_id` IS NULL OR `restaurant_id` <= 0',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @old_idx_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'mesas_de_dos' AND INDEX_NAME = 'reservationDate'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @old_idx_exists > 0,
  'ALTER TABLE `mesas_de_dos` DROP INDEX `reservationDate`',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @idx_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'mesas_de_dos' AND INDEX_NAME = 'uniq_mesas_de_dos_restaurant_date'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE `mesas_de_dos` ADD UNIQUE KEY `uniq_mesas_de_dos_restaurant_date` (`restaurant_id`, `reservationDate`)',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- openinghours
SET @tbl_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'openinghours'
);
SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'openinghours' AND COLUMN_NAME = 'restaurant_id'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `openinghours` ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @ddl := IF(
  @tbl_exists = 1,
  'UPDATE `openinghours` SET `restaurant_id` = 1 WHERE `restaurant_id` IS NULL OR `restaurant_id` <= 0',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @old_idx_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'openinghours' AND INDEX_NAME = 'unique_date'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @old_idx_exists > 0,
  'ALTER TABLE `openinghours` DROP INDEX `unique_date`',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @idx_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'openinghours' AND INDEX_NAME = 'uniq_openinghours_restaurant_date'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE `openinghours` ADD UNIQUE KEY `uniq_openinghours_restaurant_date` (`restaurant_id`, `dateselected`)',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- reservation_manager
SET @tbl_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'reservation_manager'
);
SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'reservation_manager' AND COLUMN_NAME = 'restaurant_id'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `reservation_manager` ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @ddl := IF(
  @tbl_exists = 1,
  'UPDATE `reservation_manager` SET `restaurant_id` = 1 WHERE `restaurant_id` IS NULL OR `restaurant_id` <= 0',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @idx_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'reservation_manager' AND INDEX_NAME = 'idx_reservation_manager_restaurant_date'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE `reservation_manager` ADD KEY `idx_reservation_manager_restaurant_date` (`restaurant_id`, `reservationDate`)',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- restaurant_days
SET @tbl_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_days'
);
SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_days' AND COLUMN_NAME = 'restaurant_id'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `restaurant_days` ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @ddl := IF(
  @tbl_exists = 1,
  'UPDATE `restaurant_days` SET `restaurant_id` = 1 WHERE `restaurant_id` IS NULL OR `restaurant_id` <= 0',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @old_idx_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_days' AND INDEX_NAME = 'date'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @old_idx_exists > 0,
  'ALTER TABLE `restaurant_days` DROP INDEX `date`',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @idx_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_days' AND INDEX_NAME = 'uniq_restaurant_days_restaurant_date'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE `restaurant_days` ADD UNIQUE KEY `uniq_restaurant_days_restaurant_date` (`restaurant_id`, `date`)',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- salon_condesa
SET @tbl_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'salon_condesa'
);
SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'salon_condesa' AND COLUMN_NAME = 'restaurant_id'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `salon_condesa` ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @ddl := IF(
  @tbl_exists = 1,
  'UPDATE `salon_condesa` SET `restaurant_id` = 1 WHERE `restaurant_id` IS NULL OR `restaurant_id` <= 0',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @old_idx_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'salon_condesa' AND INDEX_NAME = 'date'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @old_idx_exists > 0,
  'ALTER TABLE `salon_condesa` DROP INDEX `date`',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @idx_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'salon_condesa' AND INDEX_NAME = 'uniq_salon_condesa_restaurant_date'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE `salon_condesa` ADD UNIQUE KEY `uniq_salon_condesa_restaurant_date` (`restaurant_id`, `date`)',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- bot_conversation_messages
SET @tbl_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bot_conversation_messages'
);
SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bot_conversation_messages' AND COLUMN_NAME = 'restaurant_id'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `bot_conversation_messages` ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @ddl := IF(
  @tbl_exists = 1,
  'UPDATE `bot_conversation_messages` SET `restaurant_id` = 1 WHERE `restaurant_id` IS NULL OR `restaurant_id` <= 0',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @idx_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bot_conversation_messages' AND INDEX_NAME = 'idx_bot_conv_restaurant_phone_timestamp'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE `bot_conversation_messages` ADD KEY `idx_bot_conv_restaurant_phone_timestamp` (`restaurant_id`, `phone_number`, `timestamp`)',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- conversation_messages
SET @tbl_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'conversation_messages'
);
SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'conversation_messages' AND COLUMN_NAME = 'restaurant_id'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `conversation_messages` ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @ddl := IF(
  @tbl_exists = 1,
  'UPDATE `conversation_messages` SET `restaurant_id` = 1 WHERE `restaurant_id` IS NULL OR `restaurant_id` <= 0',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @idx_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'conversation_messages' AND INDEX_NAME = 'idx_conv_messages_restaurant_sender_created'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE `conversation_messages` ADD KEY `idx_conv_messages_restaurant_sender_created` (`restaurant_id`, `sender_number`, `created_at`)',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- conversation_sessions
SET @tbl_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'conversation_sessions'
);
SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'conversation_sessions' AND COLUMN_NAME = 'restaurant_id'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `conversation_sessions` ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @ddl := IF(
  @tbl_exists = 1,
  'UPDATE `conversation_sessions` SET `restaurant_id` = 1 WHERE `restaurant_id` IS NULL OR `restaurant_id` <= 0',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @idx_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'conversation_sessions' AND INDEX_NAME = 'idx_conv_sessions_restaurant_sender'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE `conversation_sessions` ADD KEY `idx_conv_sessions_restaurant_sender` (`restaurant_id`, `sender_number`, `status`)',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- conversation_states
SET @tbl_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'conversation_states'
);
SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'conversation_states' AND COLUMN_NAME = 'restaurant_id'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `conversation_states` ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @ddl := IF(
  @tbl_exists = 1,
  'UPDATE `conversation_states` SET `restaurant_id` = 1 WHERE `restaurant_id` IS NULL OR `restaurant_id` <= 0',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
SET @idx_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'conversation_states' AND INDEX_NAME = 'idx_conv_states_restaurant_sender_state'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE `conversation_states` ADD KEY `idx_conv_states_restaurant_sender_state` (`restaurant_id`, `sender_number`, `conversation_state`)',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- -----------------------------------------------------------------------------
-- 3) Restaurant domains/integrations + delivery and audit tables
-- -----------------------------------------------------------------------------
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

-- -----------------------------------------------------------------------------
-- 4) Notification recipients
-- -----------------------------------------------------------------------------
SET @tbl_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_integrations'
);
SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_integrations' AND COLUMN_NAME = 'restaurant_whatsapp_numbers_json'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `restaurant_integrations` ADD COLUMN `restaurant_whatsapp_numbers_json` JSON DEFAULT NULL AFTER `uazapi_token`',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

INSERT INTO restaurant_integrations (restaurant_id, restaurant_whatsapp_numbers_json)
VALUES (1, JSON_ARRAY('34692747052', '34638857294', '34686969914'))
ON DUPLICATE KEY UPDATE
  restaurant_whatsapp_numbers_json = IFNULL(restaurant_whatsapp_numbers_json, VALUES(restaurant_whatsapp_numbers_json));

-- -----------------------------------------------------------------------------
-- 5) VINOS.foto_path
-- -----------------------------------------------------------------------------
SET @tbl_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'VINOS'
);
SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'VINOS' AND COLUMN_NAME = 'foto_path'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `VINOS` ADD COLUMN `foto_path` VARCHAR(512) NULL',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- -----------------------------------------------------------------------------
-- 6) bookings phone country code + children
-- -----------------------------------------------------------------------------
SET @tbl_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bookings'
);
SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bookings' AND COLUMN_NAME = 'reservation_date'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @col_exists = 1,
  'UPDATE `bookings` SET `reservation_date` = ''1970-01-01'' WHERE `reservation_date` < ''1000-01-01''',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bookings' AND COLUMN_NAME = 'contact_phone'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @col_exists = 1,
  'ALTER TABLE `bookings` MODIFY COLUMN `contact_phone` VARCHAR(32) NULL',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bookings' AND COLUMN_NAME = 'contact_phone_country_code'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `bookings` ADD COLUMN `contact_phone_country_code` VARCHAR(8) NOT NULL DEFAULT ''34'' AFTER `contact_phone`',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bookings' AND COLUMN_NAME = 'children'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `bookings` ADD COLUMN `children` INT NOT NULL DEFAULT 0 AFTER `party_size`',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bookings' AND COLUMN_NAME = 'contact_phone_country_code'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @col_exists = 1,
  'UPDATE `bookings` SET `contact_phone_country_code` = ''34'' WHERE (`contact_phone_country_code` IS NULL OR `contact_phone_country_code` = '''') AND `contact_phone` IS NOT NULL AND `contact_phone` <> ''''',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- -----------------------------------------------------------------------------
-- 7) Reservation defaults and floors
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS restaurant_reservation_defaults (
  restaurant_id INT NOT NULL,
  opening_mode VARCHAR(16) NOT NULL DEFAULT 'both',
  morning_hours_json LONGTEXT NULL,
  night_hours_json LONGTEXT NULL,
  daily_limit INT NOT NULL DEFAULT 45,
  mesas_de_dos_limit VARCHAR(16) NOT NULL DEFAULT '999',
  mesas_de_tres_limit VARCHAR(16) NOT NULL DEFAULT '999',
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (restaurant_id),
  KEY idx_restaurant_reservation_defaults_restaurant (restaurant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS mesas_de_tres (
  id INT NOT NULL AUTO_INCREMENT,
  restaurant_id INT NOT NULL DEFAULT 1,
  reservationDate DATE NOT NULL,
  dailyLimit VARCHAR(16) NOT NULL,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uniq_mesas_de_tres_restaurant_date (restaurant_id, reservationDate),
  KEY idx_mesas_de_tres_restaurant_date (restaurant_id, reservationDate)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS restaurant_floors (
  id INT NOT NULL AUTO_INCREMENT,
  restaurant_id INT NOT NULL,
  floor_number INT NOT NULL,
  floor_name VARCHAR(64) NOT NULL,
  is_ground TINYINT(1) NOT NULL DEFAULT 0,
  is_active TINYINT(1) NOT NULL DEFAULT 1,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uniq_restaurant_floors_restaurant_number (restaurant_id, floor_number),
  KEY idx_restaurant_floors_restaurant (restaurant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS restaurant_floor_overrides (
  restaurant_id INT NOT NULL,
  `date` DATE NOT NULL,
  floor_id INT NOT NULL,
  is_active TINYINT(1) NOT NULL DEFAULT 1,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (restaurant_id, `date`, floor_id),
  KEY idx_restaurant_floor_overrides_restaurant_date (restaurant_id, `date`),
  KEY idx_restaurant_floor_overrides_floor (floor_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT INTO restaurant_reservation_defaults (
  restaurant_id,
  opening_mode,
  morning_hours_json,
  night_hours_json,
  daily_limit,
  mesas_de_dos_limit,
  mesas_de_tres_limit
)
SELECT
  r.id,
  'both',
  JSON_ARRAY(
    '08:00','08:30','09:00','09:30',
    '10:00','10:30','11:00','11:30',
    '12:00','12:30','13:00','13:30',
    '14:00','14:30','15:00','15:30',
    '16:00','16:30'
  ),
  JSON_ARRAY(
    '17:30','18:00','18:30','19:00',
    '19:30','20:00','20:30','21:00',
    '21:30','22:00','22:30','23:00',
    '23:30','00:00','00:30'
  ),
  45,
  '999',
  '999'
FROM restaurants r
ON DUPLICATE KEY UPDATE
  opening_mode = COALESCE(NULLIF(restaurant_reservation_defaults.opening_mode, ''), VALUES(opening_mode)),
  morning_hours_json = COALESCE(restaurant_reservation_defaults.morning_hours_json, VALUES(morning_hours_json)),
  night_hours_json = COALESCE(restaurant_reservation_defaults.night_hours_json, VALUES(night_hours_json)),
  daily_limit = COALESCE(restaurant_reservation_defaults.daily_limit, VALUES(daily_limit)),
  mesas_de_dos_limit = COALESCE(NULLIF(restaurant_reservation_defaults.mesas_de_dos_limit, ''), VALUES(mesas_de_dos_limit)),
  mesas_de_tres_limit = COALESCE(NULLIF(restaurant_reservation_defaults.mesas_de_tres_limit, ''), VALUES(mesas_de_tres_limit));

INSERT INTO restaurant_floors (restaurant_id, floor_number, floor_name, is_ground, is_active)
SELECT r.id, 0, 'Planta baja', 1, 1
FROM restaurants r
ON DUPLICATE KEY UPDATE
  floor_name = VALUES(floor_name),
  is_ground = VALUES(is_ground),
  is_active = VALUES(is_active);

-- -----------------------------------------------------------------------------
-- 8a) Backoffice roles/members/time entries
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS bo_roles (
  slug VARCHAR(32) NOT NULL,
  label VARCHAR(64) NOT NULL,
  sort_order INT NOT NULL DEFAULT 0,
  is_active TINYINT(1) NOT NULL DEFAULT 1,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (slug)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS bo_role_permissions (
  role_slug VARCHAR(32) NOT NULL,
  section_key VARCHAR(32) NOT NULL,
  is_allowed TINYINT(1) NOT NULL DEFAULT 0,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (role_slug, section_key),
  KEY idx_bo_role_permissions_section (section_key),
  CONSTRAINT fk_bo_role_permissions_role FOREIGN KEY (role_slug) REFERENCES bo_roles(slug) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT INTO bo_roles (slug, label, sort_order, is_active) VALUES
  ('admin', 'Admin', 10, 1),
  ('metre', 'Metre', 20, 1),
  ('jefe_cocina', 'Jefe de cocina', 30, 1),
  ('arrocero', 'Arrocero', 40, 1),
  ('pinche_cocina', 'Pinche de cocina', 50, 1),
  ('fregaplatos', 'Fregaplatos', 60, 1),
  ('ayudante_cocina', 'Ayudante de cocina', 70, 1),
  ('camarero', 'Camarero', 80, 1),
  ('responsable_sala', 'Responsable de sala', 90, 1),
  ('ayudante_camarero', 'Ayudante camarero', 100, 1),
  ('runner', 'Runner', 110, 1),
  ('barista', 'Barista', 120, 1)
ON DUPLICATE KEY UPDATE
  label = VALUES(label),
  sort_order = VALUES(sort_order),
  is_active = VALUES(is_active);

INSERT INTO bo_role_permissions (role_slug, section_key, is_allowed) VALUES
  ('admin', 'reservas', 1),
  ('admin', 'menus', 1),
  ('admin', 'ajustes', 1),
  ('admin', 'miembros', 1)
ON DUPLICATE KEY UPDATE
  is_allowed = VALUES(is_allowed);

INSERT INTO bo_role_permissions (role_slug, section_key, is_allowed) VALUES
  ('metre', 'reservas', 1),
  ('metre', 'menus', 1),
  ('jefe_cocina', 'reservas', 1),
  ('jefe_cocina', 'menus', 1)
ON DUPLICATE KEY UPDATE
  is_allowed = VALUES(is_allowed);

INSERT INTO bo_role_permissions (role_slug, section_key, is_allowed) VALUES
  ('arrocero', 'fichaje', 1),
  ('pinche_cocina', 'fichaje', 1),
  ('fregaplatos', 'fichaje', 1),
  ('ayudante_cocina', 'fichaje', 1),
  ('camarero', 'fichaje', 1),
  ('responsable_sala', 'fichaje', 1),
  ('ayudante_camarero', 'fichaje', 1),
  ('runner', 'fichaje', 1),
  ('barista', 'fichaje', 1)
ON DUPLICATE KEY UPDATE
  is_allowed = VALUES(is_allowed);

UPDATE bo_user_restaurants
SET role = LOWER(TRIM(role))
WHERE role IS NOT NULL AND role <> LOWER(TRIM(role));

UPDATE bo_user_restaurants
SET role = 'admin'
WHERE role IS NULL OR TRIM(role) = '' OR role = 'owner';

UPDATE bo_user_restaurants
SET role = 'admin'
WHERE role NOT IN (
  'admin',
  'metre',
  'jefe_cocina',
  'arrocero',
  'pinche_cocina',
  'fregaplatos',
  'ayudante_cocina',
  'camarero',
  'responsable_sala',
  'ayudante_camarero',
  'runner',
  'barista'
);

CREATE TABLE IF NOT EXISTS restaurant_members (
  id INT NOT NULL AUTO_INCREMENT,
  restaurant_id INT NOT NULL,
  bo_user_id INT NULL,
  first_name VARCHAR(128) NOT NULL,
  last_name VARCHAR(128) NOT NULL,
  email VARCHAR(255) NULL,
  dni VARCHAR(32) NULL,
  bank_account VARCHAR(64) NULL,
  phone VARCHAR(32) NULL,
  photo_url VARCHAR(1024) NULL,
  is_active TINYINT(1) NOT NULL DEFAULT 1,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_restaurant_members_restaurant_active (restaurant_id, is_active),
  KEY idx_restaurant_members_restaurant_name (restaurant_id, last_name, first_name),
  UNIQUE KEY uniq_restaurant_members_restaurant_user (restaurant_id, bo_user_id),
  UNIQUE KEY uniq_restaurant_members_restaurant_email (restaurant_id, email),
  CONSTRAINT fk_restaurant_members_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
  CONSTRAINT fk_restaurant_members_bo_user FOREIGN KEY (bo_user_id) REFERENCES bo_users(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS member_contracts (
  id INT NOT NULL AUTO_INCREMENT,
  restaurant_member_id INT NOT NULL,
  restaurant_id INT NOT NULL,
  weekly_hours DECIMAL(6,2) NOT NULL DEFAULT 40.00,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uniq_member_contracts_member (restaurant_member_id),
  KEY idx_member_contracts_restaurant (restaurant_id),
  CONSTRAINT fk_member_contracts_member FOREIGN KEY (restaurant_member_id) REFERENCES restaurant_members(id) ON DELETE CASCADE,
  CONSTRAINT fk_member_contracts_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS member_time_entries (
  id BIGINT NOT NULL AUTO_INCREMENT,
  restaurant_member_id INT NOT NULL,
  restaurant_id INT NOT NULL,
  work_date DATE NOT NULL,
  start_time TIME NULL,
  end_time TIME NULL,
  minutes_worked INT NOT NULL DEFAULT 0,
  source VARCHAR(16) NOT NULL DEFAULT 'manual',
  notes VARCHAR(255) NULL,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_member_time_entries_rest_member_date (restaurant_id, restaurant_member_id, work_date),
  KEY idx_member_time_entries_rest_date (restaurant_id, work_date),
  CONSTRAINT fk_member_time_entries_member FOREIGN KEY (restaurant_member_id) REFERENCES restaurant_members(id) ON DELETE CASCADE,
  CONSTRAINT fk_member_time_entries_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- -----------------------------------------------------------------------------
-- 8b) Group menus v2
-- -----------------------------------------------------------------------------
SET @tbl_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menusDeGrupos'
);
SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'menusDeGrupos'
    AND COLUMN_NAME = 'menu_type'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @col_exists = 0,
  "ALTER TABLE `menusDeGrupos` ADD COLUMN `menu_type` VARCHAR(64) NOT NULL DEFAULT 'closed_conventional'",
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'menusDeGrupos'
    AND COLUMN_NAME = 'is_draft'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @col_exists = 0,
  "ALTER TABLE `menusDeGrupos` ADD COLUMN `is_draft` TINYINT(1) NOT NULL DEFAULT 0",
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'menusDeGrupos'
    AND COLUMN_NAME = 'editor_version'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @col_exists = 0,
  "ALTER TABLE `menusDeGrupos` ADD COLUMN `editor_version` INT NOT NULL DEFAULT 1",
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

CREATE TABLE IF NOT EXISTS menu_dishes_catalog (
  id BIGINT NOT NULL AUTO_INCREMENT,
  restaurant_id INT NOT NULL,
  title VARCHAR(255) NOT NULL,
  description TEXT NULL,
  allergens_json JSON NULL,
  default_supplement_enabled TINYINT(1) NOT NULL DEFAULT 0,
  default_supplement_price DECIMAL(10,2) NULL,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_menu_dishes_catalog_restaurant_title (restaurant_id, title),
  KEY idx_menu_dishes_catalog_restaurant_updated (restaurant_id, updated_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS group_menu_sections_v2 (
  id BIGINT NOT NULL AUTO_INCREMENT,
  restaurant_id INT NOT NULL,
  menu_id INT NOT NULL,
  title VARCHAR(255) NOT NULL,
  section_kind VARCHAR(64) NOT NULL DEFAULT 'custom',
  position INT NOT NULL DEFAULT 0,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_group_menu_sections_v2_menu_position (menu_id, position),
  KEY idx_group_menu_sections_v2_restaurant_menu (restaurant_id, menu_id),
  CONSTRAINT fk_group_menu_sections_v2_menu
    FOREIGN KEY (menu_id) REFERENCES menusDeGrupos(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS group_menu_section_dishes_v2 (
  id BIGINT NOT NULL AUTO_INCREMENT,
  restaurant_id INT NOT NULL,
  menu_id INT NOT NULL,
  section_id BIGINT NOT NULL,
  catalog_dish_id BIGINT NULL,
  title_snapshot VARCHAR(255) NOT NULL,
  description_snapshot TEXT NULL,
  allergens_json JSON NULL,
  supplement_enabled TINYINT(1) NOT NULL DEFAULT 0,
  supplement_price DECIMAL(10,2) NULL,
  active TINYINT(1) NOT NULL DEFAULT 1,
  position INT NOT NULL DEFAULT 0,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_group_menu_section_dishes_v2_section_position (section_id, position),
  KEY idx_group_menu_section_dishes_v2_menu (menu_id),
  KEY idx_group_menu_section_dishes_v2_restaurant (restaurant_id),
  CONSTRAINT fk_group_menu_section_dishes_v2_section
    FOREIGN KEY (section_id) REFERENCES group_menu_sections_v2(id) ON DELETE CASCADE,
  CONSTRAINT fk_group_menu_section_dishes_v2_catalog
    FOREIGN KEY (catalog_dish_id) REFERENCES menu_dishes_catalog(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- -----------------------------------------------------------------------------
-- 9) Fichaje schedules + permissions
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS member_work_schedules (
  id BIGINT NOT NULL AUTO_INCREMENT,
  restaurant_member_id INT NOT NULL,
  restaurant_id INT NOT NULL,
  work_date DATE NOT NULL,
  start_time TIME NOT NULL,
  end_time TIME NOT NULL,
  notes VARCHAR(255) NULL,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uniq_member_work_schedules_rest_member_date (restaurant_id, restaurant_member_id, work_date),
  KEY idx_member_work_schedules_rest_date (restaurant_id, work_date),
  CONSTRAINT fk_member_work_schedules_member FOREIGN KEY (restaurant_member_id) REFERENCES restaurant_members(id) ON DELETE CASCADE,
  CONSTRAINT fk_member_work_schedules_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT INTO bo_role_permissions (role_slug, section_key, is_allowed) VALUES
  ('admin', 'fichaje', 1),
  ('admin', 'horarios', 1),
  ('metre', 'fichaje', 1),
  ('jefe_cocina', 'fichaje', 1)
ON DUPLICATE KEY UPDATE
  is_allowed = VALUES(is_allowed);

-- -----------------------------------------------------------------------------
-- 10) RBAC importance and custom roles
-- -----------------------------------------------------------------------------
SET @tbl_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bo_roles'
);
SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bo_roles' AND COLUMN_NAME = 'importance'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `bo_roles` ADD COLUMN `importance` INT NOT NULL DEFAULT 0 AFTER `sort_order`',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bo_roles' AND COLUMN_NAME = 'icon_key'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `bo_roles` ADD COLUMN `icon_key` VARCHAR(32) NULL AFTER `importance`',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bo_roles' AND COLUMN_NAME = 'is_system'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `bo_roles` ADD COLUMN `is_system` TINYINT(1) NOT NULL DEFAULT 0 AFTER `is_active`',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

INSERT INTO bo_roles (slug, label, sort_order, is_active, importance, icon_key, is_system) VALUES
  ('root', 'Root', 0, 1, 100, 'crown', 1),
  ('admin', 'Admin', 10, 1, 90, 'shield-user', 1),
  ('metre', 'Metre', 20, 1, 75, 'clipboard-list', 1),
  ('jefe_cocina', 'Jefe de cocina', 30, 1, 74, 'chef-hat', 1),
  ('arrocero', 'Arrocero', 40, 1, 60, 'flame', 1),
  ('pinche_cocina', 'Pinche de cocina', 50, 1, 35, 'utensils-crossed', 1),
  ('fregaplatos', 'Fregaplatos', 60, 1, 30, 'droplets', 1),
  ('ayudante_cocina', 'Ayudante de cocina', 70, 1, 40, 'utensils', 1),
  ('camarero', 'Camarero', 80, 1, 58, 'glass-water', 1),
  ('responsable_sala', 'Responsable de sala', 90, 1, 65, 'users-round', 1),
  ('ayudante_camarero', 'Ayudante camarero', 100, 1, 45, 'user-round-plus', 1),
  ('runner', 'Runner', 110, 1, 50, 'route', 1),
  ('barista', 'Barista', 120, 1, 55, 'coffee', 1)
ON DUPLICATE KEY UPDATE
  label = VALUES(label),
  sort_order = VALUES(sort_order),
  is_active = VALUES(is_active),
  importance = VALUES(importance),
  icon_key = VALUES(icon_key),
  is_system = VALUES(is_system);

INSERT INTO bo_role_permissions (role_slug, section_key, is_allowed) VALUES
  ('root', 'reservas', 1),
  ('root', 'menus', 1),
  ('root', 'ajustes', 1),
  ('root', 'miembros', 1),
  ('root', 'fichaje', 1),
  ('root', 'horarios', 1),
  ('admin', 'reservas', 1),
  ('admin', 'menus', 1),
  ('admin', 'ajustes', 1),
  ('admin', 'miembros', 1),
  ('admin', 'fichaje', 1),
  ('admin', 'horarios', 1)
ON DUPLICATE KEY UPDATE
  is_allowed = VALUES(is_allowed);

INSERT INTO bo_role_permissions (role_slug, section_key, is_allowed) VALUES
  ('metre', 'reservas', 1),
  ('metre', 'menus', 1),
  ('metre', 'fichaje', 1),
  ('jefe_cocina', 'reservas', 1),
  ('jefe_cocina', 'menus', 1),
  ('jefe_cocina', 'fichaje', 1)
ON DUPLICATE KEY UPDATE
  is_allowed = VALUES(is_allowed);

INSERT INTO bo_role_permissions (role_slug, section_key, is_allowed) VALUES
  ('arrocero', 'fichaje', 1),
  ('pinche_cocina', 'fichaje', 1),
  ('fregaplatos', 'fichaje', 1),
  ('ayudante_cocina', 'fichaje', 1),
  ('camarero', 'fichaje', 1),
  ('responsable_sala', 'fichaje', 1),
  ('ayudante_camarero', 'fichaje', 1),
  ('runner', 'fichaje', 1),
  ('barista', 'fichaje', 1)
ON DUPLICATE KEY UPDATE
  is_allowed = VALUES(is_allowed);

-- -----------------------------------------------------------------------------
-- 11) Invoices + restaurants.avatar
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS invoices (
  id INT AUTO_INCREMENT PRIMARY KEY,
  restaurant_id INT NOT NULL,

  customer_name VARCHAR(255) NOT NULL,
  customer_surname VARCHAR(255) NULL,
  customer_email VARCHAR(255) NOT NULL,
  customer_dni_cif VARCHAR(32) NULL,
  customer_phone VARCHAR(32) NULL,
  customer_address_street VARCHAR(255) NULL,
  customer_address_number VARCHAR(32) NULL,
  customer_address_postal_code VARCHAR(16) NULL,
  customer_address_city VARCHAR(128) NULL,
  customer_address_province VARCHAR(128) NULL,
  customer_address_country VARCHAR(128) NULL,

  amount DECIMAL(10,2) NOT NULL,
  payment_method ENUM('efectivo', 'tarjeta', 'transferencia', 'bizum', 'cheque') NULL,
  account_image_url VARCHAR(1024) NULL,
  invoice_date DATE NOT NULL,
  payment_date DATE NULL,

  status ENUM('borrador', 'solicitada', 'pendiente', 'enviada') DEFAULT 'borrador',
  is_reservation TINYINT(1) DEFAULT 0,
  reservation_id INT NULL,
  reservation_date DATE NULL,
  reservation_customer_name VARCHAR(255) NULL,
  reservation_party_size INT NULL,

  pdf_url VARCHAR(1024) NULL,

  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,

  FOREIGN KEY (restaurant_id) REFERENCES restaurants(id),
  FOREIGN KEY (reservation_id) REFERENCES bookings(id),
  INDEX idx_invoices_restaurant_date (restaurant_id, invoice_date),
  INDEX idx_invoices_status (restaurant_id, status),
  INDEX idx_invoices_customer_email (restaurant_id, customer_email(255)),
  INDEX idx_invoices_is_reservation (restaurant_id, is_reservation),
  INDEX idx_invoices_reservation_date (restaurant_id, reservation_date)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

SET @tbl_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurants'
);
SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'restaurants'
    AND COLUMN_NAME = 'avatar'
);
SET @ddl := IF(
  @tbl_exists = 1 AND @col_exists = 0,
  'ALTER TABLE restaurants ADD COLUMN avatar VARCHAR(1024) NULL AFTER name',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- -----------------------------------------------------------------------------
-- Finalize: mark bundled migrations as applied.
-- -----------------------------------------------------------------------------
INSERT IGNORE INTO schema_migrations (id) VALUES
  ('001_backoffice_auth.sql'),
  ('002_multitenant_restaurant_id.sql'),
  ('003_restaurant_domains_and_integrations.sql'),
  ('004_restaurant_notification_recipients.sql'),
  ('005_vinos_foto_path.sql'),
  ('006_booking_phone_prefix_and_children.sql'),
  ('007_reservation_defaults_and_floors.sql'),
  ('008_backoffice_roles_members_and_time_entries.sql'),
  ('008_group_menus_v2.sql'),
  ('009_backoffice_fichaje_schedules_and_permissions.sql'),
  ('010_backoffice_roles_importance_and_custom.sql'),
  ('011_invoices.sql');
