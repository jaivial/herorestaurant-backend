# Forky tools API

All tools execute against the authenticated active restaurant (`restaurant_id`
is never accepted from model input). Responses are JSON strings and errors are
returned by the assistant protocol.

Regenerate the matrix below with:

```sh
go run ./cmd/forky-docs > docs/FORKY_API.md  # (paste under this header)
```

## Matriz de tools (generada del registro)

| Tool | Sección | Permiso | Confirma | Schema |
|---|---|---|---|---|
| `restaurant_info` | reservas | read | no | `{"type":"object","properties":{}}` |
| `bookings_summary` | reservas | read | no | `{"type":"object","properties":{"date":{"type":"string"},"date_from":{"type":"string"},"date_to":{"type":"string"}}}` |
| `bookings_list` | reservas | read | no | `{"type":"object","properties":{"date":{"type":"string"},"date_from":{"type":"string"},"date_to":{"type":"string"},"limit":{"type":"integer"}}}` |
| `cancelled_bookings_list` | reservas | read | no | `{"type":"object","properties":{"date":{"type":"string","description":"Fecha YYYY-MM-DD o dd/MM/yyyy (opcional, por defecto hoy)"}}}` |
| `special_dates_list` | reservas | read | no | `{"type":"object","properties":{}}` |
| `campaigns_list` | campanas | read | no | `{"type":"object","properties":{"campaign_id":{"type":"integer","description":"ID de campaña concreto (opcional)"}}}` |
| `whatsapp_connection_get` | plataforma | read | no | `{"type":"object","properties":{}}` |
| `menu_visibility_list` | menus | read | no | `{"type":"object","properties":{}}` |
| `legal_pages_list` | website | read | no | `{"type":"object","properties":{"slug":{"type":"string","description":"aviso-legal, booking-policies, proteccion-datos o special-booking-politics (opcional)"}}}` |
| `site_builder_drafts_list` | website | read | no | `{"type":"object","properties":{}}` |
| `tables_list` | reservas | read | no | `{"type":"object","properties":{}}` |
| `restaurant_query` | reservas | read | no | `{"type":"object","properties":{"resource":{"type":"string","enum":["bookings","menus","wines"]},"date_from":{"type":"string"},"date_to":{"type":"string"}},"required":["resource"]}` |
| `create_booking` | reservas | write | sí | `{"type":"object","properties":{"date":{"type":"string"},"time":{"type":"string"},"people":{"type":"integer"},"name":{"type":"string"},"contact_phone":{"type":"string"},"contact_phone_country_code":{"type":"string"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["date","time","people","name","confirmed"]}` |
| `update_booking` | reservas | write | sí | `{"type":"object","properties":{"booking_id":{"type":"integer"},"date":{"type":"string"},"time":{"type":"string"},"people":{"type":"integer"},"name":{"type":"string"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["booking_id","confirmed"]}` |
| `delete_booking` | reservas | write | sí | `{"type":"object","properties":{"booking_id":{"type":"integer"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["booking_id","confirmed"]}` |
| `customers_list` | reservas | read | no | `{"type":"object","properties":{"search":{"type":"string"},"limit":{"type":"integer"}}}` |
| `booking_limits_update` | reservas | write | sí | `{"type":"object","properties":{"date":{"type":"string"},"daily_limit":{"type":"integer"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["date","daily_limit","confirmed"]}` |
| `booking_limits_get` | reservas | read | no | `{"type":"object","properties":{"date":{"type":"string"}},"required":["date"]}` |
| `catalog_list` | comida | read | no | `{"type":"object","properties":{"resource":{"type":"string","enum":["comida","bebidas","cafes","vinos","menus","pos_products","stock_items","members"]},"search":{"type":"string"},"limit":{"type":"integer"}},"required":["resource"]}` |
| `catalog_get` | comida | read | no | `{"type":"object","properties":{"resource":{"type":"string"},"id":{"type":"integer"}},"required":["resource","id"]}` |
| `catalog_create` | comida | write | sí | `{"type":"object","properties":{"resource":{"type":"string"},"name":{"type":"string"},"description":{"type":"string"},"price":{"type":"number"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["resource","name","confirmed"]}` |
| `catalog_update` | comida | write | sí | `{"type":"object","properties":{"resource":{"type":"string"},"id":{"type":"integer"},"name":{"type":"string"},"description":{"type":"string"},"price":{"type":"number"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["resource","id","confirmed"]}` |
| `catalog_delete` | comida | write | sí | `{"type":"object","properties":{"resource":{"type":"string"},"id":{"type":"integer"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["resource","id","confirmed"]}` |
| `menus_list` | menus | read | no | `{"type":"object","properties":{"include_drafts":{"type":"boolean"}}}` |
| `menu_get` | menus | read | no | `{"type":"object","properties":{"id":{"type":"integer"}},"required":["id"]}` |
| `menu_sections_get` | menus | read | no | `{"type":"object","properties":{"id":{"type":"integer"},"section_id":{"type":"integer"}},"required":["id","section_id"]}` |
| `menu_toggle_active` | menus | write | sí | `{"type":"object","properties":{"id":{"type":"integer"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["id","confirmed"]}` |
| `analytics_report` | estadisticas | read | no | `{"type":"object","properties":{"metric":{"type":"string","enum":["bookings","bookings_by_hour","revenue","revenue_by_day","products","stock"]},"date_from":{"type":"string"},"date_to":{"type":"string"}},"required":["metric"]}` |
| `ai_usage_report` | estadisticas | read | no | `{"type":"object","properties":{"granularity":{"type":"string","enum":["week","month","year"]},"date_from":{"type":"string"},"date_to":{"type":"string"}}}` |
| `whatsapp_messages_list` | estadisticas | read | no | `{"type":"object","properties":{"granularity":{"type":"string","enum":["week","month","year"]},"date_from":{"type":"string"},"date_to":{"type":"string"},"direction":{"type":"string","enum":["inbound","outbound"]},"search":{"type":"string"},"limit":{"type":"integer"}}}` |
| `schedules_list` | horarios | read | no | `{"type":"object","properties":{"date":{"type":"string"},"limit":{"type":"integer"}}}` |
| `schedules_by_date` | horarios | read | no | `{"type":"object","properties":{"date":{"type":"string"}}}` |
| `schedules_month` | horarios | read | no | `{"type":"object","properties":{"year":{"type":"integer"},"month":{"type":"integer"}}}` |
| `schedules_create` | horarios | write | sí | `{"type":"object","properties":{"date":{"type":"string"},"member_id":{"type":"integer"},"start_time":{"type":"string"},"end_time":{"type":"string"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["date","member_id","start_time","end_time","confirmed"]}` |
| `schedules_update` | horarios | write | sí | `{"type":"object","properties":{"id":{"type":"integer"},"start_time":{"type":"string"},"end_time":{"type":"string"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["id","start_time","end_time","confirmed"]}` |
| `schedules_delete` | horarios | write | sí | `{"type":"object","properties":{"id":{"type":"integer"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["id","confirmed"]}` |
| `fichaje_state_get` | fichaje | read | no | `{"type":"object","properties":{}}` |
| `fichaje_entries_list` | fichaje | read | no | `{"type":"object","properties":{"date":{"type":"string"},"member_id":{"type":"integer"},"limit":{"type":"integer"}}}` |
| `fichaje_admin_start` | fichaje | write | sí | `{"type":"object","properties":{"member_id":{"type":"integer"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["member_id","confirmed"]}` |
| `fichaje_admin_stop` | fichaje | write | sí | `{"type":"object","properties":{"member_id":{"type":"integer"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["member_id","confirmed"]}` |
| `fichaje_start` | fichaje | write | sí | `{"type":"object","properties":{"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["confirmed"]}` |
| `fichaje_stop` | fichaje | write | sí | `{"type":"object","properties":{"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["confirmed"]}` |
| `stock_warehouses_list` | stock | read | no | `{"type":"object","properties":{}}` |
| `stock_categories_list` | stock | read | no | `{"type":"object","properties":{}}` |
| `stock_items_list` | stock | read | no | `{"type":"object","properties":{"search":{"type":"string"},"kind":{"type":"string"},"category_id":{"type":"integer"},"warehouse_id":{"type":"integer"},"page":{"type":"integer"},"page_size":{"type":"integer"},"sort":{"type":"string"}}}` |
| `stock_read` | stock | read | no | `{"type":"object","required":["resource"],"properties":{"resource":{"type":"string"},"id":{"type":"string"},"q":{"type":"string"},"days":{"type":"string"},"scenario":{"type":"string"},"category_id":{"type":"string"},"food_type":{"type":"string"},"scope_key":{"type":"string"},"scope_kind":{"type":"string"}}}` |
| `members_read` | miembros | read | no | `{"type":"object","required":["resource"],"properties":{"resource":{"type":"string"},"id":{"type":"string"},"date":{"type":"string"},"date_from":{"type":"string"},"date_to":{"type":"string"},"year":{"type":"string"},"view":{"type":"string"}}}` |
| `fichaje_read` | fichaje | read | no | `{"type":"object","required":["resource"],"properties":{"resource":{"type":"string"},"date":{"type":"string"},"date_from":{"type":"string"},"date_to":{"type":"string"}}}` |
| `sheets_read` | comida | read | no | `{"type":"object","required":["resource"],"properties":{"resource":{"type":"string"},"id":{"type":"string"},"category_id":{"type":"string"},"q":{"type":"string"},"status":{"type":"string"}}}` |
| `menus_read` | menus | read | no | `{"type":"object","required":["resource"],"properties":{"resource":{"type":"string"},"id":{"type":"string"},"section_id":{"type":"string"},"date":{"type":"string"},"status":{"type":"string"},"include_drafts":{"type":"string"}}}` |
| `reservas_read` | reservas | read | no | `{"type":"object","required":["resource"],"properties":{"resource":{"type":"string"},"date":{"type":"string"}}}` |
| `website_read` | website | read | no | `{"type":"object","required":["resource"],"properties":{"resource":{"type":"string"}}}` |
| `campaigns_read` | campanas | read | no | `{"type":"object","required":["resource"],"properties":{"resource":{"type":"string"}}}` |
| `campaign_get` | campanas | read | no | `{"type":"object","properties":{"campaign_id":{"type":"integer","description":"ID de la campaña (sin él: lista)"},"include":{"type":"array","items":{"type":"string","enum":["layers","html","whatsapp_text","audience","deliveries","ai_models","blocks"]}}}}` |
| `campaign_save` | campanas | write | sí | `{"type":"object","properties":{"campaign_id":{"type":"integer"},"name":{"type":"string"},"subject":{"type":"string"},"channels":{"type":"array","items":{"type":"string","enum":["email","whatsapp"]}},"audience":{"type":"string","enum":["bookings","manual"]},"audience_days":{"type":"integer"},"manual_recipients":{"type":"array","items":{"type":"string"}},"email_per_minute":{"type":"integer"},"whatsapp_per_minute":{"type":"integer"},"design_html":{"type":"string","description":"Solo al crear: diseño inicial (HTML con clases de Tailwind)"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["confirmed"]}` |
| `campaign_design` | campanas | write | sí | `{"type":"object","properties":{"campaign_id":{"type":"integer"},"action":{"type":"string","enum":["edit","ai","undo"]},"ops":{"type":"array","items":{"type":"object"}},"version":{"type":"string"},"instruction":{"type":"string"},"element_id":{"type":"string"},"model":{"type":"string"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["campaign_id","action","confirmed"]}` |
| `campaign_image` | campanas | write | sí | `{"type":"object","properties":{"campaign_id":{"type":"integer"},"attachment":{"type":"string","description":"ref del adjunto (forky/…)"},"element_id":{"type":"string","description":"<img> que se sustituye"},"insert_after":{"type":"string"},"placement":{"type":"string","enum":["replace","insert","none"]},"alt":{"type":"string"},"version":{"type":"string"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["campaign_id","attachment","confirmed"]}` |
| `campaign_send` | campanas | write | sí | `{"type":"object","properties":{"campaign_id":{"type":"integer"},"action":{"type":"string","enum":["test","send","resume"]},"channel":{"type":"string","enum":["email","whatsapp"]},"target":{"type":"string"},"ack":{"type":"string"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["campaign_id","action","confirmed"]}` |
| `campaign_delete` | campanas | write | sí | `{"type":"object","properties":{"campaign_id":{"type":"integer"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["campaign_id","confirmed"]}` |
| `ad_get` | anuncios | read | no | `{"type":"object","properties":{"ad_id":{"type":"integer","description":"ID del anuncio (sin él: lista)"},"include":{"type":"array","items":{"type":"string","enum":["layers","html"]}}}}` |
| `ad_save` | anuncios | write | sí | `{"type":"object","properties":{"action":{"type":"string","enum":["create","update","convert","publish"]},"ad_id":{"type":"integer"},"name":{"type":"string"},"kind":{"type":"string","enum":["single","multiple"]},"starts_at":{"type":"string"},"ends_at":{"type":"string"},"always":{"type":"boolean"},"max_width":{"type":"integer"},"radius":{"type":"integer"},"background":{"type":"string"},"overlay":{"type":"string"},"active":{"type":"boolean","description":"Solo publish"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["action","confirmed"]}` |
| `ad_design` | anuncios | write | sí | `{"type":"object","properties":{"ad_id":{"type":"integer"},"action":{"type":"string","enum":["ai","undo"]},"instruction":{"type":"string"},"page":{"type":"string"},"element_id":{"type":"string"},"model":{"type":"string"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["ad_id","action","confirmed"]}` |
| `ad_image` | anuncios | write | sí | `{"type":"object","properties":{"ad_id":{"type":"integer"},"attachment":{"type":"string","description":"ref del adjunto (forky/…)"},"page":{"type":"string"},"element_id":{"type":"string"},"placement":{"type":"string","enum":["replace","insert","none"]},"alt":{"type":"string"},"version":{"type":"string"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["ad_id","attachment","confirmed"]}` |
| `ad_delete` | anuncios | write | sí | `{"type":"object","properties":{"ad_id":{"type":"integer"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["ad_id","confirmed"]}` |
| `invoices_read` | facturas | read | no | `{"type":"object","required":["resource"],"properties":{"resource":{"type":"string"},"date_from":{"type":"string"},"date_to":{"type":"string"},"name":{"type":"string"},"party_size":{"type":"string"},"phone":{"type":"string"},"time":{"type":"string"}}}` |
| `stock_item_movements_list` | stock | read | no | `{"type":"object","properties":{"id":{"type":"integer"},"page":{"type":"integer"},"page_size":{"type":"integer"}},"required":["id"]}` |
| `stock_summary` | stock | read | no | `{"type":"object","properties":{}}` |
| `stock_movement_create` | stock | write | sí | `{"type":"object","properties":{"item_id":{"type":"integer"},"warehouse_id":{"type":"integer"},"quantity":{"type":"number"},"unit_id":{"type":"integer"},"type":{"type":"string"},"direction":{"type":"string"},"waste_reason":{"type":"string"},"note":{"type":"string"},"idempotency_key":{"type":"string"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["item_id","warehouse_id","quantity","unit_id","type","idempotency_key","confirmed"]}` |
| `stock_transfer_create` | stock | write | sí | `{"type":"object","properties":{"item_id":{"type":"integer"},"from_warehouse_id":{"type":"integer"},"to_warehouse_id":{"type":"integer"},"quantity":{"type":"number"},"unit_id":{"type":"integer"},"idempotency_key":{"type":"string"},"note":{"type":"string"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["item_id","from_warehouse_id","to_warehouse_id","quantity","unit_id","idempotency_key","confirmed"]}` |
| `recipes_list` | stock | read | no | `{"type":"object","properties":{"limit":{"type":"integer"}}}` |
| `production_list` | stock | read | no | `{"type":"object","properties":{"limit":{"type":"integer"}}}` |
| `waste_costs_list` | stock | read | no | `{"type":"object","properties":{"limit":{"type":"integer"}}}` |
| `pos_visits_list` | pos | read | no | `{"type":"object","properties":{"status":{"type":"string"}}}` |
| `pos_tickets_list` | pos | read | no | `{"type":"object","properties":{"status":{"type":"string"}}}` |
| `pos_read` | pos | read | no | `{"type":"object","required":["resource"],"properties":{"resource":{"type":"string"},"id":{"type":"string"},"date":{"type":"string","description":"YYYY-MM-DD"},"date_from":{"type":"string"},"date_to":{"type":"string"},"shift_id":{"type":"string"},"station_id":{"type":"string"}}}` |
| `pos_cash_closures_list` | pos | read | no | `{"type":"object","properties":{"shift_id":{"type":"integer"}}}` |
| `pos_cash_summary` | pos | read | no | `{"type":"object","properties":{"shift_id":{"type":"integer"}}}` |
| `pos_cash_closure_create` | pos | write | sí | `{"type":"object","properties":{"shift_id":{"type":"integer"},"terminal_key":{"type":"string"},"closure_type":{"type":"string","enum":["X","Y","Z"]},"counted_cash_cents":{"type":"integer"},"note":{"type":"string"},"discrepancy_reason":{"type":"string"},"idempotency_key":{"type":"string"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["closure_type","idempotency_key","confirmed"]}` |
| `pos_visit_create` | pos | write | sí | `{"type":"object","properties":{"channel":{"type":"string"},"covers":{"type":"integer"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["channel","covers","confirmed"]}` |
| `pos_ticket_create` | pos | write | sí | `{"type":"object","properties":{"visit_id":{"type":"integer"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["visit_id","confirmed"]}` |
| `pos_ticket_line_add` | pos | write | sí | `{"type":"object","properties":{"ticket_id":{"type":"integer"},"product_id":{"type":"integer"},"quantity":{"type":"number"},"notes":{"type":"string"},"idempotency_key":{"type":"string"},"unit_price_override_cents":{"type":"integer"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["ticket_id","product_id","quantity","idempotency_key","confirmed"]}` |
| `pos_payment_create` | pos | write | sí | `{"type":"object","properties":{"ticket_id":{"type":"integer"},"method":{"type":"string","enum":["CASH","CARD","BIZUM","BANK","OTHER"]},"amount_cents":{"type":"integer"},"idempotency_key":{"type":"string"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["ticket_id","method","amount_cents","idempotency_key","confirmed"]}` |
| `pos_refund_create` | pos | write | sí | `{"type":"object","properties":{"ticket_id":{"type":"integer"},"amount_cents":{"type":"integer"},"reason":{"type":"string"},"payment_method":{"type":"string","enum":["CASH","CARD","BIZUM","BANK","OTHER"]},"idempotency_key":{"type":"string"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["ticket_id","amount_cents","reason","payment_method","idempotency_key","confirmed"]}` |
| `members_list` | miembros | read | no | `{"type":"object","properties":{}}` |
| `member_get` | miembros | read | no | `{"type":"object","properties":{"id":{"type":"integer"}},"required":["id"]}` |
| `member_balance_get` | estado_cuenta | read | no | `{"type":"object","properties":{"id":{"type":"integer"},"date":{"type":"string"}},"required":["id"]}` |
| `member_compensation_create` | miembros | write | sí | `{"type":"object","properties":{"member_id":{"type":"integer"},"pay_type":{"type":"string"},"gross_amount":{"type":"number"},"monthly_hours":{"type":"number"},"employer_cost_pct":{"type":"number"},"effective_from":{"type":"string"},"effective_to":{"type":"string"},"notes":{"type":"string"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["member_id","pay_type","gross_amount","effective_from","confirmed"]}` |
| `invoices_list` | facturas | read | no | `{"type":"object","properties":{"search":{"type":"string"},"status":{"type":"string"},"date_from":{"type":"string"},"date_to":{"type":"string"},"page":{"type":"integer"},"limit":{"type":"integer"}}}` |
| `invoice_get` | facturas | read | no | `{"type":"object","properties":{"id":{"type":"integer"}},"required":["id"]}` |
| `restaurant_settings_get` | plataforma | read | no | `{"type":"object","properties":{}}` |
| `integrations_get` | plataforma | read | no | `{"type":"object","properties":{}}` |
| `branding_get` | plataforma | read | no | `{"type":"object","properties":{}}` |
| `whatsapp_bot_config_get` | plataforma | read | no | `{"type":"object","properties":{}}` |
| `whatsapp_bot_config_update` | plataforma | write | sí | `{"type":"object","properties":{"model":{"type":"string"},"language_default":{"type":"string"},"tone":{"type":"string"},"greeting_style":{"type":"string"},"disable_attachments":{"type":"boolean"},"custom_instructions":{"type":"string"},"contact_phone":{"type":"string"},"rules":{"type":"string"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["confirmed"]}` |
| `site_published_content_get` | plataforma | read | no | `{"type":"object","properties":{}}` |
| `site_publish` | website | write | sí | `{"type":"object","properties":{"site_id":{"type":"string"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["site_id","confirmed"]}` |
| `admin_catalog` | session | read | no | `{"type":"object","properties":{"q":{"type":"string"},"section":{"type":"string"},"writes_only":{"type":"boolean"},"limit":{"type":"integer"},"offset":{"type":"integer"}}}` |
| `admin_describe` | session | read | no | `{"type":"object","properties":{"names":{"type":"array","items":{"type":"string"}},"name":{"type":"string"}}}` |
| `admin_call` | session | read | no | `{"type":"object","properties":{"name":{"type":"string"},"path_params":{"type":"object"},"query":{"type":"object"},"body":{"type":"object"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"},"calls":{"type":"array","description":"Varias lecturas en una sola llamada (máx 20)","items":{"type":"object","properties":{"name":{"type":"string"},"path_params":{"type":"object"},"query":{"type":"object"}},"required":["name"]}}}}` |


