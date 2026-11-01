-- 206_seo_item_routes_inactive_since.sql
-- Coordination id: seo_item_unavailable_v1
-- When a dish or wine route was unpublished. For 60 days its page stays live
-- (200, "no disponible ahora mismo"); after that it redirects (301) to its
-- menu. Reactivating clears it. Existing inactive rows start from updated_at.

SET @c := (SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'seo_item_routes' AND COLUMN_NAME = 'inactive_since');
SET @sql := IF(@c = 0, 'ALTER TABLE seo_item_routes ADD COLUMN inactive_since DATETIME NULL', 'DO 0');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

UPDATE seo_item_routes SET inactive_since = updated_at WHERE active = 0 AND inactive_since IS NULL;
