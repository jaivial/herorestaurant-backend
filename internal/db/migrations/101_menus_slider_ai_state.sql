-- Persist in-flight AI generation count for menu slider images so the
-- backoffice can rehydrate skeletons after page reloads.

SET @col_exists := (
    SELECT COUNT(*)
    FROM information_schema.COLUMNS
    WHERE TABLE_SCHEMA = DATABASE()
      AND TABLE_NAME = 'menus'
      AND COLUMN_NAME = 'slider_ai_generating'
);
SET @ddl := IF(
    @col_exists = 0,
    'ALTER TABLE menus ADD COLUMN slider_ai_generating INT NOT NULL DEFAULT 0',
    'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
