-- Add opening_mode column to openinghours table
-- This allows storing the explicit opening mode (morning/night/both) for a specific date
-- instead of deriving it from the hours array.

SET @col_exists = (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'openinghours'
    AND COLUMN_NAME = 'opening_mode'
);

SET @ddl := IF(@col_exists = 0,
  'ALTER TABLE `openinghours` ADD COLUMN `opening_mode` VARCHAR(16) NULL DEFAULT NULL AFTER `hoursarray`',
  'SELECT 1'
);

PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- Populate existing rows with derived mode based on current hours
-- This uses the same logic as modeFromHours in the Go code
UPDATE `openinghours` oh
SET `opening_mode` = (
  SELECT CASE
    WHEN JSON_LENGTH(hoursarray) = 0 THEN 'both'
    ELSE (
      SELECT CASE
        WHEN (
          -- Has morning hours (8:00-17:00): hours like 08:00-17:00
          SELECT COUNT(*) > 0
          FROM JSON_TABLE(
            oh.hoursarray,
            '$[*]' COLUMNS (hour VARCHAR(5) PATH '$')
          ) AS jt
          WHERE HOUR(STR_TO_DATE(jt.hour, '%H:%i')) BETWEEN 8 AND 16
        ) AND (
          -- Has night hours (after 17:00): hours like 20:00-23:00
          SELECT COUNT(*) > 0
          FROM JSON_TABLE(
            oh.hoursarray,
            '$[*]' COLUMNS (hour VARCHAR(5) PATH '$')
          ) AS jt
          WHERE HOUR(STR_TO_DATE(jt.hour, '%H:%i')) >= 17
        ) THEN 'both'
        WHEN (
          SELECT COUNT(*) > 0
          FROM JSON_TABLE(
            oh.hoursarray,
            '$[*]' COLUMNS (hour VARCHAR(5) PATH '$')
          ) AS jt
          WHERE HOUR(STR_TO_DATE(jt.hour, '%H:%i')) BETWEEN 8 AND 16
        ) THEN 'morning'
        WHEN (
          SELECT COUNT(*) > 0
          FROM JSON_TABLE(
            oh.hoursarray,
            '$[*]' COLUMNS (hour VARCHAR(5) PATH '$')
          ) AS jt
          WHERE HOUR(STR_TO_DATE(jt.hour, '%H:%i')) >= 17
        ) THEN 'night'
        ELSE 'both'
      END
    )
  END
)
WHERE `opening_mode` IS NULL AND `hoursarray` IS NOT NULL;
