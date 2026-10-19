#!/usr/bin/env bash
# Por que los 34 no casaron: probamos la MISMA normalizacion de la migracion 180
# y miramos si existen en la v2 con otro menu_id, o si de verdad no existen.
set -a; . /var/www/newvillacarmen/.env 2>/dev/null; set +a
DB="${1:-newvillacarmen}"
q() { mysql -h "${DB_HOST}" -P "${DB_PORT}" -u "${DB_USER}" -p"$DB_PASSWORD" -D "$DB" -N -B -e "$1" 2>&1 | grep -v "Using a password"; }

echo "  === $DB ==="
echo "  -- menu_id de la v2 (que menus existen) --"
q "SELECT CONCAT('menu_id=', menu_id, ' platos=', COUNT(*), ' activos=', SUM(active=1))
   FROM group_menu_section_dishes_v2 WHERE restaurant_id=1 GROUP BY menu_id ORDER BY menu_id;" | sed 's/^/     /'

echo
echo "  -- seccion de DIA y FINDE (que section_id usa cada uno) --"
q "SELECT CONCAT('section_id=', section_id, ' menu_id=', menu_id, ' platos=', COUNT(*))
   FROM group_menu_section_dishes_v2 WHERE restaurant_id=1 GROUP BY section_id, menu_id ORDER BY menu_id, section_id;" | sed 's/^/     /'

echo
echo "  -- los 34 no mapeados: normalizados, ¿existen en la v2 con la MISMA normalizacion? --"
q "
WITH n AS (
  SELECT m.legacy_table, m.legacy_num, m.legacy_desc,
         LOWER(TRIM(REGEXP_REPLACE(REGEXP_REPLACE(REGEXP_REPLACE(REGEXP_REPLACE(
           CONVERT(IFNULL(m.legacy_desc,'') USING utf8mb4),
           '\\\\([^)]*\\\\)', ''), '[aáàäâ]', 'a'), '[eéèëê]', 'e'), '[^a-zA-Z0-9]+', ' ')))
         COLLATE utf8mb4_unicode_ci AS k
    FROM legacy_dish_map m
   WHERE m.restaurant_id=1 AND m.legacy_table IN ('DIA','FINDE') AND m.v2_dish_id IS NULL
), v AS (
  SELECT d.id, d.menu_id, d.active,
         LOWER(TRIM(REGEXP_REPLACE(REGEXP_REPLACE(REGEXP_REPLACE(REGEXP_REPLACE(
           CONVERT(IFNULL(d.title_snapshot,'') USING utf8mb4),
           '\\\\([^)]*\\\\)', ''), '[aáàäâ]', 'a'), '[eéèëê]', 'e'), '[^a-zA-Z0-9]+', ' ')))
         COLLATE utf8mb4_unicode_ci AS k
    FROM group_menu_section_dishes_v2 d
   WHERE d.restaurant_id=1
)
SELECT CONCAT(n.legacy_table, ' num=', n.legacy_num,
       ' | en v2 (cualquier menu): ', IF(EXISTS (SELECT 1 FROM v WHERE v.k=n.k), 'SI', 'NO'),
       ' | en v2 menu 41/42: ', IF(EXISTS (SELECT 1 FROM v WHERE v.k=n.k AND v.menu_id IN (41,42)), 'SI', 'NO'),
       ' | ', LEFT(REPLACE(REPLACE(n.legacy_desc, CHAR(10), ' '), CHAR(13), ' '), 40))
  FROM n ORDER BY n.legacy_table, n.legacy_num;" | sed 's/^/     /'

echo
echo "  -- ¿DIA y FINDE tienen columna de precio? (para saber que se pierde) --"
q "SHOW COLUMNS FROM FINDE;" | awk '{print "     FINDE: "$1" "$2}' | head -12
