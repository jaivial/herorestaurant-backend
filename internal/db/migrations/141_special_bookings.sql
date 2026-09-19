-- Special bookings (reservas especiales): per-date special menu bookings.
--
-- Two new tables + three snapshot columns on `bookings`. The snapshot pattern
-- follows the existing extras_json / principales_json columns: when a booking
-- is created on a date that has a row in `special_dates`, the server copies
-- the date's settings (title, menus, adelanto, window) into `special_json`
-- and stores is_special_booking + is_prereserva as cheap DB-only filters.
--
-- Coordination id: special_dates_v1 (settings) + special_booking_v1 (bookings)
-- + special_booking_politics_v1 (legal). The orchestration SPEC §3 binds the
-- exact column shapes.

-- --- special_dates (per-tenant per-day settings) ---
CREATE TABLE IF NOT EXISTS `special_dates` (
  `id` BIGINT NOT NULL AUTO_INCREMENT,
  `restaurant_id` INT NOT NULL DEFAULT 1,
  `date` DATE NOT NULL,
  `is_active` TINYINT(1) NOT NULL DEFAULT 0,
  `title` VARCHAR(190) NOT NULL DEFAULT '',
  `description` TEXT NULL,
  `prereserva_enabled` TINYINT(1) NOT NULL DEFAULT 0,
  `max_per_table_enabled` TINYINT(1) NOT NULL DEFAULT 0,
  `max_per_table` INT NULL,
  `requires_adelanto` TINYINT(1) NOT NULL DEFAULT 0,
  `adelanto_payment_methods` JSON NULL,
  `adelanto_unified` TINYINT(1) NOT NULL DEFAULT 0,
  `adelanto_unified_amount` DECIMAL(10,2) NULL,
  `prereserva_starts_on` DATE NULL,
  `prereserva_ends_on` DATE NULL,
  `created_at` TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_special_dates_restaurant_date` (`restaurant_id`, `date`),
  KEY `idx_special_dates_restaurant_active` (`restaurant_id`, `is_active`, `date`),
  KEY `idx_special_dates_restaurant_range` (`restaurant_id`, `date`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Conditional FK to restaurants (same pattern as 138/140).
SET @restaurants_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurants'
);
SET @fk_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'special_dates'
    AND CONSTRAINT_NAME = 'fk_special_dates_restaurant'
);
SET @ddl := IF(@restaurants_exists = 1 AND @fk_exists = 0,
  'ALTER TABLE `special_dates` ADD CONSTRAINT `fk_special_dates_restaurant` FOREIGN KEY (`restaurant_id`) REFERENCES `restaurants`(`id`) ON DELETE CASCADE',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- --- special_date_menus (1+ rows per special date; FK cascading) ---
CREATE TABLE IF NOT EXISTS `special_date_menus` (
  `id` BIGINT NOT NULL AUTO_INCREMENT,
  `restaurant_id` INT NOT NULL DEFAULT 1,
  `special_date_id` BIGINT NOT NULL,
  `menu_id` BIGINT NULL,
  `custom_title` VARCHAR(190) NULL,
  `custom_image_url` TEXT NULL,
  `adelanto_amount` DECIMAL(10,2) NULL,
  `position` INT NOT NULL DEFAULT 0,
  `created_at` TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_special_date_menus_special_date` (`restaurant_id`, `special_date_id`, `position`),
  KEY `idx_special_date_menus_menu` (`restaurant_id`, `menu_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- FK special_date_menus.special_date_id -> special_dates.id (CASCADE).
SET @special_dates_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'special_dates'
);
SET @fk_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'special_date_menus'
    AND CONSTRAINT_NAME = 'fk_special_date_menus_special_date'
);
SET @ddl := IF(@special_dates_exists = 1 AND @fk_exists = 0,
  'ALTER TABLE `special_date_menus` ADD CONSTRAINT `fk_special_date_menus_special_date` FOREIGN KEY (`special_date_id`) REFERENCES `special_dates`(`id`) ON DELETE CASCADE',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- Conditional FK to menus (menu_id is NULLable for custom menus).
SET @menus_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menus'
);
SET @fk_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'special_date_menus'
    AND CONSTRAINT_NAME = 'fk_special_date_menus_menu'
);
SET @ddl := IF(@menus_exists = 1 AND @fk_exists = 0,
  'ALTER TABLE `special_date_menus` ADD CONSTRAINT `fk_special_date_menus_menu` FOREIGN KEY (`menu_id`) REFERENCES `menus`(`id`) ON DELETE SET NULL',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- Conditional FK to restaurants.
SET @restaurants_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurants'
);
SET @fk_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'special_date_menus'
    AND CONSTRAINT_NAME = 'fk_special_date_menus_restaurant'
);
SET @ddl := IF(@restaurants_exists = 1 AND @fk_exists = 0,
  'ALTER TABLE `special_date_menus` ADD CONSTRAINT `fk_special_date_menus_restaurant` FOREIGN KEY (`restaurant_id`) REFERENCES `restaurants`(`id`) ON DELETE CASCADE',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- --- bookings: snapshot columns ---
-- `bookings` is a legacy table with no CREATE in migrations; each ALTER is
-- guarded by an INFORMATION_SCHEMA column-existence check so this migration
-- stays idempotent and never breaks an environment where the columns already
-- exist from a prior run.

SET @bookings_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bookings'
);

-- is_special_booking
SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bookings'
    AND COLUMN_NAME = 'is_special_booking'
);
SET @ddl := IF(@bookings_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `bookings` ADD COLUMN `is_special_booking` TINYINT(1) NOT NULL DEFAULT 0 AFTER `special_menu`',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- is_prereserva
SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bookings'
    AND COLUMN_NAME = 'is_prereserva'
);
SET @ddl := IF(@bookings_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `bookings` ADD COLUMN `is_prereserva` TINYINT(1) NOT NULL DEFAULT 0 AFTER `is_special_booking`',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- special_json (MEDIUMTEXT snapshot, NULLable).
SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bookings'
    AND COLUMN_NAME = 'special_json'
);
SET @ddl := IF(@bookings_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `bookings` ADD COLUMN `special_json` MEDIUMTEXT NULL AFTER `is_prereserva`',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- Composite index for the BE-2 grid (restaurant + special-date filters).
SET @idx_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bookings'
    AND INDEX_NAME = 'idx_bookings_special'
);
SET @ddl := IF(@bookings_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE `bookings` ADD INDEX `idx_bookings_special` (`restaurant_id`, `reservation_date`, `is_special_booking`)',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- --- Seed legal page `special-booking-politics` for every existing restaurant ---
-- Default text follows SPEC §3 legal: pago del adelanto antes de 7 días, política
-- de no-show, reembolso total hasta 15 días antes, máximo del 50% después,
-- posibilidad de cambios sujetos a disponibilidad.
SET @restaurants_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurants'
);
SET @seed := IF(@restaurants_exists = 1,
  'INSERT INTO legal_pages (restaurant_id, slug, title, content_json, content_html, updated_by_user_id)
   SELECT r.id, ''special-booking-politics'', ''Políticas de Reservas Especiales'', ''[]'', ''<h2>Políticas de Reservas Especiales</h2><p>Las reservas especiales requieren el pago de un adelanto para confirmar la plaza. El adelanto deber&aacute; abonarse al menos <strong>7 d&iacute;as antes</strong> de la fecha del evento; si no se recibe en plazo, la reserva podr&aacute; ser cancelada o cedida a otra persona.</p><p><strong>Pol&iacute;tica de no-show:</strong> la no presentaci&oacute;n el d&iacute;a de la reserva sin aviso previo se considera cancelaci&oacute;n tard&iacute;a y no genera derecho a reembolso del adelanto.</p><p><strong>Reembolsos:</strong> se realizar&aacute; reembolso total del adelanto cuando la cancelaci&oacute;n se solicite hasta <strong>15 d&iacute;as antes</strong> de la fecha del evento. Pasado ese plazo, el reembolso m&aacute;ximo ser&aacute; del <strong>50%</strong> del importe abonado.</p><p><strong>Cambios:</strong> los cambios de fecha, hora o n&uacute;mero de comensales est&aacute;n sujetos a disponibilidad y deber&aacute;n solicitarse con antelaci&oacute;n suficiente para no perder las condiciones de reembolso.</p>'', NULL
   FROM restaurants r
   WHERE NOT EXISTS (
     SELECT 1 FROM legal_pages lp
     WHERE lp.restaurant_id = r.id AND lp.slug = ''special-booking-politics''
   )',
  'SELECT 1');
PREPARE stmt FROM @seed; EXECUTE stmt; DEALLOCATE PREPARE stmt;
