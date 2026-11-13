# result-repair-pgnative-k — arranque de producción en PostgreSQL + aceptación explícita del marker

Rama bot/postgres-native-20261113 (sin push y sin main). Autoridad: relé de Main 2026-11-13 15:24:59. No se ha ejecutado ninguna BD, docker, test ni producción.

## A. Ruta de arranque de producción (`cmd/server/pg_production.go`, `pg_production_start_v1`)
- **`main.go`:**
  - `DB_DRIVER=postgres` + `VC_PG_LOCAL_HTTP_QA=1` → modo QA (sin cambios).
  - `DB_DRIVER=postgres` + `DB_PG_PRODUCTION_APPROVED=1` → producción.
  - En cualquier otro caso, fatal con mensaje claro.
  - MySQL sin cambios.
- **Guardas de producción** (todas fatales):
  - `DB_PG_PRODUCTION_APPROVED=1`;
  - `DATABASE_URL` no vacía (TCP o socket, sslmode tal cual);
  - `BOT_CONTEXT_DRIVER=postgres`;
  - `BOT_CONTEXT_POSTGRES_DSN` y `BOT_CONTEXT_POSTGRES_SCHEMA` no vacías;
  - `VC_PG_LOCAL_HTTP_QA` y `VC_PG_LOCAL_HTTP_QA_SCRATCH_WAIVE` **sin definir**: las renuncias del QA siguen siendo inutilizables fuera del modo QA.
- **Comportamiento:**
  - dirección normal (`cfg.Addr`, :8080), workers y proveedores igual que en MySQL;
  - `appdb.OpenBusinessForProduction` ejecuta el gate completo: marker, `RequireNativeKeys`, `RequireOnUpdateTriggers`;
  - **sin** migraciones MySQL, porque el esquema viene del plan de importación.
- **`internal/db/business.go`:**
  - `openBusiness` pasa a ser un envoltorio de `openBusinessGated(approvalEnv, waive, accept)`;
  - tools y QA siguen igual: `DB_PG_TOOLS_QA_APPROVED` y renuncias QA;
  - producción usa `DB_PG_PRODUCTION_APPROVED` y nunca aplica renuncias.

## B. Aceptación del marker: `DB_PG_ACCEPT_IMPORT_STAGING`
- **Formato:** `orphans=N,pending=M[,fks_not_valid=K]`; orphans y pending son obligatorios.
- **Comprobación:** cada valor debe coincidir **exactamente** con el marker: `allowlisted_orphan_rows`, `pending_blocking_decisions` y `len(fks_not_valid_staging)`. Además el marker debe tener `ok=true`. Si algo no coincide, el arranque es fatal y el mensaje muestra los valores del marker.
- **Por defecto (sin la variable):** el marker tiene que estar `ok` + `ready` + 0 pendientes + 0 NOT VALID, como antes.
- Si se acepta, se registra una línea `[pg_production_start_v1] … accepted via DB_PG_ACCEPT_IMPORT_STAGING`.
- **Valor del domingo:** `orphans=731,pending=329,fks_not_valid=4`.
- **Los 329 pendientes:** 328 del plan, reproducido offline desde el schema.json con los overrides, más 1 por las FKs NOT VALID. Ninguno bloquea:
  - 190 `range_narrowing`: unsigned a rango firmado; los datos actuales caben y los ids se leen como int64;
  - 136 `on_update_explicit_marked_qa`: triggers implementados, QA en scratch;
  - 1 `storage_app_adapter`: assistant_messages.content en bytea; `assistant_ws.go` ya hace bind/scan con []byte;
  - 1 `view_activation_review`: `recent_conversations`, sin lectores en el código.
- Documentado en `docs/pgnative/cutover-runbook.md` (sección nueva y checklist) y en `scripts/cutover/40-switch.sh` (lista de variables y comprobación de nombres).

## C. Workers, bot y caminos que mutan en producción PG
- **Workers en segundo plano** (`api.NewServer`):
  - se arrancan igual que en MySQL; solo los para `BO_DISABLE_BACKGROUND_WORKERS`, y ni el driver ni el modo QA los afectan;
  - son fichaje auto-cut, campaign bounce, pre-shift, stock digest, booking reminder, WhatsApp outbox, watchdog, bot-pipeline demo y inbound recover;
  - en la ruta de producción no se fuerza `BO_DISABLE_BACKGROUND_WORKERS`.
