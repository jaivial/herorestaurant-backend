-- MiniMax AI configuration per restaurant: encrypted API key + model.
-- The API key is stored AES-256-GCM encrypted (internal/vault); the vault
-- token lives in backend env VAULT_TOKEN and is NOT stored here.

CREATE TABLE IF NOT EXISTS restaurant_minimax_config (
	restaurant_id INT NOT NULL,
	api_key_encrypted TEXT NULL,
	model VARCHAR(128) NULL,
	updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
	PRIMARY KEY (restaurant_id),
	CONSTRAINT fk_minimax_config_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
