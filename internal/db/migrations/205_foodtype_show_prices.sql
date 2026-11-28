-- 205_foodtype_show_prices.sql
-- Coordination id: foodtype_show_prices_v1
-- Per food-type public page (cafés, bebidas, postres, vinos): show or hide the
-- prices. Edited in /app/comida/<tipo> > Configuración; default keeps them on.

SET @c := (SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_page_visibility' AND COLUMN_NAME = 'cafes_show_prices');
SET @sql := IF(@c = 0, 'ALTER TABLE restaurant_page_visibility
  ADD COLUMN cafes_show_prices TINYINT(1) NOT NULL DEFAULT 1,
  ADD COLUMN bebidas_show_prices TINYINT(1) NOT NULL DEFAULT 1,
  ADD COLUMN postres_show_prices TINYINT(1) NOT NULL DEFAULT 1,
  ADD COLUMN vinos_show_prices TINYINT(1) NOT NULL DEFAULT 1', 'DO 0');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
