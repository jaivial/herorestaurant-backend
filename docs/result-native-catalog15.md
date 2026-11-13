# Native PostgreSQL port: catalog checkpoint 15 (catalog_stock syntax complete)

**Status:** the SQL in catalog_stock is now syntax-complete, but none of it has been executed or verified at runtime. The app is **not** PG-ready: `ready:false`, MySQL stays the default and the server refuses to start on PG.

## Counts (canonical inventory r32, dbmigrate-r12 basis)

| Scope | Value |
|---|---|
| Call sites (repo) | 2472 |
| Native (repo) | 1071 (+27 since r31) |
| Dynamic (repo) | 198 |
| Portable-audit failures | 0 |
| Excluded | 5 helper-internal calls, 3 prepared-statement executions |
| catalog_stock | 504/504, 43/43 files |
| core_backoffice | 406/406 |
| bookings | 161/161 |
| Remaining (repo) | 1401 |

Files:
- Inventory: `/tmp/villacarmen-bot-20261111/sql-inventory-r32.json`
- Ledger: `docs/dbmigrate/native-ledger-catalog.json`, now with `syntax_complete:true`, `pg_ready:false`, `runtime_verified:false`
- Deltas: `/tmp/villacarmen-bot-20261111/catalog-deltas11.json`

## Build
`go build -p 2 ./...` exited 0 under the inherited cgroup (200% CPU, 6G memory), with pipefail. No tests, vet, fixtures or Playwright were run.

## Sites ported (27)

### backoffice_stock_reconciliation.go (3)
- **Diff query:** portable as written (UNION, derived tables, decimal SUM/ABS scanned into float64, non-null ORDER BY keys).
- **Rebuild UPDATE:** PG has no `UPDATE ... LEFT JOIN`, so the grouped derived table becomes a correlated `SUM` subquery. The derived table has one row per (item, warehouse), so the value is the same; join keys and argument order are unchanged.
- **Missing-levels insert:** the `INSERT ... SELECT ?` parameter becomes `CAST(? AS integer)`. On PG a bare parameter in a SELECT list resolves to text and the insert fails otherwise.
- **OPEN (locking):** MySQL's multi-table UPDATE share-locks the `stock_movements` rows it reads. PG reads them unlocked.

### backoffice_stock_margin_targets.go (3, 1 dynamic)
- The dynamic table name comes from a fixed internal map: `comida_plato_categories` or `comida_bebida_categories`. Both are lowercase and exist in the schema.
- The `scope_kind` enum is read with `CAST(... AS text)`.

### backoffice_stock_permissions.go (2)
- The ODKU becomes `ON CONFLICT` on the PRIMARY key `(restaurant_id, role_slug, permission_key)`, the table's only unique key.
- Equality on the `role_slug` key is still subject to the open CI-collation gap.

### special_date_menu_sections.go (3)
- In the `INSERT ... SELECT`, each parameter is cast to its target column type (integer, bigint, numeric, smallint).
- **OPEN (rounding):** the `adelanto_amount` value is a float64 going into `decimal(10,2)`. MySQL receives it as a binary double; pgx sends it as numeric text. The two rounding paths have not been compared (float→decimal QA).

### Rows-affected gates that only lead to an existence check
- **Sites:**
  - `menu_public_seo_handlers.go` (3, 1 dynamic)
  - `menu_section_cta.go` (2)
  - `special_menu_group_booking.go` (3)
- **Why no changed-row predicate:** at all three sites, `RowsAffected==0` only triggers an existence or ownership check, so MySQL's changed-row count and PG's matched-row count give the same response.
- **SEO slug duplicates:** the MySQL 1062 branch is unchanged. On PG, `sqldb.IsUniqueViolation` is used, which has the same breadth (any unique key; only the seo_slug index is reachable).
- **Section CTA:** `cta_json` is written with `CAST(? AS jsonb)`.

### Other files
- **backoffice_comida_counts.go (1):** the table name is quoted as `"POSTRES"` on PG.
- **Portable, wrapped once around the whole final string:**
  - menu_public_seo.go (2)
  - group_menus_party_size.go (1)
  - backoffice_vinos_cutout.go (2 dynamic)
  - backoffice_comida_upload.go (2)
