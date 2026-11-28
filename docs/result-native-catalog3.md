# Native PG port: catalog_stock group 3 (compile only, NOT runtime-verified, NOT PG-ready)

## Counts (inventory r20: /tmp/villacarmen-bot-20261111/sql-inventory-r20.json, dbmigrate-r9 canonical basis)
- **Repo:** 2475 call sites, 699 native (+29 vs r19), 284 dynamic, 5 helper-internal calls excluded. portable_audit_failures is 0.
- **Core:** 406/406. **Bookings:** 161/161.
- **catalog_stock:** 132/506 native, 374 remaining (85 dynamic). 3/43 files are done:
  - backoffice_stock 69/69;
  - backoffice_stock_documents 34/34;
  - backoffice_stock_recipes 29/29.
  - Ledger: `docs/dbmigrate/native-ledger-catalog.json`.
- **Build:** `go build -p 2 ./...` exits 0 under the inherited cgroup (CPU 200%, 6G). No SQL was run, and no tests, vet, fixtures or Playwright.

## 1. Audit of `COALESCE(col,'')` in earlier ported PG variants (89 ported files)
- **Scope:** 136 `COALESCE(col,'')` occurrences, each checked against the schema types of every table that has that column name.
  - Ambiguous names were resolved per query table:
    - `restaurant_uazapi_instances.status` varchar(32);
    - `bo_user_restaurants.role` varchar(32);
    - `pos_payments.provider` varchar(64);
    - `stock_warehouses.code` varchar(32);
    - `comida_items.source_type` varchar(16);
    - `bookings.status` varchar(20).
- **Result:** all are text-typed, so they are valid and equivalent on PG.
  - The only enum/numeric/JSON cases were already explicitly cast to text: stock waste_reason ×2, unit_cost and total_cost (last phase), plus the two allergen JSON columns in backoffice_technical_sheet_link.
  - **Fixed this phase: 0** (none found). This was not a blanket rewrite.

## 2. PG timestamp read zone (internal/sqldb/OpenPostgres)
- **Source read:** pgx v5.11.0 `pgtype/timestamptz.go`.
  - Decoded timestamptz uses `time.Local` unless `TimestamptzCodec.ScanLocation` is set ("loc := time.Local"; binary path uses time.Unix).
  - `timestamp` (no tz) and `date` decode in UTC.
  - The process runs Europe/Madrid, while MySQL (session UTC, DSN without loc) returned UTC.
- **Fix:**
  - The PG session uses `RuntimeParams["timezone"]="UTC"`, which covers to_char, ::date and ::text of timestamptz.
  - `stdlib.OptionAfterConnect` registers `TimestamptzCodec{ScanLocation: time.UTC}` on every connection. The instant is unchanged; the location now matches the source, so JSON and Format rendering do too.
  - The MySQL path is unchanged.
- **This is verified only by reading the code and compiling.** Manual JSON timestamp QA (rendered offsets) is needed before cutover.

## 3. backoffice_stock_recipes.go (29)
- **Recipe create:** `InsertID`.
  - A duplicate on `uq_stock_recipe_output` (generated active_output_item_id) gives the same 400.
- **Recipe update / pricing / deactivate:** `version=version+1` always changes the row, so changed rows equal matched rows (RowsAffected 404 gating preserved).
- **Labour INSERT…SELECT:** explicit casts (integer/bigint/numeric/text), so assignment to decimal(8,2)/varchar(500) keeps MySQL's rounding and strict-length errors. RowsAffected==0 still means "invalid member".
- **Production order:**
  - `InsertID`.
  - Idempotent replay uses `sqldb.IsUniqueViolationOn(err, "uq_stock_production_idempotency", "ix__stock_production_orders__uq_stock_production_idempotency")`, the real key from plan.sql.
  - The other unique key is (restaurant_id, auto id), which cannot collide.
- **stock_levels ensure-row upserts** (2) keep their row lock: `ON CONFLICT … DO UPDATE SET stock_item_id=EXCLUDED.stock_item_id`.
- **Scalar and string expressions:**
  - VAT `EXISTS` becomes `CASE 0/1`.
  - `CONCAT(first,' ',last)` becomes `||`, with the same NULL propagation.
  - The `source` enum is cast to text.
- **Unchanged:** BOM cycle validation, quantities (Go arithmetic), allow-negative checks, movements and tx boundaries.

## Semantic risks (pending, none waived)
- **Timestamps:** manual QA of JSON timestamp rendering (see 2).
- **Search and rendering:** CI/accent LIKE and ordering; JSON text rendering.
- **Numerics:** float→numeric assignment rounding QA.
- **Schema time semantics:** ON UPDATE (136 tables) and DEFAULT (353) time semantics.
- **Unchanged:**
  - 190 range narrowings.
  - The recent_conversations view.
  - NUL QA.
  - 4 NOT VALID FKs covering 731 orphans.
  - 1117 pending, ready:false.
  - PAD SPACE.
  - FULLTEXT is unsolved.
  - Source anomalies.

MySQL stays the default and the server still refuses PG. No DB writes, activation, providers, payments, email, WhatsApp, sends, .env/proof edits, commits or deploys.

## Next bounded group: backoffice_group_menus_v2.go (70 sites, 4 dynamic), split in two coherent halves
- **Part A (~35, lines 174–1797):**
  - belongs/list/create draft;
  - sections from snapshot and load sections/dishes;
  - legacy snapshot sync and Get;
  - patch basics/menu type;
  - put sections, section visibility, delete and annotations.
- **Part B (~35, lines 1858–3655):**
  - put/patch section dishes;
  - dishes catalog search/upsert;
  - publish/toggle;
  - image uploads;
  - delete;
  - special menu image;
  - slider get/patch/upload/delete/reorder;
  - AI slider job;
  - resolve postres.
- **Then:** ws (10), items (8, 1 dynamic) and special_sections (19, 4 dynamic).