- **Ramas `sqlDialect == Postgres`** (unas 50 en internal/api): todas son variantes SQL, no saltos. No he encontrado ningún "skip on PG". El grep de los patrones skip/disabled/MySQL-only solo devuelve comentarios.
- **Store del bot:** con `DB_DRIVER=postgres`, `newBotConversationStoreFor` abre PG en `bot_context` y rechaza un `BOT_CONTEXT_DRIVER` no-postgres. Sin fallback a SQLite. El bot-pipeline (contenedor aparte) no accede a la BD.
- **Migraciones MySQL:** no se ejecutan en PG. Las tablas creadas en caliente como `modification_history` ya existen desde la importación.
- **Herramientas del host con escritura en BD:** `villacarmen-ops-check.timer` y `villacarmen-cdn-sweep.timer`, que leen `/etc/villacarmen/backend.env`.
  - `20-freeze.sh` los para.
  - Para PG necesitan `DB_DRIVER=postgres`, `DB_PG_TOOLS_QA_APPROVED=1` y `DATABASE_URL`. Hasta que haya decisión se quedan parados (documentado).
- **Release:** `deploy/release-backend-prod.sh` copia **literalmente** el env del contenedor vivo. Por eso `40-switch.sh`:
  1. guarda el env MySQL (`backend.env.mysql`);
  2. genera `backend.env.pg` = env MySQL menos las variables PG/QA más `pg-vars.env`;
  3. renombra el contenedor MySQL a `newvillacarmen-backend-prod-mysql-precutover`;
  4. arranca la imagen PG con la misma especificación que el release (`--network host --restart unless-stopped -v newvillacarmen-prod_whatsapp-bot-context:/var/lib/herorestaurant`).
- **Rollback (`90-rollback.sh`):** renombra el PG a `-pg-failed`, devuelve el contenedor MySQL a su nombre, `read_only=OFF` y start. Ya no restaura env ni tags.
- **Imagen, solo para desktop (no ejecutado):** desde la raíz multi-repo (backend en `backend/` y `preactvillacarmen/frontend/`), `docker build -f backend/Dockerfile.prod -t newvillacarmen-prod-backend:pgnative-20261113 .`
- **botstore-migrate (SQLite → `bot_context`):**
  - Comando exacto: `BOT_CONTEXT_POSTGRES_DSN='<dsn>' BOT_CONTEXT_POSTGRES_SCHEMA=bot_context botstore-migrate -mode copy -sqlite <file>`, después `-mode verify`. Antes se puede ejecutar `-mode inspect`, que no toca PG.
  - **Producción** (en `30-import.sh`): el fichero se copia del volumen con `docker run --rm --network none -v newvillacarmen-prod_whatsapp-bot-context:/src:ro … cp /src/whatsapp-bot-context.sqlite` y el DSN va por `$CUTOVER_DIR/pgsock`.
  - **Ensayo en desktop:** usar la copia R5 `~/orq/villacarmen-bot-20261111/source-context.sqlite` (tiene -wal/-shm al lado; copiarlos juntos). Otra opción es `rollback-context.sqlite` del mismo directorio. DSN: `host=/var/run/postgresql user=postgres dbname=newvillacarmen_business sslmode=disable`, dentro del contenedor del ensayo con `docker cp` del binario.
  - Binario estático: `/tmp/villacarmen-bot-20261111/botstore-migrate-pgnative`.

## Build
- `systemd-run --user` no está permitido en este sandbox ("Failed to connect to user scope bus"). He compilado con el cgroup heredado y `CGO_ENABLED=0`.
- `go build -p 2 ./...` y `go vet ./cmd/server ./internal/db` pasan.
- sha256:
  - vc-server-pgnative-static: 370b0f6c8df8dba8219afc8f1127b68ef5541b9172f82054255e37a7eb3032c6
  - botstore-migrate-pgnative: 3c5987cbdaac4b590e6a2c0b819061e695d3450140457bd0049e8614a1e1a02b

## No verificado
- El arranque real en PG en modo producción (desktop QA pendiente). Hace falta un PG importado con marker y bot_context migrado.
- Que `bot_context` se cree con `botstore-migrate -mode copy` en una BD recién importada.
- El comportamiento de los workers contra PG con datos reales (outbox, reminders) y sus envíos. En el desktop hay que vigilar que los proveedores estén desactivados o apuntando a sandbox: el modo producción **no** bloquea los envíos salientes.
