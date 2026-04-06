-- Add ai_generating column to track pending AI image enhancement jobs
-- Idempotent: only adds column if it doesn't exist
SET @dbname = DATABASE();
SET @tablename = 'comida_items';
SET @columnname = 'ai_generating';
SET @preparedStatement = (SELECT IF(
  (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
   WHERE TABLE_SCHEMA = @dbname AND TABLE_NAME = @tablename AND COLUMN_NAME = @columnname) > 0,
  'SELECT 1',
  'ALTER TABLE comida_items ADD COLUMN ai_generating TINYINT(1) NOT NULL DEFAULT 0'
));
PREPARE stmt FROM @preparedStatement;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
