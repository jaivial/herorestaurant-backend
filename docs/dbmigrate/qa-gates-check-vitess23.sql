-- qa-gates-check-vitess23.sql: READ-ONLY (not run by worker). Explains 403s for the QA user.
BEGIN TRANSACTION READ ONLY;
-- 1 app_version per membership (stock/POS/sheets need >= 0.2; default column value is 0.1)
SELECT user_id, restaurant_id, role, app_version FROM bo_user_restaurants WHERE user_id = 207;
SELECT u.id, u.is_superadmin FROM bo_users u WHERE u.id = 207;
-- 2 explicit stock/POS permission rows for the effective role (superadmin -> role 'root')
SELECT role_slug, permission_key, is_allowed FROM stock_role_permissions WHERE restaurant_id = 1 AND role_slug IN ('root','admin') ORDER BY 1,2;
SELECT role_slug, permission_key, is_allowed FROM pos_role_permissions WHERE restaurant_id = 1 AND role_slug IN ('root','admin') ORDER BY 1,2;
-- 3 POS plan feature (root bypasses; others get 402)
SELECT COUNT(*) AS pos_feature_rows FROM recurring_invoices WHERE restaurant_id = 1 AND feature_key = 'pos';
ROLLBACK;
