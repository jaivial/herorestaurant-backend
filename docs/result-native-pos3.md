# result-native-pos3 — POS checkpoint 3: `backoffice_pos_cash.go` (25 sites, 8 dynamic)

**Runtime-verified: 0 sites. Syntax port only; ready:false.** MySQL stays the default and the server still refuses PG. No cash, close or payment path was executed; no SQL and no external effects.

## Build and inventory
- `go build -p 2 ./...` gave EXIT=0 (inherited CPU 200% / 6G limits). No tests, vet, fixtures or Playwright.
- Inventory r58:
  - Canonical 2475 = native 1873 (includes 4 provisional) + blocked 3 + unwrapped 599.
  - Dynamic 86; audit failures 0.
- POS: 82/564 ported, 482 remaining (44 dynamic), 3/35 files done.
- Lexical check of the 12 dialect pairs: clean.

## Semantics (compile-only)
### Summaries
- All figures are integer-cent bigint `COUNT` / `SUM` / `COALESCE`, identical on both engines.
- `method` (an enum) is read as text.
- The dynamic shift, period and day scopes are composed and then rebound once.
- **Period scope:** `COALESCE(paid_at, voided_at, NOW())` on naive columns uses the UTC statement time. `since` and `refundSince` are bound with `NaiveUTCArg`.

### Idempotency
- **Movements:** the pre-check and insert run outside a transaction, as in the source. A race falls through to the existing duplicate path on both engines.
- **Closures:** the insert runs inside the transaction, wrapped in `TxStatementScope`, so the duplicate re-read still works on PG. Ids come from `InsertID`.
- **`closed_at`:** `time.Now()` goes through `posNaiveAny` / `NaiveUTCArg`.

### Audit events
- `JSON_OBJECT` becomes `jsonb_build_object` with explicitly typed arguments.
- The closure audit runs inside the transaction and its error is ignored, so it gets a savepoint. The movement audit runs outside any transaction.
- jsonb normalisation still needs QA.

### Enums
- Shift `status`, `closure_type` and `movement_type` are read as text.

## Review of pos2
- **Correction:** `pos_tickets.voided_at` is a naive `timestamp(0)`, not `timestamptz` as pos2 stated; I had taken the type from `pos_ticket_lines`. The visit-cancel PG twin now uses the UTC statement wall clock. Fixed in code.
- **Argument counts:** re-checked against the actual declarations; all three are correct.
  - Service-period update: 14 placeholders = 6 SET + 2 WHERE + 6 predicate.
  - Line update with notes: 9 = 3 + 3 + 3.
  - Line update without notes: 7 = 2 + 3 + 2.
- **Quantity:** the column is `numeric(12,3)`. The explicit `double precision` cast appears only where the quantity multiplies bigint cents. Without it, PG would infer bigint and truncate.

## Repair candidate: guest-label update (an ordinary technical bug, not a business decision)
- **Bug:** the raw string in `backoffice_pos_operations.go` contains `NULLIF(?,\'\')`. Both MySQL and PG reject it, so the guest-label endpoint always returns 500.
- **Fix:** `NULLIF(?,'')` on both dialects, plus `padSpaceNullIfArg` on PG.
- **Risk:** it enables a write path that is currently dead. That path only renames the guest label on OPEN tickets, is scoped to the tenant and is status-checked first.
- **Plan:** apply it in a dedicated repair batch with QA. Parity (the same failure on both engines) is kept for now.

## Gates (all unwaived)
- Import proof: 1117 pending, ready:false; 4 NOT VALID FKs with 731 orphans.
- Collation; NUL / JSON audit drops; gap locks; UUIDs; SEO and multi-key upserts; routing.
- `IGNORE` truncation in suppressions; claim batch size; search (FTS); TIME range.
- Numeric and POS float rounding QA.

## Next POS checkpoint: about 25 sites, the companion to cash
- `backoffice_pos_cash_days.go`: 18 sites (1 dynamic).
- Plus the small POS remainders, if they are coherent.
- Alternatively `backoffice_pos_stock_realtime.go`: 25 sites.

