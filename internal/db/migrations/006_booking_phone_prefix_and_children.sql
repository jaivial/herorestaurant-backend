-- Bookings: store country calling code separately and keep a children count.
-- Idempotent: only runs if bookings table exists.

SET @table_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bookings');
SET @col_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bookings' AND COLUMN_NAME = 'contact_phone_country_code');

-- Fix zero dates if table exists
SET @fix_dates := IF(@table_exists = 1, 'UPDATE bookings SET reservation_date = ''1970-01-01'' WHERE reservation_date < ''1000-01-01''', 'SELECT 1');
PREPARE stmt FROM @fix_dates; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- Alter table if exists and column doesn't
SET @ddl := IF(@table_exists = 1 AND @col_exists = 0, 'ALTER TABLE bookings MODIFY COLUMN contact_phone VARCHAR(32) NULL, ADD COLUMN contact_phone_country_code VARCHAR(8) NOT NULL DEFAULT ''34'' AFTER contact_phone, ADD COLUMN children INT NOT NULL DEFAULT 0 AFTER party_size', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- Backfill country code if column exists
SET @update := IF(@table_exists = 1 AND @col_exists = 0, 'UPDATE bookings SET contact_phone_country_code = ''34'' WHERE (contact_phone_country_code IS NULL OR contact_phone_country_code = '''') AND contact_phone IS NOT NULL AND contact_phone <> ''''', 'SELECT 1');
PREPARE stmt FROM @update; EXECUTE stmt; DEALLOCATE PREPARE stmt;
