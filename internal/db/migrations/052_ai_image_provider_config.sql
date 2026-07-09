-- AI image generation provider configuration (root-only, DB-backed).
-- Replaces the env-only WAVESPEED_API_KEY with a per-restaurant config that
-- can be managed from the backoffice. Three tables:
--   ai_image_providers        catalog of providers (seed: wavespeed)
--   ai_image_models           catalog of models per provider (seed: 6 models)
--   ai_image_provider_config  one row per restaurant: selected provider, key,
--                             and the chosen text-to-image / image-to-image models
-- Idempotent: CREATE IF NOT EXISTS + INSERT IGNORE on unique keys.

CREATE TABLE IF NOT EXISTS ai_image_providers (
    id INT AUTO_INCREMENT PRIMARY KEY,
    slug VARCHAR(64) NOT NULL,
    label VARCHAR(120) NOT NULL,
    base_url VARCHAR(255) NOT NULL,
    docs_url VARCHAR(255) NULL,
    active TINYINT(1) NOT NULL DEFAULT 1,
    sort INT NOT NULL DEFAULT 0,
    UNIQUE KEY uniq_ai_image_providers_slug (slug)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS ai_image_models (
    id INT AUTO_INCREMENT PRIMARY KEY,
    provider_slug VARCHAR(64) NOT NULL,
    slug VARCHAR(160) NOT NULL,
    label VARCHAR(160) NOT NULL,
    mode ENUM('t2i','i2i') NOT NULL,
    active TINYINT(1) NOT NULL DEFAULT 1,
    sort INT NOT NULL DEFAULT 0,
    UNIQUE KEY uniq_ai_image_models_provider_slug (provider_slug, slug)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS ai_image_provider_config (
    id INT AUTO_INCREMENT PRIMARY KEY,
    restaurant_id INT NOT NULL DEFAULT 1,
    provider_slug VARCHAR(64) NOT NULL DEFAULT 'wavespeed',
    api_key TEXT NULL,
    t2i_model_slug VARCHAR(160) NULL,
    i2i_model_slug VARCHAR(160) NULL,
    is_active TINYINT(1) NOT NULL DEFAULT 0,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    updated_by_user_id BIGINT NULL,
    UNIQUE KEY uniq_ai_image_provider_config_restaurant (restaurant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT IGNORE INTO ai_image_providers (slug, label, base_url, docs_url, active, sort)
VALUES
    ('wavespeed', 'WaveSpeed', 'https://api.wavespeed.ai', 'https://wavespeed.ai/models', 1, 0);

INSERT IGNORE INTO ai_image_models (provider_slug, slug, label, mode, active, sort)
VALUES
    ('wavespeed', 'bytedance/seedream-v5.0-pro',            'Seedream v5.0 Pro',        't2i', 1, 0),
    ('wavespeed', 'openai/gpt-image-2/text-to-image',       'GPT Image 2',              't2i', 1, 1),
    ('wavespeed', 'google/nano-banana-pro/text-to-image',   'Nano Banana Pro',          't2i', 1, 2),
    ('wavespeed', 'bytedance/seedream-v5.0-pro/edit',       'Seedream v5.0 Pro (Edit)', 'i2i', 1, 0),
    ('wavespeed', 'openai/gpt-image-2/edit',                'GPT Image 2 (Edit)',       'i2i', 1, 1),
    ('wavespeed', 'google/nano-banana-2/edit',              'Nano Banana 2 (Edit)',     'i2i', 1, 2);
