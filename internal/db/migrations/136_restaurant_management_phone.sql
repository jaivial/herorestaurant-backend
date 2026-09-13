-- Per-restaurant phone for restaurant MANAGEMENT (human handoff).
--
-- The WhatsApp bot needs a number that a human actually answers when a same-day
-- booking operation has to be refused (and when a customer asks for a person).
-- Until now it fell back to the restaurant's public phone or, worse, to the
-- bot's own connected WhatsApp number, so customers were told to call the bot.
--
-- It is authored per restaurant from the backoffice
-- /app/config?content=contacto ("Telefono de gestion") and stored next to the
-- public contact data it complements.
--
-- Coordination id: booking_same_day_contact_v1

SET @table_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_info'
);

SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_info'
    AND COLUMN_NAME = 'telefono_gestion'
);

SET @ddl := IF(@table_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `restaurant_info` ADD COLUMN `telefono_gestion` VARCHAR(64) NULL DEFAULT NULL AFTER `telefono`',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;
