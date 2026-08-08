# Forky — trabajo pendiente

## Estado actual

> Última auditoría: backend tools, RBAC, confirmaciones, auditoría, límites, CI y renderer accesible están implementados. Los puntos marcados como integración requieren credenciales/servicios del entorno de despliegue.

Forky ya dispone de:

- Contexto de usuario autenticado y restaurante activo.
- Sesiones e historial persistentes.
- Tools tenant-scoped para información del restaurante.
- CRUD confirmado de reservas.
- Tools de catálogo para comida, bebidas, cafés, vinos, menús, POS, stock y miembros.
- Consultas analíticas para reservas, productos, ingresos y stock.
- Protocolo estructurado `tool_use` / `tool_result`.
- Render frontend de bloques `forky-chart`.

> Progreso (2026-08): fase 0 completada — registro único de tools
> (`assistant_registry.go`): defs, RBAC (sección/write) y dispatch derivados del
> registro; confirmación unificada en helpers compartidos (`assistant_confirm.go`)
> con token one-shot vinculado a argumentos canónicos (se corrigió el consume que
> no bindeaba args). Gating de seguridad: `BackofficeOnly` impide que sesiones
> anónimas del asistente público lean datos de backoffice (reservas, clientes,
> stock, POS, horarios, fichaje, miembros, facturas, plataforma). Harness de
> integración gated por `FORKY_TEST_MYSQL_DSN`
> (`assistant_tool_testinfra_test.go`) y script local `scripts/test-forky.sh`
> (unit + `--with-db` con MySQL en Docker que copia el schema dev actual).
>
> Fase 1 (reads tipadas, inicio): tools de fichaje (`fichaje_state_get`,
> `fichaje_entries_list`), horarios (`schedules_by_date`, `schedules_month`) y
> miembros/estado de cuenta (`members_list`, `member_get`,
> `member_balance_get`), reutilizando los handlers de dominio vía
> `assistantCallHandler` (`assistant_handler_reuse.go`). Cada tool con tests de
> integración (aislamiento por restaurante).
>
> Fase 1 completada (reads tipadas) + Fase 2 (writes, inicio): leídas tipadas de
> stock (`stock_warehouses_list`, `stock_categories_list`, `stock_items_list`,
> `stock_item_movements_list`, `stock_summary`), POS (`pos_visits_list`,
> `pos_tickets_list`, `pos_cash_closures_list`, `pos_cash_summary`), facturas
> (`invoices_list`, `invoice_get`), plataforma (`integrations_get`,
> `branding_get`) y menús (`menus_list`, `menu_get`, `menu_sections_get`);
> `analytics_report` extendido (rango en `revenue`, `revenue_by_day`,
> `bookings_by_hour`). Writes de horarios (`schedules_create`,
> `schedules_update`, `schedules_delete`) con flujo completo de confirmación
> (preview token → execute) reutilizando los handlers de dominio via
> `assistantCallHandler` (POST + JSON body). Total tools: 51.
>
> Fase 2 (writes) continúa: stock (`stock_movement_create`,
> `stock_transfer_create`) con confirmación + persistencia real verificada
> (niveles y movimientos). Helper compartido `assistantConfirmedMutation`
> (`assistant_confirm.go`) centraliza el flujo confirmación→ejecución para todos
> los writes que reutilizan handlers HTTP. Total tools: 53.
>
> Fase 2 (writes) + Fase 4 (frontend): fichaje admin (`fichaje_admin_start`,
> `fichaje_admin_stop`) y menú (`menu_toggle_active`), todos con confirmación y
> persistencia verificada. Frontend: `ForkyChart` ampliado a multi-serie,
> stacked, estados loading/error/empty explícitos y export CSV; contrato de
> prompt actualizado para series múltiples/stacked (`assistant_llm.go`). Suite
> `ui/forky`: 54 tests verdes. Total tools: 56.
>
> Fase 2 (writes) + Fase 6 (docs): `pos_cash_closure_create` (cierre de turno de
> caja X/Y/Z con confirmación; verificado: fila de cierre creada y turno →
> CLOSED). Documentación `FORKY_API.md` regenerada desde el registro con
> `cmd/forky-docs` (matriz tool → sección → permiso → confirmación → schema;
> 57 tools). Fase 3 (hardening) cubierta: RBAC derivado del registro,
> confirmación one-shot, auditoría, límites (input 64KiB, timeout 5s, 6
> iteraciones/turno, filas acotadas) y rate-limit por IP.
>
> Fase 2 (writes) + Fase 5 (e2e backend): `member_compensation_create` (periodo
> salarial con confirmación; verificado: fila + auditoría). E2E del bucle de
> tools sobre WebSocket con MiniMax fake (`TestAssistantWS_ToolUseLoop_DB`):
> tool_use → ejecución → tool_result → texto final, contra la BD real.
> `FORKY_API.md` con 58 tools. 50 tests PASS / 0 FAIL en la suite de integración.
>
> Fase 2 (writes) + Fase 6: `booking_limits_update` (límite diario de reservas;
> reutiliza el handler legacy con body form + ctx `restaurantID`, confirmación +
> persistencia en `reservation_manager` verificada) y `pos_ticket_line_add`
> (línea de ticket POS; verificado: fila creada y total recalculado a 2000).
> Corregido gap de registro: `pos_ticket_create` no marcaba `BackofficeOnly`
> (detectado por `TestAssistantAnonymousCannotCallBackofficeTools`).
> `FORKY_API.md` con 60 tools; 51 tests PASS / 0 FAIL.
>
> Cierre (2026-08): añadida `booking_limits_get` (lectura de límite diario,
> ocupación y plazas libres; verificado con read-back tras el update). CI:
> `quality.yml` incluye check `gofmt -l cmd internal` (todo el proyecto quedó
> gofmt-limpio, incl. ficheros legacy no formateados). `FORKY_API.md` con 61
> tools; 51 tests PASS / 0 FAIL en la suite de integración.
>
> Cierre (2026-08, 2): `whatsapp_bot_config_update` (upsert config del bot
> vía `handleBOBotConfigPut`; verificado en `whatsapp_bot_config`) y fichaje
> self-service `fichaje_start`/`fichaje_stop` (reutiliza el punch admin con el
> clock member del usuario autenticado; sin DNI/password; verificado: entrada
> abierta y cerrada). `FORKY_API.md` con 64 tools; 53 tests PASS / 0 FAIL.
> Quedan solo: site-publish (subsistema website-builder instatic / WS-only) y
> e2e de navegador (Playwright, necesita stack completo).
>
> Cierre (2026-08, 3): `site_publish` implementado (reutiliza
> `handlePublishSite`; verificado: versión `published` creada, sitio marcado
> `published` con `published_version_id`). Cobertura completa por dominio.
> `FORKY_API.md` con 65 tools; 54 tests PASS / 0 FAIL en la suite de
> integración. Queda solo el e2e de navegador (Playwright, requiere stack
> completo en ejecución; el flujo de tools ya está cubierto a nivel backend en
> `TestAssistantWS_ToolUseLoop_DB`).
>
> Cierre (2026-08, 4) — E2E de navegador OK: contra el stack real en ejecución,
> `e2e/specs/forky/forky.spec.ts` pasa 4/5 (botón, modal, Esc) y, con
> `FORKY_REAL_TOOLS_E2E=1` en navegador headed (xvfb-run), el test
> «executes read-only custom tools through the real Go WebSocket» pasa (32s):
> 5 prompts reales → MiniMax real → las 65 tools Go → BD real, transcript con
> `restaurant_id|total|people|series` y sin «herramienta desconocida» ni
> «permiso insuficiente». El único test restante requiere el stub MiniMax
> (`forky-minimax-stub.ts`) + backend apuntando al stub (fixme, limitación
> headless documentada del composer de assistant-ui).
>
> Cierre (2026-08, 5) — fix de bug real descubierto por el E2E: el esquema real
> de `restaurants` no tiene columnas `phone/email/address` sino
> `contact_phone/contact_email/location/website_url`; `restaurant_info` y
> `restaurant_settings_get` fallaban en producción («la columna no existe»).
> Ahora leen esas columnas defensivamente (`assistantColumnExists` +
> `assistantRestaurantInfo` tolerante a esquema). Test de integración actualizado
> a `contact_phone` (sin ALTER) y verificado contra el esquema real: 54 PASS /
> 0 FAIL, 10 paquetes ok, gofmt limpio.
>
> Cierre (2026-08, 6) — intento del último fixme (chat con stub MiniMax):
> levantado stack dedicado (stub `forky-minimax-stub` en 8399, backend local en
> 8910 contra la BD test con `SKIP_MIGRATIONS=1` — nuevo env añadido a
> `cmd/server` como conveniencia dev/test —, y segundo backoffice en 8912 con
> `BACKEND_ORIGIN` apuntando al stub). Login HTTP y la réplica standalone del
> proxy WS funcionan, pero el proxy WS del backoffice dev (8912) → stub backend
> falla de forma consistente con «context canceled» en la query de sesión
> (quirks del entorno de dev; el proxy prod 3001→8080 funciona, verificado por
> el E2E de tools reales). Se revirtió el cambio de `test.fixme` y el log de
> debug temporal; `SKIP_MIGRATIONS` se mantiene. Estado: 3 tests de navegador
> PASS, 2 skip (fixme stub + gated `FORKY_REAL_TOOLS_E2E`), 54 tests de
> integración PASS / 0 FAIL.
>
> E2E real de tools (2026-08): suite Playwright completa en
> `backoffice/e2e/specs/forky/forky-tools-*.spec.ts` contra
> `backoffice-dev.menustudioai.com` (admin@villacarmen.com), con espera de 4s
> para el orb y `--headed --workers=1` (el composer de assistant-ui no reacciona
> en headless). 65 tools × ≥3 edge cases (analytics_report con 6) = 66 tests
> (65 tools + 1 probe), TODOS PASS en una pasada (39 min). Helper
> `e2e/helpers/forkyTools.ts` (login, orb, send, assertions read/write,
> reintentos del composer). El E2E descubrió y se corrigieron bugs reales:
> `customers_list` usaba `analytics_customers.name` (real: `display_name`) y
> fallaba la columna; `stock_items`/`recurring_invoices` usaban columnas
> inexistentes; `production_list`/`waste_costs_list` apuntaban a tablas
> inexistentes (reales: `stock_production_orders` / `stock_movements` WASTE);
> `assistantTypedDomainList` serializaba `[]byte` a base64. Fixes desplegados al
> backend dev (rebuild docker) y cubiertos con tests de integración (56 PASS /
> 0 FAIL). Script: `pnpm test:e2e:forky-tools`.

