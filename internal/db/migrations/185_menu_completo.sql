-- Menús especiales marcados como COMPLETO (sin disponibilidad).
-- Coordination id: special_menu_completo_v1
--   backoffice reservas > especial (toggle "COMPLETO" por menú de la fecha)
--     -> menus.completo
--     -> API pública (menus, menus/home, menus/sidebar, menus/{id})
--     -> preactvillacarmen ribbon rojo "COMPLETO" (página del menú, home, burger)

SET @col_exists := (
    SELECT COUNT(*) FROM information_schema.COLUMNS
    WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menus' AND COLUMN_NAME = 'completo'
);
SET @ddl := IF(@col_exists = 0, 'ALTER TABLE menus ADD COLUMN completo TINYINT(1) NOT NULL DEFAULT 0', 'SELECT 1');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
