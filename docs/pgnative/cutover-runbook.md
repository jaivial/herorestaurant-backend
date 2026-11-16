# Runbook de cutover MySQL → PostgreSQL nativo — domingo 15 nov 2026, 23:00 (Europe/Madrid)

Estado: **PREPARADO, NO EJECUTADO.** Solo se ejecuta por orden explícita de Main. Revisado en pgnative-J con hechos de producción y los flags reales de dbmigrate.

- Scripts en `scripts/cutover/`: 00, 10, 20, 30, 40, 50, 51, 90, `lib.sh`, `gensql.py`, `rehearsal-desktop.sh` y `runtime-smoke-pg.sh`.
- Por defecto están en **DRY-RUN**. Para ejecutar hacen falta `--execute` **y** `CUTOVER_GO="GO <paso>"`, escrito por el operador.
- Rechazan ejecutarse fuera de `kraken`; el ensayo, en cambio, rechaza kraken.
- Los logs van con permisos 0600 y no contienen secretos.

Leyenda: **[GO]** orden explícita de Main/Jaime · **[IRREVERSIBLE]** · **[STOP]** parada.

## Cambios tras el intento del 15-nov (ROLLBACK por 42P10)
Los scripts del repo recogen ahora los parches hechos a mano en Kraken y los fallos del intento:
- **00-precheck:** NUL con `INSTR(CAST(content AS BINARY), 0x00)` (el `LOCATE(BINARY UNHEX('00'), …)` daba 928, falso; lo correcto es 1).
- **10-backup:** `BACKUP_ROOT` por defecto `/var/www/newvillacarmen/backups/cutover-pg`, creado 0700 antes del subdirectorio; `mysqldump --no-tablespaces` (sin privilegio PROCESS); la vista rota `recent_conversations` (error 1356) se excluye con `--ignore-table` (lista en `BACKUP_IGNORE_VIEWS`, queda en el log). **Sin `--force`**: cualquier otro error para el backup. `pipefail` en dump y restore. El restore check espera a que exista la BD (`mysql -e 'USE …'`) y restaura sin `--force`.
- **20-freeze:** los timers `villacarmen-ops-check`/`villacarmen-cdn-sweep` solo se paran si existen (`systemctl cat`); en Kraken no existen → INFO.
- **30-import:** si `MYSQL_DSN` no está en el entorno se carga de `$CUTOVER_DIR/mysql-dsn.env` (0600, `MYSQL_DSN=…`, nunca se imprime). Store del bot: se copian juntos `whatsapp-bot-context.sqlite`, `-wal` y `-shm` (con el backend parado) con la imagen `alpine` ya presente, sin red; root en el contenedor hace `chown` al uid del operador y `chmod 600`. Copiar solo el `.sqlite` pierde lo que está en el WAL.
- **40-switch:** healthz debe dar **200** en 90 s; si no, STOP (exit ≠ 0) sin imprimir SWITCH_AT como si fuera bien. `SWITCH_AT` = hora del `docker run` del backend PG.
- **50-smoke:** requiere `SWITCH_AT`. Llamadas admin con `-H @$CUTOVER_DIR/vault-hdr` + cookie QA. La visita POS ya no se crea (abría ticket real; el 400 era por falta de `idempotencyKey`): se lee `GET /api/admin/pos/visits`. Tras el mensaje del bot exige `count(*) FROM bot_inbound_queue WHERE created_at >= SWITCH_AT` > 0 (espera hasta `BOT_WAIT_S`, 600 s) y logs limpios desde SWITCH_AT: backend sin `SQLSTATE|42P10|23505|panic|" 500 "` y PG sin `SQLSTATE|42P10|23505|panic|PANIC|ERROR:|FATAL:`. Cualquier fallo → exit ≠ 0 (NO-GO). Ya **no** descongela.
- **51-unfreeze** (nuevo, `CUTOVER_GO="GO unfreeze"`): arranca bot-pipeline/backoffice/laya-heads, mantenimiento OFF y comprueba las dos webs.
- **90-rollback:** el export de deltas construye la expresión por tabla con las columnas `created_at`/`updated_at` que existen (information_schema, solo tablas base); antes fallaban 105 de 215 tablas y sus CSV quedaban vacíos. Un fallo por tabla se informa y el rollback sigue. `SWITCH_AT=none` sin contenedor `-mysql-precutover` → solo `read_only=OFF` y arranque.
- **Nuevo paso obligatorio previo al freeze:** `scripts/cutover/runtime-smoke-pg.sh` en desktop contra un dump fresco del PG (ver §1.9). Sin `FAIL=0` no hay freeze.

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
8. **Imagen = overlay, no rebuild completo.** El frontend publico de produccion (commit 0c67f79 de 9 nov) es mas nuevo que el de desktop (2 nov): un `docker build -f backend/Dockerfile.prod` desde desktop regresaria el sitio. Por eso la imagen PG se crea encima de la actual (`newvillacarmen-prod-backend:privileged`, que conserva /app/static, OCR y librerias) cambiando solo `/app/backend`:
   - Binario estatico validado en desktop: `vc-server-pgnative-static`, sha256 `370b0f6c8df8dba8219afc8f1127b68ef5541b9172f82054255e37a7eb3032c6` (compilado del commit d6d2649, CGO desactivado).
   - En la ventana (solo tras la orden de Main): copiar el binario a un directorio 0700, `docker build -f deploy/pgnative/Dockerfile.overlay -t newvillacarmen-prod-backend:pgnative-20261113 <dir>` (segundos, sin red, sin carga) y comprobar el sha256 del binario dentro de la imagen.
   - Rollback: el contenedor MySQL queda renombrado con su imagen `privileged` intacta.
   - Los timers `villacarmen-ops-check` y `villacarmen-cdn-sweep` quedan parados durante el cutover (20-freeze); su reactivacion con env PG es un paso posterior documentado, no del cutover.
   El switch PG usa `40-switch.sh`, que replica la especificacion de `docker run` del release con el env PG.
