-- jefe_cocina gains stock access: the backoffice section plus the five
-- fine-grained core-ops permissions (view, adjust, waste.record,
-- count.perform, count.close). Item/warehouse/settings management stays
-- root/admin-only. Rows are only inserted when absent, so an explicit
-- administrator decision stored in either table is never overwritten.

SET @bo_role_perms_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bo_role_permissions');
SET @bo_roles_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bo_roles');

SET @insert_section := IF(@bo_role_perms_exists = 1 AND @bo_roles_exists = 1, "INSERT IGNORE INTO bo_role_permissions (role_slug, section_key, is_allowed) VALUES ('jefe_cocina', 'stock', 1)", 'SELECT 1');
PREPARE stmt FROM @insert_section; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @stock_role_perms_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'stock_role_permissions');
SET @restaurants_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurants');

SET @insert_stock_perms := IF(@stock_role_perms_exists = 1 AND @restaurants_exists = 1, "INSERT IGNORE INTO stock_role_permissions (restaurant_id, role_slug, permission_key, is_allowed) SELECT r.id, 'jefe_cocina', p.permission_key, 1 FROM restaurants r CROSS JOIN (SELECT 'stock.view' AS permission_key UNION ALL SELECT 'stock.adjust' UNION ALL SELECT 'stock.waste.record' UNION ALL SELECT 'stock.count.perform' UNION ALL SELECT 'stock.count.close') p", 'SELECT 1');
PREPARE stmt FROM @insert_stock_perms; EXECUTE stmt; DEALLOCATE PREPARE stmt;
