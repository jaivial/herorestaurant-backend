# result-native-wa1 — WhatsApp checkpoint A

**Nothing here is runtime-verified (0 sites); it is a syntax port only. PG is not ready (ready:false).** MySQL stays the default, and the server still refuses `DB_DRIVER=postgres`. No executor, provider, chat or send path was run.

## Scope: 37 sites, both files complete
- `whatsapp_bot_executor.go`: 22 sites (1 dynamic).
- `whatsapp_bot_admin_reads.go`: 15 sites (1 dynamic).

## Build and inventory
- `go build -p 2 ./...` gave EXIT=0, under the inherited CPU 200% / 6G limits. No tests, vet, fixtures or Playwright.
- Inventory: `/tmp/villacarmen-bot-20261111/sql-inventory-r47.json` (dbmigrate-r14).
  - Totals: canonical 2475 = native 1512 (includes 3 provisional) + blocked 3 + unwrapped 960. Runtime-verified: 0.
  - Other figures: dynamic 145; portable audit failures 0; helper-internal 9 and prepared-statement executions 3 excluded.
- Count change from r46: native +37, unwrapped −37.
- Lexical check of the 23 dialect pairs: no MySQL-only tokens in the PG halves, no `?` inside string literals, and placeholder counts match.
- New ledger: `docs/dbmigrate/native-ledger-whatsapp.json`.
  - WhatsApp domain: 316 sites in 53 files.
  - 37 ported, 279 remaining (50 dynamic), 2 files done.

## Changes and semantics (each kept source-faithful; all compile-only)
### Executor
- **Restaurant info, brand name, opening hours:** portable as written.
- **reservation_manager:** `"dailyLimit"` and `"reservationDate"` are quoted on PG, because the imported schema keeps the MySQL camelCase.
- **botFindBookings:**
  - `DATE_FORMAT`/`TIME_FORMAT` become `to_char`.
  - `CURDATE()` becomes the UTC statement date.
  - MySQL `CONCAT(cc, phone)` returns NULL when phone is NULL. PG `concat()` would not, so the PG half uses `||`, which does propagate NULL.
  - Open: the `special_dates` title subquery uses `LIMIT 1` without `ORDER BY`, so the row is arbitrary on both engines.
- **Create booking:** `sqldb.InsertID`. go-sql-driver v1.9.3's `LastInsertId` never errors, so the MySQL path is identical. The business guards (35-day horizon, day open, capacity) are Go-side and unchanged.
- **Cancel transaction:**
  - The `cancelled_bookings` INSERT…SELECT uses `date_trunc('second', statement_timestamp() AT TIME ZONE 'UTC')` for the naive `cancellation_date`.
  - `TIME` into `varchar(10)` gets an explicit `CAST AS text`; both engines produce `HH:MM:SS`.
  - The read whose error the source ignores is now wrapped in `sqldb.TxStatementScope`. On PG that savepoint keeps a failed read from aborting the transaction; `ErrNoRows` is not treated as a statement error.
  - The INSERT, occupancy and DELETE errors still propagate unchanged. txaudit cannot see calls inside `withTx` closures, so this was reviewed manually.
- **Modify booking:**
  - The dynamic SET list is built from fixed items. `"highChairs"` and `"babyStrollers"` are quoted per dialect through `sqlFragment`, and the statement is rebound once.
  - **Source behaviour kept, not fixed:** `curDate` comes from a DATE scanned with `parseTime`, so it is RFC3339 text and never equals `effDate`. As a result the booking's own seats are not subtracted in the capacity check. pgx produces the same string.
- **Fichaje over WhatsApp:**
  - Madrid wall-clock strings are unchanged.
  - The PG minutes calculation reuses the members-domain clock expression: it truncates like TIMESTAMPDIFF, a NULL start still yields NULL, and DST is ignored on wall clock as in MySQL.
  - Time entries sorted by the nullable `start_time` get `NULLS FIRST`.

### Admin reads
- **Enums:** `shape`, `status` and `page_type` are read with `CAST AS text`. On PG, `COALESCE(enum, '')` would raise an error, because `''` is not a valid enum label.
- **Timestamp formatting:** `'%Y-%m-%dT%H:%i:%sZ'` becomes `'YYYY-MM-DD"T"HH24:MI:SS"Z"'`.
  - timestamptz columns render in the session TimeZone, which sqldb pins to UTC (from code; runtime QA pending).
  - Naive columns render the stored UTC wall clock.
- **Area ordering:** `area_id` gets `NULLS FIRST`.
- **Cancellations list:** `ORDER BY` resolves to the formatted alias on both engines. Ties are arbitrary on both.
- **Campaign query (dynamic):** the base and its appended fragments are rebound once.

## Open gaps (unwaived; separate from the 1117 import-proof count)
- **Collation:**
  - `LOWER(TRIM(status)) IN (…)` on `ci_ai` has an accent delta.
  - Ordering on `ci_ai` text differs between ICU and MySQL (`legal_pages.slug`, page names).
  - General CI / accent / PAD SPACE differences.
- **Inherited:** invalid date text raises an error on PG; the source capacity anomaly described above; the arbitrary title subquery row.
- **NUL bytea adapter:** the raw message bytea path is covered for all assistant callers; runtime QA remains.
- **Audit JSON:** jsonb's `\u0000` rejection silently drops the audit row. Unresolved.
- **pos_ticket_create SQL `UUID()` v1:** blocked. This is technical work to schedule, not a business decision. Candidate options, to be verified by source evidence later:
  - a reviewed v1 generator;
  - the `uuid-ossp` extension (`uuid_generate_v1`), if the local PG plan accepts it.
  - Until then it stays blocked and is not claimed equivalent.
- **Source anomalies (restored dump only, not checked against production):** `restaurants.phone`, `group_menus`, `wines`, and the missing website columns. Their failures are preserved.

## Gates (all unwaived)
- **Import proof:** 1117 pending, ready:false; 4 NOT VALID FKs with 731 orphans.
- **General semantics:** source collation, numeric, JSON, ID and locking QA.
- **Email:** bounce invalid-UTF-8 suppression.
- **Site domain:** SEO two-step upsert; registrar first-site choice and UUID v1→v4; instatic multi-key upsert and `UUID()`; analytics dialect wiring.

## Next: `whatsapp_bot_special_admin_tools.go` (52 sites, 11 dynamic), split into two checkpoints of 26 sites
- **Part A, lines ≤1371, 26 sites (3 dynamic):**
  - `botFullRowHash`, `botLoadSpecialRow`, `botSpecialAdminEvent`;
  - the propose/persist path (`botProposalPersist`, dynamic at 826/835);
  - `botSpecialAdminRateOK`, `botSpecialAdminTryApprove`;
  - `botSpecialLockRefs` (transaction locks, dynamic at 1057);
  - `botSpecialAdminExecute`, `markStale`, `botSpecialAdminFlushNotify`, `botSpecialRescheduleWrite`.
- **Part B, lines 1498 onward, 26 sites (8 dynamic):**
  - `botCancelExecute`;
  - `botRestoreSource*` (lock interface querier, dynamic at 1598/1614);
  - `botRestoreDateOK`, `botRestoreExecute`, `botCreateValidate`/`FindDuplicates` (interface queriers), and the create/propose tail.
- Both parts need the transaction/interface rules: `FOR UPDATE` semantics, savepoints, `NaiveUTCArg`.

## No actions taken
No SQL execution, DB writes, activation, providers, payments, email, WhatsApp, sends, `.env`/proof edits, commit, merge or deploy.