Ramas actuales:

- `backend/feat/forky-complete-backend-tools`
- `backoffice/feat/forky-restaurant-tools`

## Pendientes críticos

### 1. Ejecutar tests con toolchain compatible

- Actualizar entorno CI/dev al toolchain declarado en `backend/go.mod` (actualmente Go 1.26; las dependencias usan APIs de Go moderno). El workflow `backend/.github/workflows/quality.yml` lo configura automáticamente.
- Ejecutar `go test ./...` (CI).
- Ejecutar `go vet ./...` (CI).
- Ejecutar tests de integración con MySQL y migraciones reales.
- Resolver cualquier discrepancia entre columnas asumidas por tools genéricas y esquema real.

### 2. Sustituir SQL genérico por servicios de dominio

Las tools de catálogo actuales usan una allowlist, pero deben reutilizar servicios existentes cuando haya lógica de negocio:

- Reservas: disponibilidad, límites diarios, horarios, arroz, menús, notificaciones.
- POS: stock automático, pagos, caja, cierres y reembolsos.
- Stock: movimientos, transferencias, recetas, producción, costes y auditoría.
- Menús: secciones, platos, suplementos, visibilidad y reglas de grupo.
- Fichaje: invariantes de turnos y entradas activas.

No ejecutar mutaciones directas si el handler/servicio existente contiene reglas adicionales.

