# Native PG port: catalog_stock group 1 (backoffice_stock.go CRUD/movements; compile only, NOT runtime-verified, NOT PG-ready)

## Counts (inventory r17: /tmp/villacarmen-bot-20261111/sql-inventory-r17.json)
- **Repo (extractor):** 2478 call sites, 627 native, 289 dynamic. portable_audit_failures is 0.
- **Extractor shift (not hidden):**
  - Two INSERT…SELECT sites (unit create, count-sheet create) now call the new helper `sqldb.InsertSelectID` with explicit `nativeSQL` variants.
  - The extractor therefore attributes their Exec/QueryRow to `internal/sqldb/sqldb.go:104/113` (helper, reported as `native:null`).
  - They **are** ported and are counted manually. That makes 629 native in effect.
- **catalog_stock:** 506 original sites (extractor shows 504 + 2 manual). 62 native, 444 remaining (86 dynamic). 0/43 files are fully done.
  - Ledger: `docs/dbmigrate/native-ledger-catalog.json`.
- **backoffice_stock.go:** 69 original sites. **62 native**, **7 remaining**.
  - The remaining 7 are the dashboard/analytics block at lines 1485, 1492, 1532, 1564, 1621, 1698 and dynamic 1783.
  - They are left for the next group because they contain SUM/avg-cost division and GROUP BY aggregation that need their own scale and nondeterminism review.
- **Build:** `go build -p 2 ./...` exits 0 under the inherited cgroup (CPU 200%, 6G). No SQL was run, and no tests, vet, fixtures or Playwright.

## Converted subgroup: warehouses, categories, items, units, targets, transfers, counts, item movements, settings, manual movements
- **Changed-rows contract** (warehouse/category/item edit with RowsAffected==0 → 404):
  - MySQL without clientFoundRows returns 0 for an unchanged save. That is source behaviour, kept including its quirk.
  - The PG UPDATE adds an `IS DISTINCT FROM` check over the full SET tuple, reusing `$n` params:
    - text uses `COLLATE "C"`, matching MySQL's binary record compare;
    - enums are cast.
- **Insert ids:** `sqldb.InsertID` (RETURNING) for warehouse, category, item and manual movement.
  - New `sqldb.InsertSelectID` for the INSERT…SELECT creates: no row means n=0, the same 404 path.
- **INSERT…SELECT params get explicit PG casts** (integer/bigint/text/numeric/smallint, plus the enum literal `CAST('STORAGE' AS "enum__stock_warehouses__type")`).
  - text, not varchar(n), so over-length values still **error** as in MySQL strict mode instead of being truncated.
- **stock_levels "ensure row" upserts:**
  - MySQL's no-op ON DUPLICATE KEY UPDATE becomes `ON CONFLICT … DO UPDATE SET stock_item_id=EXCLUDED.stock_item_id`. It keeps the existing-row lock, and the plan ON UPDATE trigger skips unchanged rows.
  - The target upsert keeps `version=stock_levels.version+1`.
  - Settings upsert: `ON CONFLICT (restaurant_id)`, with `CAST(? AS jsonb)`.
- **Scalar selects:** `EXISTS(...)` and `(a OR b)` scanned into int become `CASE WHEN … THEN 1 ELSE 0 END`.
- **JSON:** `JSON_OBJECT()` becomes `'{}'::jsonb`, cast to text (JSON render class).
- **Time:**
  - `deleted_at`/`closed_at` NOW() become `date_trunc('second', statement_timestamp())`.
  - Movement list DATE_FORMAT …Z becomes `to_char(…'YYYY-MM-DD"T"HH24:MI:SS"Z"')` (naive datetime).
- **Dynamic sites reviewed as full compositions:**
  - item list COUNT and data, built from fixed fragments plus a sortSQL whitelist, rebound once;
  - movement list COUNT and data, with validated type/date filters.
  - LIKE uses `LOWER(col) COLLATE "C"` with Go-lowered patterns, so search is case-insensitive but **accent-sensitive** (CI search gap).
- **Inventory logic and money/quantity Go arithmetic are unchanged:** FOR UPDATE locks, allow-negative checks and tx boundaries.

## Semantic gaps (pending)
- **CI/accent:** LIKE search and `ORDER BY name` collation ordering.
- **Numeric rounding:** float64 parameters into numeric(18,4)/(18,6) round on assignment (by review). Needs manual QA.
- **Duplicate detection** for the idempotency key matches on the lowercase substring "duplicate" in the PG error text (by review).
- **JSON** text rendering.
- **ON UPDATE / defaults:** the time semantics of the plan ON UPDATE trigger and the DEFAULTs.
- **Range narrowing:** unsigned bigint ids are mapped to bigint (190-column class).
- **Unchanged:**
  - 4 NOT VALID FKs covering 731 orphans.
  - 1117 pending, ready:false.
  - The recent_conversations view.
  - NUL QA.
  - PAD SPACE.
  - FULLTEXT is unsolved. The production stopword list was received (36 words, orq file), but there is no implementation yet.
  - Source anomalies.

MySQL stays the default and the server still refuses PG. No DB writes, activation, providers, payments, email, WhatsApp, sends, .env/proof edits, commits or deploys.

## Next bounded files
1. **backoffice_stock.go analytics remainder (7):**
   - dashboard counts;
   - low-stock;
   - expiring lots (MIN/SUM + correlated subquery);
   - valuation (`SUM(qty*avg_cost)/SUM(qty)` division scale);
   - reorder list;
   - dynamic 1783.
   - Requires a GROUP BY review under no ONLY_FULL_GROUP_BY.
2. **backoffice_stock_documents.go (34)** and **backoffice_stock_recipes.go (29).**
3. Then **backoffice_group_menus_v2.go (70)**, etc. Per-file counts are in the ledger.

