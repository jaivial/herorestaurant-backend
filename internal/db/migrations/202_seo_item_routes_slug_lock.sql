-- 202_seo_item_routes_slug_lock.sql
-- Bloqueo manual del slug de una ruta SEO.
-- Coordination id: seo_item_routes_v1
--
-- slug_locked = 1 significa que el slug se ha escrito a mano en el backoffice:
-- ya no se regenera aunque el plato o el vino cambie de nombre.
--
-- Las rutas generadas automaticamente (slug_locked = 0) conservan su slug
-- cuando el nombre cambia (la URL y el SEO ya no se rompen), pero se
-- reparan cuando el slug guardado salio del primer algoritmo, que partia
-- palabras con acento y cortaba a media palabra.

ALTER TABLE `seo_item_routes`
  ADD COLUMN `slug_locked` TINYINT(1) NOT NULL DEFAULT 0 AFTER `slug`;
