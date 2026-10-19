#!/usr/bin/env bash
# Diagnostico: por que hay platos legacy sin fila en la v2.
set -a; . /var/www/newvillacarmen/.env 2>/dev/null; set +a
DB="${1:-newvillacarmen}"
q() { mysql -h "${DB_HOST}" -P "${DB_PORT}" -u "${DB_USER}" -p"$DB_PASSWORD" -D "$DB" -N -B -e "$1" 2>&1 | grep -v "Using a password"; }

echo "  === $DB ==="
echo "  -- mapa: v2_dish_id NULL vs huerfanas (por legacy_table) --"
q "SELECT CONCAT(m.legacy_table, ' | v2_dish_id NULL: ', SUM(CASE WHEN m.v2_dish_id IS NULL THEN 1 ELSE 0 END),
      ' | huerfanas: ', SUM(CASE WHEN m.v2_dish_id IS NOT NULL AND d.id IS NULL THEN 1 ELSE 0 END))
   FROM legacy_dish_map m LEFT JOIN group_menu_section_dishes_v2 d ON d.id = m.v2_dish_id
  WHERE m.restaurant_id = 1 GROUP BY m.legacy_table;" | sed 's/^/     /'

echo
echo "  -- los NULL: ¿existen en DIA/FINDE? --"
q "SELECT CONCAT('mapa con v2_dish_id NULL: ', COUNT(*))
   FROM legacy_dish_map WHERE restaurant_id=1 AND v2_dish_id IS NULL;" | sed 's/^/     /'
q "SELECT CONCAT('de esos, su legacy_num existe en la tabla legacy: ',
      SUM(CASE WHEN EXISTS (SELECT 1 FROM DIA WHERE DIA.restaurant_id=1 AND DIA.NUM=m.legacy_num) THEN 1 ELSE 0 END))
   FROM legacy_dish_map m WHERE m.restaurant_id=1 AND m.legacy_table='DIA' AND m.v2_dish_id IS NULL;" | sed 's/^/     /'
q "SELECT CONCAT('de esos, su legacy_num existe en la tabla legacy: ',
      SUM(CASE WHEN EXISTS (SELECT 1 FROM FINDE WHERE FINDE.restaurant_id=1 AND FINDE.NUM=m.legacy_num) THEN 1 ELSE 0 END))
   FROM legacy_dish_map m WHERE m.restaurant_id=1 AND m.legacy_table='FINDE' AND m.v2_dish_id IS NULL;" | sed 's/^/     /'

echo
echo "  -- muestra de los NULL (descripcion legacy) --"
q "SELECT CONCAT(m.legacy_table, ' num=', m.legacy_num, ' | ', LEFT(m.legacy_desc, 55))
   FROM legacy_dish_map m WHERE m.restaurant_id=1 AND m.v2_dish_id IS NULL LIMIT 10;" | sed 's/^/     /'

echo
echo "  -- ¿los platos legacy SIN fila en el mapa (se perderian) --"
q "SELECT CONCAT('DIA sin fila en el mapa: ', COUNT(*))
   FROM DIA d WHERE d.restaurant_id=1
     AND NOT EXISTS (SELECT 1 FROM legacy_dish_map m WHERE m.restaurant_id=1 AND m.legacy_table='DIA' AND m.legacy_num=d.NUM);" | sed 's/^/     /'
q "SELECT CONCAT('FINDE sin fila en el mapa: ', COUNT(*))
   FROM FINDE d WHERE d.restaurant_id=1
     AND NOT EXISTS (SELECT 1 FROM legacy_dish_map m WHERE m.restaurant_id=1 AND m.legacy_table='FINDE' AND m.legacy_num=d.NUM);" | sed 's/^/     /'

echo
echo "  -- esos sin mapa: ¿estan en menu_dishes_catalog por nombre? --"
q "SELECT CONCAT(d.NUM, ' | ', LEFT(d.descripcion, 45), ' | en catalogo: ',
      IF(EXISTS (SELECT 1 FROM menu_dishes_catalog c WHERE c.restaurant_id=1 AND c.name = d.descripcion), 'SI', 'NO'))
   FROM DIA d WHERE d.restaurant_id=1
     AND NOT EXISTS (SELECT 1 FROM legacy_dish_map m WHERE m.restaurant_id=1 AND m.legacy_table='DIA' AND m.legacy_num=d.NUM)
   LIMIT 10;" | sed 's/^/     /'

echo
echo "  -- ¿el catalogo tiene los platos por nombre? --"
q "SELECT CONCAT('menu_dishes_catalog: ', COUNT(*), ' platos') FROM menu_dishes_catalog WHERE restaurant_id=1;" | sed 's/^/     /'
q "SELECT CONCAT('  con nombre: ', COUNT(*)) FROM menu_dishes_catalog WHERE restaurant_id=1 AND name IS NOT NULL AND name<>'';" | sed 's/^/     /'