### 3. Completar cobertura de tools por dominio

Añadir tools tipadas y específicas para:

- Horarios, días especiales, límites y disponibilidad.
- Clientes y fuentes de clientes.
- Menús, secciones, platos y categorías.
- Miembros, roles, permisos y compensaciones.
- Fichajes, turnos y horarios laborales.
- Stock: almacenes, categorías, unidades, niveles, movimientos, transferencias.
- Recetas, producción, mermas y costes.
- POS: visitas, tickets, líneas, pagos, caja, etiquetas, devoluciones.
- Facturas y exportaciones.
- Configuración del restaurante.
- WhatsApp y configuración del bot.
- Site builder y contenido publicado.
- Analítica completa con comparativas y granularidad.

Cada tool necesita esquema JSON, handler, validación, permisos, auditoría y tests.

### 4. RBAC y autorización por operación

- Resolver permisos del usuario desde la sesión activa.
- Asociar cada tool a un permiso/sección.
- Denegar tools no autorizadas antes de tocar DB.
- No confiar en instrucciones del modelo para autorizar operaciones.
- Aplicar permisos de lectura y escritura por separado.
- Restringir operaciones sensibles a roles adecuados.

### 5. Confirmación server-side

`confirmed=true` no basta como control de seguridad. Implementar:

- Preview de operación antes de mutar.
- Token de confirmación generado por backend.
- Token ligado a usuario, restaurante, tool, argumentos y sesión.
- Expiración corta.
- Un solo uso y protección contra replay.
- Confirmación adicional para borrar, reembolsar, cerrar caja o cambios masivos.
- Registro del motivo/confirmación en auditoría.

