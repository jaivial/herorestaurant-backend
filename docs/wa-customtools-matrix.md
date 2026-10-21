# Matriz global de custom tools — WhatsApp (bot) y Forky (panel / MCP / plugin)

**Rama:** `feat/wa-customtools-matrix` · **Base:** `main` @ `03d565b` · **Coordinación:** `wa_customtools_matrix_v1`

Mapeo completo de **todos los endpoints API y todos los métodos socket** del backend, con la
herramienta que los cubre en cada superficie, la exclusión documentada cuando no la hay, y el
hueco (`FALTA`) cuando falta.

- **Superficie Forky** = panel (WebSocket `/admin/assistant/ws`), plugin ChatGPT (`/api/plugin/*`) y
  servidor MCP (`/mcp`). Las tres llaman a `assistantExecuteTool` (`internal/api/assistant_tools.go:16`).
- **Superficie WhatsApp** = bot de WhatsApp (`/bot/webhook`, `/bot/webhook/evolution/{secret}`).

---

## 0. Fuentes de verdad y método

| Fuente | Qué aporta | Cómo se refresca |
|---|---|---|
| `internal/api/server.go` | Tabla de rutas real (chi). Gates exactos por ruta. | fuente primaria |
| `internal/api/forky_admin_ops.json` | Mapa generado de las **558** operaciones `/admin` con sus gates, sección y riesgo. | `go run ./cmd/forky-admin-map -server internal/api/server.go -pages ../backoffice -o internal/api/forky_admin_ops.json` |
| `internal/api/assistant_registry.go` | Registro de tools de Forky (nombre, sección, `Write`, `Confirm`, `BackofficeOnly`, schema). | fuente primaria |
| `internal/api/assistant_domain_read.go` | Núcleo de lectura reutilizable `resource×action` (`*_read`). | fuente primaria |
| `internal/api/whatsapp_bot_*.go` | Tools propias del bot (staff reads + `*_propose_*`). | fuente primaria |
| `internal/api/whatsapp_bot_privileged.go` | Lista `botForkyExcludedFromWA` (tools de Forky que NO llegan a WhatsApp). | fuente primaria |

**El registro de rutas no usa `mux.Handle`/`HandleFunc` estándar:** el router es **chi**
(`internal/api/server.go:134` `r := chi.NewRouter()`), con sub-registradores
(`RegisterWidgetRoutes`, `RegisterSiteBuilderRoutes`, `websiteBuilder.RegisterRoutes`,
`instatic.RegisterInstaticRoutes`, `MountChatGPTPlugin`, `MountMCP`). La sección 5 usa el mapa
generado; las secciones 6–8 cubren lo que el mapa no cubre.

---

## 1. Las cuatro superficies de exposición y sus gates

El registro de Forky se expone por **cuatro** caminos, todos vía `assistantExecuteTool`:

| Superficie | Entrada | Autenticación | Gate de sección/RBAC |
|---|---|---|---|
| Panel (WS) | `GET /admin/assistant/ws` | cookie `bo_session` + `allowBOWebSocketOrigin` | `assistantToolAllowed` por sección |
| Plugin ChatGPT | `POST /api/plugin/tools*` | token del plugin | idem + token por restaurante |
| MCP | `POST /mcp` | OAuth (`/mcp/oauth/*`) | idem |
| Bot WhatsApp | `POST /bot/webhook*` | **ninguna** (firma del proveedor) | **frontera de privilegio** (§2) + tool grants |

> **Regla dura (`wa_bot_forky_excluded_v1`):** toda tool nueva del registro de Forky necesita
> `BackofficeOnly: true` **o** una entrada explícita en `botForkyExcludedFromWA`
> (`internal/api/whatsapp_bot_privileged.go:100`). Sin una de las dos, la tool llega al catálogo del
> bot de WhatsApp y el ejecutor la despacha con la sesión sintética root: **bypassa los gates de
> WA en silencio**. Además, en un turno de WA toda tool de Forky con `Write: true` se **rechaza en el
> servidor** (`wa_bot_admin_write_denied_v1`, `internal/api/whatsapp_bot_webhook.go:701`): una
> escritura genérica del panel no se ejecuta porque el `confirmed=true` lo escribe el propio modelo
> y no es una decisión humana. El sustituto de esa contención es la cadena **propose→OK**.

---

## 2. Frontera de privilegio (`wa_bot_privileged_boundary_v1`)

Corrección conservadora del encargo original. El privilegio **solo** existe por dos caminos, y
ambos son 1:1 con el actor:

| Camino | Condición | Prueba |
|---|---|---|
| **(a) grupo autorizado** | el chat es exactamente el grupo de *env* (`BOT_SPECIAL_ADMIN_SCOPES` por tenant y/o `BOT_PRIVILEGED_GROUP_JIDS`) | `botGroupSenderIsMember` contra la lista de participantes **fresca** del proveedor |
| **(b) privado de un miembro actual** | chat 1:1 y el teléfono del actor (`ActorPhoneJID`) es **miembro ACTUAL** de ese grupo | `botSpecialAdminFreshMember` (fetch fresco, sin caché) |

Reglas que se aplican y que **no** admiten atajo:

- `actor` es **obligatorio** y siempre es un **teléfono** (`ActorPhoneJID`). Un mensaje identificado
  solo por **LID** (sin teléfono verificado) se **rechaza** (*fail closed*): nunca se convierte un
  LID en teléfono adivinando dígitos.
- La **lista de contactos en base de datos** (`tenant.PrivilegedContacts`) **no** es una credencial
  independiente: no concede privilegio por sí sola (desviación **B1** de PR588).
- Aprobar con `OK <código>` por privado **exige** prueba de membresía actual (**B2**).
- Un `OK <código>` ejecutado con actor **LID** en lugar de teléfono se rechaza (**B3**).
- La **operación `list`** del plano de control exige la **misma** frontera.
- `management_group` se fija **por JID estable** (`@g.us`); un nombre ambiguo se rechaza.
- Hay **un único slot** de propuesta de acceso viva por tenant: una propuesta nueva **supersede**
  la anterior (disponibilidad) y el *claim* sigue siendo atómico (`RowsAffected == 1`).

---

## 3. Leyenda de cobertura

| Marca | Significado |
|---|---|
| `admin_call` | camino genérico de Forky: reproduce la operación por su **nombre** en el mapa, con la sesión y los gates reales de la ruta. En WA solo sirve para **lecturas** (las escrituras genéricas se rechazan). |
| tool dedicada | tool propia que cubre esa operación con las reglas del dominio (reutiliza el handler o un núcleo puro equivalente). |
| `*_read(recurso)` | tool de lectura parametrizada `resource×action` (`assistant_domain_read.go`): reutiliza el handler real con sus gates exactos. |
| `manage_backoffice` | **nuevo**: tool WA `resource×action` que **propone** una operación del panel y la aplica solo tras `OK <código>`. |
| `manage_bot_access` | **nuevo**: tool WA `resource×action` del plano de control (tool grants + grupo de gestión),también propose→OK. |
| EXCLUIDO | no hay tool y no se va a crear; el motivo está en la celda. |

---

## 4. Resumen por familia (`/admin`)

| Familia | Lecturas | Escrituras | Total |
|---|---|---|---|
| `/admin/active-restaurant` | 0 | 1 | 1 |
| `/admin/analytics` | 5 | 1 | 6 |
| `/admin/arroz-types` | 1 | 0 | 1 |
| `/admin/bebidas` | 1 | 4 | 5 |
| `/admin/booking-extras` | 1 | 2 | 3 |
| `/admin/booking-notifications` | 1 | 1 | 2 |
| `/admin/bookings` | 10 | 4 | 14 |
| `/admin/bot` | 4 | 4 | 8 |
| `/admin/branding` | 1 | 1 | 2 |
| `/admin/cafes` | 1 | 4 | 5 |
| `/admin/calendar` | 1 | 0 | 1 |
| `/admin/campanas` | 7 | 6 | 13 |
| `/admin/comida` | 14 | 25 | 39 |
| `/admin/config` | 24 | 33 | 57 |
| `/admin/dashboard` | 1 | 0 | 1 |
| `/admin/dishes-catalog` | 1 | 1 | 2 |
| `/admin/domains` | 1 | 3 | 4 |
| `/admin/fichaje` | 5 | 5 | 10 |
| `/admin/group-menus` | 2 | 4 | 6 |
| `/admin/group-menus-v2` | 7 | 27 | 34 |
| `/admin/horarios` | 5 | 3 | 8 |
| `/admin/integrations` | 2 | 3 | 5 |
| `/admin/invoices` | 5 | 7 | 12 |
| `/admin/legal-pages` | 2 | 1 | 3 |
| `/admin/me` | 1 | 2 | 3 |
| `/admin/members` | 9 | 20 | 29 |
| `/admin/menu-visibility` | 1 | 1 | 2 |
| `/admin/menus` | 3 | 8 | 11 |
| `/admin/platform` | 9 | 16 | 25 |
| `/admin/platos` | 1 | 4 | 5 |
| `/admin/pos` | 50 | 76 | 126 |
| `/admin/postres` | 1 | 3 | 4 |
| `/admin/restaurant` | 1 | 1 | 2 |
| `/admin/roles` | 1 | 1 | 2 |
| `/admin/site-builder` | 1 | 3 | 4 |
| `/admin/stock` | 34 | 46 | 80 |
| `/admin/tables` | 2 | 5 | 7 |
| `/admin/users` | 0 | 2 | 2 |
| `/admin/vinos` | 2 | 3 | 5 |
| `/admin/website` | 3 | 4 | 7 |
| `/admin/widget` | 1 | 1 | 2 |
| **TOTAL** | **222** | **336** | **558** |

**Totales: 558 operaciones** (222 lecturas, 336 escrituras) de las 600+ rutas del panel: el mapa
generado excluye a propósito los *handlers* `WS|WebSocket|Upload|Image|Avatar|Import|OCR` (§6).

---

## 5. Matriz A — endpoints `/admin` (558 operaciones)

Ruta relativa a `/admin`; en producción es `/api/admin/...`. Los **gates** son la cadena exacta de
middleware de `server.go`. La columna *Herramienta Forky* incluye `admin_call` como camino genérico
para toda operación del mapa (con `confirmed=true` + `confirmation_token` en las escrituras).

