# Resumen de cutover a PostgreSQL nativo (pgnative)

Estado al 13 nov 2026, 13:55. Sustituye a `docs/cutover-summary-20261113.md` del worktree antiguo (villacarmen-bot-postgres-20261111), que queda solo como referencia.
Autoridad: mensajes de Main 11:56/11:57 (pivote a "Postgres puro", sin emulación de MySQL).
**Nada de este documento se ha ejecutado en producción. El cutover requiere GO explícito.**

## 1. Qué cambia respecto a MySQL (esquema)

- **Texto nativo.**
  - Todas las columnas de texto son `text`/`varchar` con la colación por defecto de la base. No queda `dbmigrate.ci_ai` ni ninguna colación no determinista.
  - Las columnas `_bin` (14) quedan con `COLLATE "C"`.
  - En total se convierten 888 columnas `_ci`.
- **Funciones de clave** (`docs/pgnative/app-norm.sql`, esquema `app`, IMMUTABLE):
  - `app.norm(x)`: rtrim + NFKC + quitar caracteres de ancho cero + unaccent + lower. Se usa para nombres y texto libre.
  - `app.norm_ascii(x)`: lower(rtrim(x)). Se usa para identificadores ASCII (emails, slugs, tokens, hashes, claves de idempotencia, `*_id`, `*_key`, dominios, URLs) y para las columnas con colación `general_ci` en MySQL.
- **Unicidad.**
  - 117 índices UNIQUE de expresión `ux__<tabla>__<índice MySQL>`, uno por cada UNIQUE original con columnas de texto. Las partes no textuales van en crudo y la semántica de NULL es la de siempre (NULL distintos).
  - Inventario con el destino exacto de cada `ON CONFLICT`: `docs/pgnative/native-ddl-inventory.tsv`.
- **FKs.**
  - 293 FKs, creadas `NOT VALID` y validadas en la misma importación cuando no tienen huérfanos.
  - Quedan **4 NOT VALID** por los **731 huérfanos** históricos: 1 en bo_invoice_comments, 726 en invoice_line_items (padre invoices) (de 245 facturas ausentes), 3 en site_builder_domain_mappings y 1 en site_builder_pages (los dos últimos casos, del mismo site ausente).
  - Los huérfanos no se borran ni se inventan padres; las escrituras nuevas sí se validan.
- **ON UPDATE.** 137 columnas `ON UPDATE CURRENT_TIMESTAMP` se reproducen con triggers (`dbmigrate.on_update_ts` + `on_update_mark`). Ya no hay lógica de `__wk` en esas funciones.
- **Eliminado del modelo:** columnas `__wk`/`__pad`, índices `ixwk__`/`ixpad__`, shadow FKs y la emulación PAD SPACE.
- **Conversión de un scratch antiguo:** `docs/pgnative/convert-existing-scratch.sql`. Es idempotente y deja y recrea las columnas generadas en torno al ALTER de colación. Una importación limpia no la necesita.

## 2. Cambios de código

- **B** (`result-pgnative-b.md`):
  - 648 literales SQL cambiados en 120 ficheros.
  - Eliminados `internal/mysqlcoll`, `internal/third_party/vitess`, `cmd/wk-backfill`, `cmd/collkey-compare`, los helpers `wk*` y la flag `VC_PG_WK_KEYS`.
  - 467 expresiones `rtrim(..) COLLATE ci_ai` pasan a `app.norm*`.
  - Los `ON CONFLICT` usan la expresión del ux__ correspondiente; 8 que apuntaban a PKs de texto usan las columnas crudas.
  - 35 ORDER BY de listas visibles usan `COLLATE "es-x-icu"`.
  - 23505 → 409: `IsUniqueViolationOn` reconoce los ux__, y áreas/mesas devuelven 409.
  - Gate de arranque `RequireNativeKeys`.
  - El camino MySQL queda intacto.
- **C** (`result-pgnative-c.md`): herramienta `cmd/pgnative-sqlcheck`.
  - Hace PREPARE en una transacción READ ONLY siempre revertida, más un lint estático de uso de norm, destinos ON CONFLICT y coherencia norm vs norm_ascii.
  - 6 correcciones: joins de roles, listas IN, paréntesis en fichaje, y una SQL MySQL truncada en comidaCategorySlugTaken.
- **D** (`result-pgnative-d.md`): de 72 errores de PREPARE, 7 sitios reales corregidos:
  - boolean→smallint en facturas;
  - `NULLIF(?,\'\')` en pos_operations;
  - TIMESTAMP() de MySQL en booking_reminders;
  - enum '' en analytics.

  El resto no son bugs del port.
