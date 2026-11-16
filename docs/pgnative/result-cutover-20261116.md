# Cutover MySQL → PostgreSQL de producción (Kraken), 16-nov-2026: PG EN PRODUCCIÓN

## Resultado
`RESULT=PG_LIVE`. Freeze 01:22:41 UTC → desfreeze 01:28:05 UTC (5 min sin servicio). Backend `newvillacarmen-backend-prod` con imagen `newvillacarmen-prod-backend:pgnative-20261116` (binario `0cae687e…`, commit 1f974fa) sobre `newvillacarmen-pg-prod` (volumen `newvillacarmen-pg-prod-data-20261116`, 127.0.0.1:55432).

| fase | resultado |
|---|---|
| precheck | PASS (orphans 1/726/3/1, NUL=1, sha binarios, PG/volumen/puerto libres) |
| freeze | PASS (mantenimiento ON, 4 contenedores parados, MySQL read_only) |
| backup | PASS `/var/www/newvillacarmen/backups/cutover-pg/cutover-pg-20261116T012414` (253 tablas idénticas en la restauración, sha OK) |
| import | PASS 253 tablas / 24 732 filas, crosscheck, verify, round trip, gate, sqlcheck 65 = 65 esperados, recuentos MySQL = PG, bot store SQLite (+WAL) → `bot_context` |
| stock_movements | 4 = 4, sin tocar |
| marker | `orphans=731,pending=329,fks_not_valid=4` = `DB_PG_ACCEPT_IMPORT_STAGING` |
| switch | PASS, `SWITCH_AT=2026-11-16T01:25:08+00:00` |
| smoke | PASS: healthz, menús públicos, bookings admin, POS (lectura), webhook Evolution sintético desde +34692747052 → fila en `bot_inbound_queue`, logs backend y PG sin errores (re-chequeo a los 150 s: 0) |
| desfreeze | PASS: www 200, backoffice 200 (/login) |

Respuesta del bot solo a 34692747052 (6 líneas `sender=34692747052`, ningún otro destinatario).

## Intentos previos de la misma noche (rollback pre-import, sin impacto en datos)
1. 00:48: carga 14,7 tras el backup (regla load < 6) → rollback.
2. 00:53: carga 10-15 durante 10 min (Playwright WebKit de otra sesión en Kraken) → rollback.
3. 01:11: `30-import` paso 2 `ERROR 1046 No database selected` (el arreglo del 15-nov se hizo a mano en Kraken y no estaba en el repo) → PR #761.
4. 01:17: `install -d -m 700 pgsock` sobre el directorio del PG del 15-nov (uid 70) → PR #762.

## Arreglos de código (PRs a `bot/postgres-native-20261113`)
- #755 ON CONFLICT de `bot_inbound_queue` = expresión del índice `app.norm_ascii` (causa del rollback del 15-nov); sqlcheck con EXPLAIN GENERIC_PLAN; runtime smoke.
- #756 scripts de cutover alineados con lo ejecutado el 15-nov (backup, freeze, import WAL, switch healthz, smoke con bot_inbound_queue + logs PG, 51-unfreeze, rollback por tabla).
- #757 `PUT /api/admin/booking-notifications` (bool → smallint en pgx); instatic host lookup vía `site_builder_sites`; auditoría de ON CONFLICT dinámicos.
- #758 GraphRAG: QA con neo4j-rust d509e4e, ranking IDF + stemming ES, bodies más ricos, horario semanal, `BotSnippets` compartido bot/-query, receta Kraken.
- #759 montaje del registro GraphRAG al recrear el backend; falso positivo de gitleaks.
- #760, #761, #762 30-import: DSN parseado, `-N -B --raw <db>`, pgsock nuevo.

Runtime smoke en desktop con el binario final contra copia del PG de producción: 909 peticiones, FAIL=0, sin 42P10.

## GraphRAG por restaurante (activado)
- Instancias `neo4j-rust-graphrag@1` (27100/27101) y `@343` (27102/27103), loopback, token 0600, registro `/home/jaime/graphrag/registry.tsv` 0600.
- Ingesta con rol `graphrag_ro` (solo lectura): R1 193 nodos / 192 aristas, R343 2 nodos; idempotente (unchanged=193); `graphrag-resync.timer` cada 30 min.
- Aislamiento: consultas de R1 nunca devuelven nodos de R343 y viceversa; token de R1 contra el puerto de R343 → 401; restaurante no registrado → 0 snippets.
- Bot: `VC_GRAPHRAG=1` + `VC_GRAPHRAG_REGISTRY` en el contenedor; `wa_bot_graphrag_v1 restaurant_id=1 graph_hits=4` y `=3` en dos mensajes de prueba; la respuesta a "¿Tenéis paella el domingo?" cita las paellas del grafo.
- Desactivar: recrear el contenedor sin `VC_GRAPHRAG` (env en `/srv/cutover-pg-20261115/backend.env.pg`, el de antes de activarlo) o `sudo systemctl stop neo4j-rust-graphrag@1` (0 snippets, el bot sigue igual que antes).

## Pendiente / riesgos
- En una de las dos pruebas del bot (turno privilegiado, herramienta `render_document`) MiniMax devolvió su marcado de tool-call como texto plano (`]<]minimax[>[<invoke name="render_document">…`, 5 365 caracteres) y se envió a 34692747052. 0 de 48 respuestas en los logs MySQL anteriores; no se ha podido atribuir a GraphRAG ni descartar. Revisar el parser de tool-calls de MiniMax.
- Timers del host (ops-check, cdn-sweep) no instalados en Kraken; nada que reactivar.
- Rollback a MySQL tras escrituras en PG: solo exporta filas con timestamp (ahora por tabla); conservar `newvillacarmen-backend-prod-mysql-precutover` y el backup.
- Contenedores conservados para análisis: `newvillacarmen-pg-prod-failed-20261115`, `newvillacarmen-backend-prod-pg-failed-20261115`, `newvillacarmen-backend-prod-pre-graphrag`.
- Vigilar 24 h logs y recursos.
