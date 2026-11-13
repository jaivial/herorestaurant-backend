# result-pgnative-h — kit de cutover (docs y scripts; nada ejecutado)

Autoridad: Main 14:40 (cutover el domingo 15 nov 2026 23:00 Europe/Madrid; nada en kraken hasta entonces). No se ha ejecutado nada, no se ha compilado nada y no hay commit.

## Preparado
- `docs/pgnative/cutover-runbook.md` (en español): variables, comprobaciones previas, horario con duraciones, puntos [GO], [IRREVERSIBLE] y [STOP], rollback, checklist go/no-go y fuera de alcance.
- `scripts/cutover/`:
  - **lib.sh** (guardas comunes): DRY-RUN por defecto; `--execute` + `CUTOVER_GO="GO <paso>"`; comprobación del host `kraken`; log 0600 con filtro de DSN y secretos; utilidades que listan solo **nombres** de variables.
  - **00-precheck.sh** (siempre solo lectura), con PASS/FAIL por comprobación:
    - disco;
    - contenedores y servicios;
    - descubrimiento de MySQL y del entorno del backend (solo nombres);
    - versión de MySQL;
    - recuentos de todas las tablas a fichero;
    - huérfanos 1/726/3/1;
    - NUL = 1;
    - sha256 de los binarios;
    - PG nuevo inexistente (contenedor, volumen, puerto);
    - sesiones MySQL por usuario y host.
  - **10-backup.sh**:
    - mysqldump `--single-transaction --hex-blob` + sha256;
    - restauración en un contenedor desechable sin red, con comparación de recuentos;
    - inspect e imagen del backend, ficheros env (0600, solo nombres en el log) y bot store.
  - **20-freeze.sh**: mantenimiento; parada en orden laya-heads → backoffice → bot-pipeline → backend; MySQL read_only/super_read_only ON; recuentos del freeze.
  - **30-import.sh**:
    - schema, export, crosscheck, plan, pgcompat;
    - PG nuevo (contenedor y volumen nuevos, loopback y socket);
    - import con allowlist, verify, pgnative-gate, sqlcheck contra los errores esperados y recuentos del export frente al freeze;
    - STOP duro ante cualquier fallo.
  - **40-switch.sh**: diff de nombres del entorno para revisión, tag de la imagen previa, instalación del env PG, arranque y healthz, SWITCH_AT.
  - **50-smoke.sh**: healthz, menús públicos, BO, visita POS, un mensaje del bot solo a +34692747052, logs; punto de decisión y descongelación.
  - **90-rollback.sh**: export CSV de lo escrito en PG desde SWITCH_AT, restauración del env y la imagen, read_only OFF, arranque y healthz, descongelación. PG se conserva.
- Sintaxis comprobada con `bash -n` en los 8 ficheros. Sin ejecución.

## Incógnitas (bloquean el domingo hasta resolverlas)
1. **Ubicación del MySQL de producción**: contenedor, mysqld del host o remoto. El precheck lo descubre en solo lectura.
2. **Cómo recibe el backend su entorno**: env_file de compose, `docker run -e` o fichero montado. Sin esto no hay `BACKEND_ENV` ni `BACKEND_UP_CMD` y 40/90 no pueden ejecutarse.
3. **Página de mantenimiento**: proxy, Caddy o nginx (`MAINT_ON_CMD`/`MAINT_OFF_CMD`).
4. **Retención de WhatsApp entrante**: si evo-weai-villacarmen reintenta o encola mientras el backend está parado.
5. **Credenciales**: usuario de solo lectura para precheck y dump, y usuario admin solo para read_only, ambos en defaults-file. Además DSN de dbmigrate (`MYSQL_DSN` por socket).
6. **`INDEPENDENT_REF`**: la referencia independiente que exige `-mode import`. Hay que confirmar cómo se generó en R5 y regenerarla sobre el export del domingo.
7. **orphan-allowlist.json**: está ligado al `schema_sha256`; hay que regenerarlo con el esquema de ese momento.
8. **Tamaño real de la BD y duraciones**: el import está estimado en 90 min sin medir. Se recomienda un ensayo con un dump real en scratch antes del domingo.
9. **Imagen del backend nuevo**: el binario vc-server-pgnative-static debe empaquetarse en una imagen (o montarse) con el mismo entrypoint. Todavía no está definido.
10. **Puertos y nombres**: `PG_PORT` 55432, contenedor `newvillacarmen-pg-prod`, volumen `newvillacarmen-pg-prod-data` e imagen `postgres:17-alpine` (con ICU y unaccent) son propuestas por confirmar.

## Lo que el coordinador debe verificar en kraken (solo lectura) antes del domingo
- `00-precheck.sh` completo con la salida archivada, en particular:
  - el bloque de descubrimiento de MySQL (puntos 1–2);
  - los recuentos de filas;
  - huérfanos = 1/726/3/1;
  - NUL = 1.
- `docker inspect newvillacarmen-backend-prod`: labels de compose (working_dir, config_files), mounts y **nombres** de las variables. Nunca los valores.
- Configuración de proxy y mantenimiento, y comportamiento de evo-weai ante el webhook caído (documentación o config, sin enviar mensajes).
- Espacio libre en /, /var/lib/docker y el futuro `CUTOVER_DIR`.
- Disponibilidad de la imagen postgres:17-alpine (pull previo) y de los binarios con sha256 en `expected-sha256.txt`.
- Que los scripts en DRY-RUN impriman comandos coherentes con lo descubierto (rellenar las variables del apartado 0 del runbook).
