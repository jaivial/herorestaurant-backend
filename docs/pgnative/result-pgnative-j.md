# result-pgnative-j — ensayo del import nativo en desktop (docs y scripts; nada ejecutado)

Autoridad: Main 14:56. No se ha ejecutado nada (ni docker ni BD), no hay commit y no se ha compilado.

## 1. Cómo conecta dbmigrate
- **MySQL:**
  - Solo por `MYSQL_DSN` en el entorno, formato go-sql-driver, y **obligatoriamente socket unix** (`cfg.Net == "unix"`; en otro caso se niega).
  - `-db` debe coincidir con la BD del DSN; lo confirma con `SELECT DATABASE()`.
  - El contenedor fuente no publica puerto, así que el binario estático se copia **dentro** del contenedor MySQL y se ejecuta allí, como en R5: `MYSQL_DSN='root@unix(/var/run/mysqld/mysqld.sock)/villacarmen_source'`.
  - En producción se ejecuta en el host, con el socket `/var/run/mysqld/mysqld.sock` de mysql.service.
- **PG** (import/verify/export-pg): `PG_DSN` en el entorno, cualquier DSN de pgx. Como en R5, dentro del contenedor PG: `host=/var/run/postgresql user=postgres dbname=newvillacarmen_business sslmode=disable` (auth trust en el ensayo).
- **Solo ficheros, sin BD:** crosscheck, plan, pgcompat, compare.

## 2. Secuencia exacta (`rehearsal-desktop.sh`, `30-import.sh`)
1. `-mode schema -db <db> -out schema.json`
2. `-mode export -db <db> -dir export`: escribe manifest.json, *.ndjson y *.rowhash; snapshot consistente en solo lectura, sesión con `time_zone '+00:00'`.
3. **Referencia independiente.** No hay herramienta en el repo: en R5 la hizo el coordinador con consultas MySQL `HEX(CAST(col AS BINARY))` (`independent-mysql-textbytes-hex.ndjson`). Ahora la genera `scripts/cutover/gensql.py <schema.json> independent`:
   - una SELECT por tabla, con `CONCAT('{"table":..,"row":', JSON_ARRAY(HEX(CAST(\`col\` AS BINARY))…), '}')` en el orden de columnas del esquema;
   - `SET SESSION time_zone='+00:00'` para que coincida con el export;
   - se ejecuta con el cliente `mysql -N -B --raw` y la salida va a `independent.ndjson` (0600).
4. `-mode crosscheck -dir export -independent independent.ndjson`: exit 2 si hay discrepancias.
5. `-mode plan -schema schema.json -out plan -view-override docs/dbmigrate/view-overrides.json -storage-override docs/dbmigrate/storage-overrides.json`:
   - el override de vista es para `recent_conversations`; sin él el plan es **unsupported** y sale con exit 2;
   - el de almacenamiento pasa `assistant_messages.content` a bytea (la fila con NUL);
   - comprobación añadida: plan.sql sin ci_ai, `__wk`, `__pad`, ixpad ni ixwk.
6. `-mode pgcompat -dir export -plan plan`
7. **Allowlist de huérfanos.** Se copia `docs/dbmigrate/staging-orphan-allowlist.json` (4 FKs: fk_bo_invoice_comments_invoice 1, fk_invoice_line_items_invoice 726, fk_domain_site 3, fk_page_site 1) y se sustituye `schema_sha256` por el de `export/manifest.json` (one-liner de python en los scripts). El import exige coincidencia exacta de tabla, constraint y recuento.
8. `-mode import -dir export -plan plan -independent independent.ndjson -orphan-allowlist orphan-allowlist.json`: una sola transacción sobre una BD **vacía**.
9. `-mode verify -dir export -plan plan`
10. `-mode export-pg -plan plan -out pg-export` + `-mode compare -a export/manifest.json -b pg-export/manifest.json`: ida y vuelta.
11. Repetir el import: **debe rechazarse** (BD no vacía).
12. `pgnative-gate -schema plan/schema.json` (con `PG_DSN`).
13. `pgnative-sqlcheck -schema plan/schema.json -prepare -out sqlcheck.tsv`, ejecutado **desde un directorio con `internal/`, `cmd/` y `docs/pgnative/`**. Por defecto usa `-root .` y el inventario `docs/pgnative/native-ddl-inventory.tsv`. El script lo resuelve con `tar -C worktree -cf - internal cmd docs/pgnative | docker exec -i <pg> tar -x`.
14. Recuentos exactos de todas las tablas, MySQL frente a PG, con `gensql.py count-mysql` / `count-pg` y `diff`.

