-- Special dates: per-section online toggle + personalised notice.
-- Coordination id: special_date_section_online_v1
--   backoffice /app/reservas/especial (switch per special-menu section)
--     -> special_date_menu_sections.online_enabled (default 1 = bookable online)
--     -> public GET /reservations/special-date hides disabled sections
--     -> preactvillacarmen reservas step 2 (count), principales and adelanto skip them
-- Coordination id: special_date_custom_notice_v1
--   backoffice "Notificacion personalizada" textarea
--     -> special_dates.custom_notice
--     -> public GET /reservations/special-date (custom_notice)
--     -> preactvillacarmen reservas step 2 warn notice

SET @col_exists := (
    SELECT COUNT(*)
    FROM information_schema.COLUMNS
    WHERE TABLE_SCHEMA = DATABASE()
      AND TABLE_NAME = 'special_date_menu_sections'
      AND COLUMN_NAME = 'online_enabled'
);
SET @ddl := IF(
    @col_exists = 0,
    'ALTER TABLE special_date_menu_sections ADD COLUMN online_enabled TINYINT(1) NOT NULL DEFAULT 1 AFTER adelanto_amount',
    'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @col_exists := (
    SELECT COUNT(*)
    FROM information_schema.COLUMNS
    WHERE TABLE_SCHEMA = DATABASE()
      AND TABLE_NAME = 'special_dates'
      AND COLUMN_NAME = 'custom_notice'
);
SET @ddl := IF(
    @col_exists = 0,
    'ALTER TABLE special_dates ADD COLUMN custom_notice TEXT NULL AFTER description',
    'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