## Campañas (`campaign_tools_v1`)

Todo lo que se hace en `/app/campanas/nueva` y `/app/campanas/{id}` como herramientas:

| Tool | Qué cubre del editor |
|---|---|
| `campaign_get` | Listado, cabecera, Ajustes, Flujo y resumen del diseño; `include`: `layers` (ids `e1`, `e2`… por orden de documento), `html`, `whatsapp_text`, `audience` (solo recuentos), `deliveries` (registro agregado), `ai_models`, `blocks`. |
| `campaign_save` | Crear (con la plantilla de marca o `design_html`) y cambiar nombre, asunto, canales, audiencia, lista manual y ritmo. |
| `campaign_design` | `edit`: las operaciones del creador visual (bloques de la paleta, textos, enlaces, imágenes, estilos del inspector por tamaño, visibilidad, clases, mover, duplicar, borrar, HTML); `ai`: el asistente de diseño con IA; `undo`: deshace el último cambio hecho desde un chat (`campaign_design_revisions`). |
| `campaign_image` | Una imagen ADJUNTA en el chat (ref de la línea `[Adjunto: … · ref …]`) subida al BunnyCDN del restaurante en formato apto para email (PNG/JPEG/GIF ≤400 KB tal cual; si no, JPEG) y colocada en el diseño: sustituye un `<img>` (`element_id`), añade un bloque de imagen (`insert_after`) o solo devuelve la URL (`placement=none`). |
| `campaign_send` | `test` (prueba a un email o teléfono), `send` (envío real) y `resume` (reanudar pausados con `ack`). |
| `campaign_delete` | Borrar la campaña y su registro de envíos. |