9. **Runtime smoke en desktop (OBLIGATORIO, bloqueante para el freeze):** con el commit/binario aprobado, contra un dump **fresco** del PG (p. ej. el contenedor restaurado del último import, como `vc-pgprod-snap-20261115`, usado solo como origen de lectura):
   `SRC_PGC=<contenedor PG fresco> BIN=<binario aprobado> scripts/cutover/runtime-smoke-pg.sh`
   Debe terminar con exit 0 y `FAIL=0` en `$RUN/summary.txt` (sin `SQLSTATE`/`42P10`/`23505` no esperados, sin `panic`, sin 500). Es el paso que habría pillado el 42P10 del 15-nov.

## 2. Ventana del domingo
| hora | paso | script | duración máx. | tipo |
|---|---|---|---|---|
| 22:30 | **runtime-smoke-pg.sh en desktop** contra dump fresco del PG: `FAIL=0` | runtime-smoke-pg.sh | 20 min | **[STOP]** si FAIL ≠ 0: no hay freeze |
| 22:45 | Asistencia, canal, lectura del runbook | — | 15 min | — |
| 23:00 | **[GO "GO precheck"]** precheck: todo PASS | 00-precheck.sh | 10 min | solo lectura · **[STOP]** si hay FAIL |
| 23:10 | **[GO "GO freeze"]** mantenimiento ON; parar laya-heads → backoffice → bot-pipeline → backend; MySQL `read_only=ON`; recuentos | 20-freeze.sh | 5 min | reversible · web y BO fuera de servicio |
| 23:15 | **[GO "GO backup"]** mysqldump del host (`--no-tablespaces`, sin la vista rota `recent_conversations`, sin `--force`) + sha256, restore check con `mysql:8.0.46` desechable, imagen, inspect, env, volumen del bot | 10-backup.sh | 15 min | **[STOP]** si el dump/restore falla o los recuentos difieren |
| 23:30 | **[GO "GO import"]** schema/export (binario en el host, socket MySQL, `MYSQL_DSN` de `mysql-dsn.env`), referencia independiente, crosscheck, plan, pgcompat, allowlist, PG nuevo, import, verify, ida y vuelta, gate, sqlcheck, recuentos, store del bot (`.sqlite` + `-wal` + `-shm`) | 30-import.sh | 20 min | **[STOP]** duro → 90 con `SWITCH_AT=none` |
| 23:50 | **Decisión 1** (Main/Jaime) | — | 5 min | — |
| 23:55 | **[GO "GO switch"]** diff de nombres del env, tag `mysql-pre-cutover`, backend PG, healthz 200 en 90 s (si no, STOP) | 40-switch.sh | 10 min | **[IRREVERSIBLE para datos]** desde que entra tráfico · requiere DB_PG_PRODUCTION_APPROVED=1 y el match exacto de DB_PG_ACCEPT_IMPORT_STAGING |
| 00:05 | **[GO "GO smoke"]** `SWITCH_AT=… 50-smoke.sh`: healthz, menús, BO y lectura POS (con `vault-hdr`), 1 mensaje desde +34692747052 (solo ese número) que debe aparecer en `bot_inbound_queue`, logs backend/PG limpios | 50-smoke.sh | 20 min | exit ≠ 0 → NO-GO |
| 00:25 | **Decisión 2**: GO → **[GO "GO unfreeze"]** arrancar bot-pipeline/backoffice/laya-heads y mantenimiento OFF; NO-GO → 90 | 51-unfreeze / 90 | — | — |
| 01:30 | **Hora límite**: sin GO final, rollback | 90-rollback.sh | 15 min | — |

## 3. Rollback (`90-rollback.sh`, [GO "GO rollback"])
1. Parar el backend.
2. Si hubo tráfico en PG, exportar a CSV las filas con created_at/updated_at ≥ SWITCH_AT; la expresión se construye por tabla con las columnas que existen de verdad. **No captura borrados ni tablas sin timestamps.**
3. Restaurar el env y el tag `mysql-pre-cutover`. Con `SWITCH_AT=none` y sin contenedor `-mysql-precutover` (rollback antes del switch) solo se hace el paso 4 y se arranca el backend MySQL existente.
4. MySQL `read_only=OFF`.
5. Arrancar, comprobar healthz y descongelar.

PG (contenedor y volumen) se conserva.

## 4. Checklist go/no-go
- [ ] Imagen `pgnative-20261113` construida desde el commit aprobado (id anotado); `pg-vars.env` preparado; temporizadores del host parados
- [ ] Ensayo en desktop: import y verify OK, ida y vuelta OK, gate OK, recuentos idénticos, errores de sqlcheck revisados
- [ ] `runtime-smoke-pg.sh` en desktop contra dump fresco del PG: exit 0, `FAIL=0`
- [ ] Precheck sin FAIL: huérfanos 1/726/3/1 (bo_invoice_comments, invoice_line_items, site_builder_domain_mappings, site_builder_pages); NUL = 1; disco OK
- [ ] Freeze: sin sesiones escribiendo; read_only=ON; recuentos guardados
- [ ] Backup: sha256 y restauración idénticos
- [ ] Import: todos los STOP en verde
- [ ] Switch: diff de nombres revisado; healthz
- [ ] Smoke (exit 0): el mensaje del bot solo a +34692747052 y presente en `bot_inbound_queue` desde SWITCH_AT; 0 `SQLSTATE`/`42P10`/`23505`/`panic`/500 en backend y PG
- [ ] GO final con la hora anotada
