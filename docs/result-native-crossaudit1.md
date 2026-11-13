# Cross-domain audit 1: transaction continuation after errors and time.Time binding to naive columns

**Status:** this is code-only. No SQL was executed. MySQL stays the default, the server refuses to start on PG, `ready:false`, and no gap is waived. The bounce invalid-UTF-8 suppression gap is still cutover-blocking.

## Scope and method
- **Audited:** all **1219** already-ported call sites in **144 files** across core 406, bookings 161, catalog 504, campaigns 64 and members 84. The audited count equals the native count of inventory r37.
- **Tool:** a go/packages plus go/types AST analysis in `/tmp/vc-txaudit`, kept outside the repo; it is not a test. For every `Exec/Query/QueryRow[Context]`, plus `sqldb.InsertID`/`InsertSelectID`, it records:
  - the receiver kind: `*sql.DB`, `*sql.Tx`, `*sql.Stmt`, or an interface that may be a tx;
  - every argument typed `time.Time`, `*time.Time`, `sql.NullTime` or `any`, with `any` values resolved back to their assignments;
  - for variadic spreads, the composite and append elements;
  - for every non-DB receiver, what happens to the statement's error.
- **Also listed:** 74 `BeginTx` calls, 17 functions taking `*sql.Tx`, and 2 `PrepareContext` calls on a tx. I manually reviewed the callers of all 17 tx-taking functions.
- **Outputs:**
  - Coverage by file and site: `docs/dbmigrate/crossaudit-coverage1.json` (also in `/tmp/villacarmen-bot-20261111/`)
  - Raw results: `/tmp/villacarmen-bot-20261111/crossaudit-raw-final.json`
  - Inventory: `/tmp/villacarmen-bot-20261111/sql-inventory-r37.json`. Counts are unchanged: 2472 call sites / 1219 native / 192 dynamic, 0 audit failures, 8 helper-internal and 3 prepared-statement executions excluded.

## Grounding
- **MySQL DSN:** `internal/db/mysql.go` builds `parseTime=true&charset=utf8mb4&collation=utf8mb4_unicode_ci&multiStatements=true`, with no `loc` and no `interpolateParams`.
- **go-sql-driver v1.9.3** (`dsn.go`, `packets.go`):
  - The default `Loc` is `time.UTC`.
  - Prepared execution sends a `time.Time` as `appendDateTime(v.In(cfg.Loc))`, i.e. the UTC wall clock, with fractional seconds when nonzero.
  - A zero time is sent as `0000-00-00`.
- **pgx v5.11** (`pgtype/timestamp.go`): `timestamp` (without time zone) is encoded with `discardTimeZone`, i.e. the caller's wall clock. `timestamptz` encodes the instant.
- **Consequence:** only naive columns (MySQL `DATETIME` → PG `timestamp`) and `DATE` columns bound from a non-UTC `time.Time` differ. `TIMESTAMP` (→ `timestamptz`) columns and Go-formatted strings are unaffected.
- **Transactions:** InnoDB rolls back only a failed statement and the transaction continues. PG marks the transaction aborted, so every later statement and the COMMIT fail.

## Issue 1: an error is ignored or inspected, then the same transaction continues
- **Classification of the 297 non-DB call sites, after the fixes:**
  - checked and terminating (return/panic): 251
  - returned to the caller: 9
  - inspected for ErrNoRows / `err == nil` / compound conditions that all return: 14 (no DB error; the transaction stays valid on PG)
  - "unchecked-next": 27, all reviewed manually. Each is `return err`, a row Scanned and checked later, or a statement already scoped by `TxStatementScope`.
- `continue` and `break` after an error were **not** counted as termination; that produced 0 hits.
- **Real preservation bugs FIXED** with explicit `sqldb.TxStatementScope`. Each wraps a statement whose error the source ignores before the same transaction continues:

| Site | Statement | Why it matters on PG |
|---|---|---|
| backoffice_auth.go handleBOSetPassword (×2) | reset- and invitation-token invalidation UPDATE | a failure aborted the password change |
| backoffice_bookings.go handleBOBookingReactivate | closed_days COUNT, with its Scan discarded | |
| backoffice_stock.go transfer and movement create (×2) | stock_settings allow_negative Scan, ignored | |
| backoffice_stock_analytics.go VAT create/patch (×2) | `is_default=0` UPDATE, ignored | |
| backoffice_stock_analytics.go costing | labour_cost_enabled Scan, ignored | |
| backoffice_stock_documents.go extraction save | alias lookup Scan, ignored, inside the line loop | |
| backoffice_stock_documents.go invoice confirm | stock_item_prices INSERT and supplier-alias upsert, both ignored | **plausible failures** (constraints) would have failed the whole confirm on PG |
| backoffice_stock_recipes.go production | allow_negative Scan, ignored | |
| backoffice_technical_sheet_link.go delete | movements COUNT plus DELETE units/items, all discarded | DELETE stock_items can plausibly hit a RESTRICT FK; MySQL commits the rest |
| backoffice_member_access_flows.go | `ensureUniqueUsernameTx` lookup error ignored, then the caller links the member in the same tx | |

- **Total fixed:** 16 sites, on top of the 5 already scoped in members checkpoint 2.
- **MySQL paths are byte-for-byte unchanged:** `TxStatementScope` calls `fn` directly when the dialect is not PG, and the ignored-result semantics (`_ =`) are kept.

