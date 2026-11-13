# result-native-pos4 — POS checkpoint 4: cash days and small POS leftovers (28 sites)

**Runtime-verified: 0 sites. Syntax port only; ready:false.** MySQL stays the default and the server still refuses PG. No SQL and no external effects.

## Scope (5 files complete)
| File | Sites |
|---|---|
| `backoffice_pos_cash_days.go` | 18 |
| `backoffice_pos_cash_days_billing.go` | 3 |
| `backoffice_pos_cash_days_reports.go` | 3 |
| `backoffice_pos_pin_policy.go` | 2 |
| `backoffice_pos_reservations.go` | 2 |

## Build and inventory
- `go build -p 2 ./...` gave EXIT=0 (inherited CPU 200% / 6G limits). No tests, vet, fixtures or Playwright.
- Inventory r59:
  - Canonical 2475 = native 1901 (includes 4 provisional) + blocked 3 + unwrapped 571.
  - Dynamic 84; audit failures 0.
- POS: 110/564 ported, 454 remaining (42 dynamic), 8/35 files done.
- Lexical check: clean.

## Semantics (compile-only)
### Cash-day select
- `posCashDaySelectSQL()` reads `d.status` (an enum) as text on PG; the MySQL const is kept.
- `forced_open` (smallint) scans into a bool through database/sql's int64 conversion, the same on both engines.

### Open and close
- **Open:** the id comes from `InsertID`. A violation of `uq_pos_cash_days_date` still returns 409; PG's message matches the `"duplicate"` substring check.
- **Close:**
  - The closure insert uses `InsertID` inside the transaction. Its duplicate path returns 409 without re-reading, so no savepoint is needed.
  - `closedAt` and `summary.OpenedAt` are bound with `NaiveUTCArg` into the naive timestamps of `pos_cash_days`, `pos_shifts` and `pos_cash_closures`.
  - The close audit runs inside the transaction and its error is ignored, so it gets a savepoint. The open audit runs outside any transaction.
  - `jsonb_build_object` uses typed arguments.

### Totals, billing and reports
- All amounts are integer-cent bigint sums and counts.
- Enums are read as text.
- `COALESCE(t.status, '')` becomes `COALESCE(CAST(t.status AS text), '')`; on PG, `''` is not a valid enum label and would raise an error.
- Enum `ORDER BY` follows declaration order on both engines.
- The report's nullable `table_id` sort gets `NULLS FIRST`.

### Reservations search
- `DATE_FORMAT` / `TIME_FORMAT` become `to_char`.
- The name LIKE uses `LOWER(...) COLLATE "C"` with a lowered pattern; the phone LIKE uses collation C.
- The empty-query check is typed as text.
- **Gap:** accent and PAD SPACE search behaviour is not reproduced.

## Timestamp-type audit (a cheap pass prompted by the pos3 correction)
- None of the ported POS statements writes a timestamptz statement time.
- These are all naive `timestamp(0)`, and every ported statement treats them that way:
  - `pos_tickets` paid_at and voided_at
  - `pos_visits` opened_at and closed_at
  - `pos_shifts` opened_at and closed_at
  - `pos_cash_days`, `pos_cash_closures`
  - `pos_cash_movements` and `pos_refunds` created_at
- Only `pos_ticket_lines` voided_at / created_at and `pos_service_periods` updated_at are timestamptz, and no ported statement writes them.
- No further mismatch found.

## Gates (all unwaived)
- Guest-label repair: pending a dedicated QA batch (technical fix); parity kept for now.
- Import proof: 1117 pending, ready:false; 4 NOT VALID FKs with 731 orphans.
- Source anomalies; routing; `IGNORE` truncation in suppressions; claim batch size; gap locks.
- NUL / JSON audit drops; search (FTS); TIME range; UUIDs; SEO and multi-key upserts.
- Collation; numeric and float rounding QA.

## Next POS checkpoint: about 30 sites
- `backoffice_pos_customers.go` 16 (1 dynamic) + `backoffice_pos_line_move.go` 13 = 29 sites, or
- `backoffice_pos_catalog.go` 18 (3 dynamic) + `backoffice_pos_pin.go` 12 = 30 sites.