- Núcleo compartido en `internal/api/campaign_tools.go`; ajustes, envíos, pruebas, audiencia y borrado reutilizan los handlers del backoffice.
- El diseño y el email se calculan con el código del propio editor en el servidor del backoffice (`POST /bo-tools/campaign-design` y `/bo-tools/campaign-email-compile`, autenticados con `VAULT_KEY`/`BEARER_TOKEN_KEY`; URL en `BACKOFFICE_INTERNAL_URL`), así el email guardado es idéntico al que guardaría el editor.
- Nunca devuelven datos de destinatarios (audiencia y registro solo como recuentos).
- El bot de WhatsApp tiene su gemela `manage_campaign` (contactos y grupos privilegiados): lecturas inmediatas y escrituras por propuesta + `OK <código>` (op `campaign`).
- Imágenes del chat: en Forky la ref del adjunto viaja en el manifiesto del mensaje; en WhatsApp las imágenes recientes de cada chat privilegiado quedan registradas por id de mensaje (`image_ref`, o la más reciente) y se suben al CDN al crear la propuesta, así el `OK` no depende del proveedor.

## Trío admin (todas las operaciones del backoffice) [FORKY-ADMIN-TOOLS-S01]

`admin_catalog` / `admin_describe` / `admin_call` exponen las 516 operaciones de la API `/admin`
del backoffice (mapa generado `internal/api/forky_admin_ops.json`, con la pantalla del backoffice
que usa cada ruta). Regenerar tras cambiar rutas en `server.go`:

