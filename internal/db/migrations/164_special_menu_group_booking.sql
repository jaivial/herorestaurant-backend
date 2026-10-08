-- Special menus can also be booked as a group menu (menu de grupo).
-- Coordination id: special_menu_group_booking_v1
--   backoffice configuracion (toggle "reservar como menu de grupo" + "principales
--   obligatorios") on a special-type menu
--     -> menus.special_group_menu_enabled + menus.special_principales_required
--     -> GET /api/reservations/group-menus (special menus offered as group menu)
--     -> preactvillacarmen reservas wizard group step (principales del menu especial)
--     -> booking insert validation (principales obligatorios)
--
-- A special menu is bookable as a group menu only when the toggle is on AND the
-- principales toggle (special_menu_principales_v1) is on with a non-empty dish
-- list: on with no dishes == off.

SET @col_exists := (
    SELECT COUNT(*) FROM information_schema.COLUMNS
    WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menus' AND COLUMN_NAME = 'special_group_menu_enabled'
);
SET @ddl := IF(@col_exists = 0, 'ALTER TABLE menus ADD COLUMN special_group_menu_enabled TINYINT(1) NOT NULL DEFAULT 0', 'SELECT 1');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @col_exists := (
    SELECT COUNT(*) FROM information_schema.COLUMNS
    WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menus' AND COLUMN_NAME = 'special_principales_required'
);
SET @ddl := IF(@col_exists = 0, 'ALTER TABLE menus ADD COLUMN special_principales_required TINYINT(1) NOT NULL DEFAULT 0', 'SELECT 1');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
