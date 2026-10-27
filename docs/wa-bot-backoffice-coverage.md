# Mapeo backoffice ↔ WhatsApp bot (privileged control)

Coordination ids: `wa_bot_panel_write_v1`, `wa_bot_staff_reads_v2`,
`wa_bot_privileged_control_v1`, `wa_bot_tool_grants_v1`.

Inventario del backoffice (fuente: registro de rutas de `internal/api/server.go`
+ protocolos WS): **~704 endpoints `/api/admin/*`** y **13 WebSockets**.
Superficie del bot en una conversación autorizada (contacto/grupo privilegiado):
herramientas propias + registro forky + `admin_catalog/admin_describe/admin_call`
(mapa de operaciones `forky_admin_ops.json`: 558 ops = 222 GET / 336 escrituras).

## Cobertura por dominio (tras este cambio)

| Dominio | Rutas HTTP | WS | Lectura vía bot | Escritura vía bot |
|---|---:|---:|---|---|
| Reservas / calendario / config / mesas | 76 | 3 | bot tools + forky + admin_call | `booking_propose_*` (propose+OK) + `panel_change` |
| Menús / comida / vinos / postres / group-menus | 113 | 3 | bot tools + forky + `staff_domain_read` (`menus_read`) | `panel_change` (incluye ops directas `beverage_*`, `weekday_set`) |
| Stock + fichas técnicas + producción | 111 | 1 | forky + `staff_domain_read` (`stock_read`, `sheets_read`) + `get_dish_allergens` | `panel_change` |
| POS / TPV | 128 | 0 | forky + `staff_domain_read` (`pos_read`) | `panel_change` |
| Miembros / roles / WhatsApp | 35 | 1 | forky + `staff_domain_read` (`members_read`) | `panel_change` |
| Fichaje / horarios | 19 | 1 | forky + `staff_domain_read` (`fichaje_read`) | `panel_change` (+ comandos exactos `start/stop`) |
| Facturas | 13 | 0 | forky + `staff_domain_read` (`invoices_read`) | `panel_change` |
| Campañas | 16 | 0 | `list_campaigns` + `staff_domain_read` (`campaigns_read`) | `panel_change` |
| Anuncios (ads) | 7 | 0 | `staff_domain_read` (`website_read`) | `panel_change` (gemelo REST de `ad_save`) |
| Analíticas | 6 | 0 | `analytics_report`, `ai_usage_report`, `whatsapp_messages_list` | — (solo lectura) |
| Ajustes / integraciones / bot config | 17 | 0 | forky + `get_bot_status` | `panel_change` + `manage_bot_access` |
| Website / site-builder / dominios / legal | 80 | 1 | `staff_domain_read` (`website_read`, `legal_pages_list`, `site_builder_drafts_list`) | `panel_change` |
| Plataforma (superadmin) | 25 | 0 | **fuera de alcance por diseño** | **fuera de alcance por diseño** |
| Auth / sesión / onboarding | 15 | 0 | **fuera de alcance por diseño** | **fuera de alcance por diseño** |

## Faltantes encontrados y añadidos (este WT)

1. **Todas las escrituras del panel** (336 ops del mapa + herramientas forky de
   escritura) estaban bloqueadas por la contención `wa_bot_admin_write_denied_v1`
   ("hasta que exista una aprobación ligada a la vista previa exacta").
   → **`panel_change`** (`wa_bot_panel_write_v1`): una sola herramienta
   parametrizada (`operation` + `path_params`/`query`/`body`) que PROPONE
   cualquier operación del backoffice y se aplica con `OK <código>` (máquina
   propose+OK compartida, actor+chat ligados, TTL). Reutiliza el replay real
   (`forkyAdminRunOp`) con la sesión de servicio y la misma red de seguridad
   que `admin_call` (`forkyAdminCheckOp`: scope de tenant, sin `/me`, sin
   gestión de cuentas, sin rebindeo de sesión).

2. **Mutaciones WS sin gemelo REST** (no existían en ninguna ruta HTTP, así que
   ni `admin_call` ni el mapa podían alcanzarlas) → **operaciones directas** de
   `panel_change`, reutilizando la función de dominio que usa cada handler WS:
   `beverage_set`, `beverage_create`, `beverage_delete`, `weekday_set`,
   `booking_document_delete`, `editor_preview_set`, `table_position_set`,
   `ad_schedule_check`, `site_builder_call`, `assistant_tool`.

