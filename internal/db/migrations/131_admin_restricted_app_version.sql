-- Restrict the owner admin account to the operational modules only.
--
-- App version 0.4 is the restricted line: instead of unlocking extra modules it
-- whitelists reservas, carta (menus/comida), miembros, horarios, fichaje,
-- facturas and campanas and denies everything else (stock, TPV, estadisticas,
-- plataforma, ajustes, website, reportes, estado_cuenta). See
-- internal/api/bo_app_version.go (boAppVersion04Modules).
--
-- Only this account moves to 0.4; every other user keeps its version.
UPDATE bo_user_restaurants ur
JOIN bo_users u ON u.id = ur.user_id
SET ur.app_version = '0.4'
WHERE LOWER(TRIM(u.email)) = 'admin@villacarmen.com';
