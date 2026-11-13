# Runbook de cutover MySQL → PostgreSQL nativo — domingo 15 nov 2026, 23:00 (Europe/Madrid)

Estado: **PREPARADO, NO EJECUTADO.** Solo se ejecuta por orden explícita de Main. Revisado en pgnative-J con hechos de producción y los flags reales de dbmigrate.

- Scripts en `scripts/cutover/`: 00, 10, 20, 30, 40, 50, 90, `lib.sh`, `gensql.py` y `rehearsal-desktop.sh`.
- Por defecto están en **DRY-RUN**. Para ejecutar hacen falta `--execute` **y** `CUTOVER_GO="GO <paso>"`, escrito por el operador.
- Rechazan ejecutarse fuera de `kraken`; el ensayo, en cambio, rechaza kraken.
- Los logs van con permisos 0600 y no contienen secretos.

Leyenda: **[GO]** orden explícita de Main/Jaime · **[IRREVERSIBLE]** · **[STOP]** parada.

## ⚠ Bloqueante antes del domingo (código)
`cmd/server/main.go` rechaza `DB_DRIVER=postgres` salvo con `VC_PG_LOCAL_HTTP_QA=1`. Ese modo QA obliga a:
- escuchar solo en loopback;
- `BO_DISABLE_BACKGROUND_WORKERS=1`;
- arrancar sin proveedores (WhatsApp, Stripe, email).

Por tanto **no sirve para producción**. Antes del domingo hacen falta:
1. un cambio de código revisado: una ruta de arranque PG de producción que mantenga los gates (marker, `RequireNativeKeys`, triggers);
2. una decisión sobre el marker de importación: con 4 FKs NOT VALID y los pendientes abiertos, `ready_for_cutover=false` y el gate actual rechaza el arranque;
3. una imagen nueva del backend compilada desde el commit aprobado.

**Sin esto, el paso 40 no puede ejecutarse.**

## 0. Hechos de producción (observados en solo lectura)
- **MySQL:** servicio del host `mysql.service` 8.0.46 en 127.0.0.1:3306, socket `/var/run/mysqld/mysqld.sock`, BD `newvillacarmen`, usuario `villacarmen`, unos 80 MB. El import tarda **minutos**.
- **Backend** `newvillacarmen-backend-prod`:
  - network host, restart unless-stopped, entrypoint `/app/backend`, puerto 8080;
  - lee `DB_HOST/DB_PORT/DB_NAME/DB_USER/DB_PASSWORD`;
  - volumen `newvillacarmen-prod_whatsapp-bot-context` en `/var/lib/herorestaurant` (SQLite del bot).
- **bot-pipeline:** proyecto compose `newvillacarmen-prod` (`/var/www/newvillacarmen/backend/deploy/docker-compose.prod.yml`). Despliegue con `release-backend-prod.sh` y `release-backoffice-prod.sh`, en el mismo directorio.
- **nginx:** alqueriavillacarmen.com y backoffice.alqueriavillacarmen.com → 127.0.0.1:8080 (y 3011).
- **Backups:** `/var/www/newvillacarmen/backups`, con mysqldump del **host**.

## 1. Antes del domingo
1. **Ensayo en desktop** (`scripts/cutover/rehearsal-desktop.sh`, `CUTOVER_GO="GO rehearsal" --execute`): origen `instinct-villacarmen-mysql-source-20261111`, destino PG nuevo `instinct-villacarmen-padrehearsal-20261113`.
   - Sus errores de sqlcheck, revisados, se convierten en `sqlcheck-errors-expected.txt`.
   - Las duraciones medidas sustituyen a las estimaciones de la tabla.
2. **Cómo pasa `release-backend-prod.sh` el entorno al backend** (env-file, `-e` o compose): de ahí salen `BACKEND_ENV` y `BACKEND_UP_CMD`. Comprobarlo en solo lectura, solo los nombres.
3. **Mantenimiento:** un snippet de nginx que devuelva 503 en los dos vhosts (`MAINT_ON_CMD`/`MAINT_OFF_CMD` = enlazar el snippet + `nginx -t && systemctl reload nginx`).
4. **WhatsApp entrante:** confirmar si `evo-weai-villacarmen` reintenta o encola el webhook mientras 8080 está caído.
5. **Ficheros 0600 en `$CUTOVER_DIR`:**
   - `mysql-ro.cnf` (lectura), un cliente admin solo para `read_only`, `pg.env` y `backend.env.pg` (ver 40-switch);
   - `MYSQL_DSN` por socket: `villacarmen:<pw>@unix(/var/run/mysqld/mysqld.sock)/newvillacarmen?information_schema_stats_expiry=0` (el parametro es obligatorio: sin el, la cache de estadisticas de MySQL 8 devuelve AUTO_INCREMENT NULL y el import falla).
