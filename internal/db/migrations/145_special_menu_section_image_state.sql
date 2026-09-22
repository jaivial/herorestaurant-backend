-- Upload state for special menu section images.
-- Coordination id: special_menu_sections_image_state_v1
-- The socket upload is processed by a server-side background task; this state
-- is what the editor hydrates from so a reload shows the skeleton (uploading),
-- the final image (ready) or the default dropzone (empty).

SET @col_exists := (
    SELECT COUNT(*)
    FROM information_schema.COLUMNS
    WHERE TABLE_SCHEMA = DATABASE()
      AND TABLE_NAME = 'special_menu_sections'
      AND COLUMN_NAME = 'image_state'
);
SET @ddl := IF(
    @col_exists = 0,
    'ALTER TABLE special_menu_sections ADD COLUMN image_state VARCHAR(16) NOT NULL DEFAULT ''empty''',
    'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @col_exists := (
    SELECT COUNT(*)
    FROM information_schema.COLUMNS
    WHERE TABLE_SCHEMA = DATABASE()
      AND TABLE_NAME = 'special_menu_sections'
      AND COLUMN_NAME = 'image_state_at'
);
SET @ddl := IF(
    @col_exists = 0,
    'ALTER TABLE special_menu_sections ADD COLUMN image_state_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP',
    'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
