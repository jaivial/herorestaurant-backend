# result-native-wa2 — WhatsApp checkpoint B: `whatsapp_bot_special_admin_tools.go` part A

**Nothing here is runtime-verified (0 sites); it is a syntax port only. PG is not ready (ready:false).** MySQL stays the default, and the server still refuses `DB_DRIVER=postgres`. No proposal was approved, no tool or notify flush was run, and nothing was sent.

## Scope: 26 sites (3 dynamic), source lines up to 1408 after the edits
Functions covered:
- `botFullRowHash`
- `botLoadSpecialRow` (via the new `botSpecialRowSelectSQL`)
- `botSpecialAdminEvent`
- `botProposalPersist`, including the direct-write path
- `botSpecialAdminRateOK`, `botSpecialAdminTryApprove`
- `botSpecialLockRefs`
- `botSpecialAdminExecute` (claim, `FOR UPDATE`, writes, `result_json`)
- `markStale`, `botSpecialAdminFlushNotify`
- `botSpecialRescheduleWrite`, including `booking_modifications`

## Build and inventory
- `go build -p 2 ./...` gave EXIT=0, under the inherited CPU 200% / 6G limits. No tests, vet, fixtures or Playwright.
- Inventory: `/tmp/villacarmen-bot-20261111/sql-inventory-r48.json` (dbmigrate-r14).
  - Totals: canonical 2475 = native 1538 (includes 3 provisional) + blocked 3 + unwrapped 934. Runtime-verified: 0.
  - Other figures: dynamic 142; portable audit failures 0; helper-internal 9 and prepared-statement executions 3 excluded.
- Count change from r47: native +26, unwrapped −26.
- Lexical check of the dialect pairs: clean.
- Ledger: `docs/dbmigrate/native-ledger-whatsapp.json`.
  - WhatsApp domain: 63/316 ported, 253 remaining (47 dynamic).

## Semantics (all compile-only, code-grounded)
### Row hash (`SELECT *` into RawBytes)
- No jsonb canonicalisation is involved: `special_json` and `arroz_*` are text columns.
- **Bytes are identical by construction from the driver source:**
  - go-sql-driver v1.9.3 (binary protocol, `parseTime`, loc UTC) and pgx v5 stdlib produce the same bytes per column type: int → decimal text; DATE and timestamptz → `time.Time` UTC → `RFC3339Nano`; TIME → `HH:MM:SS` (pgx TimeCodec returns the text format); text → raw bytes; NULL → nil.
  - The import plan keeps the source column order and names.
- **Hashes are only compared within one engine.** A proposal still pending across the cutover would fail closed as stale; it cannot be applied wrongly.
- Runtime QA is still required.

### Special-row select
- The MySQL const `botSpecialRowSelect` stays, because 8 not-yet-ported callers in other WhatsApp files use it.
- Ported callers use `botSpecialRowSelectSQL()` (`to_char`).
- The `FOR UPDATE` suffix is kept. PG takes the row lock and re-reads the latest version, which gives the same outcome as an InnoDB locking read.

### Transactions and interfaces (reviewed manually; txaudit cannot see these calls)
- `botSpecialAdminEvent` receives an `execQueryer` interface. When it is a `*sql.Tx`, the insert whose error the source logs and ignores now runs in `sqldb.TxStatementScope`.
- In `botProposalPersist`, the old-chats lookup whose error is ignored runs in a savepoint. The supersede statement and the RETURNING insert still propagate their errors.
- The single-active rule (`uq_wa_op_active`) is present in the plan as `ix__wa_bot_op_proposals__uq_wa_op_active`. NULL `active` values count as distinct on both engines.

### Approval claim and single use
- The `UPDATE … WHERE state='sent' AND active=1 AND expires_at > <UTC now>` keeps its `n==1` gate. The WHERE clause guarantees the row actually changes, so matched rows equal changed rows.
- A concurrent second claim blocks on the row lock, then re-evaluates the WHERE (the state is no longer `'sent'`) and updates 0 rows, the same as InnoDB.

### Time
- `UTC_TIMESTAMP()` and `NOW()` on naive columns become `date_trunc('second', statement_timestamp() AT TIME ZONE 'UTC')`.
- The `created_at` rate window (TIMESTAMP, an instant) uses truncated `statement_timestamp()` minus 10 minutes.
- `expires_at` is still bound as a Go-formatted UTC string, unchanged.

### IDs
- Proposal ids come from `sqldb.InsertID`. go-sql-driver v1.9.3's `LastInsertId` never errors, so the MySQL path is identical.

## New gaps (open; separate from the 1117 import-proof count)
- **`botSpecialLockRefs FOR SHARE`:** both engines lock the joined reference rows. MySQL's REPEATABLE READ next-key/gap locks also block inserts of new reference rows for that date; PG has no equivalent. The pre-commit `after_hash` re-validation mitigates this, but the isolation gap stays open.
- **Flush notify:** `LIMIT 5` without `ORDER BY` picks an arbitrary subset on both engines.
- **Cross-engine hash equality:** has to be proven by runtime QA before any claim is made.

## Gates (all unwaived)
- **Import proof:** 1117 pending, ready:false; 4 NOT VALID FKs with 731 orphans.
- **General semantics:** collation and PAD SPACE; NUL bytea runtime QA; jsonb `\u0000` silently dropping audit rows; numeric and locking QA.
- **Email:** bounce invalid-UTF-8 suppression.
- **Site domain:** SEO two-step upsert; registrar choice and UUID v1→v4; instatic multi-key upsert and `UUID()`.
- **POS:** `pos_ticket_create` `UUID()`. Its candidate fix (`uuid-ossp`) is technical work for a later plan revision, not a user decision.
- **Wiring:** analytics dialect.

## Next: part B, 26 sites (8 dynamic), current lines 1535–2523
- `botCancelExecute`: 1535–1601, including the `cancelled_bookings` insert with `NOW()`.
- `botRestoreSource*`: interface querier with a `lock` flag, dynamic at 1635 and 1651.
- `botRestoreDateOK`.
- `botRestoreExecute`: dynamic at 1757 and 1775.
- `botCreateValidate` / `botCreateFindDuplicates`: interface queriers, dynamic at 1936.
- The create/propose tail: 2056, 2130, 2367–2523, dynamic at 2459, 2517 and 2523.

## No actions taken
No SQL execution, DB writes, activation, providers, payments, email, WhatsApp, sends, `.env`/proof edits, commit, merge or deploy.