- **E** (`qa-kit-u4.md`):
  - Kit de QA HTTP manual con 32 escritores.
  - Una corrección: en la importación de stock sin SKU, la comprobación de existencia por nombre usa `app.norm`, así que los acentos ya no duplican. **Pendiente de recompilar el servidor** si no se hizo después de E.
- Los binarios estáticos (CGO_ENABLED=0) están en `/tmp/villacarmen-bot-20261111/`: vc-server-pgnative-static, pgnative-gate, pgnative-sqlcheck, dbmigrate-pgnative, qa-http y qa-seed-scratch. Los sha256 están en cada result-*.md.

## 3. Divergencias de app.norm frente a MySQL (documentadas, aceptadas)

Fuente: `docs/pgnative/u1-app-norm-audit.md`, con 38 pares contra MySQL 8.0.45 y los datos de R5: 117 índices y 0 grupos distintos en los datos actuales.

| Caso | MySQL unicode_ci | app.norm | Efecto |
|---|---|---|---|
| ß/ss, ligaduras (ﬁ, Œ), ancho completo, ℌ, ², ½ | iguales | iguales | igual |
| tildes, ñ/n, ü/u, ç/c, mayúsculas, combinantes | iguales | iguales | igual |
| espacios finales | iguales (PAD SPACE) | iguales (rtrim) | igual; el valor almacenado conserva sus espacios |
| Æ/æ ↔ AE/ae, Ø ↔ O, Đ ↔ D, Ł ↔ L, ı ↔ i, ʼ ↔ ' | distintos | **iguales (sobre-pliega)** | algún "duplicado" de más; dirección segura; 0 casos en los datos |
| emoji distintos (😀/😁) | iguales (rareza MySQL) | **distintos** | mejor que MySQL |
| espacio inicial, tabulador, й/и, guion blando | distintos | distintos | igual |

Divergencias adicionales, por diseño:
- **Columnas general_ci con app.norm_ascii:** solo pliegan mayúsculas y espacios finales, no acentos. Para identificadores ASCII el resultado es el mismo.
- **PKs de texto en crudo:** comparación byte a byte, sensible a mayúsculas, acentos y espacios. Afecta a bo_roles.slug, platform_settings.setting_key, whatsapp_bot_sessions.user_phone, `*_role_permissions`, pos_daily_sequences y restaurant_ai_provider_keys. Las búsquedas sobre ellas usan app.norm, pero la unicidad no pliega. Ejemplo: `QA-ROL` y `qa-rol` pueden coexistir en bo_roles.
- **Búsquedas LIKE:** `LOWER(col) COLLATE "C" LIKE lower(?)`, insensibles a mayúsculas pero **no a acentos** ("cafe" no encuentra "café"). Siguiente paso posible: `app.norm(col) LIKE app.norm(?)`.
- **Orden:** "es-x-icu" en las 35 listas reescritas; el resto usa la colación por defecto de la BD.
- **Join comida_items en menús de grupo:** se hace en SQL con la colación por defecto, no con la de MySQL unicode_ci.

## 4. Verificación

**Hecho** (scratch padnat1, network none):
- `pgnative-gate`: sin columnas `__wk`/`__pad`, sin colación dbmigrate, 117 ux__ válidos, informe de FKs y huérfanos (731), triggers y app.norm IMMUTABLE.
- `pgnative-sqlcheck -prepare`: **2086/2152 OK**. Los 66 restantes están clasificados como no-bugs del port: objetos que tampoco existen en el origen MySQL, el almacén del bot (bot_context/SQLite) y el bug conocido :680.
- QA HTTP manual del coordinador:
  - numeración de tickets;
  - áreas y mesas (duplicado → 409);
  - ß/ss;
  - café/cafe;
  - cadena fiscal (full_number);
  - importación de stock con duplicado por acento;
  - importación de productos POS idempotente;
  - facturas con boolean;
  - unos 28 escritores con duplicado → 409/400 sin segunda fila;
  - platform settings.

**NO verificado:**
- tablas del almacén del bot (SQLite / bot_context) con el PG nativo;
- website builder (tablas inexistentes también en MySQL);
- workers en segundo plano y rutas con red saliente (Stripe, WhatsApp, email, IA);
- rendimiento y planes de consulta con volumen real;
- importación completa en una BD vacía con el generador nuevo (dbmigrate-pgnative);
- concurrencia por encima de 8 peticiones paralelas;
- los 391 sitios SQL dinámicos que el sqlcheck no puede preparar.

## 5. Decisiones pendientes (Jaime / Main)

