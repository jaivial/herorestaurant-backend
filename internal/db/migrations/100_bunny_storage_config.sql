-- BunnyCDN storage credentials per restaurant (root-only, DB-backed).
-- Replaces the env-only BUNNY_* variables so each tenant can point the AI image
-- pipelines (and every other upload path) at its own storage zone.
-- One row per restaurant with the three values Bunny shows in the storage zone
-- panel: zone name, password (access key) and the public pull URL.
-- Env values remain the fallback when a field is empty.
-- Idempotent: CREATE IF NOT EXISTS.

CREATE TABLE IF NOT EXISTS bunny_storage_config (
    id INT AUTO_INCREMENT PRIMARY KEY,
    restaurant_id INT NOT NULL,
    storage_zone VARCHAR(190) NULL,
    storage_access_key TEXT NULL,
    pull_base_url VARCHAR(255) NULL,
    is_active TINYINT(1) NOT NULL DEFAULT 0,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    updated_by_user_id BIGINT NULL,
    UNIQUE KEY uniq_bunny_storage_config_restaurant (restaurant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
