#!/usr/bin/env bash
# Valida que la v2 cubre exactamente lo que hay en las tablas legacy, antes de borrarlas.
set -a; . /var/www/newvillacarmen/.env 2>/dev/null; set +a
DB="${1:-newvillacarmen}"
q() { mysql -h "${DB_HOST}" -P "${DB_PORT}" -u "${DB_USER}" -p"$DB_PASSWORD" -D "$DB" -N -B -e "$1" 2>&1 | grep -v "Using a password"; }

echo "  === $DB ==="
echo "  -- legacy vs mapa vs v2 --"
for T in DIA FINDE; do
  LEG=$(q "SELECT COUNT(*) FROM \`$T\` WHERE restaurant_id=1;")
  MAP=$(q "SELECT COUNT(*) FROM legacy_dish_map WHERE restaurant_id=1 AND legacy_table='$T';")
  NOV2=$(q "SELECT COUNT(*) FROM legacy_dish_map m LEFT JOIN group_menu_section_dishes_v2 d ON d.id=m.v2_dish_id WHERE m.restaurant_id=1 AND m.legacy_table='$T' AND d.id IS NULL;")
  ACT=$(q "SELECT COUNT(*) FROM group_menu_section_dishes_v2 WHERE restaurant_id=1 AND active=1;")
  echo "     $T: legacy=$LEG | en legacy_dish_map=$MAP | sin fila v2=$NOV2"
done
echo "     v2 platos activos (restaurant 1): $(q "SELECT COUNT(*) FROM group_menu_section_dishes_v2 WHERE restaurant_id=1 AND active=1;")"

echo
echo "  -- ¿cada legacy mapeado tiene su v2? --"
q "SELECT 'mapeados='||(SELECT COUNT(*) FROM legacy_dish_map WHERE restaurant_id=1 AND legacy_table IN ('DIA','FINDE'))
     ||' con_v2='||(SELECT COUNT(*) FROM legacy_dish_map m JOIN group_menu_section_dishes_v2 d ON d.id=m.v2_dish_id WHERE m.restaurant_id=1 AND m.legacy_table IN ('DIA','FINDE'))
     ||' sin_v2='||(SELECT COUNT(*) FROM legacy_dish_map m LEFT JOIN group_menu_section_dishes_v2 d ON d.id=m.v2_dish_id WHERE m.restaurant_id=1 AND m.legacy_table IN ('DIA','FINDE') AND d.id IS NULL);" | sed 's/^/     /'

echo
echo "  -- ¿los ‘active’ de la v2 coinciden con los de DIA/FINDE? --"
q "SELECT 'v2 activos='||(SELECT COUNT(*) FROM group_menu_section_dishes_v2 WHERE restaurant_id=1 AND active=1)
     ||' | legacy activos (DIA+FINDE)='||(SELECT (SELECT COUNT(*) FROM DIA WHERE restaurant_id=1 AND active=1)+(SELECT COUNT(*) FROM FINDE WHERE restaurant_id=1 AND active=1))
     ||' | dishes legacy con active=0 en la v2='||(SELECT COUNT(*) FROM legacy_dish_map m JOIN group_menu_section_dishes_v2 d ON d.id=m.v2_dish_id WHERE m.restaurant_id=1 AND m.legacy_table IN ('DIA','FINDE') AND d.active=0);" | sed 's/^/     /'

echo
echo "  -- ¿todos los platos legacymap﻿eados tienen foto/descripcion en la v2? --"
q "SELECT 'sin title_snapshot='||(SELECT COUNT(*) FROM legacy_dish_map m JOIN group_menu_section_dishes_v2 d ON d.id=m.v2_dish_id WHERE m.restaurant_id=1 AND m.legacy_table IN ('DIA','FINDE') AND (d.title_snapshot IS NULL OR d.title_snapshot=''));" | sed 's/^/     /'

echo
echo "  -- VINOS: paridad legacy vs v2 --"
q "SELECT 'legacy='||(SELECT COUNT(*) FROM VINOS)||' v2='||(SELECT COUNT(*) FROM group_menu_wines_v2)
     ||' mapeados='||(SELECT COUNT(*) FROM legacy_wine_map)
     ||' v2_con_foto_path='||(SELECT COUNT(*) FROM group_menu_wines_v2 WHERE foto_path IS NOT NULL AND foto_path<>'')
     ||' v2_activos='||(SELECT COUNT(*) FROM group_menu_wines_v2 WHERE active=1)
     ||' legacy_activos='||(SELECT COUNT(*) FROM VINOS WHERE active=1);" | sed 's/^/     /'

echo
echo "  -- ¿los nombres coinciden vino a vino? --"
q "SELECT COUNT(*) FROM legacy_wine_map m JOIN group_menu_wines_v2 w ON w.id=m.v2_wine_id JOIN VINOS o ON o.num=m.legacy_num WHERE o.nombre<>w.nombre;" | sed 's/^/     nombres discrepantes: /'

echo
echo "  -- ¿los precios coinciden? --"
q "SELECT COUNT(*) FROM legacy_wine_map m JOIN group_menu_wines_v2 w ON w.id=m.v2_wine_id JOIN VINOS o ON o.num=m.legacy_num WHERE ABS(COALESCE(o.precio,0)-COALESCE(w.precio,0))>0.001;" | sed 's/^/     precios discrepantes: /'

echo
echo "  -- PLATOS --"
q "SELECT 'PLATOS filas='||(SELECT COUNT(*) FROM PLATOS);" | sed 's/^/     /'
