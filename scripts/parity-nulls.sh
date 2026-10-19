#!/usr/bin/env bash
# Los 34 platos legacy con v2_dish_id NULL: ¿estan en el catalogo o hay que migrarlos?
set -a; . /var/www/newvillacarmen/.env 2>/dev/null; set +a
DB="${1:-newvillacarmen}"
q() { mysql -h "${DB_HOST}" -P "${DB_PORT}" -u "${DB_USER}" -p"$DB_PASSWORD" -D "$DB" -N -B -e "$1" 2>&1 | grep -v "Using a password"; }

echo "  === $DB ==="
echo "  -- de los 34 sin v2_dish_id, ¿cuantos tienen su descripcion en menu_dishes_catalog? --"
q "SELECT CONCAT('DIA: ', SUM(CASE WHEN EXISTS (SELECT 1 FROM menu_dishes_catalog c WHERE c.restaurant_id=1 AND TRIM(c.title) COLLATE utf8mb4_0900_ai_ci = TRIM(m.legacy_desc) COLLATE utf8mb4_0900_ai_ci) THEN 1 ELSE 0 END),
      ' de ', COUNT(*))
   FROM legacy_dish_map m WHERE m.restaurant_id=1 AND m.legacy_table='DIA' AND m.v2_dish_id IS NULL;" | sed 's/^/     /'
q "SELECT CONCAT('FINDE: ', SUM(CASE WHEN EXISTS (SELECT 1 FROM menu_dishes_catalog c WHERE c.restaurant_id=1 AND TRIM(c.title) COLLATE utf8mb4_0900_ai_ci = TRIM(m.legacy_desc) COLLATE utf8mb4_0900_ai_ci) THEN 1 ELSE 0 END),
      ' de ', COUNT(*))
   FROM legacy_dish_map m WHERE m.restaurant_id=1 AND m.legacy_table='FINDE' AND m.v2_dish_id IS NULL;" | sed 's/^/     /'

echo
echo "  -- ¿cuantos platos de la v2 tienen catalog_dish_id NULL (sin catalogo)? --"
q "SELECT CONCAT('v2 sin catalog_dish_id: ', COUNT(*), ' de ', (SELECT COUNT(*) FROM group_menu_section_dishes_v2 WHERE restaurant_id=1))
   FROM group_menu_section_dishes_v2 WHERE restaurant_id=1 AND catalog_dish_id IS NULL;" | sed 's/^/     /'

echo
echo "  -- ¿la v2 tiene ya un plato con ese titulo (aun sin estar mapeado)? --"
q "SELECT CONCAT('encontrado en la v2 por titulo: ', SUM(CASE WHEN EXISTS (SELECT 1 FROM group_menu_section_dishes_v2 d WHERE d.restaurant_id=1 AND TRIM(d.title_snapshot) COLLATE utf8mb4_0900_ai_ci = TRIM(m.legacy_desc) COLLATE utf8mb4_0900_ai_ci) THEN 1 ELSE 0 END))
   FROM legacy_dish_map m WHERE m.restaurant_id=1 AND m.legacy_table IN ('DIA','FINDE') AND m.v2_dish_id IS NULL;" | sed 's/^/     /'

echo
echo "  -- los que NO estan en v2 por titulo: son los que habria que migrar --"
q "SELECT CONCAT(m.legacy_table, ' num=', m.legacy_num, ' | ', LEFT(REPLACE(REPLACE(m.legacy_desc, CHAR(10), ' '), CHAR(13), ' '), 60))
   FROM legacy_dish_map m WHERE m.restaurant_id=1 AND m.legacy_table IN ('DIA','FINDE') AND m.v2_dish_id IS NULL
     AND NOT EXISTS (SELECT 1 FROM group_menu_section_dishes_v2 d WHERE d.restaurant_id=1 AND TRIM(d.title_snapshot) COLLATE utf8mb4_0900_ai_ci = TRIM(m.legacy_desc) COLLATE utf8mb4_0900_ai_ci)
   LIMIT 12;" | sed 's/^/     /'

echo
echo "  -- ¿y estos tienen precio? (columnas de DIA/FINDE) --"
q "SHOW COLUMNS FROM DIA;" | awk '{print "     DIA: "$1}' | head -20
