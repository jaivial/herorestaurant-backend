-- Add AI image columns to VINOS table.
-- Idempotent: checks column existence before adding.

SET @db = DATABASE();

-- ai_requested_img TINYINT(1) DEFAULT 0
SET @col = 'ai_requested_img';
SET @sql = IF(
  (SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = @db AND TABLE_NAME = 'VINOS' AND COLUMN_NAME = @col) = 0,
  CONCAT('ALTER TABLE VINOS ADD COLUMN `', @col, '` TINYINT(1) NOT NULL DEFAULT 0'),
  'SELECT 1'
);
PREPARE stmt FROM @sql;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- ai_generating_img TINYINT(1) DEFAULT 0
SET @col = 'ai_generating_img';
SET @sql = IF(
  (SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = @db AND TABLE_NAME = 'VINOS' AND COLUMN_NAME = @col) = 0,
  CONCAT('ALTER TABLE VINOS ADD COLUMN `', @col, '` TINYINT(1) NOT NULL DEFAULT 0'),
  'SELECT 1'
);
PREPARE stmt FROM @sql;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- ai_generated_img VARCHAR(512) DEFAULT NULL
SET @col = 'ai_generated_img';
SET @sql = IF(
  (SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = @db AND TABLE_NAME = 'VINOS' AND COLUMN_NAME = @col) = 0,
  CONCAT('ALTER TABLE VINOS ADD COLUMN `', @col, '` VARCHAR(512) DEFAULT NULL'),
  'SELECT 1'
);
PREPARE stmt FROM @sql;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
