# U4 manual HTTP QA kit (padnat1 scratch, native PG)

Unit pgnative-E. Nothing was run. The server is the QA server on loopback :18080, with outbound HTTP disabled and workers off. Every admin route is under `/api/admin` and needs the QA cookie (`bo_session`) plus `X-Restaurant-Id: 1`. Bodies are in `docs/pgnative/qa-kit/`; copy them into the container, e.g. to /tmp/qa-kit.

## How to call
- JSON:
  ```
  qa-http -X POST -url http://127.0.0.1:18080/api/admin/<route> -cookie-file /tmp/qa-cookie -H 'Content-Type: application/json;X-Restaurant-Id: 1' -n 400 < /tmp/qa-kit/<file>.json
  ```
- Multipart (busybox):
  ```
  wget -q -S -O - --header "Cookie: $(cat /tmp/qa-cookie)" --header 'X-Restaurant-Id: 1' --header 'Content-Type: multipart/form-data; boundary=----qakitU4boundary' --post-file=/tmp/qa-kit/<file>.multipart http://127.0.0.1:18080/api/admin/stock/items/import
  ```
  The cookie only reaches argv inside the container. Never echo it.
- **IDs:** take `{id}` values from the previous response.
- **"dup":** the second body differs only by case, accent or trailing space.
- **Expected results:** native = app.norm/app.norm_ascii folding, so a dup must be 409 (or the handler's own 400/409 message), **never 500 and never a second row**.
- **After each block:** count rows with a read-only `SELECT count(*) … WHERE restaurant_id=1 AND app.norm(col)=app.norm('…')`.

## Writers

| # | Route (POST unless noted) | Body file(s) | Expected |
|---|---|---|---|
| 1a | stock/items/import (preview) | stock-import-1-preview.multipart (CSV: stock-import-1.csv) | 200 `preview:true`, validRows 2 |
| 1b | stock/items/import (`confirm=1`) | stock-import-1-confirm.multipart | 200, 2 items + category "QA Categoría" created |
| 1c | stock/items/import (`confirm=1`) | stock-import-2-confirm.multipart (same names, other case/accent/trailing space) | 400 "No valid rows to import", rows flagged "item already exists"; still 2 items and 1 category. Uses the **fixed** name check (see below). |
| 2 | pos/products/import-confirm | pos-import-confirm.json (set sourceId to an existing comida_items id of rid 1) | 200. Repeat → idempotent: ON CONFLICT (restaurant_id, source_type, source_id), no 2nd product; the POS category is upserted via ux__pos_product_categories |
| 3a | invoices | invoice-create.json (`is_reservation:true`) | 200/201, the row's is_reservation = 1 (boolean→integer cast, fixed in D) |
| 3b | PUT invoices/{id} | invoice-update.json (`is_reservation:false`) | 200, is_reservation = 0, totals updated |
| 3c | invoices | invoice-create-dup-number.json (`qa-inv-ab-1` vs `QA-INV-ÁB-1`) | 200. No unique key on invoice_number in the source or native schema, so a 2nd invoice is allowed on both engines (numbering may reassign the number) |
| 4a | bookings | booking-create.json | 200 + booking id. Confirmation sends are blocked: outbound HTTP is off; check the log |
| 4b | comida/categorias | comida-category-1.json then comida-category-2-dup.json | 200, then 409 "Ya existe una categoria con ese nombre" |
| 4c | comida/platos/categorias | plato-category-1.json then plato-category-2-dup.json | 200, then 409/400 (ux__comida_plato_categories slug, norm_ascii) |
| 4d | menus/dia/dishes | menu-dia-dish.json | 200 (no unique key; a 2nd identical post adds a row, same as MySQL) |
| 5 | stock/categories | stock-category-1.json → stock-category-2-dup.json | 200 → 400 (source status for a duplicate) |
| 6 | stock/warehouses | stock-warehouse.json → stock-warehouse-dup.json (code `qa-wh `) | 200 → 409/400 on ux__stock_warehouses code |
| 7 | stock/suppliers | stock-supplier-1.json → stock-supplier-2-dup.json | 200 → 409 "Supplier already exists" |
| 8 | PUT stock/suppliers/{id}/aliases | stock-supplier-aliases.json (2 aliases equal under norm; set stockItemId/stockUnitId) | 200, **1** alias row (ON CONFLICT ux__stock_supplier_aliases) |
| 9 | stock/vat-rates | stock-vat-1.json → stock-vat-2-dup.json | 200 → 409/400 (ux__stock_vat_rates) |
| 10 | stock/items | stock-item-1.json → stock-item-2-dup-sku.json | 200 → 409/400 (ux__stock_items sku, app.norm) |
| 11 | stock/items/{id}/units | stock-item-unit.json → stock-item-unit-dup.json | 200 → 409/400 (ux__stock_item_units code) |
| 12 | PUT stock/roles/{slug}/permissions (slug=admin) | stock-role-permissions.json, twice | 200 both, idempotent (ON CONFLICT on the raw PK) |
| 13 | pos/categories | pos-category-1.json → pos-category-2-dup.json | 200 → 409/400 (ux__pos_product_categories name) |
| 14 | pos/products | pos-product-1.json → pos-product-2-dup-sku.json (set vatRateId) | 200 → 409/400 (ux__pos_products sku) |
| 15 | pos/tags | pos-tag-1.json → pos-tag-2-dup.json | 200 → 409 "Tag already exists" |
| 16 | pos/packs | pos-pack-1.json → pos-pack-2-dup.json | 200 → 409/400 (ux__pos_packs name) |
| 17 | pos/kitchen/stations | pos-kitchen-station-1.json → …-2-dup.json | 200 → 409/400 |
| 18 | pos/service-periods | pos-service-period-1.json → …-2-dup.json | 200 → 409/400 |
| 19 | pos/modifier-groups | pos-modifier-group-1.json → …-2-dup.json | 200 → 409/400 |
| 20 | pos/customers | pos-customer-1.json → pos-customer-2-dup.json (same email, other case + trailing space) | 200 → 409 CUSTOMER_EXISTS |
| 21 | PUT pos/roles/{slug}/permissions | pos-role-permissions.json, twice | 200 both, idempotent |
| 22 | members | member-1.json → member-2-dup.json (same email, other case) | 200 → 409/400 (ux__restaurant_members email) |
| 23 | roles | role-1.json → role-2-dup.json (`QA-ROL`) | 200 → 409/400. bo_roles.slug is a raw PK in native: **divergence**, a case-different slug may be accepted (by design, see result-pgnative-b) |
| 24 | campanas | campaign-create.json | 200 (draft only; never call /send, /test or /resume) |
| 25 | legal-pages/qa-aviso | legal-page.json, twice | 200 both, upsert on ux__legal_pages (restaurant_id, slug) |
| 26 | tables (entity area) | area-1.json → area-2-dup.json | 200 → **409** (boPremiumDBStatus) |
| 27 | tables (entity table; set area_id) | table-1.json → table-2-dup.json | 200 → 409 |
| 28 | booking-extras | booking-extra-1.json → booking-extra-2-dup.json | 200 → 409/400 (ux__restaurant_booking_extras slug) |
| 29 | config/salons (set floorId) | salon-1.json → salon-2-dup.json | 200 → 409 (IsUniqueViolation) |
| 30 | PUT me/preferences | preferences-1.json → preferences-2-same-key-case.json | 200 both; one row (ON CONFLICT ux__user_preferences pref_key, norm_ascii) holding the value of the 2nd |
| 31 | config/chatgpt-plugin/tokens | chatgpt-token.json | 200. The token appears in the response **once**: do not print it; pipe to /dev/null and check the count of rows |
| 32 | platform/restaurants (superadmin) | platform-restaurant-1.json → platform-restaurant-2-dup.json | 200 → 409/400 (ux__restaurants slug) |

## Fix made while reading (source only)
internal/api/backoffice_stock_import.go, the PG text of the no-SKU existence check: `LOWER(name)=LOWER(?)` → `app.norm(name) = app.norm(?)`. Before, accent variants ("QA Cafe" vs "QA Café") were imported as new rows, while MySQL's ci collation reports them as existing. The server must be rebuilt before row 1c. I did **not** rebuild it in this unit.

## Skip: needs Stripe / WhatsApp / email / outbound network
- invoices/{id}/send, invoices/{id}/send-whatsapp
- campanas/{id}/test, /send, /resume, campanas/ai/chat, campanas/email-validation, campanas/bounces/sync
- members/{id}/whatsapp/verification/*, members/{id}/phone/verification/*, members/{id}/invitation/resend, members/{id}/password-reset/send, members/whatsapp/*
- bookings/{id}/resend-confirmation
- site-builder/domains/register|provision, domains/quote|register|verify
- comida/{tipo}/{id}/image/ai, group-menus-v2/*/image/ai, group-menus-v2/{id}/slider/images/ai
- stock/settings/classify-seasonality and the stock invoice extraction (LLM)
- platform/whatsapp/{id}/renew-qr, platform/subscriptions/{id}/toggle (Stripe)
- every Stripe checkout and booking-checkout route
