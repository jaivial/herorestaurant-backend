-- Special menus: per-section price + optional link to a special date.
-- Coordination id: special_menu_price_date_v1
--   backoffice (section price input, "Fecha especial" select)
--     -> menus.special_date_id / special_menu_sections.price
--     -> public menus API (html preview, preactvillacarmen, reservas button query)
--     -> WhatsApp bot menu tools (price + date + prereserva)

SET @col_exists := (
    SELECT COUNT(*) FROM information_schema.COLUMNS
    WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'special_menu_sections' AND COLUMN_NAME = 'price'
);
SET @ddl := IF(@col_exists = 0, 'ALTER TABLE special_menu_sections ADD COLUMN price DECIMAL(10,2) NULL', 'SELECT 1');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @col_exists := (
    SELECT COUNT(*) FROM information_schema.COLUMNS
    WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menus' AND COLUMN_NAME = 'special_date_id'
);
SET @ddl := IF(@col_exists = 0, 'ALTER TABLE menus ADD COLUMN special_date_id BIGINT NULL', 'SELECT 1');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
