# Native PG port: catalog_stock group 12 (margin_scopes + menu_visibility_admin + admin_menus + special_menu_principales; compile only, NOT runtime-verified, NOT PG-ready)

## Counts (inventory r29: /tmp/villacarmen-bot-20261111/sql-inventory-r29.json, dbmigrate-r12)
- **Repo:** 2472 call sites, 985 native (+32), 226 dynamic. portable_audit_failures is 0.
  - Excluded: 5 helper-internal calls and 3 prepared-stmt calls.
- **Core:** 406/406. **Bookings:** 161/161.
- **catalog_stock:** 418/504 native, 86 remaining (28 dynamic). 19/43 files are done:
  - backoffice_stock_margin_scopes 9/9;
  - menu_visibility_admin 8/8;
  - admin_menus 8/8 (5 dynamic);
  - special_menu_principales 7/7.
  - Ledger: `docs/dbmigrate/native-ledger-catalog.json`.
- **Build:** `go build -p 2 ./...` exits 0 under the inherited cgroup (CPU 200%, 6G). No SQL was run, and no tests, vet, fixtures or Playwright.

## Changes
### backoffice_stock_margin_scopes.go
- **Scope create:** `InsertID` in its tx.
- **Update, bands replace, delete:** portable. The RowsAffected gate on delete is matched rows on both.
- **`ORDER BY FIELD(scope_kind, 'GLOBAL','COMIDA_TYPE','COMIDA_CATEGORY','STOCK_CATEGORY')`:** becomes a CASE over the same 4 enum labels, with `ELSE 0` matching FIELD's 0 for "absent".
- **Monetary/percentage precision:**
  - There is **no SQL division or rounding** in this file; the margin math stays in Go.
  - `decimal(5,2)` pct columns receive Go float64 params, so on both dialects the value rounds to 2 decimals on assignment. This is by review; manual QA is pending.

### menu_visibility_admin.go
- **All 8 sites are portable.**
- **`UPDATE … RowsAffected==0`:** on 0 the source re-checks that the row exists and then continues.
  - PG reports matched rows, so an unchanged row returns 1 and reaches the same continuation.
  - This is an equivalent outcome by review.

### special_menu_principales.go
- **List:** `COALESCE(CAST(alergenos_json AS text),'[]')`.
- **Enable toggle:** the same existence-re-check pattern, so the outcome is equivalent.
- **Add principal:**
  - `INSERT…SELECT MAX(position)+1` gets explicit casts.
  - `ON DUPLICATE KEY UPDATE dish_id = dish_id` becomes `ON CONFLICT (section_id, dish_id) DO UPDATE SET dish_id = <table>.dish_id`, a no-op that keeps the row lock.
  - The concurrent position computation is unlocked on both, as in the source.

### admin_menus.go
- **v2 / legacy-map dish active toggles:**
  - `RowsAffected <= 0` drives the fallback and the "status already set" message.
  - MySQL counts changed rows, so PG adds `AND active IS DISTINCT FROM CAST($1 AS smallint)` to both statements.
  - The MySQL multi-table `UPDATE … JOIN legacy_dish_map` becomes `UPDATE … FROM legacy_dish_map`.
- **Legacy CRUD/search on DIA/FINDE (5 dynamic):**
  - `table` is the fixed "DIA"/"FINDE" literal from the route wrappers.
  - **SOURCE ANOMALY, preserved:** neither table exists in the exported or live schema, so these statements fail on MySQL and PG alike.
  - PG quotes `"DIA"`/`"FINDE"`/`"NUM"`/`"DESCRIPCION"`/`"TIPO"` exactly as written. **No substitute table or meaning was chosen**; a fix needs separate approval.
  - Search LIKE uses `LOWER + COLLATE "C"` (CI gap).

**Identifiers:** none are mixed-case except those quoted legacy names.

## App-semantic gaps (separate ledger, NOT part of the 1117 import-proof count; marker ready:false)
- **Category insert id:** provisional "0 on conflict"; MySQL is unproven.
- **Wine precio float(10,2):** proposal only.
- **Averages:** double-rounding edge.
- **JSON:** nested containment; changed-row equality, text and timestamp QA.
- **Dessert mirror:** INSERT…SELECT id order.
- **New this phase:** pct decimal assignment rounding of Go floats (QA).

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

## Next remaining (r29, 86 sites / 28 dynamic)
| file | remaining | dynamic |
|---|---|---|
| backoffice_vinos.go | 7 | 7 |
| backoffice_stock_import.go | 6 | 0 |
| backoffice_menus.go | 6 | 6 |
| wines_v2.go | 5 | 0 |
| menu_weekday_availability.go | 5 | 2 |
| backoffice_dish_same_day_booking.go | 5 | 0 |
| admin_vinos.go | 5 | 5 |
| stock_digest, special_menu_cta, backoffice_vinos_ws, backoffice_menu_visibility, backoffice_comida_ai | 4 each | … |
| others (12 files) | 1–3 each | … |