## 3. Salidas esperadas y ready_for_cutover
- **Exit codes:**
  - export: `ok:true`, exit 0;
  - crosscheck, pgcompat y compare: exit 2 si algo difiere;
  - plan: exit 2 solo con unsupported;
  - import y verify: exit 2 ante cualquier error.
- **Import:** escribe `dbmigrate.marker('import_verified')` con `fks_not_valid` = 4, `orphan_total` = 731 y **`ready_for_cutover=false`**. El valor es false porque hay FKs NOT VALID y pendientes abiertos (storage override, vista, on_update QA).
- **Arranque del servidor con este marker:**
  - el gate de arranque PG (`openBusiness`) exige `ok`, `ready`, 0 pendientes y 0 NOT VALID; si no, rechaza;
  - solo el modo QA local (`VC_PG_LOCAL_HTTP_QA`) admite la renuncia `import-marker`;
  - `shadow-fk` ya no hace nada en el flujo nativo;
  - **el import no necesita ninguna renuncia**: estas solo aplican al servidor QA.
- **Para producción** hace falta decidir: o un marker "ready" aceptando explícitamente los 4 NOT VALID y los pendientes, o una nueva política de gate.
- **`RequireNativeKeys` y `RequireOnUpdateTriggers`:** deben pasar sin renuncias.

## 4. Scripts nuevos y corregidos
- **Nuevos:**
  - `scripts/cutover/rehearsal-desktop.sh`: DRY-RUN por defecto; `--execute` + `CUTOVER_GO="GO rehearsal"`; rechaza kraken; tiempo por paso; resultados en `$REH_DIR/run-<ts>`.
  - `scripts/cutover/gensql.py`: genera la referencia independiente y los recuentos.
- **`30-import.sh` reescrito** con los flags reales:
  - overrides, generación de la referencia, crosscheck y regeneración de la allowlist;
  - import dentro del contenedor PG;
  - verify, ida y vuelta, gate, sqlcheck contra la lista esperada, recuentos y hook del store del bot.
- **`00-precheck.sh`:**
  - consultas de huérfanos corregidas: tabla padre `invoices` y tabla hija `invoice_line_items` (antes ponía bo_invoices / bo_invoice_line_items, que eran incorrectas);
  - recuento de NUL con `LOCATE(BINARY UNHEX('00'), content)`, porque INSTR con la colación ci ignora el NUL;
  - mysql.service, BD newvillacarmen y discos.
- **`10-backup.sh`:** mysqldump del host, `/var/www/newvillacarmen/backups`, `mysql:8.0.46` para la restauración de prueba y tar del volumen del contexto del bot.
- **`20-freeze.sh`:** nota de compose, cliente admin obligatorio y BD por defecto.
- **`40-switch.sh`:** nombres de variables necesarios para PG y **bloqueante** documentado.
- **`docs/pgnative/cutover-runbook.md`:** reescrito con los hechos de producción, el bloqueante, duraciones en minutos (unos 80 MB) y el ensayo.

## 5. Hallazgos clave
1. **Bloqueante:** el servidor no puede arrancar en PG en producción con el código actual (`main.go` solo lo permite en el modo QA local). Hace falta un cambio de código revisado con GO de Main, más la decisión sobre el marker.
2. **Variables del cambio (solo nombres):**
   - `DB_DRIVER=postgres`, `DATABASE_URL` (TCP 127.0.0.1:55432 sirve, porque el backend va con network host), `DB_PG_TOOLS_QA_APPROVED=1`;
   - `BOT_CONTEXT_DRIVER=postgres`, `BOT_CONTEXT_POSTGRES_DSN`, `BOT_CONTEXT_POSTGRES_SCHEMA=bot_context`.

   Hoy no existe ningún `DATABASE_URL` en producción; es nuevo.
3. **Store del bot:** está en SQLite, en el volumen. Hay que migrarlo a `bot_context` con botstore-migrate dentro de la ventana; falta ensayar ese comando.

## Incógnitas
- Cómo pasa `release-backend-prod.sh` el entorno (env-file, `-e` o compose) y cómo arrancar la imagen PG con ese script.
- Si evo-weai retiene el WhatsApp entrante con 8080 caído.
- Snippet de mantenimiento de nginx.
- Comando exacto de botstore-migrate para el volumen de producción.
- Lista esperada de errores de sqlcheck: saldrá del ensayo.
- Si `postgres:17-alpine` trae ICU y unaccent. Se espera que sí; lo confirma el ensayo, porque el gate comprueba app.norm.
- **Que la referencia generada con gensql coincida con el export en las 253 tablas.** En R5, la referencia del coordinador coincidía en 252 tablas más los floats de `group_menu_wines_v2`, ya resuelto a precisión float32. El ensayo lo demuestra con crosscheck.