## Issue 2: time.Time bound to a timezone-less column
- **Typed time arguments at ported sites (complete list from the AST pass):**
  - `bo_sessions` (auth, middleware, MCP): already bound as `.UTC()`. OK.
  - `compensationOverlaps`: an explicit timestamptz PG twin from catalog/core. OK.
  - `booking_checkouts.expires_at`, `mcp_oauth_codes`/`tokens.expires_at`: `timestamptz`, so the instant is preserved. OK.
  - **`stock_movements.expires_at`** (naive DATETIME), from `stockParseExpiryDate`:
    - A `YYYY-MM-DD` input parses as UTC midnight, so it is identical on both engines.
    - An **RFC3339 input with a non-UTC offset** is stored by MySQL as the UTC wall clock; pgx would store the offset wall clock.
    - **FIXED** at 3 sites (stock count lines, movement create, invoice confirm lines) with the new `sqldb.NaiveUTCNullArg`.
  - The tokens and pre-shift sites from members checkpoint 2 already use `NaiveUTCArg`.
- **Formatted strings (not affected, verified):** Madrid wall-clock strings (fichaje), the `retentionUntil` date string, the special-date and stock-document dates, and every BETWEEN/= date parameter in members.
- **Spreads and `any` helpers:** the AST pass resolved these to ints or strings: `vinoV2LegacyNumArgs`, `sourceIDArgs`, the provision seed args (no `time` usage in the file), the comida filter args, `dates` (`string` field), `toAnySlice` ids, and the `nullable*`/`stockNullable*`/`nullIfEmpty` helpers (string/int/float only, judged by signature and name).
- **Zero time:** `time.Time{}` is sent by MySQL as `0000-00-00`, which strict mode rejects. pgx would store `0001-01-01`. No ported site was found that can pass a zero time to a naive column, but that is not proven for helper-built values.

## New helpers (internal/sqldb), reviewed from code
- **`TxStatementScope`:**
  - Uses a fixed savepoint name, `sqldb_stmt`. PG resolves the most recent same-name savepoint, so nesting is safe.
  - If `fn` fails, it runs `ROLLBACK TO SAVEPOINT`; if that also fails, it returns `errors.Join` of both.
  - On success it runs `RELEASE`. A RELEASE error is returned, which can only happen if the transaction is already broken.
  - If the SAVEPOINT itself fails (an already-aborted tx or a cancelled ctx), `fn` is not run and the error is returned. Callers that ignore the result behave as before.
  - A non-DB error from `fn` (for example `ErrNoRows` or `errUsernameTaken`) also rolls back to the savepoint, which is harmless.
  - The helper executes no SQL on MySQL. Its 3 statements count as helper-internal, not app call sites.
- **`NaiveUTCArg` / `NaiveUTCNullArg`:** they bind the UTC wall clock on PG only. An invalid `NullTime` becomes NULL, and on MySQL the original value is passed untouched.

## Duplicate-user retry (`ensureBOUserForMemberTx`): precise classification
- **Statement order in the transaction** (create path): INSERT restaurant_members, contract upsert, then the **first consistent read**, which is either the SELECT bo_users by email (when an email exists) or the username-uniqueness SELECT. After that comes INSERT bo_users and, on error, the retry SELECT by email.
- **MySQL (REPEATABLE READ):**
  - The retry is a plain consistent read against the snapshot taken at that first read.
  - InnoDB's shared lock on the duplicate record does not change what a non-locking SELECT sees.
  - The retry therefore finds a row only if it was **visible in the snapshot**. In practice that means the synthetic-email case (no email, so the first read was the username SELECT) or a pre-existing row the first SELECT already covered.
  - A user committed **after** the snapshot (the race the comment mentions) is not found, so MySQL returns the original error.
- **PG with the savepoint (READ COMMITTED):** each statement takes a fresh snapshot. The retry sees a concurrently committed user and **reuses it**.
- **Actual difference:** only in the concurrent-creation race, and the outcome differs: MySQL returns an error, PG links the existing user. It is the same in the non-race paths. A username-unique conflict that has no email match fails on both engines.
- This stays **OPEN** (an isolation delta). No isolation level was changed globally, and the source sets no per-transaction level.

## Unresolved and not audited
- **Interface receivers (13):** e.g. `adjustOccupancy` and the `getCategory*` queryers. They were classified by their own error handling, and their callers return the errors (reviewed). Callers outside the ported domains (whatsapp_bot, assistant) were not audited.
- **Classifier limits:** the classification is AST and heuristic, not a control-flow proof. A helper returning `any` was trusted by its signature, and a `time.Time` hidden inside a struct field or an interface built in another function is not traced.
- **Domains not ported yet:** pos_billing, whatsapp_bot, site_analytics, tools_cmd, assistant, migration_tooling and db_runner (1253 sites) must apply both rules as they are ported.
- **Other open items:** the isolation default (MySQL RR vs PG RC) is a separate pending item beyond the bo_users case; there is no runtime or concurrency QA; fractional-second rounding ties are unverified.

## Build
`go build -p 2 ./...` exited 0 under the inherited cgroup (200% CPU, 6G memory), with pipefail. No tests, vet, fixtures or Playwright were run.

## Next step
1. Resume site_analytics checkpoint A (72 sites), applying both rules at port time.
2. Optionally extend the same AST audit to non-ported tx callers before they are ported.

## Not done
No commit, merge, deploy, DB writes, providers or sends, and no `.env`, source or proof-data edits.

