-- Add generic weekday open/closed defaults to reservation defaults.

SET @table_exists := (
  SELECT COUNT(*)
  FROM information_schema.tables
  WHERE table_schema = DATABASE()
    AND table_name = 'restaurant_reservation_defaults'
);

SET @col_exists := (
  SELECT COUNT(*)
  FROM information_schema.columns
  WHERE table_schema = DATABASE()
    AND table_name = 'restaurant_reservation_defaults'
    AND column_name = 'weekday_open_json'
);

SET @ddl := IF(
  @table_exists = 1 AND @col_exists = 0,
  'ALTER TABLE restaurant_reservation_defaults ADD COLUMN weekday_open_json LONGTEXT NULL AFTER night_hours_json',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @backfill := IF(
  @table_exists = 1,
  'UPDATE restaurant_reservation_defaults
   SET weekday_open_json = ''{\"monday\":false,\"tuesday\":false,\"wednesday\":false,\"thursday\":true,\"friday\":true,\"saturday\":true,\"sunday\":true}''
   WHERE weekday_open_json IS NULL OR TRIM(weekday_open_json) = ''''',
  'SELECT 1'
);
PREPARE stmt FROM @backfill;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
