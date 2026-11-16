# Native PostgreSQL port: site_analytics checkpoint 6 (inventory classification fix, analytics service and breakdowns)

**Status:** this code compiles. No analytics SQL, refresh, tracking ingestion or API path was executed. The app is not PG-ready: `ready:false`, MySQL stays the default and the server refuses to start on PG.

## 1. Inventory classification fix (new tool dbmigrate-r13)
- **Binary:** `/tmp/villacarmen-bot-20261111/dbmigrate-r13`, sha256 `025787cc29ae92103ba6d9fb41333420935e9f24b8a1ea582ff05ce2ee8dcba0`. It was built static with `CGO_ENABLED=0`.
- **Old proofs preserved:** r42 and all earlier inventories were not overwritten.
- **New explicit markers** (in `internal/api/sql_native.go`):
  - **`blockedOnPGSQL(q)`:** a MySQL-only statement with **no PG port**. It panics if reached on PG, and the caller skips it on PG explicitly. The inventory counts it in `blocked_on_pg_sites` and **not** in `native_sites`.
  - **`provisionalSQL(mysql, pg)` / `pgProvisionalSQL(q)`:** these behave exactly like nativeSQL / pgOnlySQL. They mark a **syntax-ported but open cutover gate**. They are counted as native and also listed in `provisional_gate_list`.
  - The inventory now also reports `native_by_kind` and `runtime_verified_sites` (= 0).
- **Audit of every marker use in the repo:**

| Marker | Count | Sites |
|---|---|---|
| `blockedOnPGSQL` | 2 | `instatic_manager.go:211` (multi-key upsert), `:903` (SQL UUID v1). Both were previously mislabelled `mysqlOnlySQL` and counted native. |
| `provisionalSQL` | 1 | `site_builder_registrar.go:169` (UUID v1→v4 and the LIMIT 1 site choice) |
| `pgProvisionalSQL` | 2 | `seo_item_routes.go:707`, `:715` (the two-step any-key upsert) |
| `mysqlOnlySQL` | 3 | MySQL halves of explicit dialect branches whose PG half is its own counted site: `backoffice_technical_sheet_steps.go:143` (PG 133/138), `booking_documents.go:413` (PG 394), `seo_item_routes.go:700` (PG provisional 707/715) |
| `pgOnlySQL` | 4 | `backoffice_technical_sheet_steps.go:133`, `:138`, `booking_documents.go:394`, `modification_history.go:43` (PG DDL) |

- **Denominator unchanged:** 2474 call sites. There was no renaming or denominator change beyond the explicit marker swap at these 5 sites.

## 2. Counts (canonical inventory r43)

| Class | Sites |
|---|---|
| Native (syntax-ported, includes 3 provisional) | **1398** |
| Blocked on PG (not native) | **2** |
| Unwrapped / not ported | **1074** |
| **Total** | **2474** (= 1398 + 2 + 1074) |
| Runtime-verified | **0** |
| Dynamic | 179 |
| Portable-audit failures | 0 |
| Excluded | 9 helper-internal calls, 3 prepared-statement executions |

- **Correction to r42:** it reported "wrapped 1367". The correct reading is 1365 native + 2 blocked.
- **site_analytics:** 179 syntax-ported + 2 blocked of 203; 22 remaining (9 dynamic); 11/17 files done.
- Inventory: `/tmp/villacarmen-bot-20261111/sql-inventory-r43.json`
- Ledger: `docs/dbmigrate/native-ledger-site.json`, which now separates native, blocked, unwrapped and runtime-verified (0).
- Semantic notes: `/tmp/villacarmen-bot-20261111/site-semantics6.json`

## 3. Analytics: service.go (27) and breakdowns.go (6)
- **New dialect switch:** the `internal/analytics` package gets its own `sqlDialect`, `nativeSQL`/`portableSQL` and `SetSQLDialect` (`internal/analytics/sql_native.go`). **OPEN wiring item:** the cutover must set this together with the api dialect. Nothing sets either one today.
- **Dates (source grounded):**
  - `DateRange` values are **UTC-midnight** `time.Time` (from `time.Parse` and `dateOnly`).
  - The MySQL driver (`loc=UTC`) sends `YYYY-MM-DD 00:00:00` against DATE columns; pgx binds the same UTC date. The BETWEEN bounds are therefore identical.
  - `DATE(naive timestamp)` becomes `CAST(.. AS date)`, the UTC wall date as in the source. Dates are **not** converted to Madrid time; that is source behaviour, not a fix.
  - The AST pass confirms every `time.Time` argument comes from a UTC-labelled value, so no `NaiveUTCArg` is needed.