3. **Media (subidas y generación AI)** (`wa_bot_panel_media_v1`): el replay de
   `panel_change` solo envía JSON, así que el multipart era inalcanzable →
   operaciones directas parametrizadas **`media_upload`** (kinds:
   `booking_document`, `special_section`, `vino`, `ads`) y **`media_ai`**
   (kinds: `vino`, `vino_cutout`, `comida`, `section_dish`, `menu_preview`,
   `slider`, `ads_enhance`, `ads_generate`), reutilizando las funciones de
   dominio reales. Casos con persistencia inline en el handler (logo, avatar,
   texturas, factura, step image) quedan documentados como pendientes de
   extracción.

4. **Lecturas excluidas sin herramienta dedicada**: los miembros de
   `botForkyExcludedFromWA` que no tenían equivalente en WhatsApp
   (`pos_read`, `stock_read`, `members_read`, `fichaje_read`, `sheets_read`,
   `menus_read`, `reservas_read`, `website_read`, `campaigns_read`,
   `invoices_read`, `legal_pages_list`, `site_builder_drafts_list`).
   → **`staff_domain_read`** (`wa_bot_staff_reads_v2`): una sola herramienta
   parametrizada (`tool` + `input`) que ejecuta esas lecturas por el camino real
   del asistente (con sus gates de ruta exactos) limitada a conversaciones
   autorizadas con prueba de pertenencia fresca.

5. **Mapa de operaciones incompleto**: `forky_admin_ops.json` solo cubría
   `server.go` (558 ops) y perdía los sub-routers `/website-builder/*` (27),
   `/site-builder/*` (29), `/instatic/*` (5) y 16 rutas JSON → generador
   `cmd/forky-admin-map` extendido y mapa regenerado: **635 operaciones**, todo
   `admin_call`/`panel_change` alcanza el 100% de las rutas request/response
   del panel.

## Menús especiales y fechas especiales al 100% (`forky_bot_special_full_v1`)

Forky (`admin_call` + `special_media_upload`) y el bot (`panel_change`) cubren
todo lo que hacen `/app/comida/menus` (menú especial) y `/app/reservas/especial`
(fechas festivas):

| Necesidad | Antes | Ahora |
|---|---|---|
| Saber el body de una escritura | `admin_describe` solo decía `{"json": true}`: el modelo adivinaba (2026-10-27: mandó `adelanto_amount` al PATCH de la sección del catálogo) | campo `body` en el mapa (`bodiesEs` del generador): contrato exacto de las 24 operaciones del dominio + reservas con bloque `special` |
| Cambiar UN campo de una fecha especial | solo `POST /config/special-dates` (reemplazo total) | `PATCH /config/special-dates` (fusión: GET + campos enviados + mismo guardado) |
| Reenviar una fecha tal cual la devuelve el GET | `sections[].id` no era `section_id`: rechazo | `id` aceptado como `section_id` |
| SEO del menú | ruta fuera del mapa (mapa desfasado) | mapa regenerado: 646 operaciones |
| Imagen principal del menú especial / imagen de menú personalizado de una fecha | multipart, inalcanzable | `media_upload` kinds `special_menu` y `special_date_menu` (mismas funciones de dominio que las rutas) |
| Usar la imagen que mandan al chat | `media_upload` exigía `data_base64` (un modelo no puede escribir una imagen) | bot: `image_ref` del chat (`chat_jid` fijado por el servidor, imagen anclada al proponer); Forky: `attachment` del chat con `special_media_upload` |

## Fuera de alcance (por diseño, documentado)

- Auth/sesión (`/login`, `/logout`, `/me/*`, `/active-restaurant`), gestión de
  cuentas (`/users`, `/roles`, `/platform/users*`) y operaciones superadmin de
  plataforma: el bot gestiona el restaurante, no las cuentas del panel.
- El turno conversacional del asistente vía WS (`assistant_ws:message`): loop
  LLM sin vista previa exacta posible; sus efectos deterministas van por
  `assistant_tool`/`panel_change`.
- 25 rutas multipart con persistencia inline en el handler (logo de marca,
  avatar, texturas de mesa, imagen de factura, step image de ficha): pendientes
  de extraer su función de dominio.
- Suscripciones WS (streams de eventos en tiempo real): el bot no se suscribe;
  el equivalente son lecturas bajo demanda (`admin_call`, `staff_domain_read`).
