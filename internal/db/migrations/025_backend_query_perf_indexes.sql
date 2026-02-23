-- Improve read-path performance for high-traffic backoffice/admin queries.
-- Idempotent: each index is added only if missing and table exists.

SET @table_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bookings');
SET @idx_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bookings' AND INDEX_NAME = 'idx_bookings_restaurant_date_time_id');
SET @ddl := IF(@table_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE `bookings` ADD KEY `idx_bookings_restaurant_date_time_id` (`restaurant_id`, `reservation_date`, `reservation_time`, `id`)',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @table_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bookings');
SET @idx_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bookings' AND INDEX_NAME = 'idx_bookings_restaurant_date_added_id');
SET @ddl := IF(@table_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE `bookings` ADD KEY `idx_bookings_restaurant_date_added_id` (`restaurant_id`, `reservation_date`, `added_date`, `id`)',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @table_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bookings');
SET @idx_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bookings' AND INDEX_NAME = 'idx_bookings_restaurant_date_status_added_id');
SET @ddl := IF(@table_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE `bookings` ADD KEY `idx_bookings_restaurant_date_status_added_id` (`restaurant_id`, `reservation_date`, `status`, `added_date`, `id`)',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @table_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bookings');
SET @idx_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bookings' AND INDEX_NAME = 'ft_bookings_customer_contact_commentary');
SET @ddl := IF(@table_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE `bookings` ADD FULLTEXT KEY `ft_bookings_customer_contact_commentary` (`customer_name`, `contact_email`, `commentary`)',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @table_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_days');
SET @idx_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_days' AND INDEX_NAME = 'idx_restaurant_days_restaurant_date');
SET @ddl := IF(@table_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE `restaurant_days` ADD KEY `idx_restaurant_days_restaurant_date` (`restaurant_id`, `date`)',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @table_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'hour_configuration');
SET @idx_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'hour_configuration' AND INDEX_NAME = 'idx_hour_configuration_restaurant_date');
SET @ddl := IF(@table_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE `hour_configuration` ADD KEY `idx_hour_configuration_restaurant_date` (`restaurant_id`, `date`)',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @table_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'hours_percentage');
SET @idx_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'hours_percentage' AND INDEX_NAME = 'idx_hours_percentage_restaurant_date');
SET @ddl := IF(@table_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE `hours_percentage` ADD KEY `idx_hours_percentage_restaurant_date` (`restaurant_id`, `reservationDate`)',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @table_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menu_visibility');
SET @idx_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menu_visibility' AND INDEX_NAME = 'idx_menu_visibility_restaurant_menu_key');
SET @ddl := IF(@table_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE `menu_visibility` ADD KEY `idx_menu_visibility_restaurant_menu_key` (`restaurant_id`, `menu_key`)',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @table_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'invoices');
SET @idx_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'invoices' AND INDEX_NAME = 'idx_invoices_restaurant_status_date');
SET @ddl := IF(@table_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE `invoices` ADD KEY `idx_invoices_restaurant_status_date` (`restaurant_id`, `status`, `invoice_date`)',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @table_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'invoices');
SET @idx_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'invoices' AND INDEX_NAME = 'idx_invoices_restaurant_isres_resdate');
SET @ddl := IF(@table_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE `invoices` ADD KEY `idx_invoices_restaurant_isres_resdate` (`restaurant_id`, `is_reservation`, `reservation_date`)',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @table_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'invoices');
SET @idx_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'invoices' AND INDEX_NAME = 'ft_invoices_customer_contact');
SET @ddl := IF(@table_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE `invoices` ADD FULLTEXT KEY `ft_invoices_customer_contact` (`customer_name`, `customer_email`)',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;
