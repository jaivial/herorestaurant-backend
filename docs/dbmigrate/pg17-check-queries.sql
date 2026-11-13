-- PG17 syntax/plan check of the FOR KEY SHARE-in-EXISTS form (sqldb_native_pg_v1).
-- Run ONLY on the isolated staging copy (newvillacarmen_business). Everything is
-- PREPARE (parse + plan, no execution) inside a transaction that is rolled back.
BEGIN;
PREPARE vc_chk_bootstrap(integer) AS
WITH v(user_id, restaurant_id, role) AS (VALUES (CAST($1 AS integer), 1, CAST('admin' AS varchar(32))))
INSERT INTO bo_user_restaurants (user_id, restaurant_id, role)
SELECT v.user_id, v.restaurant_id, v.role FROM v
WHERE EXISTS (SELECT 1 FROM bo_users u WHERE u.id = v.user_id FOR KEY SHARE) AND EXISTS (SELECT 1 FROM restaurants r WHERE r.id = v.restaurant_id FOR KEY SHARE)
ON CONFLICT DO NOTHING;
PREPARE vc_chk_branding(integer, text) AS
WITH v(restaurant_id, brand_name) AS (VALUES (CAST($1 AS integer), CAST($2 AS varchar(255))))
INSERT INTO restaurant_branding (restaurant_id, brand_name) SELECT v.restaurant_id, v.brand_name FROM v
WHERE EXISTS (SELECT 1 FROM restaurants r WHERE r.id = v.restaurant_id FOR KEY SHARE) ON CONFLICT DO NOTHING;
PREPARE vc_chk_steps(text, text, integer, bigint, bigint) AS
UPDATE stock_recipe_steps t SET title=COALESCE(v.ti, t.title), description=COALESCE(v.de, t.description)
FROM (VALUES (CAST($1 AS text), CAST($2 AS text))) AS v(ti, de)
WHERE t.restaurant_id=$3 AND t.id=$4 AND t.recipe_id=$5
  AND (t.title COLLATE "C", t.description COLLATE "C") IS DISTINCT FROM (COALESCE(v.ti, t.title) COLLATE "C", COALESCE(v.de, t.description) COLLATE "C");
SELECT name, statement IS NOT NULL AS prepared FROM pg_prepared_statements WHERE name LIKE 'vc_chk_%' ORDER BY name;
ROLLBACK;

