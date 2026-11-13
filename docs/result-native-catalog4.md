# Native PG port: catalog_stock group 4 (backoffice_group_menus_v2.go part A; compile only, NOT runtime-verified, NOT PG-ready)

## Counts (inventory r21: /tmp/villacarmen-bot-20261111/sql-inventory-r21.json, dbmigrate-r9 canonical basis)
- **Repo:** 2475 call sites, 728 native (+29 vs r20's 699), 281 dynamic, 5 helper-internal calls excluded. portable_audit_failures is 0.
- **Core:** 406/406. **Bookings:** 161/161.
- **catalog_stock:** 161/506 native, 345 remaining (82 dynamic). 3/43 files are done.
  - Ledger: `docs/dbmigrate/native-ledger-catalog.json`.
- **backoffice_group_menus_v2.go:** 29/70 native (all of part A), **41 remaining (1 dynamic)** at current lines 1932–3729.
- **Build:** `go build -p 2 ./...` exits 0 under the inherited cgroup (CPU 200%, 6G). No SQL was run, and no tests, vet, fixtures or Playwright.

## Changed functions (part A)
- **`ensureBOMenuV2Belongs`:** portable.
- **`handleBOGroupMenusV2List` (dynamic):** the composed SELECT (fixed text plus the fixed `is_draft = 0` filter) is wrapped and rebound once.
- **`handleBOGroupMenusV2CreateDraft`:**
  - The menus INSERT uses `sqldb.InsertID`. The MySQL LastInsertId error was ignored; a RETURNING error now surfaces.
  - The default-section inserts are portable.
- **`ensureBOMenuV2SectionsFromSnapshot`:**
  - COUNT and legacy-column reads are portable.
  - The `insertSection` helper returns `sqldb.InsertID`.
  - The dish insert uses `CAST(? AS jsonb)` for allergens_json.
- **`loadBOMenuV2SectionsWithDishes`, `loadBOMenuV2SectionDishes`, `loadBOMenuV2DishByID`:**
  - Read `CAST(d.allergens_json AS text)` and `CAST(cta_json AS text)`.
  - The other COALESCE fallbacks are all text/smallint columns, so they stay valid.
  - The comments inside the SQL are valid on PG.
- **`syncBOMenuV2LegacySnapshot`:** the UPDATE of longtext columns is portable.
- **`handleBOGroupMenusV2Get`, `GetSectionDishes`, `PatchBasics`, `PatchMenuType`:** portable. Their RowsAffected is not checked.
- **`handleBOGroupMenusV2PutSections`:**
  - The tx ownership read and the section updates are portable.
  - The new-section INSERT uses `InsertID`.
  - The composed `DELETE … NOT IN (?, …)` is rebound once.
- **`handleBOGroupMenusV2PatchSectionVisibility` (dynamic)** keeps the changed-rows contract:
  - MySQL returns 0 for an unchanged save, so the response is "Section not found" (source quirk, kept).
  - PG appends `AND ROW(cols) IS DISTINCT FROM ROW(CAST(? AS smallint), CAST(? AS text))` over exactly the SET columns, with the args duplicated in order.
  - web_placement is compared byte-wise with `COLLATE "C"`.
- **`handleBOGroupMenusV2DeleteSection`:** the DELETE is portable; RowsAffected uses matched rows on both.
- **`handleBOGroupMenusV2PatchSectionAnnotations`:** keeps the changed-rows contract. `annotations_json COLLATE "C" IS DISTINCT FROM CAST($1 AS text)` (longtext).
- **`propagateSectionDishToCatalog`:** uses `allergens_json = CAST(? AS jsonb)`.

**What was checked:**
- types, unique keys and FKs (sections and catalog have only a PK; dishes have `uq_group_menu_dishes_restaurant_ui_data`, unused here);
- strict-mode assignment;
- NULL ordering (no nullable ORDER BY columns in part A);
- NOW() (none in part A);
- decimal arithmetic (none in SQL).

**Source anomalies:** no part A SQL references `menu_dishes_catalog.name` or the DIA/FINDE tables. Part B (catalog search/upsert, resolve postres) must be checked for them. If present, they are to be preserved and documented, not fixed.

## Unresolved risks (pending, none waived)
- **JSON:** render class for allergens/cta/annotations JSON text.
- **ON UPDATE:** time semantics for updated_at/modified_at (the plan trigger uses transaction time and rounding; 136 tables, 353 defaults).
- **CI/accent:** ordering and comparison of section and menu titles.
- **Timestamps:** manual JSON timestamp QA (UTC ScanLocation from code reading only).
- **Unchanged:**
  - 190 range narrowings.
  - The recent_conversations view.
  - NUL adapter QA.
  - 4 NOT VALID FKs covering 731 orphans.
  - 1117 pending, ready:false.
  - PAD SPACE.
  - FULLTEXT.
  - Source anomalies.

MySQL stays the default and the server still refuses PG. No DB writes, activation, providers, payments, email, WhatsApp, sends, .env/proof edits, commits or deploys.

## Next coherent remainder: backoffice_group_menus_v2.go part B (41 sites, 1 dynamic)
- **Section dishes:** PutSectionDishes (≈1932–2120) and PatchSectionDish (≈2146–2344).
- **Dishes catalog:** search and upsert (≈2436–2531; check for the name/title anomaly).
- **Menu lifecycle:** publish (2548–2611), toggle active (2630–2664) and delete.
- **Images:** section dish, menu preview and special menu image uploads.
- **Slider:** get, patch, upload, delete, reorder, AI job and fail (≈3005–3633).
- **Resolve postres** (3707–3729; check for DIA/FINDE).

This could be split again (~20 + ~21) if review depth needs it. After that come ws (10), items (8) and special_sections (19).