### 6. Auditoría

Crear registro estructurado para cada tool call:

- usuario;
- restaurante;
- sesión Forky;
- nombre de tool;
- versión de tool;
- argumentos saneados;
- entidad afectada;
- resultado resumido;
- error;
- timestamp;
- duración;
- correlation/request ID.

No guardar secretos, contraseñas, tokens ni datos innecesarios.

### 7. Protocolo MiniMax/Anthropic

Verificar con respuestas reales:

- SSE `content_block_start` y `content_block_delta`.
- Inputs JSON fragmentados en varios eventos.
- Múltiples `tool_use` en un turno.
- Mensajes assistant con bloques completos.
- Mensajes user con `tool_result` exactos.
- `stop_reason=tool_use` y `stop_reason=end_turn`.
- Errores parciales y reconexión WebSocket.
- No duplicar texto al reintentar.
- Persistir tool turns de forma reproducible.

### 8. Gráficos frontend

> PARCIALMENTE IMPLEMENTADO (2026-08): `backoffice/ui/forky/ForkyChart.tsx` sustituye el renderer CSS mínimo por Recharts. Hecho: bar, line, area y pie/donut; schema validation; tooltips; leyenda; colores por tema; tabla accesible equivalente (`<details>` con `<table>`); responsive container; `prefers-reduced-motion` (desactiva animación). El backend (`assistant_llm.go.buildAssistantSystemPrompt`) ahora instruye al modelo a emitir tablas Markdown GFM para registros multifila y bloques ```forky-chart \{title,type,data[label,value]\}``` para analítica; `MarkdownTextPrimitive` usa `remark-gfm` para renderizar tablas de reservas con estilos.

Pendiente:

- stacked bar;
- comparativas de series;
- estados loading/error/empty explícitos;
- exportación de datos (CSV).

### 9. Tests necesarios

#### Backend

- Catálogo completo de definitions.
- Validación de todos los JSON schemas.
- Routing de cada tool.
- CRUD happy path.
- Validaciones de entrada.
- Aislamiento entre restaurantes.
- RBAC por operación.
- Confirmación, expiración y replay.
- Auditoría.
- Transacciones y rollback.
- Errores DB.
- Tool-use MiniMax realista.
- Múltiples tools por turno.
- WebSocket reconnect y deduplicación.

#### Frontend

- Render de cada tipo de gráfico.
- JSON inválido.
- Datos vacíos.
- Accesibilidad.
- Responsive.
- Dark/light theme.
- Reduced motion.
- Tool status y errores.

#### E2E

- Login de usuario.
- Selección de restaurante A.
- Tool read/write en A.
- Verificación de que B no es accesible.
- Cambio a restaurante B.
- Confirmación de mutación.
- Consulta analítica con gráfico.
- Reconexión WebSocket.

### 10. Documentación API

Documentar por tool:

- nombre;
- propósito;
- JSON schema;
- permisos requeridos;
- restaurant scope;
- side effects;
- confirmación;
- errores;
- respuesta;
- ejemplos;
- versión.

Añadir matriz `tool → endpoint/servicio → permiso → tabla/eventos`.

### 11. Rendimiento y límites

- Timeout por tool.
- Límite de filas y tamaño de respuesta.
- Paginación.
- Rate limit para usuarios autenticados.
- Máximo de tools por turno.
- Máximo de iteraciones por conversación.
- Cancelación por disconnect.
- Evitar consultas N+1.
- Métricas de latencia y errores.

### 12. Seguridad y privacidad

- Validar IDs y ownership siempre en backend.
- No permitir nombres de tabla/columna enviados por el modelo.
- Sanitizar argumentos y markdown.
- No exponer información de otros restaurantes.
- No devolver credenciales ni tokens.
- No permitir mutaciones arbitrarias mediante una tool SQL.
- Revisar prompt injection en datos almacenados.
- Separar tools públicas de tools backoffice.

## Criterio de cierre

La tarea queda completa cuando:

1. Todas las tools están registradas y ejecutables.
2. Cada tool reutiliza lógica de dominio o documenta por qué no.
3. RBAC, ownership y auditoría están activos.
4. Confirmaciones son server-side, expirables y one-shot.
5. CRUD relevante del backend tiene cobertura tipada.
6. Gráficos usan componentes React/Recharts accesibles.
7. Tests unitarios, integración y E2E pasan con Go compatible.
8. No existe fuga cross-tenant.
9. Documentación y matriz de tools están actualizadas.
10. CI ejecuta typecheck, tests, vet, lint y E2E.
