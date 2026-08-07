-- 089: durable WhatsApp verification and delivery queue support.
-- Keep provider secrets out of this table; only a one-way code digest is stored.
CREATE TABLE IF NOT EXISTS whatsapp_verification_codes (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  restaurant_id INT NOT NULL,
  phone VARCHAR(32) NOT NULL,
  code_digest CHAR(64) NOT NULL,
  expires_at DATETIME NOT NULL,
  attempts SMALLINT UNSIGNED NOT NULL DEFAULT 0,
  verified_at DATETIME NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_whatsapp_verification_lookup (restaurant_id, phone, expires_at),
  CONSTRAINT fk_whatsapp_verification_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- message_deliveries is the outbox used by webhook and notification senders.
-- These fields make pending rows safely claimable by a worker without losing
-- retries after a process restart. ALTERs are guarded for old installations.
SET @table_exists := (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='message_deliveries');
SET @col_exists := (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='message_deliveries' AND COLUMN_NAME='attempts');
SET @ddl := IF(@table_exists=1 AND @col_exists=0, 'ALTER TABLE message_deliveries ADD COLUMN attempts SMALLINT UNSIGNED NOT NULL DEFAULT 0 AFTER status', 'SELECT 1'); PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;
SET @col_exists := (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='message_deliveries' AND COLUMN_NAME='next_attempt_at');
SET @ddl := IF(@table_exists=1 AND @col_exists=0, 'ALTER TABLE message_deliveries ADD COLUMN next_attempt_at DATETIME NULL AFTER attempts', 'SELECT 1'); PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;
SET @col_exists := (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='message_deliveries' AND COLUMN_NAME='locked_at');
SET @ddl := IF(@table_exists=1 AND @col_exists=0, 'ALTER TABLE message_deliveries ADD COLUMN locked_at DATETIME NULL AFTER next_attempt_at', 'SELECT 1'); PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;
SET @col_exists := (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='message_deliveries' AND COLUMN_NAME='locked_by');
SET @ddl := IF(@table_exists=1 AND @col_exists=0, 'ALTER TABLE message_deliveries ADD COLUMN locked_by VARCHAR(128) NULL AFTER locked_at', 'SELECT 1'); PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;
SET @idx_exists := (SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='message_deliveries' AND INDEX_NAME='idx_message_deliveries_queue');
SET @ddl := IF(@table_exists=1 AND @idx_exists=0, 'ALTER TABLE message_deliveries ADD KEY idx_message_deliveries_queue (status, next_attempt_at, locked_at, id)', 'SELECT 1'); PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;
