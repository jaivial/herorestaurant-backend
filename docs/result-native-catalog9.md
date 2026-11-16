# Native PG port: catalog_stock group 9 (backoffice_stock_analytics.go; compile only, NOT runtime-verified, NOT PG-ready)

## Counts (inventory r26: /tmp/villacarmen-bot-20261111/sql-inventory-r26.json, dbmigrate-r12)
- **Repo:** 2472 call sites, 883 native (+26), 252 dynamic. portable_audit_failures is 0.
  - Excluded: 5 helper-internal calls and 3 prepared-stmt calls.
- **Core:** 406/406. **Bookings:** 161/161.
- **catalog_stock:** 316/504 native, 188 remaining (54 dynamic). 10/43 files are done; **backoffice_stock_analytics.go is 26/26**.
  - Ledger: `docs/dbmigrate/native-ledger-catalog.json`.
- **Build:** `go build -p 2 ./...` exits 0 under the inherited cgroup (CPU 200%, 6G). No SQL was run, and no tests, vet, fixtures or Playwright.

## Changes (26 sites)
### AI entitlements
- **Upsert:** `ON CONFLICT (restaurant_id)`.
- **Call counter UPDATE:** portable. It always changes the matched row, so RowsAffected changed rows equal matched rows.

### Affluence
- **POS-owned check:** `EXISTS` becomes CASE 0/1.
- **Upsert:** `ON CONFLICT (restaurant_id, service_date, service_type)`, keeping `source='MANUAL'`.

### Usage / forecast
- **`DATEDIFF(MAX,MIN)+1` on datetime** becomes `(MAX::date - MIN::date)+1` (a date difference, as in MySQL).
- **Time windows:**
  - `CURDATE() - INTERVAL 8 WEEK` becomes `CAST(<statement UTC naive> AS date) - 56`.
  - `NOW() - INTERVAL 8 WEEK` on datetime becomes `<statement UTC naive> - interval '56 days'`.
- **avg_daily division:**
  - The source scale is 4 (SUM of decimal(18,4)) plus div_precision_increment 4 (production-verified), so the quotient has scale 8.
  - PG: `ROUND(…/GREATEST(…,1), 8)`.
  - **Pending numeric QA:** PG computes the quotient at 16 or more significant digits before ROUND, so double rounding can differ only when digits 9–16 round to exactly …5. This cannot be checked statically.
- **GROUP BY i.id, u.id (no ONLY_FULL_GROUP_BY):**
  - `item_usage.avg_daily/total_usage` come from a derived table grouped by stock_item_id and joined on i.id, so there is one value per group.
  - PG adds them to GROUP BY, which leaves the groups unchanged.
  - `i.name`, `u.factor_to_base` and `u.label` are covered by PK functional dependency.
  - The query is **not ambiguous**.

### Seasonality / AI reports
- **Reads:** `CAST(COALESCE(jsonb,'{}') AS text)` (JSON render class).
- **Report INSERT:** `CAST(? AS jsonb)` ×2 plus `InsertID`, kept in the source's `saveErr == nil` branch.
- **Usage INSERT:** portable.

### VAT rates
- **List and default reset:** portable.
- **Create:** `InsertID`.
- **Update:** keeps the changed-rows 404 gate: an unchanged save answers 404 (source quirk).
  - PG uses `IS DISTINCT FROM` over (name `COLLATE "C"`, rate numeric(5,2), is_default, is_active).
  - The preceding `is_default=0` reset runs in the same tx on both, so the counted change is identical.
- **In-use check:** `(EXISTS OR EXISTS)` becomes CASE 0/1.
- **Delete:** portable.

### Prices / costing
- **Price insert and avg_unit_cost update:** portable.
- **Latest price:** `effective_at <= <statement UTC naive>` (effective_at is NOT NULL, so there is no NULL ordering issue).
- **POS sales window:** `created_at >= <statement time> - make_interval(days => CAST(? AS integer))` (timestamptz).
- **Recipe pricing list:** portable.

**Unchanged:** stock/money Go arithmetic and the AI provider code. No AI calls were made.

## Provisional / new gaps (tracked separately, NOT part of the 1117 import-proof count)
- **Category ODKU insert id (comida.go ×3): still provisional, not resolved and not equivalent.**
  - PG returns 0 on conflict, so the source fallback lookup runs.
  - The MySQL 8.0.46 insert_id on the update path is unproven.
  - Local evidence checked: go-sql-driver reads insert_id verbatim. Only MariaDB 11.8 docs are installed, which is a different server and not used as evidence. No MySQL server source is available.
  - Needs a coordinator probe.
- **avg_daily rounding:** double-rounding edge (above).
- **Wine precio float(10,2):** proposal only (`plan-revision-proposal-float-md.json`); imported proofs are untouched.

**Unchanged pending (none waived):**
- CI/accent/PAD SPACE (ORDER BY name).
- FULLTEXT.
- 190 range narrowings.
- ON UPDATE (136) / DEFAULT (353) time semantics.
- The recent_conversations view.
- NUL QA.
- JSON text and timestamp QA.
- JSON_CONTAINS nested arrays.
- 4 NOT VALID FKs covering 731 orphans.
- 1117 pending, ready:false.
- Source anomalies.

MySQL stays the default and the server still refuses PG. No DB writes, activation, providers, payments, email, WhatsApp, sends, .env/proof edits, commits or deploys.

## Next
| file | remaining | dynamic |
|---|---|---|
| public_menus.go | 18 | 10 |
| backoffice_stock_suppliers.go | 17 | 2 |

