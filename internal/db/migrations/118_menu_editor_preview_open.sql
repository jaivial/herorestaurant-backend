-- Persist the menu editor preview visibility per restaurant-owned menu.

SET @col_exists := (
    SELECT COUNT(*)
    FROM information_schema.COLUMNS
    WHERE TABLE_SCHEMA = DATABASE()
      AND TABLE_NAME = 'menus'
      AND COLUMN_NAME = 'editor_preview_open'
);
SET @ddl := IF(
    @col_exists = 0,
    'ALTER TABLE menus ADD COLUMN editor_preview_open TINYINT(1) NOT NULL DEFAULT 1',
    'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
