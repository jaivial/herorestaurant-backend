-- Menu weekly availability + booking group-menu assignment flag.
--
-- Two concerns live here because they ship together as one feature:
--   1. `menu_weekday_availability` stores, per menu and per weekday, whether
--      that menu can be served. The backoffice editor writes it over the
--      group-menus-v2 WebSocket; the WhatsApp bot reads it to resolve which
--      menu is served by default for a booking's weekday.
--   2. `bookings.menu_de_grupo_assigned` records whether a reservation was
--      booked with a menu de grupo (in addition to the existing
--      `menu_de_grupo_id`), so the bot can decide between the assigned menu
--      and the default "menú cerrado convencional" menus for that weekday.
--
-- Every step is guarded with INFORMATION_SCHEMA + PREPARE/EXECUTE so the
-- migration is a no-op on already-conformant databases.

-- --- bookings.menu_de_grupo_id (legacy column, may be absent on fresh DBs) --
SET @bookings_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bookings'
);
SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bookings' AND COLUMN_NAME = 'menu_de_grupo_id'
);
SET @ddl := IF(@bookings_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `bookings` ADD COLUMN `menu_de_grupo_id` INT NULL DEFAULT NULL',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- --- bookings.menu_de_grupo_assigned (new explicit boolean) ---
SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bookings' AND COLUMN_NAME = 'menu_de_grupo_assigned'
);
SET @ddl := IF(@bookings_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `bookings` ADD COLUMN `menu_de_grupo_assigned` TINYINT(1) NOT NULL DEFAULT 0',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- Backfill the flag for reservations that already carry an assigned menu.
SET @backfill := IF(@bookings_exists = 1,
  'UPDATE `bookings` SET `menu_de_grupo_assigned` = 1 WHERE `menu_de_grupo_id` IS NOT NULL AND `menu_de_grupo_id` > 0',
  'SELECT 1');
PREPARE stmt FROM @backfill; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- --- menu_weekday_availability ---
CREATE TABLE IF NOT EXISTS `menu_weekday_availability` (
  `id` BIGINT NOT NULL AUTO_INCREMENT,
  `restaurant_id` INT NOT NULL,
  `menu_id` INT NOT NULL,
  `weekday` VARCHAR(10) NOT NULL,
  `available` TINYINT(1) NOT NULL DEFAULT 0,
  `created_at` TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_menu_weekday_availability` (`restaurant_id`, `menu_id`, `weekday`),
  KEY `idx_menu_weekday_availability_lookup` (`restaurant_id`, `weekday`, `available`),
  KEY `idx_menu_weekday_availability_menu` (`restaurant_id`, `menu_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- FK to `menus` only when that table exists (fresh installs create it first).
SET @menus_table_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menus'
);
SET @fk_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menu_weekday_availability'
    AND CONSTRAINT_NAME = 'fk_menu_weekday_availability_menu'
);
SET @ddl := IF(@menus_table_exists = 1 AND @fk_exists = 0,
  'ALTER TABLE `menu_weekday_availability` ADD CONSTRAINT `fk_menu_weekday_availability_menu` FOREIGN KEY (`menu_id`) REFERENCES `menus`(`id`) ON DELETE CASCADE',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;
