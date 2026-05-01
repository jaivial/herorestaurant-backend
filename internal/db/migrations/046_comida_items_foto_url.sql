-- Only add foto_url column if it doesn't already exist
SET @column_exists = (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'comida_items'
    AND COLUMN_NAME = 'foto_url'
);
SET @sql = IF(@column_exists = 0,
  'ALTER TABLE comida_items ADD COLUMN foto_url VARCHAR(1024) DEFAULT NULL AFTER foto',
  'SELECT 1');
PREPARE stmt FROM @sql;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
