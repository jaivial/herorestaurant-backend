-- Backoffice A/B versioning: per-user app version per restaurant.
--
-- app_version selects which modules a user sees:
--   '0.1' (default) → reservas, menus/platos, comida, facturas, fichaje,
--                      horarios, miembros (intersected with role ACL).
--   '0.2'            → adds stock, tpv (POS), servidores/plataforma,
--                      estadisticas and the Anuncios tab inside Config.
--
-- The column lives on bo_user_restaurants so the same account can be on
-- different versions in different restaurants.

ALTER TABLE bo_user_restaurants
    ADD COLUMN app_version VARCHAR(8) NOT NULL DEFAULT '0.1' AFTER role;

-- A/B tester with full v0.2 access. Password: root123 (bcrypt, cost 10).
-- is_superadmin=1 makes the session resolve the 'root' role; the explicit
-- bo_user_restaurants row below pins restaurant 1 + app_version 0.2.
INSERT INTO bo_users (email, name, password_hash, is_superadmin)
VALUES ('root@villacarmen.com', 'Root', '$2a$10$vCLHQq1y32ZLDiQTbZPgFed1mT/sUXYvo/t.h9Nx71AzJ8GFBQ8PO', 1)
ON DUPLICATE KEY UPDATE
  name = VALUES(name),
  is_superadmin = VALUES(is_superadmin);

INSERT INTO bo_user_restaurants (user_id, restaurant_id, role, app_version)
SELECT u.id, 1, 'root', '0.2'
FROM bo_users u
WHERE u.email = 'root@villacarmen.com'
ON DUPLICATE KEY UPDATE
  role = VALUES(role),
  app_version = VALUES(app_version);

-- Current/existing admin stays on the stable v0.1 line (the column default
-- already does this for every existing row; keep the explicit update so the
-- intent is visible and the value survives any future default change).
UPDATE bo_user_restaurants ur
JOIN bo_users u ON u.id = ur.user_id
SET ur.app_version = '0.1'
WHERE LOWER(TRIM(u.email)) = 'admin@villacarmen.com';
