# Native PostgreSQL port — core_backoffice group 2 (config / platform / provision / translations / server)

Status: **converted and built** (`go build -p 2 ./...` exit 0, static `dbmigrate-r7` exit 0). The PG text has never run, the semantics are unverified until manual QA, and the server still refuses `DB_DRIVER=postgres`. The MySQL default is unchanged.

## Ledger (`docs/dbmigrate/native-ledger-core.json`, from `sql-inventory-r7.json`)

| | value |
|---|---|
| core_backoffice sites | 403 in 63 files |
| native (group 1 + group 2) | **170** (57 + 113) |
| remaining | **233** (19 dynamic) |
| files fully native | 22 |
| portable audit failures | 0 (`-schema` audit: MySQL-only constructs + 11 mixed-case schema identifiers) |

Group 2 converted all its call sites (113: 105 static + 8 dynamic) in `backoffice_config.go`, `backoffice_config_ai_image/bunny/chatgpt_plugin/minimax.go`, `backoffice_platform.go`, `backoffice_provision.go`, `translations.go`, `bo_app_version.go`, `bunny_config.go`, `chatgpt_plugin_tokens.go`, `restaurant_profile.go`, `backoffice_branding_logo.go` and `server.go`. One exception: **`server.go:1696`** (wines, built from `vinoV2From` fragments) stays for the catalog_stock domain.

## Notable PG decisions (all are in `docs/dbmigrate/native-semantic-notes-core.json`)

- **Quoted identifiers:** PG needs quotes for `"reservationDate"`, `"dailyLimit"`, `"POSTRES"`, `"NUM"`, `"DESCRIPCION"` and `"TIPO"`; unquoted they fold to lowercase and fail. This is caught by the new portable audit.
- **Time zones:**
  - timestamp columns use `now()` or `to_char(… AT TIME ZONE 'UTC', …)`;
  - naive datetime columns (`bo_sessions.expires_at`, `uazapi.connected_at`, `recurring_invoices.next_run_at`) use `(now() AT TIME ZONE 'UTC')` and a plain `to_char`;
  - Go `time.Time` values for naive columns are passed as `.UTC()`.
- **Upserts:** conflict targets are the real unique keys. `restaurant_floors` uses `(restaurant_id, floor_number, scope_key)` (generated column); `ai_image_provider_config` keeps `COALESCE(EXCLUDED.api_key, current)`.
- **Multi-table DELETE:** written with `USING`.
- **Stripe:** `#>>` JSON paths, with a regex-guarded cast instead of `CAST AS UNSIGNED`.
- **MRR division:** `ROUND(…, 6)`, matching MySQL's decimal scale+4.
- **Insert ids:** `InsertID` for the restaurant and user inserts on the platform.

## INSERT IGNORE: equivalence NOT claimed

Sites:
- group 1 bootstrap admin;
- `platform:483`;
- `provision:42`;
- provision seeds for `restaurant_branding`, `restaurant_reservation_defaults`, `restaurant_info`, `restaurant_bot_ai_config` and `restaurant_page_visibility`.

The PG form reproduces two of IGNORE's skips explicitly: duplicate keys through `ON CONFLICT DO NOTHING`, and FK failures through `EXISTS` on every parent, with each value bound once in a `VALUES` CTE.

**Pending delta:** MySQL IGNORE truncates over-length strings (`role` varchar(32) taken from the request at `platform:483`, `brand_name`), while PG rejects them. This is recorded as pending. It is not accepted on the user's behalf.

Other open deltas:
- JSON-null Stripe fields come out as `'0'/''` on PG.
- The ON UPDATE same-value gap applies to upserts.
- The CI/PAD SPACE collation gap is still open.

## Next group (core_backoffice group 3): technical sheets

The `backoffice_technical_sheet*.go` files (~90 sites, of which ~13 are dynamic, `_link.go` alone has 7) are next. After that:
- **group 4:** labour cost, production labour, hours / hour_split / hour_percentages, occupancy, calendar, affluence, dashboard;
- **group 5:** modifications, events, widget, public_unsubscribe, n8n, ad_tools, accounting exports and the rest.

The app is NOT PostgreSQL-ready: 2304 sites remain across all domains.

