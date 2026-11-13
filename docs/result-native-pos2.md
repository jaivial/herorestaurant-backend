# result-native-pos2 — POS checkpoint 2: `backoffice_pos_operations.go` (27 sites)

**Runtime-verified: 0 sites. Syntax port only; ready:false.** MySQL stays the default and the server still refuses PG. No SQL, DB writes or external effects.

## Build and inventory
- `go build -p 2 ./...` gave EXIT=0 (inherited CPU 200% / 6G limits). No tests, vet, fixtures or Playwright.
- Inventory r57:
  - Canonical 2475 = native 1848 (includes 4 provisional) + blocked 3 + unwrapped 624.
  - Dynamic 94; audit failures 0.
- POS: 57/564 ported, 507 remaining (52 dynamic), 2/35 files done.
- Lexical check: three intentional arity flags (lines 79, 387, 389). These are the PG changed-rows predicates; the extra arguments are supplied.

## Semantics (compile-only)
### Changed-rows contracts kept
- **Service-period PATCH:** MySQL counts only changed rows, so saving identical values returns a 404. The PG twin adds a typed `ROW(...) IS DISTINCT FROM` predicate over name (collation C), the enum, start/end time, sort order and active flag.
- **Line quantity PATCH:** the same contract, over (quantity, recomputed line total, notes).
- **`ROUND(? * cents)`:** the PG twin casts the parameter to `double precision`. Without the cast, PG would infer `bigint` and truncate fractional quantities. MySQL's DOUBLE `ROUND` and PG's `round(double)` both use rint (half-even) in the code I read. Rounding still needs QA.

### Ticket create
- The duplicate-key path reads the existing ticket inside the same transaction. On PG a failed insert aborts the transaction, so the insert now runs in `TxStatementScope` and the follow-up read still works (otherwise the result would be a 500).
- PG's "duplicate key value violates unique constraint" message matches the `"duplicate"` substring check.
- The id comes from `InsertID`.

### Visit cancel
- Two statements whose errors the source ignores — the table-id read and the void-tickets update — each run in a savepoint on PG; the commit is kept.
- Timestamps: `voided_at` (timestamptz) uses the statement time; `closed_at` (naive) uses the UTC wall clock.

### Cash closure
- `summary.OpenedAt` and `time.Now()` are bound with `NaiveUTCArg`. Without it, pgx would store the process's local wall clock.

### Other
- Enums are read as text.
- Shift and service-period ids come from `InsertID`.
- An open-shift unique violation still maps to the existing 409 path.

## New source anomaly (preserved, not fixed)
- The guest-label UPDATE's raw Go string contains `NULLIF(?,\'\')` with literal backslashes. That is invalid SQL on MySQL and on PG, so the statement fails on both engines.
- I kept the failure. The candidate fix is `NULLIF(?,'')`, which needs separate approval and QA.

## Gates (all unwaived)
- **New:** POS float rounding QA; the guest-label anomaly.
- **Carried:**
  - Import proof: 1117 pending, ready:false; 4 NOT VALID FKs with 731 orphans.
  - Gap locks; NUL / JSON audit drops; UUIDs; SEO and multi-key upserts; routing.
  - `IGNORE` truncation (suppressions and outbox); claim batch size.
  - Collation; numeric QA; search (FTS); TIME range.
- **Botstore pool timezone:** the unified-schema path pins UTC; only the schema-less fallback uses the server default.
- Production-confirmed source anomalies are listed in earlier reports.

## Next coherent POS checkpoint: `backoffice_pos_cash.go` (25 sites, 8 dynamic)
- Cash closures and summaries, the companion to the shift close above.
- After that:
  - `pos_stock_realtime` 25
  - `pos_fiscal` 23
  - `pos_packs` 23
  - `pos_kitchen` 22
  - then rail, checkout, core POS and premium in parts of about 30 sites.

