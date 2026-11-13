# Runbook de cutover MySQL → PostgreSQL nativo — domingo 15 nov 2026, 23:00 (Europe/Madrid)

Estado: **PREPARADO, NO EJECUTADO.** Solo se ejecuta por orden explícita de Main. Revisado en pgnative-J con hechos de producción y los flags reales de dbmigrate.

- Scripts en `scripts/cutover/`: 00, 10, 20, 30, 40, 50, 90, `lib.sh`, `gensql.py` y `rehearsal-desktop.sh`.
- Por defecto están en **DRY-RUN**. Para ejecutar hacen falta `--execute` **y** `CUTOVER_GO="GO <paso>"`, escrito por el operador.
- Rechazan ejecutarse fuera de `kraken`; el ensayo, en cambio, rechaza kraken.
- Los logs van con permisos 0600 y no contienen secretos.

Leyenda: **[GO]** orden explícita de Main/Jaime · **[IRREVERSIBLE]** · **[STOP]** parada.

## Arranque de producción en PG (resuelto en pgnative-K, rama bot/postgres-native-20261113)
`cmd/server` tiene ahora una ruta de **producción** para `DB_DRIVER=postgres` (`cmd/server/pg_production.go`, coordination id `pg_production_start_v1`). El modo QA local (`VC_PG_LOCAL_HTTP_QA`) no cambia.

La ruta de producción mantiene la dirección normal (:8080), los workers en segundo plano y los proveedores, igual que el modo MySQL, y ejecuta todos los gates:
- marker de importación;
- `RequireNativeKeys` (117 índices ux__, app.norm IMMUTABLE, sin `__wk`/`__pad`/colación dbmigrate);
- `RequireOnUpdateTriggers`.

**No aplica** las migraciones MySQL en PG. Variables (solo nombres; los valores van en `$CUTOVER_DIR/pg-vars.env`, 0600):

| variable | valor / regla |
|---|---|
| `DB_DRIVER` | `postgres` |
| `DB_PG_PRODUCTION_APPROVED` | `1` (aprobación explícita; sin ella el servidor se niega a arrancar) |
| `DATABASE_URL` | `postgres://…@127.0.0.1:55432/newvillacarmen_business?sslmode=disable` (TCP o socket; sslmode tal cual) |
| `DB_PG_ACCEPT_IMPORT_STAGING` | `orphans=731,pending=329,fks_not_valid=4`: debe coincidir **exactamente** con el marker o el arranque es fatal. Sin ella, el marker tiene que estar "ready", igual que antes |
| `BOT_CONTEXT_DRIVER` | `postgres` |
| `BOT_CONTEXT_POSTGRES_DSN` | el mismo DSN |
| `BOT_CONTEXT_POSTGRES_SCHEMA` | `bot_context` |
| `VC_PG_LOCAL_HTTP_QA`, `VC_PG_LOCAL_HTTP_QA_SCRATCH_WAIVE` | **no deben existir**: el servidor se niega a arrancar |

Los 329 pendientes del marker son 328 del plan más 1 por las FKs NOT VALID:
- **190 `range_narrowing`:** unsigned → rango firmado; los datos caben; los generadores de id son int64.
- **136 `on_update_explicit_marked_qa`:** triggers implementados y verificados en scratch.
- **1 `storage_app_adapter`:** assistant_messages.content como bytea; el código ya hace bind/scan con []byte.
- **1 `view_activation_review`:** recent_conversations; ningún lector en el código.

Ninguno bloquea la ejecución; se aceptan explícitamente con la variable. Los 731 huérfanos (4 FKs NOT VALID) se conservan sin tocarlos.

**Temporizadores del host** (`villacarmen-ops-check`, `villacarmen-cdn-sweep`) leen `/etc/villacarmen/backend.env` y escriben en la BD. Hay que pararlos en el freeze. Para reactivarlos en PG necesitan `DB_DRIVER=postgres`, `DB_PG_TOOLS_QA_APPROVED=1` y `DATABASE_URL`. Decisión: dejarlos parados hasta el día siguiente.

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
8. **Imagen solo para desktop/QA** (no se ejecuta aquí): desde la raíz multi-repo con este backend en `backend/`:
   `docker build -f backend/Dockerfile.prod -t newvillacarmen-prod-backend:pgnative-20261113 .`
   En producción, `release-backend-prod.sh` reconstruye desde su contexto cerrado. El switch PG usa `40-switch.sh`, que replica la especificación de `docker run` del release con el env PG.

## 2. Ventana del domingo
| hora | paso | script | duración máx. | tipo |
|---|---|---|---|---|
| 22:45 | Asistencia, canal, lectura del runbook | — | 15 min | — |
| 23:00 | **[GO "GO precheck"]** precheck: todo PASS | 00-precheck.sh | 10 min | solo lectura · **[STOP]** si hay FAIL |
| 23:10 | **[GO "GO freeze"]** mantenimiento ON; parar laya-heads → backoffice → bot-pipeline → backend; MySQL `read_only=ON`; recuentos | 20-freeze.sh | 5 min | reversible · web y BO fuera de servicio |
| 23:15 | **[GO "GO backup"]** mysqldump del host + sha256, restore check con `mysql:8.0.46` desechable, imagen, inspect, env, volumen del bot | 10-backup.sh | 15 min | **[STOP]** si los recuentos difieren |
| 23:30 | **[GO "GO import"]** schema/export (binario en el host, socket MySQL), referencia independiente, crosscheck, plan, pgcompat, allowlist, PG nuevo, import, verify, ida y vuelta, gate, sqlcheck, recuentos, store del bot | 30-import.sh | 20 min | **[STOP]** duro → 90 con `SWITCH_AT=none` |
| 23:50 | **Decisión 1** (Main/Jaime) | — | 5 min | — |
| 23:55 | **[GO "GO switch"]** diff de nombres del env, tag `mysql-pre-cutover`, backend PG, healthz | 40-switch.sh | 10 min | **[IRREVERSIBLE para datos]** desde que entra tráfico · requiere DB_PG_PRODUCTION_APPROVED=1 y el match exacto de DB_PG_ACCEPT_IMPORT_STAGING |
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
- [ ] Imagen `pgnative-20261113` construida desde el commit aprobado (id anotado); `pg-vars.env` preparado; temporizadores del host parados
- [ ] Ensayo en desktop: import y verify OK, ida y vuelta OK, gate OK, recuentos idénticos, errores de sqlcheck revisados
- [ ] Precheck sin FAIL: huérfanos 1/726/3/1 (bo_invoice_comments, invoice_line_items, site_builder_domain_mappings, site_builder_pages); NUL = 1; disco OK
- [ ] Freeze: sin sesiones escribiendo; read_only=ON; recuentos guardados
- [ ] Backup: sha256 y restauración idénticos
- [ ] Import: todos los STOP en verde
- [ ] Switch: diff de nombres revisado; healthz
- [ ] Smoke: OK, el mensaje del bot solo a +34692747052, 0 errores 23505/500
- [ ] GO final con la hora anotada
