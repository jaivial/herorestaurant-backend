# result-native-wa3 — `whatsapp_bot_special_admin_tools.go` part B (file complete)

**Nothing here is runtime-verified (0 sites); it is a syntax port only. PG is not ready (ready:false).** MySQL stays the default, and the server still refuses `DB_DRIVER=postgres`. No approval, tool, notify, create, cancel, restore, executor, provider or WhatsApp path was run.

## Scope: 26 sites (8 dynamic)
- The file is now fully syntax-ported: 52 of 52 sites.
- Functions covered:
  - `botCancelExecute`
  - `botRestoreSourceFull` (interface querier with a `lock` flag)
  - `botRestoreDateOK`, `botRestoreExecute`
  - `botCreateValidate`, `botCreateFindDuplicates`, the create execute path
  - `botEditPropose`, `botEditExecute`
  - `botCancelBlockers`

## Build and inventory
- `go build -p 2 ./...` gave EXIT=0, under the inherited CPU 200% / 6G limits. No tests, vet, fixtures or Playwright.
- Inventory: `/tmp/villacarmen-bot-20261111/sql-inventory-r49.json`.
  - Totals: canonical 2475 = native 1564 (includes 3 provisional) + blocked 3 + unwrapped 908. Runtime-verified: 0.
  - Other figures: dynamic 134; portable audit failures 0.
- Count change from r48: native +26, unwrapped −26.
- Lexical check of the dialect pairs: clean.
- Ledger: `docs/dbmigrate/native-ledger-whatsapp.json`.
  - WhatsApp domain: 89/316 ported, 227 remaining (39 dynamic), 3 files complete.

## Semantics (compile-only, source-grounded)
### Cancel
- The full-row `SELECT *` backups (`bookings`, `modification_history`) are portable as written.
- The formatted read uses `to_char` and quoted camelCase columns.
- The `cancelled_bookings` insert quotes the camelCase columns and uses the naive UTC statement time for `NOW()`. Its id comes from `InsertID`; the failure message is unchanged.
- The DELETE keeps its `n!=1` gate.

### Restore
- **Interface callers:** propose passes `s.db` with `lock=false`; execute passes the transaction with `lock=true`. Errors fail closed (marked stale and rolled back), so the transaction never continues after one.
- **Dynamic INSERTs** into `bookings` and `modification_history`:
  - Backup column names are quoted per dialect (backtick vs double quote, exact case) and the statement is rebound once.
  - Values are Go strings. pgx sends string arguments in text format (`chooseParameterFormatCode`), and the server parses them per column type.

### Edit and create
- `botEditCurrentSelectSQL` and `botSpecialRowSelectSQL` are used by the ported callers only. The MySQL consts stay, so the 8 unported files still compile and behave as before.
- The edit's dynamic SET is limited to whitelisted columns, quoted per dialect.
- The create insert uses `InsertID` with quoted camelCase columns. The panel validation and the second validation before commit are unchanged.

### `botCancelBlockers`
- PG forbids `FOR UPDATE` together with an aggregate. The PG twin therefore counts over a `FOR UPDATE` subquery, which locks the rows that exist.

## Qualifications and gates (explicit, not accepted)
- **Byte-identical claims are source-review only.** This applies to the row hash, `row_backup` and `modification_history_backup`: the code paths were read in go-sql-driver v1.9.3 and pgx v5.11 stdlib (a DATE becomes `time.Time`, which database/sql renders as `RFC3339Nano`). There is no runtime QA proof.
- **Cutover gate: pending proposals and stored backups.** Nobody has accepted that these simply fail as stale. Proposals hashed on MySQL, and cancel backups written on MySQL then restored on PG, need an explicit cutover plan. Options are to drain or expire them before the switch, or to replay and rehash them with verification.
- **TIME range:** MySQL TIME allows −838 to 838 hours; PG `time` covers 00:00–24:00. This stays unresolved, with no adapter.
- **Locking gaps:**
  - `botCreateFindDuplicates`: MySQL REPEATABLE READ next-key locks make a twin insert wait; PG has no gap locks, so the duplicate-create race is open. A candidate fix is a shared advisory lock taken in every insert path. It is not implemented, because it needs the panel and bot paths together.
  - `botCancelBlockers`: protection on PG relies on the booking row's `FOR UPDATE`. The invoices FK takes a key-share lock, which conflicts with it, so invoices are covered. `booking_documents` has no FK to `bookings`, so a concurrent document insert is serialized only if its code path locks the booking row. Unverified.
- **Source anomaly (restored dump only):** `modification_history` has no `restaurant_id`. Restored backups contain only the columns that existed when they were written.
- **All earlier gates remain:**
  - Import proof: 1117 pending, ready:false; 4 NOT VALID FKs with 731 orphans.
  - Collation and PAD SPACE; NUL bytea QA; jsonb `\u0000` silently dropping audit rows.
  - Bounce invalid UTF-8; SEO two-step upsert; registrar choice and UUID; instatic multi-key upsert; POS `UUID()` v1 (candidate `uuid-ossp`, technical work).
  - Analytics dialect wiring; numeric, JSON and locking QA.
  - Lock-ref gap locks.

## Next coherent checkpoint, 36 sites: special-admin satellite operations
These files share the propose/execute pattern and still use the MySQL `botSpecialRowSelect`, so they will switch to `botSpecialRowSelectSQL()`.
- `whatsapp_bot_floor.go`: 7 sites (2 dynamic)
- `whatsapp_bot_groupmenu_admin.go`: 8 sites (2 dynamic, `botGMenuQuerier` interface)
- `whatsapp_bot_groupmenu_remove.go`: 6 sites (1 dynamic)
- `whatsapp_bot_rice_admin.go`: 6 sites (1 dynamic)
- `whatsapp_bot_mobility_admin.go`: 5 sites (2 dynamic)
- `whatsapp_bot_flags.go`: 4 sites (1 dynamic)

`whatsapp_bot_extras_admin.go` (6) follows with `catalog_extras` (10).

## No actions taken
No SQL execution, DB writes, activation, providers, payments, email, WhatsApp, sends, `.env`/proof edits, commit, merge or deploy.