```sh
go run ./cmd/forky-admin-map -server internal/api/server.go -pages ../backoffice -o internal/api/forky_admin_ops.json
```

ACL: el catálogo sólo lista las secciones que la persona abre en el panel (`boAuthCanAccessSection`,
importancia de rol para `rolesAdminGate`/root, superadmin), y `admin_call` repite la petición por el
router real (`s.Routes()`) con la cookie `bo_session` de la propia persona (tomada del handshake del
WebSocket, nunca del modelo) y los secretos del servidor: deciden los mismos gates de la ruta
(sección, permisos de TPV/stock, plan, root). Las escrituras requieren `confirmed=true` +
`confirmation_token`. Excluidas: login/logout, WebSockets, subidas de ficheros, `/assistant/*` y las
rutas públicas.

## Convenciones

- **Scope**: todas las tools se ejecutan contra el restaurante activo autenticado
  (`restaurant_id` nunca proviene del modelo). El ownership se aplica siempre en
  SQL (`WHERE restaurant_id=?`).
- **Permisos**: la sección de cada tool se resuelve contra los mismos permisos de
  sección del backoffice (RBAC). Las tools `write` requieren además capacidad de
  escritura en la sección.
- **Confirmación**: toda tool `write` exige `confirmed=true` + `confirmation_token`
  emitido por el backend (one-shot, expiración corta, vinculado a argumentos
  canónicos). `confirmed=false` devuelve `requires_confirmation` con el token.
- **Seguridad**: las tools marcadas como backoffice exigen sesión autenticada;
  las sesiones anónimas del asistente público solo pueden ejecutar
  `restaurant_info` y `restaurant_query`.
- **Límites**: input ≤ 64 KiB; timeout por tool 5s; máx. 6 iteraciones tool por
  turno; filas acotadas por tool (default 100, máx 500); rate-limit por IP.
- **Auditoría**: cada tool call se registra en `assistant_tool_audit`
  (usuario, restaurante, tool, resumen del resultado, error, duración).
