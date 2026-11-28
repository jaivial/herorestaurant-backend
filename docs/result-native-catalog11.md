# Native PG port: catalog_stock group 11 (group_menus.go + menu_section_dessert_source.go + backoffice_group_menus.go; compile only, NOT runtime-verified, NOT PG-ready)

## Counts (inventory r28: /tmp/villacarmen-bot-20261111/sql-inventory-r28.json, dbmigrate-r12)
- **Repo:** 2472 call sites, 953 native (+35), 231 dynamic. portable_audit_failures is 0.
  - Excluded: 5 helper-internal calls and 3 prepared-stmt calls.
- **Core:** 406/406. **Bookings:** 161/161.
- **catalog_stock:** 386/504 native, 118 remaining (33 dynamic). 15/43 files are done:
  - **group_menus.go 14/14 (6 dynamic)**;
  - **menu_section_dessert_source.go 11/11 (1 dynamic)**;
  - **backoffice_group_menus.go 10/10 (2 dynamic)**.
  - Ledger: `docs/dbmigrate/native-ledger-catalog.json`.
- **Build:** `go build -p 2 ./...` exits 0 under the inherited cgroup (CPU 200%, 6G). No SQL was run, and no tests, vet, fixtures or Playwright.

## Changes
### group_menus.go
- **Legacy menu list (3 fixed query variants):**
  - `ORDER BY created_at DESC` gets an explicit `NULLS LAST`, since created_at is nullable and MySQL sorts NULL last in DESC.
  - The public display list's `created_at ASC` gets `NULLS FIRST`.
  - Each query is rebound once.
- **CRUD:**
  - The create INSERT uses `InsertID`.
  - Update, toggle and delete are portable; RowsAffected is not gated there.
  - `price` is scanned into a string. pgx v5.11 `NumericCodec.DecodeDatabaseSQLValue` returns the numeric text with scale kept (e.g. `12.50`), the same as MySQL's decimal bytes (driver-source review).
- **Section-kinds and dishes enrichment (dynamic, Sprintf IN lists):**
  - JSON columns ×3 are cast to text.
  - `d.title_snapshot LIKE CONCAT(ci.nombre,' (+%')` becomes `d.title_snapshot COLLATE "C" LIKE (ci.nombre || ' (+%')`. This is case-sensitive on PG (CI gap).
  - Each query is rebound once.
- **Platos list:** `CAST(alergenos_json AS text)`.

### menu_section_dessert_source.go
- **POSTRES carrier:** the lookups use the `menus.legacy_source_table = 'POSTRES'` string value; this is **not** the POSTRES table.
  - The menu and section INSERTs use `InsertID`.
- **Dessert dishes read:** JSON is cast to text.
- **dessert_source update and mirror delete:** portable.
- **Mirror copy INSERT…SELECT:**
  - Explicit casts on the select-list params; json is copied to jsonb.
  - `ORDER BY src.position, src.id` is kept.
  - **New pending:** PG identity values usually follow the SELECT order but this is not formally guaranteed. Positions are copied explicitly; only id order could differ.
- **Mirror section ids:** the optional IN list is rebound once.

### backoffice_group_menus.go
- **List (dynamic fixed status filter):** `created_at DESC NULLS LAST`.
- **Rice names:** `LOWER(TRIM(categoria)) COLLATE "C" LIKE 'arroz%'` (CI/accent gap).
- **CRUD:** `InsertID` on create. Delete keeps its RowsAffected 0 → "Menu not found" gate, using matched rows on both.

**Identifiers:** no mixed-case identifiers in these files. No DIA/FINDE or `menu_dishes_catalog.name` references; the known anomalies elsewhere are unchanged and unfixed.

## App-semantic gaps (separate ledger, NOT part of the 1117 import-proof count; marker ready:false)
- **Category ODKU insert id:** provisional "0 on conflict"; MySQL is unproven.
- **Wine precio float(10,2):** proposal only.
- **AVG/quotient double-rounding edge:** usage, plus supplier AVG ×2.
- **JSON_CONTAINS** nested-array semantics.
- **JSON:** changed-row equality, text and timestamp QA.
- **New:** id order of the INSERT…SELECT mirror copy.
- **New:** case-sensitive LIKE in the dish enrichment and the rice categoria filter. This is part of the CI gap.

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

## Next remaining files (r28, 118 sites / 33 dynamic)
| file | remaining | dynamic |
|---|---|---|
| backoffice_stock_margin_scopes.go | 9 | 0 |
| menu_visibility_admin.go | 8 | 0 |
| admin_menus.go | 8 | 5 |
| special_menu_principales.go | 7 | 0 |
| backoffice_vinos.go | 7 | 7 |
| backoffice_stock_import.go | 6 | 0 |
| backoffice_menus.go | 6 | 6 |
| wines_v2.go | 5 | 0 |
| menu_weekday_availability.go | 5 | 2 |
| backoffice_dish_same_day_booking.go | 5 | 0 |
| admin_vinos.go | 5 | 5 |
| others (17 files) | 1–4 each | … |