- **created_at:** `group_menus_party_size.go` scans `created_at` into `time.Time`. That the UTC scan location makes it match MySQL comes from reading the driver code only.
- The cutout, upload, AI and provider code was not run.

## End-of-domain static review (all 43 catalog files)
- **Wrap audit:** no wrapper is followed by an outside `+` concatenation (0 hits).
- **MySQL-only functions:** a scan for MySQL-only functions (NOW, DATE_ADD, DATE_FORMAT, GROUP_CONCAT, JSON_*, LAST_INSERT_ID, IFNULL) outside `nativeSQL`/`sqlFragment` found 10 lines. Each one is the MySQL half of a multi-line `nativeSQL` pair.
- **Cross-check on stock grouping:** the backoffice_stock.go expiring query groups by `warehouse_id`, and its PG half adds only the tenant column that the WHERE clause already fixes. The stock_digest.go variant does not group by warehouse, so its `any_value` gap stands.
- **Limit:** this review is regex/line based, not an AST proof.

## Semantic ledger: OPEN (not accepted choices; separate from the 1117 import-proof count)

### Insert IDs and grouping
- **Same-day dish upsert insert ID:** PG returns 0 on the conflict/update branch. This is provisional and unproven against MySQL; it is the same gap as the category upsert, now covering 2 sites.
- **Stock digest `any_value(warehouse_id)`:** the pick is arbitrary. It may differ from MySQL's arbitrary row for items in several warehouses, which changes the consumed subtotal in the digest text.

### Locking
- The reconciliation UPDATE takes no read locks on stock_movements in PG; MySQL share-locks them.

### Float, decimal and rounding
- `adelanto_amount` float→decimal rounding (special_date_menu_sections).
- The wine `float(10,2)` rounding/range fix is a proposal only.
- Double rounding in averages and quotients.
- Percentage float rounding QA.

### Other app-level gaps
- Nested JSON containment.
- JSON changed-row equality, text rendering and timestamp QA.
- Dessert mirror insert-ID ordering.

### General gaps
- CI, accent and PAD SPACE collation semantics.
- FULLTEXT.
- 190 range narrowings.
- ON UPDATE on 136 tables and 353 schema defaults.
- The broken `recent_conversations` view.
- NUL adapter QA.
- 4 FKs left NOT VALID, covering 731 orphan rows.

### Source anomalies (preserved as failures)
- DIA/FINDE tables absent.
- `menu_dishes_catalog.name` absent.
- `bookings.created_at` absent.
- `modification_history.restaurant_id` absent.

## Remaining domains (remaining-callers.json files × r32)
| Domain | Files left | Sites left | Dynamic |
|---|---|---|---|
| pos_billing | 35 | 564 | 53 |
| whatsapp_bot | 53 | 316 | 53 |
| site_analytics | 17 | 201 | 22 |
| members_staff | 7 | 84 | 1 |
| campaigns_messaging | 7 | 64 | 5 |
| tools_cmd | 12 | 57 | 9 |
| assistant | 8 | 55 | 22 |
| migration_tooling_native | 6 of 7 | 55 | 31 |
| db_runner | 2 | 5 | 2 |
| **Total** | | **1401** | |

The total matches 2472 − 1071. No call sites fall outside these domains.

## Recommended next campaign: campaigns_messaging (64 sites, 7 files)
| File | Sites | Dynamic |
|---|---|---|
| backoffice_campaigns.go | 29 | 5 |
| campaign_email_retry.go | 13 | |
| campaign_auto_retake.go | 6 | |
| campaign_bounce_sync.go | 5 | |
| campaign_tools.go | 5 | |
| campaign_email_guard.go | 4 | |
| campaign_email_validation.go | 2 | |

It is one coherent checkpoint covering the campaign outbox, retry and locking paths. It must be SQL-only: no workers, email or sends.

No new domain was started.

## Not done
No commit, merge, deploy, DB writes, providers or sends, and no `.env`, source or proof-data edits.