1. **GO al cutover**, con ventana y responsable.
2. **Plan de huérfanos**: los 731 se mantienen con FKs NOT VALID. Opciones: archivado con tombstones / reserva de dominio, recuperar padres de copias antiguas, o seguir así. No se borra ni se inventa nada sin decisión.
3. **Replay de pagos en checkout**: comparar el payload contra los importes, método y client id originales (issue de dinero previo al port; solo investigado).
4. **Revisión de la heurística norm vs norm_ascii**: casos dudosos username, phone, code (stock_item_units, stock_warehouses), sku, contact_phone, numero_mesa, ticket_number. Lista completa en native-ddl-inventory.tsv.
5. **Bug de inserción de movimiento de stock** `backoffice_stock_documents.go:680` (INSERT con más expresiones que columnas): documentado, no corregido por decisión.
6. **Objetos ausentes también en el origen MySQL** (código muerto o creado en caliente): restaurant_members.pos_pin_hash, website_assets, website_publish_history, website_pages.is_homepage/meta_description, closed_days, modification_history.restaurant_id, bookings.created_at, restaurants.phone, menu_dishes_catalog.name, site_builder_domain_mappings.restaurant_id, VINOS. Decidir entre crear (migración nueva) o retirar el código.
7. **Checkout de dominios del site-builder**: sigue OFF.
8. **PKs de texto sensibles a mayúsculas** (bo_roles.slug, etc.): aceptar o añadir un ux__ app.norm adicional.

## 6. GraphRAG y licencias

- **GraphRAG:** diseño en `docs/graphrag/design.md`.
  - Una instancia neo4j-rust por restaurante, solo en loopback, con su propio directorio de datos y su token 0600.
  - El registro restaurant_id → (puerto, token) está solo en el servidor; el cliente y el modelo nunca eligen grafo, puerto ni token.
  - Hay scripts `scripts/graphrag/tenant-instance.sh` y `cmd/graph-ingest`.
  - Las instancias se validaron con restaurantes de prueba.
  - **No implementado:** ingesta de datos de cada tenant desde PG (whatsapp_bot_config, restaurant_bot_ai_config) y retrieval en el bot.
- **Licencias:** se ha retirado el vendoring de Vitess (Apache-2.0) y sus tablas de pesos. La cuestión GPL de las tablas UCA desaparece con app.norm. No se añaden dependencias nuevas: go.mod sin cambios por este pivote, y `go mod tidy` offline sin diferencias.

## 7. Runbook de cutover (PROPUESTA — requiere GO explícito; nada ejecutado)

1. **Preparación:** recompilar los binarios desde el commit aprobado y anotar sus sha256. Comprobar en la BD destino que existen ICU ("es-x-icu") y la extensión unaccent.
2. **Backup:**
   - dump completo de MySQL producción (consistente, con checksum);
   - snapshot de la configuración y `.env`, guardados fuera del repo y sin imprimir secretos;
   - copia de los ficheros SQLite del bot store.
3. **Parar escrituras:** mantenimiento en el frontend/backoffice; workers parados (`BO_DISABLE_BACKGROUND_WORKERS=1`); webhooks de WhatsApp/Stripe en cola o en pausa.
4. **Export:** `dbmigrate -mode schema` y `-mode export` desde MySQL (socket unix, solo lectura).
5. **Plan e import:**
   - `dbmigrate-pgnative -mode plan`, revisando `ready_for_cutover` y los pendientes;
   - `-mode import` sobre una BD PG **vacía**, con la allowlist exacta de los 731 huérfanos;
   - `-mode verify` para comparar conteos y digests por tabla.
6. **Bot store:** `botstore-migrate` de SQLite al esquema bot_context.
7. **Gates:** `pgnative-gate` debe pasar (huérfanos = 731, informe). Después `pgnative-sqlcheck -prepare`, sin errores nuevos respecto a los 66 clasificados.
8. **Smoke:** arranque con `DB_DRIVER=postgres` y el guard de arranque (marker, native keys, triggers). Después:
   - healthz;
   - login BO;
   - lectura de reservas, carta y stock;
   - una reserva, una factura y un ticket POS de prueba, borrados lógicamente;
   - búsqueda con acentos;
   - una de cada ruta de duplicado del kit U4.
9. **Apertura:** reactivar workers y webhooks. Vigilar los logs de 23505/500 durante 24 h.
10. **Rollback:**
    - Mientras no haya escrituras nuevas en PG: volver a `DB_DRIVER=mysql` con el dump intacto.
    - Si ya hubo escrituras en PG, hace falta un export PG → MySQL (`export-pg` + comparación), **todavía no ensayado**. Por eso el punto de no retorno debe fijarse explícitamente antes del GO.

Sin secretos en este documento. Ninguna acción de este runbook se ha ejecutado.
