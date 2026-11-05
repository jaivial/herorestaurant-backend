-- 207_special_date_whatsapp_only.sql
-- Coordination id: special_date_whatsapp_only_v1
-- A special date (e.g. Comida de Navidad) can be booked only through the
-- restaurant's WhatsApp: the public wizard shows a notice with the WhatsApp
-- number instead of the time / party selectors, and the public booking
-- endpoints refuse that date. Edited in /app/reservas/especial; default off.

SET @c := (SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'special_dates' AND COLUMN_NAME = 'whatsapp_only');
SET @sql := IF(@c = 0, 'ALTER TABLE special_dates ADD COLUMN whatsapp_only TINYINT(1) NOT NULL DEFAULT 0 AFTER prereserva_enabled', 'DO 0');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
