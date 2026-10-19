-- AI usage and WhatsApp message logs.
--
-- ai_usage_logs: one row per LLM request (Forky, WhatsApp bot, translations,
-- stock AI, OCR). Tokens and cost are captured from the provider response so
-- the Estadísticas > IA y WhatsApp tab can aggregate messages and spend by
-- week, month or year. Coordination id: ai_usage_logs_v1.
--
-- whatsapp_message_logs: append-only ledger of every WhatsApp message the
-- backend records (inbound customer messages and outbound sent messages,
-- including manual staff replies and notifications). Coordination id:
-- whatsapp_message_logs_v1.

CREATE TABLE IF NOT EXISTS ai_usage_logs (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    feature VARCHAR(64) NOT NULL,
    provider VARCHAR(48) NOT NULL DEFAULT '',
    model VARCHAR(120) NOT NULL DEFAULT '',
    input_tokens BIGINT NOT NULL DEFAULT 0,
    output_tokens BIGINT NOT NULL DEFAULT 0,
    cache_read_tokens BIGINT NOT NULL DEFAULT 0,
    cache_write_tokens BIGINT NOT NULL DEFAULT 0,
    cost_usd DECIMAL(14,8) NOT NULL DEFAULT 0,
    priced TINYINT(1) NOT NULL DEFAULT 0,
    is_fallback TINYINT(1) NOT NULL DEFAULT 0,
    correlation_id VARCHAR(64) NOT NULL DEFAULT '',
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    KEY idx_ai_usage_restaurant_time (restaurant_id, created_at),
    KEY idx_ai_usage_restaurant_feature_time (restaurant_id, feature, created_at),
    KEY idx_ai_usage_restaurant_model_time (restaurant_id, model, created_at),
    CONSTRAINT fk_ai_usage_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS whatsapp_message_logs (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    direction ENUM('inbound','outbound') NOT NULL,
    message_type VARCHAR(32) NOT NULL DEFAULT 'text',
    user_phone VARCHAR(64) NOT NULL DEFAULT '',
    content MEDIUMTEXT NOT NULL,
    source VARCHAR(64) NOT NULL DEFAULT '',
    status ENUM('received','sent','failed') NOT NULL DEFAULT 'sent',
    tool_name VARCHAR(128) NOT NULL DEFAULT '',
    provider_message_id VARCHAR(191) NOT NULL DEFAULT '',
    ai_model VARCHAR(120) NOT NULL DEFAULT '',
    input_tokens BIGINT NOT NULL DEFAULT 0,
    output_tokens BIGINT NOT NULL DEFAULT 0,
    cost_usd DECIMAL(14,8) NOT NULL DEFAULT 0,
    correlation_id VARCHAR(64) NOT NULL DEFAULT '',
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    KEY idx_wa_logs_restaurant_time (restaurant_id, created_at),
    KEY idx_wa_logs_restaurant_direction_time (restaurant_id, direction, created_at),
    KEY idx_wa_logs_restaurant_phone_time (restaurant_id, user_phone, created_at),
    CONSTRAINT fk_wa_logs_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- The old fallback model is superseded by MiniMax-M3.1-Flash. Only rewrite
-- rows that still point at the previous default; explicit tenant choices for
-- other models are preserved.
UPDATE restaurant_bot_ai_config
SET fallback_model = 'minimax/MiniMax-M3.1-Flash'
WHERE fallback_model IN ('minimax/MiniMax-M3', 'MiniMax-M3');
