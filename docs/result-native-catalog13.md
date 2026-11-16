# Native PG port: catalog_stock group 13 (wines/menus: backoffice_vinos, backoffice_menus, wines_v2, menu_weekday_availability, admin_vinos; compile only, NOT runtime-verified, NOT PG-ready)

## Counts (inventory r30: /tmp/villacarmen-bot-20261111/sql-inventory-r30.json, dbmigrate-r12)
- **Repo:** 2472 call sites, 1013 native (+28), 206 dynamic. portable_audit_failures is 0.
  - Excluded: 5 helper-internal calls and 3 prepared-stmt calls.
- **Core:** 406/406. **Bookings:** 161/161.
- **catalog_stock:** 446/504 native, 58 remaining (8 dynamic). 24/43 files are done:
  - backoffice_vinos 7/7 (7 dynamic);
  - backoffice_menus 6/6 (6 dynamic);
  - wines_v2 5/5;
  - menu_weekday_availability 5/5 (2 dynamic);
  - admin_vinos 5/5 (5 dynamic).
  - Ledger: `docs/dbmigrate/native-ledger-catalog.json`.
- **Build:** `go build -p 2 ./...` exits 0 under the inherited cgroup (CPU 200%, 6G). No SQL was run, and no tests, vet, fixtures or Playwright.

## Changes
### backoffice_vinos.go
- **List/detail:**
  - `vinoV2NumSQL()` (bigint, range class).
  - has_foto becomes CASE 0/1; `production_type` is cast to text.
  - The reviewed `vinoV2From`/`vinoV2WhereLegacyNumJoined` fragments are used; each query is rebound once.
- **Foto updates and tipo lookups:** whole composed statements wrapped with portableSQL.
- **Wine PATCH (dynamic SET)** keeps the changed-rows "Wine not found" gate via `pgChangedRowsPredicate(…, winesV2PGTypes)`.
  - precio is compared as `real` against `CAST(? AS real)`.
  - **Because of the float(10,2) gap**, a value MySQL rounds to "equal" can compare as distinct on PG. The proposal is still pending; round equivalence is **not** claimed.

### admin_vinos.go
- Active, field and foto UPDATEs and the tipo lookup: whole composed statements wrapped once.
- RowsAffected is not gated in this file.

### wines_v2.go
- **Insert:** `INSERT…SELECT MAX(position)+1` gets explicit casts.
  - `precio` uses `CAST(? AS real)`, with **no D=2 rounding**; this is the float(10,2) gap, proposal only.
  - `InsertID`: the aggregate row always exists.
- **legacy_wine_map:** next-num and insert are portable.
- **Legacy delete:** a **partial-wrap bug was found and fixed**. The generator had produced `portableSQL(`…WHERE `)+vinoV2WhereLegacyNum`, which would leave that fragment's `?` unbound on PG. The whole statement is now wrapped.
  - A grep audit of all ported files found no other `portableSQL/nativeSQL(`…`)+ident` partial wraps.

### menu_weekday_availability.go
- **Upsert:** `ON CONFLICT (restaurant_id, menu_id, weekday)`.
- **Menu lists ×2:**
  - `COALESCE(price,'')` on decimal becomes `COALESCE(CAST(price AS text),'')`. PG would reject `''` for numeric; MySQL returns a string.
  - `modified_at DESC NULLS LAST` (nullable).
  - Each query is rebound once.

### backoffice_menus.go (legacy DIA/FINDE)
- **SOURCE ANOMALY, preserved:** `table` is the fixed "DIA"/"FINDE" route literal and neither table exists. All 6 statements fail on MySQL and PG alike.
- PG quotes `"DIA"`/`"FINDE"`/`"NUM"`/`"DESCRIPCION"`/`"TIPO"` as written. **No substitute table.**
- **No changed-rows predicate** for the patch and set-price gates, because the column types cannot be grounded.
  - The source's set-price `RowsAffected==0 → INSERT` would duplicate PRECIO rows on an unchanged MySQL save. It is unreachable while the tables are absent.
  - This is documented, not fixed.

## App-semantic gaps (separate ledger, NOT part of the 1117 import-proof count; marker ready:false)
- **Category insert id:** provisional "0 on conflict".
- **Wine precio float(10,2):** round/range proposal only. This now also affects the wine PATCH changed-row comparison and the wine insert.
- **Averages:** double-rounding.
- **JSON:** nested containment; changed-row/text/timestamp QA.
- **Dessert mirror:** id order.
- **Percentage floats:** rounding QA.

**Unchanged:**
- CI/accent/PAD SPACE.
- FULLTEXT.
- 190 range narrowings.
- ON UPDATE (136) / DEFAULT (353) time semantics.
- The recent_conversations view.
- NUL adapter QA.
- 4 NOT VALID FKs covering 731 orphans.
- Source anomalies.

Compilation is not equivalence. MySQL stays the default and the server still refuses PG. No DB writes, activation, providers, payments, email, WhatsApp, sends, .env/proof edits, commits or deploys.

## Next remaining (r30: 58 sites / 8 dynamic, 19 files)
| file | remaining | dynamic |
|---|---|---|
| backoffice_stock_import.go | 6 | 0 |
| backoffice_dish_same_day_booking.go | 5 | 0 |
| stock_digest.go | 4 | 0 |
| special_menu_cta.go | 4 | 0 |
| backoffice_vinos_ws.go | 4 | 4 |
| backoffice_menu_visibility.go | 4 | 0 |
| backoffice_comida_ai.go | 4 | 0 |
| special_menu_group_booking.go | 3 | 0 |
| special_date_menu_sections.go | 3 | 0 |
| menu_public_seo_handlers.go | 3 | 1 |
| backoffice_stock_reconciliation.go | 3 | 0 |
| backoffice_stock_margin_targets.go | 3 | 1 |
| menu_section_cta.go | 2 | 0 |
| menu_public_seo.go | 2 | 0 |
| backoffice_vinos_cutout.go | 2 | 2 |
| backoffice_stock_permissions.go | 2 | 0 |
| backoffice_comida_upload.go | 2 | 0 |
| group_menus_party_size.go | 1 | 0 |
| backoffice_comida_counts.go | 1 | 0 |

These 58 sites could plausibly close the domain in one or two checkpoints.