- **Buckets:**
  - `WEEKDAY` (0=Mon..6=Sun) becomes `EXTRACT(ISODOW)-1`, with `GROUP BY 1`.
  - `HOUR(MIN(paid_at))` on the naive UTC column becomes `EXTRACT(HOUR)`, the UTC hour as in the source.
  - The DST/Madrid question does not arise because the source is UTC-based. The hourly distribution is a UTC hour, which is preserved and flagged.
- **Grouping:** `GROUP BY v.id` with `v.covers` is valid on PG through the PK dependency. Product and category names are grouped under ci_ai, part of the CI gap. Ties in `ORDER BY revenue DESC LIMIT 10` are nondeterministic on both engines.
- **Decimals:** `AVG(decimal(18,6))` becomes `ROUND(AVG(..), 10)`, matching MySQL's AVG scale of 6 + `div_precision_increment` 4. SUMs are exact on both engines.
- **Upserts:** 5 ODKUs become `ON CONFLICT` on each table's **single** collidable unique key (the PK is a generated id in every case):
  - customers: identity
  - customer_sources: source_ref
  - sales_documents: source
  - sales_lines: source line
  - stock_facts: source
  
  The assignments read no other updated columns, so EXCLUDED and the old-row CASE are equivalent to MySQL's left-to-right order.
- **Enums and text:**
  - `source_type`, `status`, `method` and `type` are read as text.
  - `COALESCE(i.status, '')` becomes `COALESCE(CAST(i.status AS text), '')`; an enum against `''` would error on PG.
  - `IF` becomes `CASE` in the phone concatenation; CONCAT's NULL handling cannot be reached there because of the guard.
- **JSON:** `waste_breakdown_json` is written with `CAST(? AS jsonb)` and read as text (JSON text QA).
- **Insert IDs:** the sync run id uses `sqldb.InsertID`.
- **Time:** `completed_at = NOW()` (naive) becomes the UTC statement second.
- **Transaction:** every Refresh transaction error is checked and returned (AST: 0 ignored). The rollback and `markRunFailed` run outside the transaction. No interface receivers are involved.

## Cutover gates and open items (unwaived)
- **SEO two-step upsert:** not atomic, and the key priority is assumed.
- **Registrar:** the first-site choice is unproven, and its UUID v1 → v4 is unaccepted.
- **instatic:** the instance any-unique-key upsert is BLOCKED, and the domain-mapping UUID insert is BLOCKED.
- **Bounce invalid-UTF-8 suppression:** still cutover-blocking.
- **New:** the analytics dialect wiring.
- **Website anomalies:** the evidence is the restored dump only, not live production.
- **Import proof:** 1117 pending items with `ready:false`, which is separate from the semantic gaps.
- **Still open:**
  - 4 NOT VALID FKs with 731 orphans;
  - CI/accent/PAD SPACE and FULLTEXT;
  - 190 ranges and 136 ON UPDATE / 353 defaults;
  - the view and NUL QA;
  - the numeric, JSON, ID and lock QA;
  - crossaudit limits.

## Build
`go build -p 2 ./...` exited 0 under the inherited cgroup (200% CPU, 6G memory), with pipefail. No tests, vet, fixtures or Playwright were run.

## Next: ads and AI usage (22 sites, 9 dynamic)

| File | Sites | Dynamic |
|---|---|---|
| backoffice_ads.go | 9 | 2 |
| backoffice_ai_usage.go | 7 | 4 |
| ai_usage_logging.go | 2 | 1 |
| cafes_page_seo.go | 2 | 2 |
| public_ads.go | 1 | |
| public_ads_embed.go | 1 | |

## Not done
No commit, merge, deploy, DB writes, providers, payments, email, WhatsApp or sends, and no `.env`, source or proof-data edits.

