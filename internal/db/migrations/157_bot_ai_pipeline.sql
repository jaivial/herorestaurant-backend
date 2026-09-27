-- WhatsApp bot AI routing + provider keys. Coordination id: wa_bot_ai_providers_v1
-- restaurant_bot_ai_config: primary/fallback model ("provider/model") and the
-- tenant knowledge chunks indexed into the SQLite FTS RAG (wa_bot_rag_fts_v1).
-- restaurant_ai_provider_keys: provider API keys (opencode-go, minimax, jev)
-- encrypted with VAULT_KEY (AES-256-GCM bound to restaurant + provider).
CREATE TABLE IF NOT EXISTS restaurant_bot_ai_config (
	restaurant_id INT NOT NULL,
	primary_model VARCHAR(128) NULL,
	fallback_model VARCHAR(128) NULL,
	knowledge_json MEDIUMTEXT NULL,
	updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
	PRIMARY KEY (restaurant_id),
	CONSTRAINT fk_bot_ai_config_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS restaurant_ai_provider_keys (
	restaurant_id INT NOT NULL,
	provider VARCHAR(32) NOT NULL,
	api_key_encrypted TEXT NOT NULL,
	updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
	PRIMARY KEY (restaurant_id, provider),
	CONSTRAINT fk_ai_provider_keys_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