| Método | Ruta | Gates exactos (server.go) | Sección | Riesgo | Herramienta Forky (panel/MCP/plugin) | Herramienta WhatsApp |
|---|---|---|---|---|---|---|
| `POST` | `/active-restaurant` | `s.requireBOSession` | session | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — el bot no cambia de restaurante |
| `GET` | `/analytics/affluence` | `s.requireBOSession`, `statisticsGate` | estadisticas | read | `analytics_report` | `admin_call` (lectura) |
| `GET` | `/analytics/affluence/seasonal` | `s.requireBOSession`, `statisticsGate` | estadisticas | read | `analytics_report` | `admin_call` (lectura) |
| `GET` | `/analytics/ai-usage` | `s.requireBOSession`, `statisticsGate` | estadisticas | read | `ai_usage_report` | `ai_usage_report` |
| `GET` | `/analytics/overview` | `s.requireBOSession`, `statisticsGate` | estadisticas | read | `analytics_report` | `analytics_report` |
| `POST` | `/analytics/refresh` | `s.requireBOSession`, `statisticsGate` | estadisticas | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/analytics/whatsapp-messages` | `s.requireBOSession`, `statisticsGate` | estadisticas | read | `whatsapp_messages_list` | `whatsapp_messages_list` |
| `GET` | `/arroz-types` | `s.requireBOSession`, `reservasGate` | reservas | read | `menus_read(arroz_types)` | `menus_read(arroz_types)` |
| `GET` | `/bebidas` | `s.requireBOSession`, `menusGate` | menus | read | `catalog_list(bebidas)` | `catalog_list(bebidas)` |
| `POST` | `/bebidas` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `DELETE` | `/bebidas/{id}` | `s.requireBOSession`, `menusGate` | menus | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PATCH` | `/bebidas/{id}` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/bebidas/{id}/toggle` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/booking-extras` | `s.requireBOSession`, `reservasGate` | reservas | read | `admin_call` | `admin_call` (lectura) |
| `POST` | `/booking-extras` | `s.requireBOSession`, `reservasGate` | reservas | write | `catalog_create(booking_extras)`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `booking_propose_catalog_extra_create` |
| `DELETE` | `/booking-extras/{id}` | `s.requireBOSession`, `reservasGate` | reservas | destructive | `catalog_delete(booking_extras)`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `booking_propose_catalog_extra_delete` |
| `GET` | `/booking-notifications` | `s.requireBOSession`, `miembrosGate`, `rolesAdminGate` | miembros | read | `reservas_read(booking_notifications)` | `admin_call` (lectura) |
| `PUT` | `/booking-notifications` | `s.requireBOSession`, `miembrosGate`, `rolesAdminGate` | miembros | write | `whatsapp_bot_config_update(notif)`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `booking_propose_notif_settings` |
| `GET` | `/bookings` | `s.requireBOSession`, `reservasGate` | reservas | read | `bookings_list`<br>`bookings_summary` | EXCLUIDO — GET con escritura (marcado `write` en el mapa) |
| `POST` | `/bookings` | `s.requireBOSession`, `reservasGate` | reservas | write | `create_booking`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/bookings/cancelled` | `s.requireBOSession`, `reservasGate` | reservas | read | `cancelled_bookings_list` | `list_cancelled_bookings` |
| `POST` | `/bookings/cancelled/{id}/reactivate` | `s.requireBOSession`, `reservasGate` | reservas | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — writes-control legacy RETENIDO |
| `GET` | `/bookings/documents/{docId}/file` | `s.requireBOSession`, `reservasGate` | reservas | read | `admin_call` | `admin_call` (lectura) |
| `GET` | `/bookings/export` | `s.requireBOSession`, `reservasGate` | reservas | read | `admin_call` | `admin_call` (lectura) |
| `GET` | `/bookings/modified` | `s.requireBOSession`, `reservasGate` | reservas | read | `reservas_read(bookings_modified)` | `reservas_read(bookings_modified)` |
| `GET` | `/bookings/search` | `s.requireBOSession`, `reservasGate` | reservas | read | `admin_call` | `admin_call` (lectura) |
| `GET` | `/bookings/{id}` | `s.requireBOSession`, `reservasGate` | reservas | read | `admin_call` | `get_booking_details` |
| `PATCH` | `/bookings/{id}` | `s.requireBOSession`, `reservasGate` | reservas | write | `update_booking`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `booking_propose_edit` |
| `POST` | `/bookings/{id}/cancel` | `s.requireBOSession`, `reservasGate` | reservas | destructive | `delete_booking`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `booking_propose_cancel` |
| `GET` | `/bookings/{id}/documents` | `s.requireBOSession`, `reservasGate` | reservas | read | `admin_call` | `admin_call` (lectura) |
| `GET` | `/bookings/{id}/qr` | `s.requireBOSession`, `reservasGate` | reservas | read | `admin_call` | EXCLUIDO — GET con escritura (marcado `write` en el mapa) |
| `GET` | `/bookings/{id}/receipt.pdf` | `s.requireBOSession`, `reservasGate` | reservas | read | `admin_call` | `admin_call` (lectura) |
| `GET` | `/bot/ai/{restaurantId}` | `s.requireBOSession`, `rootOnlyGate` | root | read | `admin_call` | EXCLUIDO — clave de proveedor — fuera de alcance |
| `PUT` | `/bot/ai/{restaurantId}` | `s.requireBOSession`, `rootOnlyGate` | root | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — clave de proveedor — fuera de alcance |
| `GET` | `/bot/config` | `s.requireBOSession`, `ajustesGate`, `rolesAdminGate` | ajustes | read | `whatsapp_bot_config_get` | `whatsapp_bot_config_get` |
| `PUT` | `/bot/config` | `s.requireBOSession`, `ajustesGate`, `rolesAdminGate` | ajustes | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/bot/pipeline/{restaurantId}` | `s.requireBOSession`, `rootOnlyGate` | root | read | `admin_call` | EXCLUIDO — pipeline DSPy — se deja solo al panel |
| `GET` | `/bot/settings/{restaurantId}` | `s.requireBOSession`, `rootOnlyGate` | root | read | `admin_call` | EXCLUIDO — prompt del bot — se deja solo al panel |
| `PUT` | `/bot/settings/{restaurantId}` | `s.requireBOSession`, `rootOnlyGate` | root | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — prompt del bot — se deja solo al panel |
| `POST` | `/bot/settings/{restaurantId}/preview` | `s.requireBOSession`, `rootOnlyGate` | root | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/branding` | `s.requireBOSession`, `reservasGate` | reservas | read | `branding_get` | `branding_get` |
| `POST` | `/branding` | `s.requireBOSession`, `reservasGate` | reservas | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/cafes` | `s.requireBOSession`, `menusGate` | menus | read | `catalog_list(cafes)` | `catalog_list(cafes)` |
| `POST` | `/cafes` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `DELETE` | `/cafes/{id}` | `s.requireBOSession`, `menusGate` | menus | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PATCH` | `/cafes/{id}` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/cafes/{id}/toggle` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/calendar` | `s.requireBOSession`, `reservasGate` | reservas | read | `admin_call` | `admin_call` (lectura) |
| `GET` | `/campanas` | `s.requireBOSession`, `reservasGate`, `campaignsGate` | reservas | read | `campaigns_list` | `list_campaigns` |
| `POST` | `/campanas` | `s.requireBOSession`, `reservasGate`, `campaignsGate` | reservas | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/campanas/preview` | `s.requireBOSession`, `reservasGate`, `campaignsGate` | reservas | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — envío real a clientes — fuera de alcance |
| `GET` | `/campanas/template` | `s.requireBOSession`, `reservasGate`, `campaignsGate` | reservas | read | `campaigns_read(template)` | `admin_call` (lectura) |
| `GET` | `/campanas/unsubscribed` | `s.requireBOSession`, `reservasGate`, `campaignsGate` | reservas | read | `campaigns_list` | `admin_call` (lectura) |
| `DELETE` | `/campanas/{campaignId}` | `s.requireBOSession`, `reservasGate`, `campaignsGate` | reservas | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/campanas/{campaignId}` | `s.requireBOSession`, `reservasGate`, `campaignsGate` | reservas | read | `admin_call` | `admin_call` (lectura) |
| `PUT` | `/campanas/{campaignId}` | `s.requireBOSession`, `reservasGate`, `campaignsGate` | reservas | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/campanas/{campaignId}/audience` | `s.requireBOSession`, `reservasGate`, `campaignsGate` | reservas | read | `admin_call` | `admin_call` (lectura) |
| `GET` | `/campanas/{campaignId}/recipients` | `s.requireBOSession`, `reservasGate`, `campaignsGate` | reservas | read | `admin_call` | `admin_call` (lectura) |
| `POST` | `/campanas/{campaignId}/send` | `s.requireBOSession`, `reservasGate`, `campaignsGate` | reservas | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — envío real a clientes — fuera de alcance |
| `GET` | `/campanas/{campaignId}/status` | `s.requireBOSession`, `reservasGate`, `campaignsGate` | reservas | read | `admin_call` | `admin_call` (lectura) |
| `POST` | `/campanas/{campaignId}/test` | `s.requireBOSession`, `reservasGate`, `campaignsGate` | reservas | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — envío real a clientes — fuera de alcance |
| `GET` | `/comida/bebidas/categorias` | `s.requireBOSession`, `menusGate` | menus | read | `catalog_list(bebidas)` | EXCLUIDO — GET con escritura (marcado `write` en el mapa) |
| `POST` | `/comida/bebidas/categorias` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/comida/bebidas/categorias/check` | `s.requireBOSession`, `menusGate` | menus | read | `admin_call` | `admin_call` (lectura) |
| `POST` | `/comida/bulk-stock-link/apply` | `s.requireBOSession`, `withBOStockTimeout`, `productionTypeGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/comida/bulk-stock-link/preview` | `s.requireBOSession`, `withBOStockTimeout`, `productionTypeGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/comida/categorias` | `s.requireBOSession`, `menusGate` | menus | read | `admin_call` | EXCLUIDO — GET con escritura (marcado `write` en el mapa) |
| `POST` | `/comida/categorias` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `DELETE` | `/comida/categorias/{id}` | `s.requireBOSession`, `menusGate` | menus | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PATCH` | `/comida/categorias/{id}` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/comida/counts` | `s.requireBOSession`, `menusGate` | menus | read | `menus_read(comida_counts)` | `menus_read(comida_counts)` |
| `PATCH` | `/comida/items/{id}/production-type` | `s.requireBOSession`, `withBOStockTimeout`, `productionTypeGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/comida/platos/categorias` | `s.requireBOSession`, `menusGate` | menus | read | `catalog_list(comida)` | EXCLUIDO — GET con escritura (marcado `write` en el mapa) |
| `POST` | `/comida/platos/categorias` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/comida/technical-sheets` | `s.requireBOSession`, `withBOStockTimeout`, `sheetsViewGate` | stock | read | `sheets_read(list)` | `admin_call` (lectura) |
| `POST` | `/comida/technical-sheets` | `s.requireBOSession`, `withBOStockTimeout`, `sheetsManageGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/comida/technical-sheets/ensure` | `s.requireBOSession`, `withBOStockTimeout`, `sheetsManageGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `DELETE` | `/comida/technical-sheets/{id}` | `s.requireBOSession`, `withBOStockTimeout`, `sheetsDeleteGate` | stock | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/comida/technical-sheets/{id}` | `s.requireBOSession`, `withBOStockTimeout`, `sheetsViewGate` | stock | read | `sheets_read(get)` | `admin_call` (lectura) |
| `PATCH` | `/comida/technical-sheets/{id}` | `s.requireBOSession`, `withBOStockTimeout`, `sheetsManageGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/comida/technical-sheets/{id}/allergens` | `s.requireBOSession`, `withBOStockTimeout`, `sheetsViewGate` | stock | read | `sheets_read(allergens)` | `admin_call` (lectura) |
| `PATCH` | `/comida/technical-sheets/{id}/allergens` | `s.requireBOSession`, `withBOStockTimeout`, `sheetsManageGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/comida/technical-sheets/{id}/components` | `s.requireBOSession`, `withBOStockTimeout`, `sheetsViewGate` | stock | read | `sheets_read(components)` | `admin_call` (lectura) |
| `POST` | `/comida/technical-sheets/{id}/components` | `s.requireBOSession`, `withBOStockTimeout`, `sheetsManageGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `DELETE` | `/comida/technical-sheets/{id}/components/{componentId}` | `s.requireBOSession`, `withBOStockTimeout`, `sheetsManageGate` | stock | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PATCH` | `/comida/technical-sheets/{id}/components/{componentId}` | `s.requireBOSession`, `withBOStockTimeout`, `sheetsManageGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/comida/technical-sheets/{id}/cost` | `s.requireBOSession`, `withBOStockTimeout`, `sheetsViewGate` | stock | read | `sheets_read(cost)` | `admin_call` (lectura) |
| `POST` | `/comida/technical-sheets/{id}/duplicate` | `s.requireBOSession`, `withBOStockTimeout`, `sheetsManageGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/comida/technical-sheets/{id}/publish` | `s.requireBOSession`, `withBOStockTimeout`, `sheetsPublishGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/comida/technical-sheets/{id}/steps` | `s.requireBOSession`, `withBOStockTimeout`, `sheetsViewGate` | stock | read | `sheets_read(steps)` | `admin_call` (lectura) |
| `POST` | `/comida/technical-sheets/{id}/steps` | `s.requireBOSession`, `withBOStockTimeout`, `sheetsStepsGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PUT` | `/comida/technical-sheets/{id}/steps/order` | `s.requireBOSession`, `withBOStockTimeout`, `sheetsStepsGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `DELETE` | `/comida/technical-sheets/{id}/steps/{stepId}` | `s.requireBOSession`, `withBOStockTimeout`, `sheetsStepsGate` | stock | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PATCH` | `/comida/technical-sheets/{id}/steps/{stepId}` | `s.requireBOSession`, `withBOStockTimeout`, `sheetsStepsGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/comida/technical-sheets/{id}/usage` | `s.requireBOSession`, `withBOStockTimeout`, `sheetsViewGate` | stock | read | `sheets_read(usage)` | `admin_call` (lectura) |
| `GET` | `/comida/{tipo}` | `s.requireBOSession`, `menusGate` | menus | read | `catalog_list/get` | `catalog_list/get` |
| `POST` | `/comida/{tipo}` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `DELETE` | `/comida/{tipo}/{id}` | `s.requireBOSession`, `menusGate` | menus | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/comida/{tipo}/{id}` | `s.requireBOSession`, `menusGate` | menus | read | `admin_call` | `admin_call` (lectura) |
| `PATCH` | `/comida/{tipo}/{id}` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/config/ads` | `s.requireBOSession`, `adsGate` | anuncios | read | `website_read(ads_list)` | `admin_call` (lectura) |
| `POST` | `/config/ads` | `s.requireBOSession`, `adsGate` | anuncios | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `DELETE` | `/config/ads/{adId}` | `s.requireBOSession`, `adsGate` | anuncios | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PUT` | `/config/ads/{adId}` | `s.requireBOSession`, `adsGate` | anuncios | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/config/bunny-storage` | `s.requireBOSession`, `rootOnlyGate` | root | read | `admin_call` | EXCLUIDO — clave de proveedor — fuera de alcance |
| `POST` | `/config/bunny-storage` | `s.requireBOSession`, `rootOnlyGate` | root | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — clave de proveedor — fuera de alcance |
| `GET` | `/config/chatgpt-plugin/tokens` | `s.requireBOSession`, `rootOnlyGate` | root | read | `admin_call` | EXCLUIDO — credencial del plugin — fuera de alcance |
| `POST` | `/config/chatgpt-plugin/tokens` | `s.requireBOSession`, `rootOnlyGate` | root | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — credencial del plugin — fuera de alcance |
| `POST` | `/config/chatgpt-plugin/tokens/revoke` | `s.requireBOSession`, `rootOnlyGate` | root | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — credencial del plugin — fuera de alcance |
| `POST` | `/config/check-website` | `s.requireBOSession`, `reservasGate` | reservas | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — llamada externa — se deja solo al panel |
| `GET` | `/config/daily-limit` | `s.requireBOSession`, `reservasGate` | reservas | read | `booking_limits_get` | `booking_limits_get` |
| `POST` | `/config/daily-limit` | `s.requireBOSession`, `reservasGate` | reservas | write | `booking_limits_update`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `config_propose_daily_limit` |
| `GET` | `/config/day` | `s.requireBOSession`, `reservasGate` | reservas | read | `admin_call` | `admin_call` (lectura) |
| `POST` | `/config/day` | `s.requireBOSession`, `reservasGate` | reservas | write | `booking_limits_update`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `config_propose_day` |
| `GET` | `/config/defaults` | `s.requireBOSession`, `reservasGate` | reservas | read | `admin_call` | `admin_call` (lectura) |
| `POST` | `/config/defaults` | `s.requireBOSession`, `reservasGate` | reservas | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/config/email-provider` | `s.requireBOSession`, `reservasGate` | reservas | read | `admin_call` | EXCLUIDO — clave de proveedor — fuera de alcance |
| `POST` | `/config/email-provider` | `s.requireBOSession`, `reservasGate` | reservas | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — clave de proveedor — fuera de alcance |
| `GET` | `/config/floors` | `s.requireBOSession`, `reservasGate` | reservas | read | `admin_call` | EXCLUIDO — GET con escritura (marcado `write` en el mapa) |
| `POST` | `/config/floors` | `s.requireBOSession`, `reservasGate` | reservas | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/config/floors/date` | `s.requireBOSession`, `reservasGate` | reservas | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/config/floors/defaults` | `s.requireBOSession`, `reservasGate` | reservas | read | `admin_call` | EXCLUIDO — GET con escritura (marcado `write` en el mapa) |
| `POST` | `/config/floors/defaults` | `s.requireBOSession`, `reservasGate` | reservas | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/config/hour-split` | `s.requireBOSession`, `reservasGate` | reservas | read | `admin_call` | `admin_call` (lectura) |
| `POST` | `/config/hour-split` | `s.requireBOSession`, `reservasGate` | reservas | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/config/hour-split-percentages` | `s.requireBOSession`, `reservasGate` | reservas | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/config/location-booking` | `s.requireBOSession`, `reservasGate` | reservas | read | `admin_call` | `admin_call` (lectura) |
| `POST` | `/config/location-booking` | `s.requireBOSession`, `reservasGate` | reservas | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/config/mandatory-menus` | `s.requireBOSession`, `reservasGate` | reservas | read | `menus_read(mandatory_menus)` | `menus_read(mandatory_menus)` |
| `POST` | `/config/mandatory-menus` | `s.requireBOSession`, `reservasGate` | reservas | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/config/mesas-de-dos` | `s.requireBOSession`, `reservasGate` | reservas | read | `admin_call` | `admin_call` (lectura) |
| `POST` | `/config/mesas-de-dos` | `s.requireBOSession`, `reservasGate` | reservas | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `config_propose_mesas(2)` |
| `GET` | `/config/mesas-de-tres` | `s.requireBOSession`, `reservasGate` | reservas | read | `admin_call` | `admin_call` (lectura) |
| `POST` | `/config/mesas-de-tres` | `s.requireBOSession`, `reservasGate` | reservas | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `config_propose_mesas(3)` |
| `GET` | `/config/minimax` | `s.requireBOSession`, `rootOnlyGate` | root | read | `admin_call` | EXCLUIDO — clave de proveedor — fuera de alcance |
| `POST` | `/config/minimax` | `s.requireBOSession`, `rootOnlyGate` | root | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — clave de proveedor — fuera de alcance |
| `GET` | `/config/mobility-day` | `s.requireBOSession`, `reservasGate` | reservas | read | `admin_call` | `admin_call` (lectura) |
| `POST` | `/config/mobility-day` | `s.requireBOSession`, `reservasGate` | reservas | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/config/opening-hours` | `s.requireBOSession`, `reservasGate` | reservas | read | `admin_call` | `admin_call` (lectura) |
| `POST` | `/config/opening-hours` | `s.requireBOSession`, `reservasGate` | reservas | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/config/restaurant-info` | `s.requireBOSession`, `reservasGate` | reservas | read | `restaurant_settings_get` | `restaurant_settings_get` |
| `POST` | `/config/restaurant-info` | `s.requireBOSession`, `reservasGate` | reservas | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/config/salon-condesa` | `s.requireBOSession`, `reservasGate` | reservas | read | `admin_call` | `admin_call` (lectura) |
| `POST` | `/config/salon-condesa` | `s.requireBOSession`, `reservasGate` | reservas | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `config_propose_salon_condesa` |
| `GET` | `/config/salons` | `s.requireBOSession`, `reservasGate` | reservas | read | `admin_call` | `admin_call` (lectura) |
| `POST` | `/config/salons` | `s.requireBOSession`, `reservasGate` | reservas | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/config/salons/day-status` | `s.requireBOSession`, `reservasGate` | reservas | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `DELETE` | `/config/salons/{salonId}` | `s.requireBOSession`, `reservasGate` | reservas | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PUT` | `/config/salons/{salonId}` | `s.requireBOSession`, `reservasGate` | reservas | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/config/special-dates` | `s.requireBOSession`, `reservasGate` | reservas | read | `special_dates_list` | `list_special_dates` |
| `POST` | `/config/special-dates` | `s.requireBOSession`, `reservasGate` | reservas | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/config/special-dates/stats` | `s.requireBOSession`, `reservasGate` | reservas | read | `reservas_read(special_date_stats)` | `reservas_read(special_date_stats)` |
| `GET` | `/config/stripe-connect` | `s.requireBOSession`, `rootOnlyGate` | root | read | `admin_call` | EXCLUIDO — GET con escritura (marcado `write` en el mapa) |
| `POST` | `/config/stripe-connect/dashboard` | `s.requireBOSession`, `rootOnlyGate` | root | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — onboarding Stripe — fuera de alcance |
| `GET` | `/config/stripe-connect/delete-precheck` | `s.requireBOSession`, `rootOnlyGate` | root | read | `admin_call` | `admin_call` (lectura) |
| `POST` | `/config/stripe-connect/disconnect` | `s.requireBOSession`, `rootOnlyGate` | root | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — onboarding Stripe — fuera de alcance |
| `POST` | `/config/stripe-connect/onboard` | `s.requireBOSession`, `rootOnlyGate` | root | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — cobro/onboarding Stripe — fuera de alcance |
| `GET` | `/dashboard/metrics` | `s.requireBOSession`, `reservasGate` | reservas | read | `admin_call` | `admin_call` (lectura) |
| `GET` | `/dishes-catalog/search` | `s.requireBOSession`, `menusGate` | menus | read | `admin_call` | `admin_call` (lectura) |
| `POST` | `/dishes-catalog/upsert` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/domains/quote` | `s.requireBOSession`, `ajustesGate` | ajustes | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — COBRO real (dominio) — fuera de alcance |
| `POST` | `/domains/register` | `s.requireBOSession`, `ajustesGate` | ajustes | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — COBRO real (dominio) — fuera de alcance |
| `GET` | `/domains/search` | `s.requireBOSession`, `ajustesGate` | ajustes | read | `admin_call` | `admin_call` (lectura) |
| `POST` | `/domains/verify` | `s.requireBOSession`, `ajustesGate` | ajustes | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/fichaje/admin/start` | `s.requireBOSession`, `fichajeGate`, `rolesAdminGate` | fichaje | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/fichaje/admin/stop` | `s.requireBOSession`, `fichajeGate`, `rolesAdminGate` | fichaje | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/fichaje/entries` | `s.requireBOSession`, `fichajeGate`, `rolesAdminGate` | fichaje | read | `fichaje_entries_list` | `fichaje_entries_list` |
| `PATCH` | `/fichaje/entries/{id}` | `s.requireBOSession`, `fichajeGate`, `rolesAdminGate` | fichaje | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/fichaje/hourly-costs` | `s.requireBOSession`, `fichajeGate`, `rolesAdminGate` | fichaje | read | `fichaje_read(hourly_costs)` | `admin_call` (lectura) |
| `GET` | `/fichaje/labour-cost` | `s.requireBOSession`, `fichajeGate`, `rolesAdminGate` | fichaje | read | `fichaje_read(labour_cost)` | `admin_call` (lectura) |
| `GET` | `/fichaje/ping` | `s.requireBOSession`, `fichajeGate` | fichaje | read | `admin_call` | `admin_call` (lectura) |
| `POST` | `/fichaje/start` | `s.requireBOSession`, `fichajeGate` | fichaje | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/fichaje/state` | `s.requireBOSession`, `fichajeGate` | fichaje | read | `fichaje_state_get` | `fichaje_state_get` |
| `POST` | `/fichaje/stop` | `s.requireBOSession`, `fichajeGate` | fichaje | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/group-menus` | `s.requireBOSession`, `menusGate` | menus | read | `menus_list` | `menus_list` |
| `POST` | `/group-menus` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/group-menus-v2` | `s.requireBOSession`, `menusGate` | menus | read | `menus_list`<br>`menus_read(group_menus_v2_list)` | `menus_list`<br>`menus_read(group_menus_v2_list)` |
| `POST` | `/group-menus-v2/drafts` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/group-menus-v2/postres/resolve` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `DELETE` | `/group-menus-v2/{id}` | `s.requireBOSession`, `menusGate` | menus | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/group-menus-v2/{id}` | `s.requireBOSession`, `menusGate` | menus | read | `menu_get` | EXCLUIDO — GET con escritura (marcado `write` en el mapa) |
| `PATCH` | `/group-menus-v2/{id}/basics` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/group-menus-v2/{id}/beverage-options` | `s.requireBOSession`, `menusGate` | menus | read | `menus_read(beverage_options)` | `admin_call` (lectura) |
| `PATCH` | `/group-menus-v2/{id}/menu-type` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/group-menus-v2/{id}/publish` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/group-menus-v2/{id}/same-day-booking` | `s.requireBOSession`, `menusGate` | menus | read | `admin_call` | `admin_call` (lectura) |
| `DELETE` | `/group-menus-v2/{id}/same-day-booking/{dishId}` | `s.requireBOSession`, `menusGate` | menus | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/group-menus-v2/{id}/same-day-booking/{dishId}` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PUT` | `/group-menus-v2/{id}/sections` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `DELETE` | `/group-menus-v2/{id}/sections/{sectionId}` | `s.requireBOSession`, `menusGate` | menus | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PATCH` | `/group-menus-v2/{id}/sections/{sectionId}/annotations` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PATCH` | `/group-menus-v2/{id}/sections/{sectionId}/dessert-source` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/group-menus-v2/{id}/sections/{sectionId}/dishes` | `s.requireBOSession`, `menusGate` | menus | read | `menu_sections_get`<br>`menus_read(group_menus_v2_section_dishes)` | EXCLUIDO — GET con escritura (marcado `write` en el mapa) |
| `PUT` | `/group-menus-v2/{id}/sections/{sectionId}/dishes` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PATCH` | `/group-menus-v2/{id}/sections/{sectionId}/dishes/{dishId}` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PATCH` | `/group-menus-v2/{id}/sections/{sectionId}/visibility` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/group-menus-v2/{id}/slider` | `s.requireBOSession`, `menusGate` | menus | read | `menus_read(group_menus_v2_slider)` | EXCLUIDO — GET con escritura (marcado `write` en el mapa) |
| `PATCH` | `/group-menus-v2/{id}/slider` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PUT` | `/group-menus-v2/{id}/special-cta` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PUT` | `/group-menus-v2/{id}/special-group-booking` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PUT` | `/group-menus-v2/{id}/special-principales` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/group-menus-v2/{id}/special-sections` | `s.requireBOSession`, `menusGate` | menus | read | `menus_read(group_menus_v2_special)` | `admin_call` (lectura) |
| `POST` | `/group-menus-v2/{id}/special-sections` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PUT` | `/group-menus-v2/{id}/special-sections/order` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `DELETE` | `/group-menus-v2/{id}/special-sections/{sectionId}` | `s.requireBOSession`, `menusGate` | menus | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PATCH` | `/group-menus-v2/{id}/special-sections/{sectionId}` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/group-menus-v2/{id}/special-sections/{sectionId}/principales` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `DELETE` | `/group-menus-v2/{id}/special-sections/{sectionId}/principales/{dishId}` | `s.requireBOSession`, `menusGate` | menus | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/group-menus-v2/{id}/toggle-active` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PATCH` | `/group-menus-v2/{id}/visibility` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `DELETE` | `/group-menus/{id}` | `s.requireBOSession`, `menusGate` | menus | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/group-menus/{id}` | `s.requireBOSession`, `menusGate` | menus | read | `admin_call` | `admin_call` (lectura) |
| `PUT` | `/group-menus/{id}` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/group-menus/{id}/toggle` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/horarios` | `s.requireBOSession`, `horariosGate` | horarios | read | `schedules_list` | `schedules_list` |
| `POST` | `/horarios` | `s.requireBOSession`, `horariosGate` | horarios | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/horarios/calendar` | `s.requireBOSession`, `horariosGate` | horarios | read | `schedules_by_date` | `admin_call` (lectura) |
| `GET` | `/horarios/member-range` | `s.requireBOSession`, `horariosGate` | horarios | read | `admin_call` | `admin_call` (lectura) |
| `GET` | `/horarios/month` | `s.requireBOSession`, `horariosGate` | horarios | read | `schedules_month` | `schedules_month` |
| `GET` | `/horarios/my-schedule` | `s.requireBOSession`, `fichajeGate` | fichaje | read | `admin_call` | `admin_call` (lectura) |
| `DELETE` | `/horarios/{id}` | `s.requireBOSession`, `horariosGate` | horarios | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PUT` | `/horarios/{id}` | `s.requireBOSession`, `horariosGate` | horarios | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/integrations` | `s.requireBOSession`, `ajustesGate` | ajustes | read | `integrations_get` | `integrations_get` |
| `POST` | `/integrations` | `s.requireBOSession`, `ajustesGate` | ajustes | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/integrations/uazapi/servers` | `s.requireBOSession`, `ajustesGate`, `rolesAdminGate` | ajustes | read | `admin_call` | EXCLUIDO — infra de proveedor — fuera de alcance |
| `POST` | `/integrations/uazapi/servers` | `s.requireBOSession`, `ajustesGate`, `rolesAdminGate` | ajustes | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — infra de proveedor — fuera de alcance |
| `PATCH` | `/integrations/uazapi/servers/{id}` | `s.requireBOSession`, `ajustesGate`, `rolesAdminGate` | ajustes | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/invoices` | `s.requireBOSession`, `facturasGate` | facturas | read | `invoices_list` | `invoices_list` |
| `POST` | `/invoices` | `s.requireBOSession`, `facturasGate` | facturas | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/invoices/search-reservation` | `s.requireBOSession`, `facturasGate` | facturas | read | `invoices_read(search_reservation)` | `admin_call` (lectura) |
| `DELETE` | `/invoices/{id}` | `s.requireBOSession`, `facturasGate` | facturas | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/invoices/{id}` | `s.requireBOSession`, `facturasGate` | facturas | read | `invoice_get` | `invoice_get` |
| `PUT` | `/invoices/{id}` | `s.requireBOSession`, `facturasGate` | facturas | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/invoices/{id}/comments` | `s.requireBOSession`, `facturasGate` | facturas | read | `admin_call` | `admin_call` (lectura) |
| `POST` | `/invoices/{id}/comments` | `s.requireBOSession`, `facturasGate` | facturas | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `DELETE` | `/invoices/{id}/comments/{commentId}` | `s.requireBOSession`, `facturasGate` | facturas | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PUT` | `/invoices/{id}/comments/{commentId}` | `s.requireBOSession`, `facturasGate` | facturas | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/invoices/{id}/pdf` | `s.requireBOSession`, `facturasGate` | facturas | read | `admin_call` | `admin_call` (lectura) |
| `POST` | `/invoices/{id}/send` | `s.requireBOSession`, `facturasGate` | facturas | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — envío real de factura — fuera de alcance |
| `GET` | `/legal-pages` | `s.requireBOSession`, `reservasGate` | reservas | read | `legal_pages_list` | `admin_call` (lectura) |
| `GET` | `/legal-pages/{slug}` | `s.requireBOSession`, `reservasGate` | reservas | read | `legal_pages_list` | `admin_call` (lectura) |
| `POST` | `/legal-pages/{slug}` | `s.requireBOSession`, `reservasGate` | reservas | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/me` | `s.requireBOSession` | session | read | `admin_call` | EXCLUIDO — operación sobre la cuenta de la sesión |
| `POST` | `/me/password` | `s.requireBOSession` | session | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PUT` | `/me/preferences` | `s.requireBOSession` | session | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/members` | `s.requireBOSession`, `miembrosGate`, `rolesAdminGate` | miembros | read | `members_list` | `members_list` |
| `POST` | `/members` | `s.requireBOSession`, `miembrosGate`, `rolesAdminGate` | miembros | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/members/whatsapp/cancel` | `s.requireBOSession`, `rootOnlyGate` | root | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — gestión de la conexión — fuera de alcance |
| `POST` | `/members/whatsapp/connect` | `s.requireBOSession`, `miembrosGate`, `rolesAdminGate` | miembros | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — gestión de la conexión — fuera de alcance |
| `GET` | `/members/whatsapp/connection` | `s.requireBOSession`, `miembrosGate`, `rolesAdminGate` | miembros | read | `whatsapp_connection_get` | EXCLUIDO — GET con escritura (marcado `write` en el mapa) |
| `POST` | `/members/whatsapp/disconnect` | `s.requireBOSession`, `miembrosGate`, `rolesAdminGate` | miembros | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — gestión de la conexión — fuera de alcance |
| `POST` | `/members/whatsapp/flush-queue` | `s.requireBOSession`, `miembrosGate`, `rolesAdminGate` | miembros | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — envío real a clientes — fuera de alcance |
| `POST` | `/members/whatsapp/send` | `s.requireBOSession`, `miembrosGate`, `rolesAdminGate` | miembros | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — envío real a clientes — fuera de alcance |
| `POST` | `/members/whatsapp/subscribe` | `s.requireBOSession`, `rootOnlyGate` | root | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — gestión de la conexión — fuera de alcance |
| `DELETE` | `/members/{id}` | `s.requireBOSession`, `miembrosGate`, `rolesAdminGate` | miembros | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/members/{id}` | `s.requireBOSession`, `miembrosGate`, `rolesAdminGate` | miembros | read | `member_get` | `member_get` |
| `PATCH` | `/members/{id}` | `s.requireBOSession`, `miembrosGate`, `rolesAdminGate` | miembros | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/members/{id}/compensations` | `s.requireBOSession`, `miembrosGate`, `rolesAdminGate` | miembros | read | `members_read(compensations_list)` | `admin_call` (lectura) |
| `POST` | `/members/{id}/compensations` | `s.requireBOSession`, `miembrosGate`, `rolesAdminGate` | miembros | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `DELETE` | `/members/{id}/compensations/{compensationId}` | `s.requireBOSession`, `miembrosGate`, `rolesAdminGate` | miembros | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PATCH` | `/members/{id}/compensations/{compensationId}` | `s.requireBOSession`, `miembrosGate`, `rolesAdminGate` | miembros | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/members/{id}/ensure-user` | `s.requireBOSession`, `miembrosGate`, `rolesAdminGate` | miembros | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/members/{id}/invitation/resend` | `s.requireBOSession`, `miembrosGate`, `rolesAdminGate` | miembros | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — envío real de correo — fuera de alcance |
| `POST` | `/members/{id}/password-reset/send` | `s.requireBOSession`, `miembrosGate`, `rolesAdminGate` | miembros | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — envío real de correo — fuera de alcance |
| `PUT` | `/members/{id}/phone` | `s.requireBOSession`, `miembrosGate`, `rolesAdminGate` | miembros | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/members/{id}/phone/verification/confirm` | `s.requireBOSession`, `miembrosGate`, `rolesAdminGate` | miembros | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — verificación de identidad — fuera de alcance |
| `POST` | `/members/{id}/phone/verification/send` | `s.requireBOSession`, `miembrosGate`, `rolesAdminGate` | miembros | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — envío real de WhatsApp — fuera de alcance |
| `GET` | `/members/{id}/stats` | `s.requireBOSession`, `miembrosGate`, `rolesAdminGate` | miembros | read | `members_read(stats)` | `admin_call` (lectura) |
| `GET` | `/members/{id}/stats-range` | `s.requireBOSession`, `miembrosGate`, `rolesAdminGate` | miembros | read | `members_read(stats_range)` | `admin_call` (lectura) |
| `GET` | `/members/{id}/stats-year` | `s.requireBOSession`, `miembrosGate`, `rolesAdminGate` | miembros | read | `members_read(stats_year)` | `admin_call` (lectura) |
| `GET` | `/members/{id}/table-data` | `s.requireBOSession`, `miembrosGate`, `rolesAdminGate` | miembros | read | `members_read(table_data)` | `admin_call` (lectura) |
| `GET` | `/members/{id}/time-balance` | `s.requireBOSession`, `miembrosGate`, `rolesAdminGate` | miembros | read | `member_balance_get` | `member_balance_get` |
| `POST` | `/members/{id}/whatsapp/verification/confirm` | `s.requireBOSession`, `miembrosGate`, `rolesAdminGate` | miembros | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — verificación de identidad — fuera de alcance |
| `POST` | `/members/{id}/whatsapp/verification/send` | `s.requireBOSession`, `miembrosGate`, `rolesAdminGate` | miembros | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — envío real de WhatsApp — fuera de alcance |
| `GET` | `/menu-visibility` | `s.requireBOSession`, `menusGate` | menus | read | `menu_visibility_list` | EXCLUIDO — GET con escritura (marcado `write` en el mapa) |
| `POST` | `/menu-visibility` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/menus/dia` | `s.requireBOSession`, `menusGate` | menus | read | `menus_list` | `menus_list` |
| `POST` | `/menus/dia/dishes` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `DELETE` | `/menus/dia/dishes/{id}` | `s.requireBOSession`, `menusGate` | menus | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PATCH` | `/menus/dia/dishes/{id}` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/menus/dia/price` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/menus/finde` | `s.requireBOSession`, `menusGate` | menus | read | `menus_list` | `menus_list` |
| `POST` | `/menus/finde/dishes` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `DELETE` | `/menus/finde/dishes/{id}` | `s.requireBOSession`, `menusGate` | menus | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PATCH` | `/menus/finde/dishes/{id}` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/menus/finde/price` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/menus/selector` | `s.requireBOSession`, `menusGate` | menus | read | `menus_read(menu_selector)` | `admin_call` (lectura) |
| `GET` | `/platform/dashboard` | `s.requireBOSession`, `s.requireBOSuperadmin` | superadmin | read | `admin_call` | `admin_call` (lectura) |
| `GET` | `/platform/domains` | `s.requireBOSession`, `s.requireBOSuperadmin` | superadmin | read | `admin_call` | `admin_call` (lectura) |
| `GET` | `/platform/restaurants` | `s.requireBOSession`, `s.requireBOSuperadmin` | superadmin | read | `admin_call` | `admin_call` (lectura) |
| `POST` | `/platform/restaurants` | `s.requireBOSession`, `s.requireBOSuperadmin` | superadmin | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PATCH` | `/platform/restaurants/{id}` | `s.requireBOSession`, `s.requireBOSuperadmin` | superadmin | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/platform/restaurants/{id}/activate` | `s.requireBOSession`, `s.requireBOSuperadmin` | superadmin | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — gestión de tenants (superadmin) — fuera de alcance |
| `POST` | `/platform/restaurants/{id}/deactivate` | `s.requireBOSession`, `s.requireBOSuperadmin` | superadmin | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — gestión de tenants (superadmin) — fuera de alcance |
| `GET` | `/platform/stripe-connect` | `s.requireBOSession`, `s.requireBOSuperadmin` | superadmin | read | `admin_call` | `admin_call` (lectura) |
| `PUT` | `/platform/stripe-connect/restaurants/{id}/fee` | `s.requireBOSession`, `s.requireBOSuperadmin` | superadmin | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PUT` | `/platform/stripe-connect/settings` | `s.requireBOSession`, `s.requireBOSuperadmin` | superadmin | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/platform/stripe/payments` | `s.requireBOSession`, `s.requireBOSuperadmin` | superadmin | read | `admin_call` | `admin_call` (lectura) |
| `POST` | `/platform/stripe/refund` | `s.requireBOSession`, `s.requireBOSuperadmin` | superadmin | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — devolución de dinero — fuera de alcance |
| `GET` | `/platform/subscriptions` | `s.requireBOSession`, `s.requireBOSuperadmin` | superadmin | read | `admin_call` | `admin_call` (lectura) |
| `POST` | `/platform/subscriptions/{id}/toggle` | `s.requireBOSession`, `s.requireBOSuperadmin` | superadmin | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — gestión de tenants (superadmin) — fuera de alcance |
| `GET` | `/platform/uazapi-servers` | `s.requireBOSession`, `s.requireBOSuperadmin` | superadmin | read | `admin_call` | `admin_call` (lectura) |
| `GET` | `/platform/users` | `s.requireBOSession`, `s.requireBOSuperadmin` | superadmin | read | `admin_call` | EXCLUIDO — gestión de cuentas del panel |
| `POST` | `/platform/users` | `s.requireBOSession`, `s.requireBOSuperadmin` | superadmin | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — gestión de cuentas del panel |
| `PATCH` | `/platform/users/{id}` | `s.requireBOSession`, `s.requireBOSuperadmin` | superadmin | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — gestión de cuentas del panel |
| `POST` | `/platform/users/{id}/assign` | `s.requireBOSession`, `s.requireBOSuperadmin` | superadmin | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — gestión de cuentas del panel |
| `DELETE` | `/platform/users/{id}/assign/{restaurantId}` | `s.requireBOSession`, `s.requireBOSuperadmin` | superadmin | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/platform/users/{id}/password` | `s.requireBOSession`, `s.requireBOSuperadmin` | superadmin | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — gestión de cuentas del panel |
| `POST` | `/platform/users/{id}/revoke-sessions` | `s.requireBOSession`, `s.requireBOSuperadmin` | superadmin | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — gestión de cuentas del panel |
| `GET` | `/platform/whatsapp` | `s.requireBOSession`, `s.requireBOSuperadmin` | superadmin | read | `admin_call` | `admin_call` (lectura) |
| `POST` | `/platform/whatsapp/{id}/disconnect` | `s.requireBOSession`, `s.requireBOSuperadmin` | superadmin | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — gestión de tenants (superadmin) — fuera de alcance |
| `POST` | `/platform/whatsapp/{id}/renew-qr` | `s.requireBOSession`, `s.requireBOSuperadmin` | superadmin | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — gestión de tenants (superadmin) — fuera de alcance |
| `GET` | `/platos` | `s.requireBOSession`, `menusGate` | menus | read | `admin_call` | `admin_call` (lectura) |
| `POST` | `/platos` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `DELETE` | `/platos/{id}` | `s.requireBOSession`, `menusGate` | menus | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PATCH` | `/platos/{id}` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/platos/{id}/toggle` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/pos/accounting/export.csv` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posReportsGate` | pos | read | `admin_call` | EXCLUIDO — GET con escritura (marcado `write` en el mapa) |
| `POST` | `/pos/activation-acceptances` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posSettingsGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/pos/activation-readiness` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posSettingsGate` | pos | read | `admin_call` | `admin_call` (lectura) |
| `GET` | `/pos/bootstrap` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posViewGate` | pos | read | `admin_call` | `admin_call` (lectura) |
| `GET` | `/pos/cash-days` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posViewGate` | pos | read | `pos_read(cash_days_range)` | `admin_call` (lectura) |
| `POST` | `/pos/cash-days` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posShiftGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/pos/cash-days/current` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posViewGate` | pos | read | `pos_read(cash_day_current)` | `admin_call` (lectura) |
| `GET` | `/pos/cash-days/{date}/billing` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posViewGate` | pos | read | `admin_call` | `admin_call` (lectura) |
| `POST` | `/pos/cash-days/{date}/bulk-checkout` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posCheckoutGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/pos/cash-days/{date}/tables` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posViewGate` | pos | read | `admin_call` | `admin_call` (lectura) |
| `POST` | `/pos/cash-days/{id}/close` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posShiftGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/pos/cash/closures` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posReportsGate` | pos | read | `pos_cash_closures_list` | `admin_call` (lectura) |
| `POST` | `/pos/cash/closures` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posShiftGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/pos/cash/closures/{id}` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posReportsGate` | pos | read | `pos_read(cash_closure_get)` | `admin_call` (lectura) |
| `GET` | `/pos/cash/movements` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posReportsGate` | pos | read | `pos_read(cash_movements)` | `admin_call` (lectura) |
| `POST` | `/pos/cash/movements` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posCheckoutGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/pos/cash/summary` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posReportsGate` | pos | read | `pos_cash_summary` | `admin_call` (lectura) |
| `GET` | `/pos/categories` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posViewGate` | pos | read | `admin_call` | `admin_call` (lectura) |
| `POST` | `/pos/categories` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posCatalogGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `DELETE` | `/pos/categories/{id}` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posCatalogGate` | pos | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PATCH` | `/pos/categories/{id}` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posCatalogGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/pos/covers` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posReportsGate` | pos | read | `pos_read(covers_report)` | `admin_call` (lectura) |
| `POST` | `/pos/covers/adjustments` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posCoversAdjustGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/pos/covers/reconciliation` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posReportsGate` | pos | read | `admin_call` | `admin_call` (lectura) |
| `POST` | `/pos/covers/reconciliation/rebuild` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posSettingsGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/pos/customers` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posSellGate` | pos | read | `admin_call` | `admin_call` (lectura) |
| `POST` | `/pos/customers` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posSellGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/pos/customers/{id}` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posSellGate` | pos | read | `admin_call` | `admin_call` (lectura) |
| `PATCH` | `/pos/customers/{id}` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posSellGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/pos/customers/{id}/anonymise` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posSettingsGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/pos/drawer/open` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posCheckoutGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/pos/fiscal/series` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posReportsGate` | pos | read | `admin_call` | `admin_call` (lectura) |
| `GET` | `/pos/fiscal/series/{seriesId}/verify` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posReportsGate` | pos | read | `admin_call` | `admin_call` (lectura) |
| `GET` | `/pos/health` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posSettingsGate` | pos | read | `admin_call` | `admin_call` (lectura) |
| `POST` | `/pos/kitchen/dispatches/{id}/status` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posKitchenGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/pos/kitchen/queue` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posViewGate` | pos | read | `pos_read(kitchen_queue)` | `admin_call` (lectura) |
| `GET` | `/pos/kitchen/routes` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posViewGate` | pos | read | `admin_call` | `admin_call` (lectura) |
| `POST` | `/pos/kitchen/routes` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posKitchenGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `DELETE` | `/pos/kitchen/routes/{id}` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posKitchenGate` | pos | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/pos/kitchen/stations` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posViewGate` | pos | read | `admin_call` | `admin_call` (lectura) |
| `POST` | `/pos/kitchen/stations` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posKitchenGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PATCH` | `/pos/kitchen/stations/{id}` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posKitchenGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/pos/modifier-groups` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posViewGate` | pos | read | `admin_call` | `admin_call` (lectura) |
| `POST` | `/pos/modifier-groups` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posCatalogGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/pos/modifier-groups/{groupId}/options` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posCatalogGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `DELETE` | `/pos/modifier-groups/{id}` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posCatalogGate` | pos | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PATCH` | `/pos/modifier-groups/{id}` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posCatalogGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `DELETE` | `/pos/modifier-options/{id}` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posCatalogGate` | pos | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PATCH` | `/pos/modifier-options/{id}` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posCatalogGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `DELETE` | `/pos/pack-components/{id}` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posCatalogGate` | pos | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PATCH` | `/pos/pack-components/{id}` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posCatalogGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/pos/packs` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posViewGate` | pos | read | `admin_call` | `admin_call` (lectura) |
| `POST` | `/pos/packs` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posCatalogGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `DELETE` | `/pos/packs/{id}` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posCatalogGate` | pos | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PATCH` | `/pos/packs/{id}` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posCatalogGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/pos/packs/{packId}/components` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posCatalogGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/pos/pin` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posSellGate` | pos | read | `admin_call` | `admin_call` (lectura) |
| `POST` | `/pos/pin` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posSellGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/pos/pin/verify` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posSellGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/pos/products` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posViewGate` | pos | read | `pos_read(products_list)`<br>`catalog_list(pos_products)` | `admin_call` (lectura) |
| `POST` | `/pos/products` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posCatalogGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `DELETE` | `/pos/products/{id}` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posCatalogGate` | pos | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/pos/products/{id}` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posViewGate` | pos | read | `admin_call` | `admin_call` (lectura) |
| `PATCH` | `/pos/products/{id}` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posCatalogGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/pos/products/{id}/stock-rules` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posViewGate` | pos | read | `admin_call` | `admin_call` (lectura) |
| `PUT` | `/pos/products/{id}/stock-rules` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posStockMappingGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/pos/products/{productId}/modifier-groups` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posViewGate` | pos | read | `admin_call` | `admin_call` (lectura) |
| `PUT` | `/pos/products/{productId}/modifier-groups` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posCatalogGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/pos/reports/card-reconciliation` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posReportsGate` | pos | read | `admin_call` | `admin_call` (lectura) |
| `GET` | `/pos/reports/sales` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posReportsGate` | pos | read | `pos_read(sales_report)` | `admin_call` (lectura) |
| `GET` | `/pos/reports/sales.csv` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posReportsGate` | pos | read | `admin_call` | `admin_call` (lectura) |
| `GET` | `/pos/reports/stock` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posReportsGate` | pos | read | `admin_call` | `admin_call` (lectura) |
| `GET` | `/pos/reservations/eligible` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posViewGate` | pos | read | `admin_call` | `admin_call` (lectura) |
| `GET` | `/pos/reservations/{bookingId}/visit` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posViewGate` | pos | read | `admin_call` | `admin_call` (lectura) |
| `GET` | `/pos/roles/{slug}/permissions` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posSettingsGate` | pos | read | `admin_call` | `admin_call` (lectura) |
| `PUT` | `/pos/roles/{slug}/permissions` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posSettingsGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/pos/service-periods` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posViewGate` | pos | read | `pos_read(service_periods)` | `admin_call` (lectura) |
| `POST` | `/pos/service-periods` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posSettingsGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `DELETE` | `/pos/service-periods/{id}` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posSettingsGate` | pos | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PATCH` | `/pos/service-periods/{id}` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posSettingsGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/pos/settings` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posViewGate` | pos | read | `admin_call` | `admin_call` (lectura) |
| `PATCH` | `/pos/settings` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posSettingsGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/pos/shifts/current` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posViewGate` | pos | read | `pos_read(shift_current)` | `admin_call` (lectura) |
| `POST` | `/pos/shifts/open` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posShiftGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/pos/shifts/{id}/close` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posShiftGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/pos/stock-anomalies` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posStockMappingGate` | pos | read | `admin_call` | `admin_call` (lectura) |
| `POST` | `/pos/stock-anomalies/{id}/resolve` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posStockMappingGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/pos/stock-exceptions` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posStockMappingGate` | pos | read | `admin_call` | `admin_call` (lectura) |
| `POST` | `/pos/stock-exceptions/replay` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posStockMappingGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/pos/stock-readiness` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posStockMappingGate` | pos | read | `admin_call` | `admin_call` (lectura) |
| `GET` | `/pos/tags` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posViewGate` | pos | read | `admin_call` | `admin_call` (lectura) |
| `POST` | `/pos/tags` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posCatalogGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/pos/tickets` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posViewGate` | pos | read | `pos_tickets_list` | `pos_tickets_list` |
| `GET` | `/pos/tickets/hourly` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `s.requireBOPOSViewOrFichajeAdmin` | pos | read | `pos_read(tickets_hourly)` | `admin_call` (lectura) |
| `GET` | `/pos/tickets/series` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `s.requireBOPOSViewOrFichajeAdmin` | pos | read | `pos_read(tickets_series)` | `admin_call` (lectura) |
| `GET` | `/pos/tickets/{id}` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posViewGate` | pos | read | `admin_call` | `admin_call` (lectura) |
| `POST` | `/pos/tickets/{id}/adjustments` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posDiscountGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/pos/tickets/{id}/checkout` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posCheckoutGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/pos/tickets/{id}/courses` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posViewGate` | pos | read | `admin_call` | `admin_call` (lectura) |
| `POST` | `/pos/tickets/{id}/courses/fire` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posKitchenGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/pos/tickets/{id}/customer` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posSellGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/pos/tickets/{id}/discount` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posDiscountGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/pos/tickets/{id}/fiscal-document` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posRefundGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/pos/tickets/{id}/guest-label` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posSellGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/pos/tickets/{id}/kitchen-dispatches` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posSellGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/pos/tickets/{id}/lines` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posSellGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PATCH` | `/pos/tickets/{id}/lines/{lineId}` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posSellGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/pos/tickets/{id}/lines/{lineId}/comp` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posDiscountGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/pos/tickets/{id}/lines/{lineId}/move` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posSellGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/pos/tickets/{id}/lines/{lineId}/tags` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posSellGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/pos/tickets/{id}/lines/{lineId}/void` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posLineVoidGate` | pos | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PATCH` | `/pos/tickets/{id}/operator` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posSellGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/pos/tickets/{id}/recall` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posSellGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/pos/tickets/{id}/refunds` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posRefundGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/pos/tickets/{id}/tags` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posSellGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/pos/tickets/{id}/void` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posSellGate` | pos | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/pos/visits` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posViewGate` | pos | read | `pos_visits_list` | `pos_visits_list` |
| `POST` | `/pos/visits` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posSellGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/pos/visits/{id}` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posViewGate` | pos | read | `admin_call` | `admin_call` (lectura) |
| `PATCH` | `/pos/visits/{id}` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posVisitManageGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/pos/visits/{id}/cancel` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posVisitManageGate` | pos | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/pos/visits/{id}/close` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posCheckoutGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PATCH` | `/pos/visits/{id}/customer` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posSellGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/pos/visits/{id}/merge` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posVisitManageGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/pos/visits/{id}/park` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posVisitManageGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/pos/visits/{id}/tickets` | `s.requireBOSession`, `s.requireBOPOSFeature`, `withBOPOSTimeout`, `posSellGate` | pos | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/postres` | `s.requireBOSession`, `menusGate` | menus | read | `catalog_list(postres)` | `catalog_list(postres)` |
| `POST` | `/postres` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `DELETE` | `/postres/{id}` | `s.requireBOSession`, `menusGate` | menus | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PATCH` | `/postres/{id}` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/restaurant/pages/visibility` | `s.requireBOSession`, `menusGate` | menus | read | `admin_call` | `admin_call` (lectura) |
| `PATCH` | `/restaurant/pages/visibility` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/roles` | `s.requireBOSession`, `miembrosGate`, `rolesAdminGate` | miembros | read | `admin_call` | EXCLUIDO — gestión de cuentas del panel |
| `POST` | `/roles` | `s.requireBOSession`, `miembrosGate`, `rolesAdminGate` | miembros | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — gestión de cuentas del panel |
| `POST` | `/site-builder/billing/checkout` | `s.requireBOSession`, `ajustesGate` | ajustes | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — COBRO real (Stripe) — fuera de alcance |
| `POST` | `/site-builder/domains/provision` | `s.requireBOSession`, `ajustesGate` | ajustes | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — COBRO real (dominio) — fuera de alcance |
| `POST` | `/site-builder/domains/register` | `s.requireBOSession`, `ajustesGate` | ajustes | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — COBRO real (dominio) — fuera de alcance |
| `GET` | `/site-builder/domains/search` | `s.requireBOSession`, `ajustesGate` | ajustes | read | `admin_call` | `admin_call` (lectura) |
| `PUT` | `/stock/affluence` | `s.requireBOSession`, `withBOStockTimeout`, `stockForecastGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/stock/ai/recommendations` | `s.requireBOSession`, `stockForecastGate`, `stockCostsViewGate`, `s.requireBOStockAI` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — llamada a IA externa (coste) — se deja solo al panel |
| `GET` | `/stock/categories` | `s.requireBOSession`, `withBOStockTimeout`, `stockViewGate` | stock | read | `stock_categories_list` | `stock_categories_list` |
| `POST` | `/stock/categories` | `s.requireBOSession`, `withBOStockTimeout`, `stockItemsGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `DELETE` | `/stock/categories/{id}` | `s.requireBOSession`, `withBOStockTimeout`, `stockItemsGate` | stock | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PATCH` | `/stock/categories/{id}` | `s.requireBOSession`, `withBOStockTimeout`, `stockItemsGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/stock/costing` | `s.requireBOSession`, `withBOStockTimeout`, `stockCostsViewGate` | stock | read | `stock_read(costing)` | `admin_call` (lectura) |
| `POST` | `/stock/counts` | `s.requireBOSession`, `withBOStockTimeout`, `stockCountPerformGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/stock/counts/{id}` | `s.requireBOSession`, `withBOStockTimeout`, `stockViewGate` | stock | read | `admin_call` | `admin_call` (lectura) |
| `POST` | `/stock/counts/{id}/close` | `s.requireBOSession`, `withBOStockTimeout`, `stockCountCloseGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/stock/documents` | `s.requireBOSession`, `stockOCRUploadGate` | stock | read | `stock_read(documents_list)` | EXCLUIDO — OCR document_get RETENIDO (superficie prompt-injection) |
| `POST` | `/stock/documents/extract-text` | `s.requireBOSession`, `stockOCRUploadGate`, `s.requireBOStockAI` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/stock/documents/{id}` | `s.requireBOSession`, `stockOCRUploadGate` | stock | read | `admin_call` | EXCLUIDO — OCR document_get RETENIDO (superficie prompt-injection) |
| `POST` | `/stock/documents/{id}/confirm-invoice` | `s.requireBOSession`, `stockOCRConfirmGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — OCR document_get RETENIDO (superficie prompt-injection) |
| `POST` | `/stock/documents/{id}/confirm-recipe` | `s.requireBOSession`, `stockOCRConfirmGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — OCR document_get RETENIDO (superficie prompt-injection) |
| `DELETE` | `/stock/documents/{id}/original` | `s.requireBOSession`, `stockOCRConfirmGate` | stock | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — OCR document_get RETENIDO (superficie prompt-injection) |
| `GET` | `/stock/documents/{id}/original` | `s.requireBOSession`, `stockOCRUploadGate` | stock | read | `admin_call` | EXCLUIDO — OCR document_get RETENIDO (superficie prompt-injection) |
| `POST` | `/stock/documents/{id}/reject` | `s.requireBOSession`, `stockOCRUploadGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — OCR document_get RETENIDO (superficie prompt-injection) |
| `PATCH` | `/stock/documents/{id}/review` | `s.requireBOSession`, `stockOCRUploadGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — OCR document_get RETENIDO (superficie prompt-injection) |
| `GET` | `/stock/expiring` | `s.requireBOSession`, `withBOStockTimeout`, `stockViewGate` | stock | read | `stock_read(expiring)` | `admin_call` (lectura) |
| `GET` | `/stock/export` | `s.requireBOSession`, `withBOStockTimeout`, `stockViewGate` | stock | read | `admin_call` | `admin_call` (lectura) |
| `GET` | `/stock/forecast` | `s.requireBOSession`, `withBOStockTimeout`, `stockForecastGate` | stock | read | `stock_read(forecast)` | `admin_call` (lectura) |
| `GET` | `/stock/item-options` | `s.requireBOSession`, `withBOStockTimeout`, `stockViewGate` | stock | read | `stock_read(item_options)` | `admin_call` (lectura) |
| `GET` | `/stock/items` | `s.requireBOSession`, `withBOStockTimeout`, `stockViewGate` | stock | read | `stock_items_list`<br>`catalog_list(stock_items)` | `stock_items_list`<br>`catalog_list(stock_items)` |
| `POST` | `/stock/items` | `s.requireBOSession`, `withBOStockTimeout`, `stockItemsGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `DELETE` | `/stock/items/{id}` | `s.requireBOSession`, `withBOStockTimeout`, `stockItemsGate` | stock | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PATCH` | `/stock/items/{id}` | `s.requireBOSession`, `withBOStockTimeout`, `stockItemsGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/stock/items/{id}/movements` | `s.requireBOSession`, `withBOStockTimeout`, `stockViewGate` | stock | read | `stock_item_movements_list` | `admin_call` (lectura) |
| `POST` | `/stock/items/{id}/movements` | `s.requireBOSession`, `withBOStockTimeout`, `stockVersionGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/stock/items/{id}/prices` | `s.requireBOSession`, `withBOStockTimeout`, `stockCostsManageGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PATCH` | `/stock/items/{id}/targets` | `s.requireBOSession`, `withBOStockTimeout`, `stockItemsGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/stock/items/{id}/units` | `s.requireBOSession`, `withBOStockTimeout`, `stockViewGate` | stock | read | `stock_read(item_units)` | `admin_call` (lectura) |
| `POST` | `/stock/items/{id}/units` | `s.requireBOSession`, `withBOStockTimeout`, `stockItemsGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `DELETE` | `/stock/items/{id}/units/{unitId}` | `s.requireBOSession`, `withBOStockTimeout`, `stockItemsGate` | stock | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/stock/labour-members` | `s.requireBOSession`, `withBOStockTimeout`, `stockCostsManageGate` | stock | read | `stock_read(labour_members)` | `admin_call` (lectura) |
| `GET` | `/stock/margin-scopes` | `s.requireBOSession`, `withBOStockTimeout`, `stockCostsViewGate` | stock | read | `stock_read(margin_scopes)` | `admin_call` (lectura) |
| `PUT` | `/stock/margin-scopes` | `s.requireBOSession`, `withBOStockTimeout`, `stockCostsManageGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/stock/margin-scopes/defaults` | `s.requireBOSession`, `withBOStockTimeout`, `stockCostsViewGate` | stock | read | `stock_read(margin_scope_defaults)` | `admin_call` (lectura) |
| `GET` | `/stock/margin-scopes/resolve` | `s.requireBOSession`, `withBOStockTimeout`, `stockCostsViewGate` | stock | read | `stock_read(margin_scope_resolve)` | `admin_call` (lectura) |
| `GET` | `/stock/margin-scopes/targets` | `s.requireBOSession`, `withBOStockTimeout`, `stockCostsViewGate` | stock | read | `stock_read(margin_targets)` | `admin_call` (lectura) |
| `DELETE` | `/stock/margin-scopes/{id}` | `s.requireBOSession`, `withBOStockTimeout`, `stockCostsManageGate` | stock | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/stock/permissions/mine` | `s.requireBOSession`, `withBOStockTimeout` | session | read | `stock_read(permissions_mine)` | `admin_call` (lectura) |
| `GET` | `/stock/production-labour/entries` | `s.requireBOSession`, `withBOStockTimeout`, `stockCostsViewGate`, `rolesAdminGate` | stock | read | `stock_read(production_labour_entries)` | `admin_call` (lectura) |
| `GET` | `/stock/production-orders` | `s.requireBOSession`, `withBOStockTimeout`, `stockCostsViewGate`, `rolesAdminGate` | stock | read | `production_list` | `admin_call` (lectura) |
| `GET` | `/stock/production-orders/{id}/labour` | `s.requireBOSession`, `withBOStockTimeout`, `stockCostsViewGate`, `rolesAdminGate` | stock | read | `stock_read(production_labour_list)` | `admin_call` (lectura) |
| `POST` | `/stock/production-orders/{id}/labour` | `s.requireBOSession`, `withBOStockTimeout`, `stockCostsManageGate`, `rolesAdminGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `DELETE` | `/stock/production-orders/{id}/labour/{allocationId}` | `s.requireBOSession`, `withBOStockTimeout`, `stockCostsManageGate`, `rolesAdminGate` | stock | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/stock/recipes` | `s.requireBOSession`, `withBOStockTimeout`, `stockRecipesViewGate` | stock | read | `recipes_list` | `admin_call` (lectura) |
| `POST` | `/stock/recipes` | `s.requireBOSession`, `withBOStockTimeout`, `stockRecipesManageGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `DELETE` | `/stock/recipes/{id}` | `s.requireBOSession`, `withBOStockTimeout`, `stockRecipesManageGate` | stock | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/stock/recipes/{id}` | `s.requireBOSession`, `withBOStockTimeout`, `stockRecipesViewGate` | stock | read | `stock_read(recipe_get)` | `admin_call` (lectura) |
| `PATCH` | `/stock/recipes/{id}` | `s.requireBOSession`, `withBOStockTimeout`, `stockRecipesManageGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PATCH` | `/stock/recipes/{id}/pricing` | `s.requireBOSession`, `withBOStockTimeout`, `stockCostsManageGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/stock/recipes/{id}/production` | `s.requireBOSession`, `withBOStockTimeout`, `stockProductionGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/stock/recipes/{id}/production/preview` | `s.requireBOSession`, `withBOStockTimeout`, `stockRecipesViewGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/stock/reconciliation` | `s.requireBOSession`, `withBOStockTimeout`, `stockViewGate` | stock | read | `stock_read(reconciliation)` | `admin_call` (lectura) |
| `POST` | `/stock/reconciliation/rebuild` | `s.requireBOSession`, `withBOStockTimeout`, `stockSettingsGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/stock/roles/{slug}/permissions` | `s.requireBOSession`, `withBOStockTimeout`, `stockSettingsGate` | stock | read | `admin_call` | `admin_call` (lectura) |
| `PUT` | `/stock/roles/{slug}/permissions` | `s.requireBOSession`, `withBOStockTimeout`, `stockSettingsGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/stock/settings` | `s.requireBOSession`, `withBOStockTimeout`, `stockViewGate` | stock | read | `stock_read(settings)` | `admin_call` (lectura) |
| `PATCH` | `/stock/settings` | `s.requireBOSession`, `withBOStockTimeout`, `stockSettingsGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/stock/settings/classify-seasonality` | `s.requireBOSession`, `stockSettingsGate`, `s.requireBOStockAI` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — llamada a IA externa (coste) — se deja solo al panel |
| `GET` | `/stock/summary` | `s.requireBOSession`, `withBOStockTimeout`, `stockViewGate` | stock | read | `stock_summary` | `stock_summary` |
| `GET` | `/stock/suppliers` | `s.requireBOSession`, `withBOStockTimeout`, `stockViewGate` | stock | read | `stock_read(suppliers_list)` | `admin_call` (lectura) |
| `POST` | `/stock/suppliers` | `s.requireBOSession`, `withBOStockTimeout`, `stockItemsGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `DELETE` | `/stock/suppliers/{id}` | `s.requireBOSession`, `withBOStockTimeout`, `stockItemsGate` | stock | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PATCH` | `/stock/suppliers/{id}` | `s.requireBOSession`, `withBOStockTimeout`, `stockItemsGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/stock/suppliers/{id}/aliases` | `s.requireBOSession`, `withBOStockTimeout`, `stockViewGate` | stock | read | `stock_read(supplier_aliases)` | `admin_call` (lectura) |
| `PUT` | `/stock/suppliers/{id}/aliases` | `s.requireBOSession`, `withBOStockTimeout`, `stockItemsGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/stock/suppliers/{id}/prices` | `s.requireBOSession`, `withBOStockTimeout`, `stockViewGate` | stock | read | `stock_read(supplier_prices)` | `admin_call` (lectura) |
| `POST` | `/stock/transfers` | `s.requireBOSession`, `withBOStockTimeout`, `stockTransferGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/stock/valuation` | `s.requireBOSession`, `withBOStockTimeout`, `stockViewGate` | stock | read | `stock_read(valuation)` | `admin_call` (lectura) |
| `GET` | `/stock/vat-rates` | `s.requireBOSession`, `withBOStockTimeout`, `stockCostsViewGate` | stock | read | `stock_read(vat_list)` | `admin_call` (lectura) |
| `POST` | `/stock/vat-rates` | `s.requireBOSession`, `withBOStockTimeout`, `stockCostsManageGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `DELETE` | `/stock/vat-rates/{id}` | `s.requireBOSession`, `withBOStockTimeout`, `stockCostsManageGate` | stock | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PATCH` | `/stock/vat-rates/{id}` | `s.requireBOSession`, `withBOStockTimeout`, `stockCostsManageGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/stock/warehouses` | `s.requireBOSession`, `withBOStockTimeout`, `stockViewGate` | stock | read | `stock_warehouses_list` | EXCLUIDO — GET con escritura (marcado `write` en el mapa) |
| `POST` | `/stock/warehouses` | `s.requireBOSession`, `withBOStockTimeout`, `stockWarehousesGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `DELETE` | `/stock/warehouses/{id}` | `s.requireBOSession`, `withBOStockTimeout`, `stockWarehousesGate` | stock | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PATCH` | `/stock/warehouses/{id}` | `s.requireBOSession`, `withBOStockTimeout`, `stockWarehousesGate` | stock | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/tables` | `s.requireBOSession`, `reservasGate` | reservas | read | `tables_list` | `list_tables` |
| `POST` | `/tables` | `s.requireBOSession`, `reservasGate` | reservas | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PUT` | `/tables` | `s.requireBOSession`, `reservasGate` | reservas | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `DELETE` | `/tables/template/{floorNumber}` | `s.requireBOSession`, `reservasGate` | reservas | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/tables/template/{floorNumber}` | `s.requireBOSession`, `reservasGate` | reservas | read | `admin_call` | `admin_call` (lectura) |
| `POST` | `/tables/template/{floorNumber}` | `s.requireBOSession`, `reservasGate` | reservas | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `DELETE` | `/tables/{id}` | `s.requireBOSession`, `reservasGate` | reservas | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PATCH` | `/users/{id}/role` | `s.requireBOSession`, `miembrosGate`, `rolesAdminGate` | miembros | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — gestión de cuentas del panel |
| `PATCH` | `/users/{id}/version` | `s.requireBOSession`, `miembrosGate`, `rootOnlyGate` | miembros | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — gestión de cuentas del panel |
| `GET` | `/vinos` | `s.requireBOSession`, `menusGate` | menus | read | `catalog_list(vinos)` | `catalog_list(vinos)` |
| `POST` | `/vinos` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `DELETE` | `/vinos/{id}` | `s.requireBOSession`, `menusGate` | menus | destructive | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/vinos/{id}` | `s.requireBOSession`, `menusGate` | menus | read | `admin_call` | `admin_call` (lectura) |
| `PATCH` | `/vinos/{id}` | `s.requireBOSession`, `menusGate` | menus | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/website` | `s.requireBOSession`, `ajustesGate` | ajustes | read | `website_read(premium_website)` | `admin_call` (lectura) |
| `POST` | `/website` | `s.requireBOSession`, `ajustesGate` | ajustes | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `PUT` | `/website` | `s.requireBOSession`, `ajustesGate` | ajustes | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `POST` | `/website/ai-generate` | `s.requireBOSession`, `ajustesGate` | ajustes | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | EXCLUIDO — llamada a IA externa (coste) — se deja solo al panel |
| `GET` | `/website/menu-templates` | `s.requireBOSession`, `ajustesGate` | ajustes | read | `website_read(menu_templates)` | `admin_call` (lectura) |
| `PUT` | `/website/menu-templates` | `s.requireBOSession`, `ajustesGate` | ajustes | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |
| `GET` | `/website/templates` | `s.requireBOSession`, `ajustesGate` | ajustes | read | `website_read(website_templates)` | `admin_call` (lectura) |
| `GET` | `/widget/settings` | `s.requireBOSession`, `reservasGate` | reservas | read | `website_read(widget_settings)` | `admin_call` (lectura) |
| `PUT` | `/widget/settings` | `s.requireBOSession`, `reservasGate` | reservas | write | `admin_call`<br>*(escritura: `confirmed=true` + `confirmation_token`)* | `manage_backoffice` (propose→OK) |

---

## 6. Matriz B — rutas `/admin` fuera del mapa generado

El generador (`cmd/forky-admin-map`) excluye los *handlers* cuyo nombre coincide con
`WS$|WebSocket|Upload|Image|Avatar|Import|OCR`, las rutas `/assistant/*` y las que acaban en `/ws`.
Estas rutas **no** son alcanzables por `admin_call` hoy: son parte de los huecos del §9.

| Método | Ruta | Handler | Motivo de la exclusión del mapa | Cobertura |
|---|---|---|---|---|
| `GET` | `/admin/assistant/ws` | `handleBOAssistantWS` | `/assistant/*` (es el propio chat de Forky) | n/a (es la superficie) |
| `POST` | `/admin/assistant/media/upload` | `handleBOAssistantMediaUpload` | `/assistant/*` | n/a |
| `POST` | `/admin/assistant/transcribe` | `handleBOAssistantTranscribe` | `/assistant/*` | n/a |
| `GET` | `/admin/ws` | `handleBOGlobalWS` | socket | solo difunde eventos (`special_date`) |
| `GET` | `/admin/site-builder/ws` | `handleBOSiteBuilderWS` | socket | 18 métodos → 18 operaciones REST del §5.2 |
| `GET` | `/admin/tables/ws` | `handleBOPremiumTablesWS` | socket | `table_edit`, `layout_edit`, `template_edit`, `template_delete` (escritura) + `sync`/`refresh` |
| `GET` | `/admin/reservas/ws` | `handleBOReservasColumnsWS` | socket | solo difunde `reservas_columns` |
| `GET` | `/admin/fichaje/ws` | `handleBOFichajeWS` | socket | `ad_save` (escritura), `ad_schedule_check`, `join_restaurant` |
| `GET` | `/admin/vinos/ws` | `handleBOVinosAIWS` | socket | `sync`/`refresh` (la generación de IA es el POST `/vinos/{id}/image/ai`, fuera del mapa) |
| `GET` | `/admin/comida/ws` | `handleBOComidaAIWS` | socket | sin métodos de entrada (difunde el progreso de la IA) |
| `GET` | `/admin/comida/technical-sheets/ws` | `handleBOTechnicalSheetsWS` | socket | `search` (búsqueda de fichas) |
| `GET` | `/admin/group-menus-v2/ws` | `handleBOGroupMenusV2AIWS` | socket | `beverage_set/create/delete`, `weekday_set`, `editor_preview_set`, `special_section_image_upload` (escritura) + lecturas |
| `GET` | `/admin/bookings/documents/ws` | `handleBOBookingDocumentsWS` | socket | `bookingDocumentUpload/List/Delete` (subida de ficheros) |
| `GET` | `/admin/members/whatsapp/ws` | `handleBOMembersWhatsAppWS` | socket | solo difunde el estado de la conexión |
| `GET` | `/admin/config/stripe-connect/ws` | `handleBOStripeConnectWS` | socket | solo difunde el estado de Stripe Connect |
| `POST` | `/admin/{tipo}/{id}/image`, `/admin/vinos/{id}/image*`, `/admin/branding/logo`, `/admin/members/{id}/avatar`, `/admin/tables/{id}/texture-image`, `/admin/group-menus-v2/.../image*`, `/admin/config/ads/{adId}/image/*`, `/admin/config/special-dates/menu-image`, `/admin/invoices/{id}/upload-image` | varios | `Image`/`Upload`/`Avatar` | binarios y generación de imagen: fuera del alcance de las tools de texto |
| `POST` | `/admin/stock/items/import`, `/admin/pos/products/import-*` | varios | `Import` | importación masiva: fuera de alcance |
| `POST` | `/admin/stock/documents/upload`, `/admin/stock/ocr-scan` | varios | `OCR`/`Upload` | **OCR `document_get` RETENIDO** (superficie prompt-injection) |
| `GET` | `/admin/comida/ai-image/status`, `/admin/config/ai-image`, `/admin/config/ai-image/providers` | varios | `Image` | config de proveedor de IA (fuera del mapa) |
| `GET`/`POST`/`PUT` | `/admin/campanas/email-validation`, `/admin/campanas/bounces/sync` | varios | **mapa desactualizado** (§9.1) | hoy NO alcanzables por `admin_call` |
| `GET`/`PUT` | `/admin/bot/ai/{restaurantId}/privileged-contacts` | varios | **mapa desactualizado** (§9.1) | hoy NO alcanzables por `admin_call` |

---

## 7. Matriz C — API pública, legacy, widget, plugin, MCP y webhooks

Ninguna de estas rutas es una operación de *backoffice*: son superficies de **cliente** o de
**infraestructura**. No tienen tool de admin y no se van a añadir (el encargo es la operación del
backoffice, no replicar la web pública en el chat). Se documentan para que la matriz sea completa.

| Superficie | Rutas | Gates | Tool | Motivo |
|---|---|---|---|---|
| Pública | `GET /public/booking`, `POST /public/booking/{confirm,cancel,rice}`, `GET /public/booking-policies`, `GET /public/legal-page`, `GET /public/ads*` | pública (tenant por host) | — | API de cliente; el bot ya tiene sus propias tools de reserva |
| Widget | `GET /widget/settings`, `GET /widget/reservations/*`, `POST /widget/bookings/front`, `PUT /widget/settings` | `withWidgetRestaurant` (+ `requireAdmin` en el PUT) | `website_read(widget_settings)` para el GET de backoffice equivalente | superficie de reserva embebida |
| Legacy raíz | `/menu-visibility`, `/unsubscribe*`, `/reservations/*`, `/menus/*`, `/comida/*`, `/vinos`, `/postres`, `/api_vinos.php`, `*.php` de gestión, `/bookings/front*`, `/insert_booking.php`, `/fetch_bookings.php`, `/edit_booking.php`, `/delete_booking.php`, `/reactivate_booking.php`, `/update_daily_limit.php`, `/open_day.php`, `/close_day.php`, `/fetch_occupancy.php`, `/_n8n*`, `/n8nReminder.php` | `withRestaurant` (+ `requireAdmin` en las de admin) | — | endpoints legacy PHP; las equivalencias modernas están en `/admin` y se cubren por `admin_call` |
| Bot webhook | `POST /bot/webhook`, `POST /bot/webhook/evolution/{secret}` | secreto del proveedor / token de instancia | — | es la entrada del bot, no una operación |
| Plugin ChatGPT | `GET /.well-known/*`, `GET /openapi.yaml|json`, `POST /api/plugin/tools`, `POST /api/plugin/tools/{name}` | token del plugin | `assistantExecuteTool` | superficie pública de tools (ver §1) |
| MCP | `POST /mcp`, `GET /mcp/oauth/*`, `POST /mcp/oauth/{authorize,token}` | OAuth | `assistantExecuteTool` | superficie pública de tools (ver §1) |
| Pasarela | `POST /stripe/webhook`, `POST /stripe/connect-webhook` | firma del proveedor | — | cobros: fuera de alcance |
| Salud | `GET /healthz` | — | — | — |

---

## 8. Matriz D — métodos socket (WebSocket)

Todos los sockets viven en `internal/api` (9 `websocket.Upgrader`); no hay SSE. Ningún método de
socket tiene tool propia: los sockets del backoffice son **superficies de edición en vivo** y su
efecto real es una operación REST, que sí está en la matriz A.

| Socket | Método (cliente→servidor) | Efecto | Tool que lo cubre |
|---|---|---|---|
| `/admin/assistant/ws` | `hello`, `message`, `ping` | `message` ejecuta el bucle de tools de Forky | las tools del registro (§11) |
| `/assistant/ws` (público) | `hello`, `message`, `ping` | solo tools de lectura (anónimo) | `restaurant_info` y lecturas públicas |
| `/admin/ws` | — (solo difusión) | eventos `special_date` | — |
| `/admin/site-builder/ws` | `sites.list/create/get/update/delete`, `pages.*`, `components.list`, `publish.start/status`, `domains.*` | replican las 18 operaciones REST de site-builder | `admin_call` **no** las alcanza hoy (fuera del mapa) → hueco §9.1 |
| `/admin/tables/ws` | `table_edit`, `layout_edit`, `template_edit`, `template_delete` (escritura), `sync`, `refresh`, `join_tables` | edición de mesas en vivo | `admin_call` (escritura → `manage_backoffice` tras `OK`) |
| `/admin/fichaje/ws` | `ad_save` (escritura), `ad_schedule_check`, `join_restaurant` | anuncios del restaurante | `admin_call` (`/config/ads`) |
| `/admin/comida/technical-sheets/ws` | `search` | búsqueda paginada de fichas | `sheets_read(list)` |
| `/admin/group-menus-v2/ws` | `beverage_list/set/create/delete`, `weekday_refresh/set`, `editor_preview_set`, `special_section_image_upload`, `sync`/`refresh`/`join*` | edición de menús v2 | `admin_call` para las REST equivalentes |
| `/admin/vinos/ws` | `sync`, `refresh` | instantáneas de la IA de imagen | — (la IA es el POST `/vinos/{id}/image/ai`, fuera del mapa) |
| `/admin/comida/ws` | — (solo difusión) | progreso de la IA | — |
| `/admin/bookings/documents/ws` | `bookingDocumentUpload`, `bookingDocumentList`, `bookingDocumentDelete` | ficheros de reserva | — (fuera de alcance: subida de ficheros) |
| `/admin/reservas/ws` | — (solo difusión) | `reservas_columns` | — |
| `/admin/members/whatsapp/ws` | — (solo difusión) | estado de la conexión | `get_bot_status` |
| `/admin/config/stripe-connect/ws` | — (solo difusión) | estado de Stripe | — |

---

## 9. FALTANTE — huecos detectados y estado

### 9.1 El mapa de operaciones está desactualizado (hueco de paridad de Forky)

`internal/api/forky_admin_ops.json` se regeneró por última vez en `3b73b42` (516 → 556 ops) y el
panel ha seguido creciendo. Hoy **`admin_call` no alcanza**:

- `POST /admin/campanas/email-validation`, `POST /admin/campanas/bounces/sync` (de `#537`),
- `GET|PUT /admin/bot/ai/{restaurantId}/privileged-contacts` (de `#503`),
- **todas** las rutas registradas por sub-registradores dentro de `/admin`:
  `site-builder/*` (30), `website-builder/*` (33) e `instatic/*` (5) — el generador solo lee
  `server.go`, y estas se registran desde `site_builder.go`, `website_builder.go` e
  `instatic_manager.go`.

→ **Resuelto en esta rama**: el generador pasa a leer también los sub-registradores (con los gates
del `Group` que los envuelve) y el mapa se regenera.

### 9.2 GETs del backoffice que escriben (19 auditados)

El flag `write` del mapa se derivaba del **método HTTP**, así que 19 GETs que **escriben** estaban
marcados como lectura y eran alcanzables desde WhatsApp sin confirmación. Auditoría completa
(`wa_customtools_get_writes_v1`):

| GET | Qué escribe |
|---|---|
| `/admin/menu-visibility` | `INSERT menu_visibility` (`ensureMenuVisibilityRows`) |
| `/admin/comida/platos/categorias` | `INSERT/UPDATE comida_plato_categories` |
| `/admin/comida/bebidas/categorias` | `INSERT/UPDATE comida_bebida_categories` |
| `/admin/comida/categorias` | `INSERT/UPDATE comida_*_categories` |
| `/admin/group-menus-v2/{id}` | `INSERT group_menu_sections_v2`, `group_menu_section_dishes_v2`, `menus` |
| `/admin/group-menus-v2/{id}/sections/{sectionId}/dishes` | `INSERT menus`, `group_menu_sections_v2` |
| `/admin/group-menus-v2/{id}/slider` | `UPDATE ai_image_provider_config` (condicional) |
| `/admin/config/floors`, `/admin/config/floors/defaults` | `INSERT/UPDATE restaurant_floors` |
| `/admin/bookings` | `UPDATE booking_documents` (+ borrado en Bunny) y `INSERT restaurant_floors` |
| `/admin/bookings/{id}/qr` | `UPDATE bookings.qr_url/receipt_url` + subida a Bunny |
| `/admin/members/whatsapp/connection` | `UPDATE restaurant_uazapi_instances`, `INSERT restaurant_integrations` |
| `/admin/stock/warehouses` | `INSERT stock_warehouses` (fila por defecto) |
| `/admin/stock/documents/{id}/original` | `INSERT stock_document_access_audit` |
| `/admin/pos/accounting/export.csv` | `INSERT accounting_exports` (auditoría) |
| `/admin/me` | `UPDATE bo_sessions` (auto-reparación) |
| `/admin/config/stripe-connect` | `INSERT/UPDATE restaurant_stripe_connect` + llamada a Stripe |
| `/admin/config/ai-image`, `/admin/comida/ai-image/status` | `UPDATE ai_image_provider_config` (condicional) |

→ **Resuelto en esta rama**: esos GET pasan a marcarse `write` en el mapa, con lo que la
contención de WA los rechaza y Forky los exige con `confirmed=true` + `confirmation_token`.

### 9.3 Escrituras del panel sin tool en WhatsApp

De 336 escrituras, 20 tienen tool `*_propose_*` propia; el resto (campañas, carta, menús v2,
miembros, stock, TPV, facturas, configuración) **no** tenía ninguna: el bot solo podía leer.

→ **Resuelto en esta rama** con `manage_backoffice` (tool WA `resource×action`): propone la
operación del panel y la aplica **solo** tras `OK <código>` del actor verificado, reutilizando la
ruta real (sus gates, sus reglas y su scoping de tenant).

### 9.4 Plano de control del bot

No existía ninguna tool para gestionar el acceso del propio bot (quién está autorizado, qué
herramientas puede usar cada conversación, grupo de gestión).

→ **Resuelto en esta rama** con `manage_bot_access` (tool WA `resource×action`), con la frontera
de §2 y **sin** la lista de contactos en BD como credencial.

### 9.5 Paridad de Forky con las tools nuevas

Las tools nuevas de WhatsApp no están en el registro de Forky (son tools del bot), pero **la misma
capacidad** sí está disponible en Forky por `admin_call` (escritura con `confirmed=true` +
`confirmation_token`). Además se registra `backoffice_write` en el registro de Forky para que el
panel pueda ejecutar la misma operación por su cuenta, con `BackofficeOnly` y exclusión explícita
de WA.

---

## 10. Exclusiones documentadas (retenidas)

| Superficie | Qué | Por qué |
|---|---|---|
| OCR `document_get` (`/admin/stock/documents/*`, `/admin/stock/ocr-scan`) | no implementada | superficie de prompt-injection; queda retenida hasta un diseño con salida minimizada |
| Writes-control legacy (`POST /admin/bookings/cancelled/{id}/reactivate`, `reactivate_booking.php`) | no implementada | decisión expresa: la reactivación de reservas la hace el restaurante en el panel |
| Gestión de cuentas del panel (`/admin/users*`, `/admin/roles*`, `/admin/platform/users*`) | no implementada | el bot gestiona el restaurante, no las personas que usan el panel |
| Sesión (`/admin/me*`) y `/admin/active-restaurant` | no implementada | la sesión de servicio es de un superadmin real; el bot no toca sus credenciales ni cambia de restaurante |
| Cobros (`/admin/site-builder/billing/checkout`, `/admin/domains/*`, `/admin/config/stripe-connect/*`, `/admin/platform/stripe/refund`) | no implementada | dinero real |
| Envío a clientes (`/admin/campanas/{id}/send|test`, `/admin/campanas/preview`, `/admin/members/whatsapp/send|flush-queue`, `/admin/invoices/{id}/send`, `emailAdvertising/*`) | no implementada | mensajes reales a clientes |
| Claves de proveedor (`/admin/config/minimax`, `/admin/config/bunny-storage`, `/admin/config/ai-image*`, `/admin/bot/ai/*`, `/admin/config/chatgpt-plugin/tokens*`, `/admin/integrations/uazapi/servers*`) | no implementada | credenciales |
| Subidas de ficheros e imágenes (binarios) | no implementada | fuera del alcance textual; algunas sí están en el panel vía `admin_call` |
| Tenants / superadmin (`/admin/platform/*` salvo lecturas de catálogo) | no implementada | gestión de la plataforma |

---

## 11. Inventario de tools

### 11.1 Registro de Forky (`assistantToolRegistry`)

Se listan en `docs/FORKY_API.md` (generado por `go run ./cmd/forky-docs`). Resumen:

- **Lecturas de dominio reutilizables** (`resource×action`): `pos_read`, `stock_read`,
  `members_read`, `fichaje_read`, `sheets_read`, `menus_read`, `reservas_read`, `website_read`,
  `campaigns_read`, `invoices_read`.
- **Lecturas tipadas**: reservas, menú, catálogo, horarios, fichaje, stock, TPV, miembros, facturas,
  analítica, configuración.
- **Escrituras**: cada una con `Write: true` + `Confirm: true` (`confirmed=true` +
  `confirmation_token` emitido por el servidor).
- **Trío de administración**: `admin_catalog`, `admin_describe`, `admin_call`.

### 11.2 Tools del bot de WhatsApp

- **Cliente**: `send_message`, `send_menu_buttons`, `send_image`, `send_document`,
  `send_location`, `send_contact`, `get_rice_menu`, `list_menus`, `get_menu_details`,
  `get_booking_menu`, `get_coffee_menu`, `get_drinks_menu`, `get_wines_menu`,
  `list_booking_extras`, `get_default_schedule`, `get_day_schedule`, `check_day_capacity`,
  `get_bookings`, `create_booking`, `cancel_booking`, `modify_booking`.
- **Staff (grupo)**: `get_date_overview`, `get_booking_details`, `add_booking_note`,
  `list_cancelled_bookings`, `list_special_dates`, `get_booking_config`, `list_tables`,
  `bookings_summary`, `list_campaigns`, `get_bot_status`, `list_menu_visibility`.
- **Fecha especial / reservas (propose→OK)**: `special_booking_get`,
  `special_booking_propose_{adelanto,menus,reschedule,create}`, `booking_propose_{reschedule,cancel,edit,create,restore,set_rice,set_group_menu,set_extras,set_mobility,remove_group_menu,set_floor,set_flags,catalog_extra_create,catalog_extra_delete,notif_settings}`,
  `config_propose_{day,daily_limit,mesas,salon_condesa}`.
- **Nuevas en esta rama**: `manage_backoffice` (operaciones del panel), `manage_bot_access`
  (plano de control del bot).

### 11.3 Tools de Forky que NO llegan a WhatsApp (`botForkyExcludedFromWA`)

`cancelled_bookings_list`, `special_dates_list`, `tables_list`, `campaigns_list`,
`whatsapp_connection_get`, `menu_visibility_list`, `legal_pages_list`, `site_builder_drafts_list`,
`pos_read`, `stock_read`, `members_read`, `fichaje_read`, `sheets_read`, `menus_read`,
`reservas_read`, `website_read`, `campaigns_read`, `invoices_read` — y, con esta rama,
`backoffice_write` (la escritura genérica del panel la hace el bot con `manage_backoffice` y su
cadena propose→OK, nunca el modelo con un `confirmed=true` que se escribe a sí mismo).

Motivo de cada una: sus datos salen por las tools de staff del bot con la puerta de miembro
fresco (`botSpecialAdminAuthorize`), y el contexto root sintético del camino privilegiado no puede
convertirse en un bypass de esa puerta.

---

## 12. Verificación manual (sin tests automáticos)

1. `go build ./...` y `go vet ./internal/api` (build acotado: `systemd-run --user --scope -p MemoryMax=8G`).
2. `go run ./cmd/forky-admin-map -server internal/api/server.go -pages ../backoffice -o internal/api/forky_admin_ops.json`
   → el mapa debe incluir ya las rutas de `site-builder`, `website-builder`, `instatic`,
   `campanas/email-validation`, `campanas/bounces/sync` y `bot/ai/{id}/privileged-contacts`, y los
   19 GET que escriben deben aparecer con `write: true`.
3. `go run ./cmd/forky-docs > docs/FORKY_API.md` → el registro debe incluir la tool nueva de
   Forky.
4. En el grupo autorizado: un mensaje de un **miembro actual** ofrece el catálogo completo; un
   mensaje de un **no miembro** no; un privado de un teléfono **no verificado** no.
5. `manage_backoffice` genera una propuesta y **no** aplica nada hasta el `OK <código>` del mismo
   actor en el mismo chat; el código no sirve en otro chat ni para otro actor.
6. `manage_bot_access` con `action=list` exige la misma frontera; `management_group` con nombre
   (sin JID) se rechaza.
