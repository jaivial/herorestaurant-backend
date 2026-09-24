-- WhatsApp bot human takeover. Coordination id: wa_bot_human_handoff_v1
-- bot_paused_until: when restaurant staff write manually from the WhatsApp
-- phone, the bot stays silent for this conversation until this instant.
SET @col_exists := (
    SELECT COUNT(*) FROM information_schema.COLUMNS
    WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'whatsapp_bot_sessions' AND COLUMN_NAME = 'bot_paused_until'
);
SET @ddl := IF(@col_exists = 0,
  'ALTER TABLE whatsapp_bot_sessions ADD COLUMN bot_paused_until DATETIME(3) NULL',
  'SELECT 1');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
