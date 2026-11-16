# result-native-assistant1 — assistant checkpoint A

**Nothing here is runtime-verified (0 sites); it is a syntax port only. PG is not ready (ready:false).** MySQL stays the default, and the server still refuses `DB_DRIVER=postgres`.

## Scope done: 32 sites, all 3 files complete
- `internal/api/assistant_catalog_tools.go`: 16 sites (was 15, plus 1 from the explicit split of the generic `catalog_update` into mysqlOnlySQL×2 + pgOnlySQL×1).
- `internal/api/assistant_tools.go`: 12 sites.
- `internal/api/assistant_analytics_tools.go`: 4 sites.

## Build and inventory
- `go build -p 2 ./...` gave EXIT=0, under the inherited CPU 200% / 6G limits. No tests, vet, fixtures or Playwright.
- Inventory: `/tmp/villacarmen-bot-20261111/sql-inventory-r45.json` (dbmigrate-r14).
  - Totals: canonical 2475 = native 1452 (includes 3 provisional) + blocked 2 + unwrapped 1021. Runtime-verified: 0.
  - Other figures: dynamic 147; portable audit failures 0; helper-internal 9 and prepared-statement executions 3 excluded.
  - By kind: nativeSQL 491, portableSQL 934, selectedSQL 14, mysqlOnlySQL 5, pgOnlySQL 5, provisionalSQL 1, pgProvisionalSQL 2, blockedOnPGSQL 2.
- Count change from r44: call sites +1 (the split above); native +32; unwrapped −31.
- txaudit on the 3 files: no transactions, no transaction-interface parameters, no `time.Time` arguments, so no savepoint or NaiveUTC helpers were needed.
- New ledger: `docs/dbmigrate/native-ledger-assistant.json`, holding the assistant domain (56 sites, 8 files): 32 ported, 24 remaining, 0 dynamic among the remaining.

## Changes
### Catalog
- **Fixed identifiers:** quoted on PG through `assistantCatIdent` (`"BEBIDAS"`, `"CAFES"`, `"POSTRES"`, `"NUM"`, `"DESCRIPCION"`).
- **Prices:** read as `CAST(.. AS text)`. Written as `CAST(CAST(? AS double precision) AS numeric(10,2))`, or `AS bigint` for `pos_products` cents.
- **Create:** PG uses `sqldb.InsertID` (RETURNING `"num"`/`"id"`). The MySQL path keeps the source Exec and ignores the LastInsertId error, as before.
- **Update and delete:** PG keeps the MySQL changed-rows contract (`updated`/`deleted` is true only when n==1). It does this with `ROW(..) IS DISTINCT FROM ROW(..)`, comparing text with `COLLATE "C"`. Soft delete uses `flag IS DISTINCT FROM 0`.
- **Vinos:**
  - Uses `vinoV2NumSQL()`.
  - Update adds a changed-rows predicate and `precio` is cast to real.
  - Delete adds `active IS DISTINCT FROM 0`.

### Typed lists and safe reads
- **Typed lists:** enum and TIME columns are read with `CAST AS text`.
- **customers_list:** a PAD SPACE-faithful `rtrim` replaces `NULLIF`.
- **pos_visits status filter:** compares text under `ci_ai`, so an unknown status gives no match instead of an enum cast error.
- **Safe reads:** `config_json` and the enum `status` are read as text.
- **assistantColumnExists:** uses `current_schema()`.

### Tools
- Audit inserts use `CAST(? AS jsonb)`. Their errors are ignored, outside any transaction.
- **delete_booking:** changed-rows predicate `status COLLATE "C" IS DISTINCT FROM 'cancelled'`; `DATE_FORMAT` becomes `to_char`.
- **Booking list:** `reservation_time` is read as text.

### Analytics
- `DATE_FORMAT` becomes `to_char`, with a matching GROUP BY.
- `HOUR()` becomes `CAST(EXTRACT(HOUR ..) AS integer)`.
- `DATE_ADD(?, INTERVAL 1 DAY)` becomes `CAST(? AS timestamp) + INTERVAL '1 day'`; `received_at` is a naive UTC column.

## New semantic gaps (open; not waived and not counted in the 1117)
- **LIKE:** case-insensitive only. MySQL accent folding and PAD SPACE are not reproduced, so assistant search is a cutover gate.
- **`NULLIF(?,'')` with space-only input:** the MySQL result depends on the connection collation, which is not grounded from the DSN.
- **Float64 to decimal(10,2)/bigint:** rounding at half-way values needs QA.
- **Vinos `precio` text:** it depends on the MySQL driver protocol (binary float32 `'g'` formatting vs text `'12.30'`), and `interpolateParams` is not grounded. This is a gate. The existing float(10,2) storage rounding issue is still a proposal only.
- **Invalid date strings:** PG raises an error; MySQL warns or returns NULL.
- **CI comparisons:** PAD SPACE and accent behaviour differs for bookings `status NOT IN`, the pos_visits status filter, UCA ignorable characters in customer names, and site `ORDER BY` on a `ci_ai` varchar.
- **jsonb in audit rows:** jsonb rejects `\u0000`, so the audit row is silently lost in that case; JSON text normalisation needs QA.
- **Products top-20:** ties are ordered nondeterministically on both engines.
- **Source anomaly:** `assistantCount` uses `group_menus` and `wines`, which are absent from the schema. The failure is preserved, not remapped.

## Unchanged gates and gaps (all unwaived)
- **Site domain:** SEO two-step upsert (not atomic, assumed key priority); registrar first-site choice and UUID v1→v4; 2 instatic sites blocked; bounce invalid-UTF-8 suppression; analytics dialect wiring.
- **Import proof:** 1117 pending, ready:false; 4 NOT VALID FKs with 731 orphans.
- **General semantics:** CI, accent and PAD SPACE; FULLTEXT; 190 range narrowings; 136 ON UPDATE tables and 353 defaults; the broken view; NUL adapter QA; numeric, JSON, ID and locking QA.
- **Crossaudit limits:** interface and unported callers; isolation (MySQL RR vs PG RC).
- **Website evidence:** the missing-website anomalies come only from the restored dump, not live production.

## No actions taken
No SQL execution, DB writes, activation, providers, payments, email, WhatsApp, sends, `.env`/proof edits, commit, merge or deploy.

## Next: checkpoint B, 24 sites (none dynamic), the rest of assistant
- `assistant_ws.go`: 10
- `assistant_pos_tools.go`: 5
- `assistant_confirmation.go`: 4
- `assistant_memory.go`: 4
- `assistant_llm.go`: 1

Checkpoint B includes the transaction-interface callers through assistant_ws and whatsapp, and the confirmation-token single-use locking.

