-- Special menus: optional call-to-action button rendered below the sections.
-- Coordination id: special_menu_cta_v1
--   backoffice configuracion ("Mostrar boton reservar")
--     -> menus.special_cta (JSON: enabled, label, action, menu_id, whatsapp_phone,
--        whatsapp_message, special_date_id)
--     -> resolved href (restaurant website base_url) in BO + public menu payloads
--     -> html preview + preactvillacarmen SpecialMenuCta button

SET @col_exists := (
    SELECT COUNT(*) FROM information_schema.COLUMNS
    WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menus' AND COLUMN_NAME = 'special_cta'
);
SET @ddl := IF(@col_exists = 0, 'ALTER TABLE menus ADD COLUMN special_cta JSON NULL', 'SELECT 1');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
