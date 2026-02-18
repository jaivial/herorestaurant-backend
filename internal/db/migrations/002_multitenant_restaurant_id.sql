-- Multitenant tracking: attach all restaurant-owned rows to a restaurant.
-- For Villa Carmen (existing data), everything defaults to restaurant_id=1.
-- Idempotent: only runs if legacy tables exist.

-- Helper: check and run ALTER TABLE if table exists and column doesn't
SET @table_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'DIA');
SET @col_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'DIA' AND COLUMN_NAME = 'restaurant_id');
SET @ddl := IF(@table_exists = 1 AND @col_exists = 0, 'ALTER TABLE `DIA` ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1, ADD KEY `idx_DIA_restaurant_tipo_active` (`restaurant_id`, `TIPO`, `active`)', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @table_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'FINDE');
SET @col_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'FINDE' AND COLUMN_NAME = 'restaurant_id');
SET @ddl := IF(@table_exists = 1 AND @col_exists = 0, 'ALTER TABLE `FINDE` ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1, ADD KEY `idx_FINDE_restaurant_tipo_active` (`restaurant_id`, `TIPO`, `active`)', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @table_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'POSTRES');
SET @col_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'POSTRES' AND COLUMN_NAME = 'restaurant_id');
SET @ddl := IF(@table_exists = 1 AND @col_exists = 0, 'ALTER TABLE `POSTRES` ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1, ADD KEY `idx_POSTRES_restaurant_active` (`restaurant_id`, `active`)', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @table_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'VINOS');
SET @col_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'VINOS' AND COLUMN_NAME = 'restaurant_id');
SET @ddl := IF(@table_exists = 1 AND @col_exists = 0, 'ALTER TABLE `VINOS` ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1, ADD KEY `idx_VINOS_restaurant_tipo_active` (`restaurant_id`, `tipo`, `active`)', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @table_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bookings');
SET @col_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bookings' AND COLUMN_NAME = 'restaurant_id');
SET @ddl := IF(@table_exists = 1 AND @col_exists = 0, 'ALTER TABLE `bookings` ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1, ADD KEY `idx_bookings_restaurant_date_status` (`restaurant_id`, `reservation_date`, `status`)', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @table_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'cancelled_bookings');
SET @col_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'cancelled_bookings' AND COLUMN_NAME = 'restaurant_id');
SET @ddl := IF(@table_exists = 1 AND @col_exists = 0, 'ALTER TABLE `cancelled_bookings` ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1, ADD KEY `idx_cancelled_restaurant_reservation_date` (`restaurant_id`, `reservation_date`)', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @table_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menu_visibility');
SET @col_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menu_visibility' AND COLUMN_NAME = 'restaurant_id');
SET @ddl := IF(@table_exists = 1 AND @col_exists = 0, 'ALTER TABLE `menu_visibility` ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @table_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menusDeGrupos');
SET @col_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menusDeGrupos' AND COLUMN_NAME = 'restaurant_id');
SET @ddl := IF(@table_exists = 1 AND @col_exists = 0, 'ALTER TABLE `menusDeGrupos` ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1, ADD KEY `idx_menusDeGrupos_restaurant_active` (`restaurant_id`, `active`)', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @table_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'daily_limits');
SET @col_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'daily_limits' AND COLUMN_NAME = 'restaurant_id');
SET @ddl := IF(@table_exists = 1 AND @col_exists = 0, 'ALTER TABLE `daily_limits` ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @table_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'hour_configuration');
SET @col_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'hour_configuration' AND COLUMN_NAME = 'restaurant_id');
SET @ddl := IF(@table_exists = 1 AND @col_exists = 0, 'ALTER TABLE `hour_configuration` ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @table_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'hours_percentage');
SET @col_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'hours_percentage' AND COLUMN_NAME = 'restaurant_id');
SET @ddl := IF(@table_exists = 1 AND @col_exists = 0, 'ALTER TABLE `hours_percentage` ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @table_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'mesas_de_dos');
SET @col_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'mesas_de_dos' AND COLUMN_NAME = 'restaurant_id');
SET @ddl := IF(@table_exists = 1 AND @col_exists = 0, 'ALTER TABLE `mesas_de_dos` ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @table_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'openinghours');
SET @col_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'openinghours' AND COLUMN_NAME = 'restaurant_id');
SET @ddl := IF(@table_exists = 1 AND @col_exists = 0, 'ALTER TABLE `openinghours` ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @table_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'reservation_manager');
SET @col_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'reservation_manager' AND COLUMN_NAME = 'restaurant_id');
SET @ddl := IF(@table_exists = 1 AND @col_exists = 0, 'ALTER TABLE `reservation_manager` ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1, ADD KEY `idx_reservation_manager_restaurant_date` (`restaurant_id`, `reservationDate`)', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @table_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_days');
SET @col_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_days' AND COLUMN_NAME = 'restaurant_id');
SET @ddl := IF(@table_exists = 1 AND @col_exists = 0, 'ALTER TABLE `restaurant_days` ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @table_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'salon_condesa');
SET @col_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'salon_condesa' AND COLUMN_NAME = 'restaurant_id');
SET @ddl := IF(@table_exists = 1 AND @col_exists = 0, 'ALTER TABLE `salon_condesa` ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @table_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bot_conversation_messages');
SET @col_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bot_conversation_messages' AND COLUMN_NAME = 'restaurant_id');
SET @ddl := IF(@table_exists = 1 AND @col_exists = 0, 'ALTER TABLE `bot_conversation_messages` ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1, ADD KEY `idx_bot_conv_restaurant_phone_timestamp` (`restaurant_id`, `phone_number`, `timestamp`)', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @table_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'conversation_messages');
SET @col_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'conversation_messages' AND COLUMN_NAME = 'restaurant_id');
SET @ddl := IF(@table_exists = 1 AND @col_exists = 0, 'ALTER TABLE `conversation_messages` ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1, ADD KEY `idx_conv_messages_restaurant_sender_created` (`restaurant_id`, `sender_number`, `created_at`)', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @table_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'conversation_sessions');
SET @col_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'conversation_sessions' AND COLUMN_NAME = 'restaurant_id');
SET @ddl := IF(@table_exists = 1 AND @col_exists = 0, 'ALTER TABLE `conversation_sessions` ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1, ADD KEY `idx_conv_sessions_restaurant_sender` (`restaurant_id`, `sender_number`, `status`)', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @table_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'conversation_states');
SET @col_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'conversation_states' AND COLUMN_NAME = 'restaurant_id');
SET @ddl := IF(@table_exists = 1 AND @col_exists = 0, 'ALTER TABLE `conversation_states` ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1, ADD KEY `idx_conv_states_restaurant_sender_state` (`restaurant_id`, `sender_number`, `conversation_state`)', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;