6. **Binarios y fuentes:** `bin/` con sha256 en `expected-sha256.txt`, `src/` con un checkout del commit aprobado, y la imagen `postgres:17-alpine` ya descargada.
7. **Migración del store del bot:** SQLite del volumen → esquema `bot_context` con `botstore-migrate`. El comando exacto (`BOTSTORE_MIGRATE_CMD`) se ensaya en desktop.
8. **Bloqueante de código** (arriba): cambio revisado, imagen nueva y decisión sobre el marker.

## 2. Ventana del domingo
| hora | paso | script | duración máx. | tipo |
|---|---|---|---|---|
| 22:45 | Asistencia, canal, lectura del runbook | — | 15 min | — |
| 23:00 | **[GO "GO precheck"]** precheck: todo PASS | 00-precheck.sh | 10 min | solo lectura · **[STOP]** si hay FAIL |
| 23:10 | **[GO "GO freeze"]** mantenimiento ON; parar laya-heads → backoffice → bot-pipeline → backend; MySQL `read_only=ON`; recuentos | 20-freeze.sh | 5 min | reversible · web y BO fuera de servicio |
| 23:15 | **[GO "GO backup"]** mysqldump del host + sha256, restore check con `mysql:8.0.46` desechable, imagen, inspect, env, volumen del bot | 10-backup.sh | 15 min | **[STOP]** si los recuentos difieren |
| 23:30 | **[GO "GO import"]** schema/export (binario en el host, socket MySQL), referencia independiente, crosscheck, plan, pgcompat, allowlist, PG nuevo, import, verify, ida y vuelta, gate, sqlcheck, recuentos, store del bot | 30-import.sh | 20 min | **[STOP]** duro → 90 con `SWITCH_AT=none` |
| 23:50 | **Decisión 1** (Main/Jaime) | — | 5 min | — |
| 23:55 | **[GO "GO switch"]** diff de nombres del env, tag `mysql-pre-cutover`, backend PG, healthz | 40-switch.sh | 10 min | **[IRREVERSIBLE para datos]** desde que entra tráfico · requiere el bloqueante resuelto |
| 00:05 | **[GO "GO smoke"]** healthz, menús, BO, visita POS, 1 mensaje desde +34692747052 (solo ese número), logs | 50-smoke.sh | 20 min | — |
| 00:25 | **Decisión 2**: GO → arrancar bot-pipeline/backoffice/laya-heads y mantenimiento OFF; NO-GO → 90 | 50 / 90 | — | — |
| 01:30 | **Hora límite**: sin GO final, rollback | 90-rollback.sh | 15 min | — |

## 3. Rollback (`90-rollback.sh`, [GO "GO rollback"])
1. Parar el backend.
2. Si hubo tráfico en PG, exportar a CSV las filas con created_at/updated_at ≥ SWITCH_AT. **No captura borrados ni tablas sin timestamps.**
3. Restaurar el env y el tag `mysql-pre-cutover`.
4. MySQL `read_only=OFF`.
5. Arrancar, comprobar healthz y descongelar.

PG (contenedor y volumen) se conserva.

## 4. Checklist go/no-go
- [ ] Bloqueante de código resuelto e imagen nueva con sha256 anotado
- [ ] Ensayo en desktop: import y verify OK, ida y vuelta OK, gate OK, recuentos idénticos, errores de sqlcheck revisados
- [ ] Precheck sin FAIL: huérfanos 1/726/3/1 (bo_invoice_comments, invoice_line_items, site_builder_domain_mappings, site_builder_pages); NUL = 1; disco OK
- [ ] Freeze: sin sesiones escribiendo; read_only=ON; recuentos guardados
- [ ] Backup: sha256 y restauración idénticos
- [ ] Import: todos los STOP en verde
- [ ] Switch: diff de nombres revisado; healthz
- [ ] Smoke: OK, el mensaje del bot solo a +34692747052, 0 errores 23505/500
- [ ] GO final con la hora anotada
