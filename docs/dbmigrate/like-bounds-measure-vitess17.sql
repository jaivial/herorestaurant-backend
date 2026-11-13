-- like-bounds-measure-vitess17.sql: READ-ONLY measuring queries for the coordinator
-- (repair-vitess17). NOT run by the worker. PostgreSQL scratch (business schema).
-- Each query returns aggregate counts only (no row values). Bound proposal per site =
-- 4 x max candidates (mysqlcoll.Bound), recorded next to the site, re-measured per import.
BEGIN TRANSACTION READ ONLY;

-- 1 POSTRES (admin_postres search, comida listPostres, assistant postres)
SELECT 'POSTRES' AS site, MAX(n) AS max_per_tenant, COUNT(*) AS tenants FROM (SELECT restaurant_id, COUNT(*) n FROM "POSTRES" GROUP BY restaurant_id) x;
-- 2 stock_items active (stock list, stock options, assistant stock_items_list)
SELECT 'stock_items_active', MAX(n), COUNT(*) FROM (SELECT restaurant_id, COUNT(*) n FROM stock_items WHERE deleted_at IS NULL AND is_active=1 GROUP BY restaurant_id) x;
SELECT 'stock_items_all', MAX(n), COUNT(*) FROM (SELECT restaurant_id, COUNT(*) n FROM stock_items GROUP BY restaurant_id) x;
-- 3 invoices list
SELECT 'invoices', MAX(n), COUNT(*) FROM (SELECT restaurant_id, COUNT(*) n FROM invoices GROUP BY restaurant_id) x;
-- 4 POS customers
SELECT 'pos_customers', MAX(n), COUNT(*) FROM (SELECT restaurant_id, COUNT(*) n FROM pos_customers WHERE anonymised_at IS NULL GROUP BY restaurant_id) x;
-- 5 checkcancel (restaurant, date, time, party_size)
SELECT 'checkcancel', MAX(n), COUNT(*) FROM (SELECT restaurant_id, reservation_date, reservation_time, party_size, COUNT(*) n FROM bookings GROUP BY 1,2,3,4) x;
-- 6 stock_recipes active (dish allergens, technical sheet)
SELECT 'stock_recipes_active', MAX(n), COUNT(*) FROM (SELECT restaurant_id, COUNT(*) n FROM stock_recipes WHERE is_active=1 GROUP BY restaurant_id) x;
SELECT 'stock_recipes_all', MAX(n), COUNT(*) FROM (SELECT restaurant_id, COUNT(*) n FROM stock_recipes GROUP BY restaurant_id) x;
-- 7 comida_items per source_type (listCatalogItems); 9 + 14 platos active / platos all (group menus v2, group_menus TitleJoin)
SELECT 'comida_items_by_type', MAX(n), COUNT(*) FROM (SELECT restaurant_id, source_type, COUNT(*) n FROM comida_items GROUP BY 1,2) x;
SELECT 'comida_items_platos_active', MAX(n), COUNT(*) FROM (SELECT restaurant_id, COUNT(*) n FROM comida_items WHERE source_type='platos' AND active=1 GROUP BY restaurant_id) x;
SELECT 'comida_items_platos_all', MAX(n), COUNT(*) FROM (SELECT restaurant_id, COUNT(*) n FROM comida_items WHERE source_type='platos' GROUP BY restaurant_id) x;
SELECT 'group_menu_dishes_per_tenant', MAX(n), COUNT(*) FROM (SELECT restaurant_id, COUNT(*) n FROM group_menu_section_dishes_v2 GROUP BY restaurant_id) x;
-- 8 wines
SELECT 'group_menu_wines_v2', MAX(n), COUNT(*) FROM (SELECT restaurant_id, COUNT(*) n FROM group_menu_wines_v2 GROUP BY restaurant_id) x;
-- 10 bookings searches (all, and non-cancelled for the invoice booking lookup)
SELECT 'bookings_all', MAX(n), COUNT(*) FROM (SELECT restaurant_id, COUNT(*) n FROM bookings GROUP BY restaurant_id) x;
SELECT 'bookings_not_cancelled', MAX(n), COUNT(*) FROM (SELECT restaurant_id, COUNT(*) n FROM bookings WHERE status != 'cancelled' GROUP BY restaurant_id) x;
-- 11 POS reservations (restaurant, date)
SELECT 'bookings_per_day', MAX(n), COUNT(*) FROM (SELECT restaurant_id, reservation_date, COUNT(*) n FROM bookings GROUP BY 1,2) x;
-- 12 assistant catalog resources
SELECT 'analytics_customers', MAX(n), COUNT(*) FROM (SELECT restaurant_id, COUNT(*) n FROM analytics_customers GROUP BY restaurant_id) x;
SELECT 'BEBIDAS', MAX(n), COUNT(*) FROM (SELECT restaurant_id, COUNT(*) n FROM "BEBIDAS" GROUP BY restaurant_id) x;
SELECT 'CAFES', MAX(n), COUNT(*) FROM (SELECT restaurant_id, COUNT(*) n FROM "CAFES" GROUP BY restaurant_id) x;
SELECT 'menus', MAX(n), COUNT(*) FROM (SELECT restaurant_id, COUNT(*) n FROM menus GROUP BY restaurant_id) x;
SELECT 'pos_products', MAX(n), COUNT(*) FROM (SELECT restaurant_id, COUNT(*) n FROM pos_products GROUP BY restaurant_id) x;
SELECT 'restaurant_members', MAX(n), COUNT(*) FROM (SELECT restaurant_id, COUNT(*) n FROM restaurant_members GROUP BY restaurant_id) x;
-- 13 AI usage: whatsapp_message_logs max rows in any 1 / 7 / 31-day window per tenant + content bytes
WITH d AS (SELECT restaurant_id, created_at::date AS day, COUNT(*) n, SUM(octet_length(COALESCE(content,''))) b FROM whatsapp_message_logs GROUP BY 1,2),
w AS (SELECT restaurant_id, day, n,
        SUM(n) OVER (PARTITION BY restaurant_id ORDER BY day RANGE BETWEEN INTERVAL '6 days' PRECEDING AND CURRENT ROW) n7,
        SUM(n) OVER (PARTITION BY restaurant_id ORDER BY day RANGE BETWEEN INTERVAL '30 days' PRECEDING AND CURRENT ROW) n31,
        SUM(b) OVER (PARTITION BY restaurant_id ORDER BY day RANGE BETWEEN INTERVAL '30 days' PRECEDING AND CURRENT ROW) b31
      FROM d)
SELECT 'whatsapp_message_logs' AS site, MAX(n) AS max_1d, MAX(n7) AS max_7d, MAX(n31) AS max_31d, MAX(b31) AS max_bytes_31d FROM w;

ROLLBACK;
