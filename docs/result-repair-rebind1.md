# result-repair-rebind1: quote-aware PG placeholder rebind, lock release correction, readiness-marker correction

Code only. go build -p 2 ./... exits 0. No tests, fixtures or harness: this is a source audit plus compile. Nothing was executed and nothing was read from a DB. MySQL and SQLite text are untouched. Runtime-verified 0, PG readyfalse. The r5 proof is untouched.

## Lexer contract (`internal/sqldb/rebind.go`, `sqldb_rebind_v2`)
`RebindPostgres(q) (out, n, err)` assumes PostgreSQL with `standard_conforming_strings=on` (the server default since 9.1).

**Skipped as text** (any `?` inside stays literal):
- `'…'` strings, where `''` is a quote and backslash is not an escape.
- `E'…'` escape strings, recognised only when the E is not preceded by an identifier char. Backslash escapes the next char.
- `"…"` identifiers, where `""` is a quote.
- `$tag$…$tag$` / `$$…$$` dollar quotes. A `$` after an identifier char is part of the identifier; `$<digit>` is a numbered parameter.
- `--` comments up to LF (CRLF works), and nested `/* */` comments.

**Refused** (error, never a guessed binding):
- Unterminated string, identifier, dollar quote or comment.
- A MySQL backtick.
- `?|`, `?&`, `??` outside literals.
- Mixing `?` with `$n`.
- **Not lexically detectable:** a bare jsonb `?` (key-exists) operator looks the same as a placeholder. The codebase contract is that PG text never uses it; use `jsonb_exists()`.

**Failure mode:**
- `sqldb.Rebind` (all callers: nativeSQL, portableSQL, pgOnlySQL, analytics, tool twins) logs `[sqldb_rebind_v2] refused` and returns `sqldb_rebind_refused /* reason */`. That statement fails at parse time: no panic, no execution with wrong bindings.
- MySQL input is returned unchanged.
- `botstore.DB.Rebind` now delegates PG text to the same lexer; SQLite is unchanged.

## Caller audit (static, source-only)
- **Scan:** all non-test Go string literals in internal/ and cmd/ containing `?` and an SQL keyword: 3454. The same lexer was ported to a throwaway Python script under `/tmp`; it is not repo code.
- **Findings:** 19, none of them PG text that changes behaviour.
  - 5 MySQL backtick halves (flags, floor, groupmenu_remove, rice_admin, and the inventory regex). They are never rebound on PG.
  - 1 MySQL REGEXP `'(is )?'` half (campaign_email_guard). Its PG twin already uses `{0,1}`.
  - 8 concatenation fragments (server.go menu table quoting, backoffice_menus, pos_pin `INTERVAL '…'`). The full composed statements are well-formed at runtime.
  - 5 non-SQL strings (prose, regex).
- **Result:** for every PG-bound literal, the new placeholder count equals the old rebind's. The change is behaviour-preserving for current SQL and fail-closed for new SQL.
- **Placeholder helpers:**
  - Repeated-arg helpers (`posChangedRowArgs`, `pgChangedRowsPredicate`) only append args. They don't touch placeholders, which are still rebound once over the full composed text.
  - `sqlFragment` returns un-rebound pieces and `portableSQL` rebinds the whole statement once.
- **Residual:** dynamic statements are audited per literal fragment, not per composed string.

## Lock helper correction (`sqldb.TryNamedLock`)
- **The wiring1 claim was wrong.** It said "conn.Close frees the lock even if unlock fails". In fact `database/sql.Conn.Close` returns the physical connection to the pool, and the session advisory lock stays with it.
- **Now:**
  - Release reads the unlock result: PG `pg_advisory_unlock` bool, MySQL `RELEASE_LOCK` = 1.
  - On an error or a not-released result, `conn.Raw(func(any) error { return driver.ErrBadConn })` makes database/sql discard that physical connection. The server session closes and the lock is freed. Then `Close`.
  - On a confirmed unlock, the connection goes back to the pool without the lock.
  - The 10s background context still runs after caller cancellation.
- **Remaining case:** a process killed mid-run. The OS closes the socket and the server ends the session.

## Readiness marker correction (`internal/db.OpenBusinessForTool`)
- **Static source fact:** `internal/dbmigrate/pgimport.go` creates `dbmigrate.marker` and inserts `('import_verified', <report json>)` even when the import is NOT ready. The report fields are `ok`, `ready_for_cutover`, `pending_blocking_decisions` and `fks_not_valid_staging`.
- **wiring1 gate was wrong:** it treated the row's existence as readiness.
- **Now:** the gate parses the value and requires `ok && ready_for_cutover && pending_blocking_decisions == 0 && no NOT VALID FKs`.
- **Not claimed:** whether that row exists or what it holds in the local PG copy. No DB read was done.

## Counts (inventory r81)
- **Call sites:** 2485, all classified.
  - nativeSQL 845
  - portableSQL 1472
  - selectedSQL 82
  - pgOnlySQL 42
  - mysqlOnlySQL 44
- **Helper-internal (excluded):** 13.
- **Provisional / blocked:** 0 / 0.
- **Runtime-verified:** 0.

## Gates (unwaived)
- **Import proof:** 1117 pending, readyfalse; 4 NOT VALID FKs / 731 orphans.
- **Inventory gates:** IGNORE truncation, gap locks, NUL/JSON, search, TIME, UUID node identity, multikey, collation/numeric, fiscal hash, 136 onupdate.
- **Domain/isolation fixes:** not runtime-validated.
- **Stripe:** finite Stripe key retention; event recorded before processing.
- **DNS proof:** absent.
- **Provider/demo:** orphaned provider resources; demo Stripe Connect shared hash.
- **Lexer:** the bare jsonb `?` limitation.
- **Pending repairs:** guest-label/catalog-scope repairs pending dedicated QA; PIN columns dump-only.

## Next: critical semantics repairs, then a QA plan
1. **Stripe reconcile:**
   - Before reusing an `uncertain:` key older than the retention window, look up the session (by stored id or metadata).
   - Add a processed flag on `stripe_events`: a row is claimed, processing succeeds, then it is marked processed; a retry of an unprocessed event re-runs.
2. **IGNORE truncation sites:** suppressions, outbox, categories, fiscal series. Make the length checks explicit in code so PG matches MySQL non-strict truncation where the source relies on it.
3. **Gap-lock duplicate-create sites:** transaction-scoped advisory locks on the natural key, for all writers.
4. **TIME ±838h and NUL/JSON audit adapters; collation/PAD SPACE comparisons:** keep them explicit.
5. **Local PG copy runtime QA:**
   - Read-only checks first: schema readiness, then a smoke of each domain's read paths.
   - Then write paths on a disposable copy.
   - Then lift the tool gate, then the server refusal, only after QA passes.

