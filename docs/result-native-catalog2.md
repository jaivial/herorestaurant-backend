# Native PG port: catalog_stock group 2 (compile only, NOT runtime-verified, NOT PG-ready)

## Inventory attribution fixed (canonical basis)
- **Extractor fix:** new extractor `/tmp/villacarmen-bot-20261111/dbmigrate-r9` (`internal/dbmigrate/inventory.go`).
  - It recognises `sqldb.InsertSelectID` as a call-site method by AST callee name. The query is argument 3, as for `InsertID`, so the site is attributed at the caller with its `nativeSQL`/`portableSQL` wrapper.
  - Exec/QueryRow calls **inside the `internal/sqldb` helper package** are now excluded from the site count and reported as `helper_internal_calls_excluded` (5). They execute caller-supplied, already-counted queries.
  - No manual overrides.
- **Counting basis:**
  - Canonical r18/r19 counts only source call sites (2475).
  - r16 (2478) included 3 helper-internal calls, so its canonical count was 2475.
  - r17 (2478, 627 native) included 5 helper-internal calls and lost the 2 InsertSelectID callers.
  - The r17 ledger's "629" was the manual figure. It now equals the tool figure in r18 (629 native before this group).
  - Historical r16/r17 inventories and ledgers are untouched.
- **Idempotent replay** (stock movement insert):
  - The `strings.Contains(err,"duplicate")` check is replaced by the new `sqldb.IsUniqueViolationOn(err, "uq_stock_movement_idempotency", "ix__stock_movements__uq_stock_movement_idempotency")`.
  - It matches MySQL 1062 naming that key, or PG 23505 with the plan constraint name. No other errors are swallowed.
  - The table has only the auto-id PK and that unique key, so on MySQL this is the same set of real cases, narrower only for impossible ones.
  - Not yet applied to backoffice_stock_recipes.go:500, which is still unported and has the same pattern.

## Counts (inventory r19: /tmp/villacarmen-bot-20261111/sql-inventory-r19.json)
- **Repo:** 2475 call sites, 670 native (+41 vs r18's 629), 284 dynamic. 5 helper-internal calls excluded. portable_audit_failures is 0.
- **Core:** 406/406. **Bookings:** 161/161.
- **catalog_stock:** 103/506 native, 403 remaining (85 dynamic). 2/43 files are done: **backoffice_stock.go 69/69** and **backoffice_stock_documents.go 34/34**.
  - Ledger: `docs/dbmigrate/native-ledger-catalog.json`.
- **Build:** `go build -p 2 ./...` exits 0 under the inherited cgroup (CPU 200%, 6G). No SQL was run, and no tests, vet, fixtures or Playwright.

## Converted (41 sites)
### backoffice_stock.go analytics (7)
- **Coverage %:**
  - The source scale is 4 (SUM of decimal(18,4)) plus div_precision_increment 4 (production-verified), so the quotient has scale 8, then ×100.
  - PG: `ROUND(…/NULLIF(…,0), 8)*100`.
  - LEAST/GREATEST args are COALESCEd, so PG's NULL-ignoring LEAST is never reached.
- **Valuation and items export average cost:**
  - SUM(qty(18,4)×avg(18,6)) has scale 10. Divided by SUM(qty), the quotient has scale 14.
  - PG: `ROUND(…, 14)`.
  - Export rows with no stock_levels yield NULL avg, which is a float scan error on **both** dialects (source behaviour, kept).
- **Expiring:**
  - Under no ONLY_FULL_GROUP_BY, the correlated subquery uses the ungrouped `m.restaurant_id`.
  - PG adds it to GROUP BY. WHERE fixes it to one value, so the groups are identical and nothing is ambiguous.
  - The outer `MIN(m.occurred_at)` inside the subquery is valid PG.
  - NOW()/DATE_ADD become statement-time UTC naive plus `make_interval(days => CAST(? AS integer))`.
- **Valuation/export GROUP BY i.id:** this is a PK functional dependency, so PG accepts `i.restaurant_id` in the subquery.
- **Movements CSV export (dynamic):**
  - Head and where fragments are sqlFragment'd and rebound once.
  - The 90-day window becomes `- interval '90 days'` from statement time.
  - `COALESCE(decimal/enum,'')` becomes `COALESCE(CAST(col AS text),'')` (PG rejects `''` for numeric/enum).
  - DATE_FORMAT becomes to_char.
  - The same enum-COALESCE fix was applied to the movement list fragment ported last phase.

### backoffice_stock_documents.go (34)
- **Scan insert:**
  - `IF(?='',NULL,?)` becomes `CASE WHEN CAST(? AS text)='' …`, with the same parameter order.
  - `NULLIF(?,'')` into date becomes `CAST(NULLIF(CAST(? AS text),'') AS date)`.
  - raw_extraction is `CAST(? AS jsonb)`.
  - InsertID.
- **Supplier alias match:** `ORDER BY (supplier_code=?) DESC NULLS LAST` (MySQL sorts NULL last in DESC).
- **Lists:** `file_path … IS NULL` boolean becomes CASE 0/1. `JSON_OBJECT()` becomes `'{}'::jsonb` cast to text.
- **Review header and line UPDATEs** (RowsAffected==0 → 409/404, source changed-rows contract):
  - `IS DISTINCT FROM` with text `COLLATE "C"`.
  - Numerics cast to the column precision (18,4/18,6/18,2), so values that MySQL rounding makes equal still count as unchanged.
  - The enum is cast.
- **Confirm path:**
  - FOR UPDATE.
  - The stock_levels ensure-row no-op upsert keeps its row lock.
  - The purchase movement and the avg-cost UPDATE are unchanged; the Go arithmetic is untouched.
  - The supplier alias upsert is `ON CONFLICT (restaurant_id,supplier_name,supplier_code,normalized_description)`. NULL supplier_code is distinct in both, as in MySQL.
- **Recipe OCR insert:** InsertID.
- **Original-file delete:** statement-time timestamp.
- **No business meaning changed:** confirm status gating, duplicate-confirm response, costs and quantities are as in the source.

## New or confirmed deltas (pending, none waived)
- **timestamptz scanned into `time.Time`** (document created_at) — **new global check:**
  - MySQL parseTime with loc UTC gives UTC in JSON.
  - The pgx location of decoded values is not verified, so the JSON offset rendering may differ.
- **Prior domains not yet re-audited for the `COALESCE(enum/decimal, '')` pattern** (audit item).
- **Other:**
  - JSON render class.
  - CI/accent LIKE and ordering.
  - float→numeric assignment rounding (QA).
  - ON UPDATE/defaults time semantics.
  - 190 ranges.
- **Unchanged:**
  - 4 NOT VALID FKs covering 731 orphans.
  - 1117 pending, ready:false.
  - The recent_conversations view.
  - NUL QA.
  - PAD SPACE.
  - FULLTEXT is unsolved.
  - Source anomalies.

MySQL stays the default and the server still refuses PG. No DB writes, activation, providers, payments, email, WhatsApp, sends, .env/proof edits, commits or deploys.

## Next bounded files
1. **backoffice_stock_recipes.go (29).** Apply `IsUniqueViolationOn` at :500 with that table's actual idempotency key.
2. **backoffice_group_menus_v2.go (70, 4 dynamic)** and its ws/items/special_sections files.
3. **comida/public menus/vinos**, the dynamic-heavy files.

