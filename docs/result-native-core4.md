# Native PostgreSQL port — RowsAffected fix + core_backoffice group 4 (labour / hours / salons / occupancy / calendar / affluence / dashboard)

Status: **built** (`go build -p 2 ./...` exit 0). No PG statement has been executed, so semantics are unverified until manual QA. The server still refuses `DB_DRIVER=postgres` and MySQL SQL is unchanged. The marker stays `ready:false`; the 1117 pending items and the 4 NOT VALID FKs (731 orphans) are unchanged.

## Part A — RowsAffected (MySQL changed-rows contract kept)

The DSN has no `clientFoundRows`, so MySQL counts **changed** rows. Where a caller relies on that count, the PG branch now updates only when the tuple really changes: a null-safe, byte-wise `(cols COLLATE "C") IS DISTINCT FROM (new values)` over the whole SET tuple, with values bound once through `VALUES`. No write means the `updated_at` trigger doesn't fire, as in MySQL.
- **`technical_sheet_steps.go` step update:** 404 on a same-value update on both dialects.
- **`backoffice_labour_cost.go` compensation update:** same predicate (enum/numeric/date/text tuple).
- **`hour_percentages.go` UPDATE:** success only when changed, same as MySQL.
- **Correction:** `sheets.go:268` and `:450` already re-check existence when the count is 0, so they answer identically on both dialects. My earlier report wrongly listed them as deltas.

## Part B — group 4: 79 sites converted, 0 left

Files: `backoffice_production_labour`, `backoffice_labour_cost`, `hours`, `hour_split`, `hour_percentages`, `backoffice_salons`, `occupancy`, `calendar_data`, `backoffice_calendar`, `backoffice_affluence` (including its dynamic seasonal query) and `backoffice_dashboard`.

Source-grounded decisions:
- **Decimal division:** MySQL's decimal division is emulated with `ROUND` at MySQL's result scale: `minutes/60` → 4, `gross/monthly_hours` → 6, `pct/100` → 6. `NULLIF(…,0)` replaces MySQL's NULL-on-division-by-zero. This assumes `div_precision_increment=4`, which is not in the source evidence and is recorded pending.
- **Booleans:** `MAX(cond)` becomes `MAX(CASE…)`; `SELECT EXISTS` becomes `CASE WHEN EXISTS … THEN 1 ELSE 0 END`, because it is scanned into an int.
- **Strings and dates:** `CONCAT` becomes `||`; `CURDATE()` (UTC) becomes `(now() AT TIME ZONE 'UTC')::date`; `MONTH()` becomes `EXTRACT(MONTH)`; untyped `COALESCE(?, '9999-12-31')` and `NULLIF(?, '')` are cast to `date`.
- **JSON:** `JSON_OBJECT` becomes `jsonb_build_object` with typed parameters; audit snapshots are bound `CAST(? AS jsonb)`.
- **Identifiers:** `"reservationDate"`, `"dailyLimit"`, `"hoursPercentages"` and `"hourData"` are quoted.
- **Upserts:** conflict targets are the real keys; occupancy uses `count = table.count + EXCLUDED.count`.
- **Insert ids:** 3 `LastInsertId` calls become `InsertID`.
- **Duplicate detection:** 3 checks that matched error-message text now use `sqldb.IsUniqueViolation` (MySQL 1062 or SQLSTATE 23505). The old string checks would have returned 500 on PG.
- **GROUP BY:** prod runs without ONLY_FULL_GROUP_BY, but every GROUP BY in this group already lists all non-aggregated columns, so no aggregation choice was needed.

## Ledger (`docs/dbmigrate/native-ledger-core.json`, `sql-inventory-r9.json`)

| | value |
|---|---|
| core_backoffice | 405 sites, **340 native**, **65 remaining** (5 dynamic) in 18 files, 45 files done |
| portable audit failures | 0 |
| whole repo | 2476 sites, 340 native |

## Items needing your action or evidence (not claimed)

- **FOR KEY SHARE inside EXISTS:** not verified on PG17. `docs/dbmigrate/pg17-check-queries.sql` is PREPARE-only inside a rolled-back transaction, for your isolated staging copy. It also prepares the steps changed-row UPDATE.
- **Stripe parsing:** the `amount` JSON number-text rendering is still open. The `CAST AS UNSIGNED` replica only covers leading digits after optional whitespace. It is **not** shown equivalent for negatives (MySQL wraps), decimals/exponents, a `+` sign, or values over 18 digits, and no source evidence of such values was collected.
- **Collation, accent and PAD SPACE:** these remain cutover blockers. A precise site ledger has started in `docs/dbmigrate/collation-site-ledger.json` (sheet ILIKE search; 6 ORDER BY-on-text sites in converted code; ci_ai equality/UNIQUE; byte-wise changed predicates).
- **Labour overlap check:** binding `time.Time` as `date` only equals MySQL for UTC-midnight values. Verify on QA.

## Next group (core_backoffice group 5, the last; not started)

65 sites in 18 files: `modifications_api` (12, 2 dynamic), `ad_tools` 8, `events` 6, `beverage_options` 6, `backoffice_accounting_exports` (5, 1 dynamic), `public_unsubscribe` 5, `n8n_reminder` 5, `modification_history` 4, `widget_routes` (2, 1 dynamic), `salon_condesa`, `opening_hours` and `mobility_config` (2 each), `server.go:1696` (wines; this belongs with catalog), `rice_types`, `modification_checker`, `marketing_advertising`, `guest_count_flags` and `checkcancel` (1 each).

