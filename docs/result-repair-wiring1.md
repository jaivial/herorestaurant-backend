# result-repair-wiring1: cutover wiring (code only, nothing activated)

go build -p 2 ./... exits 0. Runtime-verified 0. Nothing was executed: no DB connection, SQL, lock or tool run. MySQL stays the default and the server still refuses PG. The r5 proof is untouched.

## Dialect selection (`db_dialect_selection_v1`)
- **`internal/db.BusinessDialect(DB_DRIVER)`:** "" or "mysql" → MySQL; "postgres" → Postgres; anything else → error. There is no fallback between engines.
- **cmd/server:**
  - Refuses an invalid DB_DRIVER.
  - Still refuses postgres ("not runtime-verified (cutover blocked)").
  - Sets the dialect once via the new `api.SetSQLDialect` and the existing `analytics.SetSQLDialect` (MySQL while refused).
  - MySQL open and migrations are unchanged.
- **Tools:** `internal/db.OpenBusinessForTool(ctx, cfg)`.
  - **MySQL:** the existing `OpenMySQL`, unchanged.
  - **PostgreSQL:** fails closed unless all of these hold:
    - `DB_PG_TOOLS_QA_APPROVED=1` is set. It is an explicit reviewed decision, not proof.
    - `DATABASE_URL` is set. There is no MySQL fallback.
    - The ping succeeds.
    - `dbmigrate.marker` has `import_verified`. That marker is written only by a verified PG import, and is not present today because the import proof is readyfalse.
  - **PG pool:** `sqldb.OpenPostgres(DATABASE_URL, "public")`: search_path=public, TimeZone=UTC, timestamptz ScanLocation=UTC.
  - **No schema creation or migration on PG:** the schema comes only from the import plan.

## Wired entrypoints
| entrypoint | MySQL | PostgreSQL |
|---|---|---|
| cmd/server | unchanged | refused |
| cmd/ops-check | OpenMySQL + MySQL migrations | gates above; MySQL migrations skipped; twins selected via `dialect` |
| cmd/cdn-sweep | OpenMySQL, schema = MySQL DB name | gates above; registry schema `public` (information_schema lower-case, quoted identifiers); **`--apply` refused** (dry-run only until QA) |
| analytics | MySQL | set from the server dialect (server refuses PG) |
| botstore unified (`bot_context`) | n/a | unchanged (OpenPostgres with schema pin, UTC) |
| botstore legacy schemaless PG | n/a | now `sqldb.OpenPostgresUTC`: UTC session and scan pin, no search_path (closes the legacy UTC gap) |

## Advisory / run locks
- **New helper:** `sqldb.TryNamedLock(ctx, dialect, db, name)`.
  - Takes the lock on one pinned `*sql.Conn`: MySQL `GET_LOCK(?,0)`, PG `pg_try_advisory_lock(hashtextextended($1,0))`.
  - Release runs on the same connection with a fresh 10s background context, so it still works after the caller's ctx was cancelled or a step failed. The connection is then closed, which ends the session and frees the lock even if the unlock statement failed.
- **Used by:** ops-check, cdn-sweep and comida-stock-backfill (MySQL-only tool).
- **Removes** the pooled-session lock/release mismatch on both engines.
- **log.Fatal paths:** they skip deferred releases, but process exit closes the session.

## Counts (inventory r80)
- **Call sites:** 2485, all classified.
  - nativeSQL 845
  - portableSQL 1472
  - selectedSQL 82
  - pgOnlySQL 42
  - mysqlOnlySQL 44
- **Delta vs r79:**
  - −6 pooled lock statements, now inside the sqldb helper (helper-internal 9 → 13).
  - +1 PG readiness marker query.
- **Provisional / blocked:** 0 / 0.
- **Runtime-verified:** 0.

## What still cannot work on PostgreSQL (actionable)
1. **The whole server/app.** DB_DRIVER=postgres is refused until runtime QA of the full business path on the local PG copy, and the import proof (1117 pending, ready:false) is still open.
2. **ops-check and cdn-sweep on PG** need:
   - an import-verified PG with the marker;
   - `DB_PG_TOOLS_QA_APPROVED=1`;
   - and, for cdn-sweep, QA of the registry/schema match before `--apply` can be allowed.
3. **MySQL-only runtime utilities.** They have no PG path; port only if they are needed after cutover:

   | tool | sites |
   |---|---|
   | backfill-translations | 9 |
   | comida-stock-backfill | 6 SQL + lock helper |
   | group-menu-arroz-cleanup | 5 |
   | update-restaurant-info | 4 |
   | migrate-wine-images | 3 |
   | chatgpt-plugin-token | 1 |
   | seed-legal-pages | 1 |

4. **Legitimately MySQL-only, staying:**
   - cmd/dbmigrate source check (1).
   - internal/dbmigrate source reads (4).
   - internal/db bootstrap + MySQL migration runner (5).

## Retained critical caveats (not external blockers: code to do)
- **Stripe idempotency keys** are retained for a finite window (about 24h). An `uncertain:<key>` attempt older than that cannot be replayed safely; it needs a reconcile step (retrieve or list the session by metadata) before any new key. Do not assume indefinite retry.
- **`stripe_events` is recorded before processing,** so a processing failure turns Stripe retries into duplicates. A processed flag and reconcile are needed.
- **DNS proof protocol:** to be designed later. Unverified mappings must not activate routing; the router's mapping lookup stays dead today.
- **No further domain feature code** in this batch.

## Gates (unwaived)
- **Import proof:** 1117 pending, readyfalse; 4 NOT VALID FKs / 731 orphans.
- **SQL semantics:** IGNORE truncation, gap locks, NUL/JSON, search, TIME, UUID node identity, multikey, collation/numeric, fiscal hash, 136 onupdate.
- **Domain/isolation fixes:** not runtime-validated.
- **Provider resources:** orphaned provider resources; demo Stripe Connect shared hash.
- **Rebind:** botstore and sqldb Rebind are not quote-aware.
- **Pending repairs:** guest-label/catalog-scope repairs pending dedicated QA; PIN columns dump-only.

## Next
1. **Quote-aware Rebind:**
   - sqldb.Rebind and botstore Rebind skip '…', "…", `…`, -- / /* */ comments and $tag$ dollar-quotes.
   - Fail closed on jsonb `?`, `?|`, `?&` operators outside literals.
   - Then audit every caller for literal '?'.
2. **Critical semantics repair + QA plan:**
   - Stripe reconcile (key expiry, processed flag).
   - IGNORE truncation sites (suppressions, outbox, category, fiscal).
   - Gap-lock duplicate-create sites (advisory locks).
   - TIME ±838h, NUL/JSON audit, collation/PAD SPACE.
   - Then a local runtime QA run on the PG copy (read-only checks first), and only then lift the server refusal.

