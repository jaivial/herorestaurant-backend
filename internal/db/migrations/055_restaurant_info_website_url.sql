-- Ensure contact website URL is stored per restaurant.
SET @restaurant_info_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_info'
);

SET @website_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_info' AND COLUMN_NAME = 'website'
);
SET @ddl := IF(
  @restaurant_info_exists = 1 AND @website_exists = 0,
  'ALTER TABLE restaurant_info ADD COLUMN website VARCHAR(512) NOT NULL DEFAULT '''' AFTER clasificacion',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @menu_url_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_info' AND COLUMN_NAME = 'menu_url'
);
SET @ddl := IF(
  @restaurant_info_exists = 1 AND @menu_url_exists = 0,
  'ALTER TABLE restaurant_info ADD COLUMN menu_url VARCHAR(512) NOT NULL DEFAULT '''' AFTER website',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;
