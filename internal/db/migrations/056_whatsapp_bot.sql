-- WhatsApp bot: per-tenant sessions, message history and personalization config.

CREATE TABLE IF NOT EXISTS whatsapp_bot_sessions (
  restaurant_id INT NOT NULL,
  user_phone VARCHAR(32) NOT NULL,
  push_name VARCHAR(191) NOT NULL DEFAULT '',
  last_message_at DATETIME(3) NOT NULL,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (restaurant_id, user_phone),
  KEY idx_wa_bot_sessions_last (last_message_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS whatsapp_bot_messages (
  id BIGINT NOT NULL AUTO_INCREMENT,
  restaurant_id INT NOT NULL,
  user_phone VARCHAR(32) NOT NULL,
  role ENUM('user','assistant','tool') NOT NULL,
  content MEDIUMTEXT NOT NULL,
  tool_name VARCHAR(128) DEFAULT NULL,
  created_at DATETIME(3) NOT NULL,
  PRIMARY KEY (id),
  KEY idx_wa_bot_messages_session (restaurant_id, user_phone, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS whatsapp_bot_config (
  restaurant_id INT NOT NULL,
  config_json JSON DEFAULT NULL,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (restaurant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
