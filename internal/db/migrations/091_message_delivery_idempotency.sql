-- 091: idempotency key for automated WhatsApp deliveries.
SET @table_exists := (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='message_deliveries');
SET @col_exists := (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='message_deliveries' AND COLUMN_NAME='delivery_key');
SET @ddl := IF(@table_exists=1 AND @col_exists=0, 'ALTER TABLE message_deliveries ADD COLUMN delivery_key VARCHAR(191) NULL AFTER event', 'SELECT 1'); PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;
SET @idx_exists := (SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='message_deliveries' AND INDEX_NAME='uniq_message_deliveries_delivery_key');
SET @ddl := IF(@table_exists=1 AND @idx_exists=0, 'ALTER TABLE message_deliveries ADD UNIQUE KEY uniq_message_deliveries_delivery_key (delivery_key)', 'SELECT 1'); PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;
