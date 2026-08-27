-- Track in-flight AI image generation per ad so the backoffice can rehydrate
-- a skeleton after page reloads. Idempotent: only adds columns if missing.
SET @status_col_exists := (
    SELECT COUNT(*)
    FROM information_schema.COLUMNS
    WHERE TABLE_SCHEMA = DATABASE()
      AND TABLE_NAME = 'restaurant_ads'
      AND COLUMN_NAME = 'image_generation_status'
);
SET @status_ddl := IF(
    @status_col_exists = 0,
    "ALTER TABLE restaurant_ads ADD COLUMN image_generation_status ENUM('idle','pending','ready','failed') NOT NULL DEFAULT 'idle' AFTER ctas_json",
    'SELECT 1'
);
PREPARE stmt FROM @status_ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @started_col_exists := (
    SELECT COUNT(*)
    FROM information_schema.COLUMNS
    WHERE TABLE_SCHEMA = DATABASE()
      AND TABLE_NAME = 'restaurant_ads'
      AND COLUMN_NAME = 'image_generation_started_at'
);
SET @started_ddl := IF(
    @started_col_exists = 0,
    'ALTER TABLE restaurant_ads ADD COLUMN image_generation_started_at TIMESTAMP NULL DEFAULT NULL AFTER image_generation_status',
    'SELECT 1'
);
PREPARE stmt FROM @started_ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
