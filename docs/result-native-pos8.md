# result-native-pos8 — POS checkpoint 8: kitchen + split (30 sites)

**Runtime-verified: 0 sites. Syntax port only; ready:false.** MySQL stays the default and the server still refuses PG. No dispatch, void, SQL or external effect.

## Scope (2 files complete)
| File | Sites |
|---|---|
| `backoffice_pos_kitchen.go` | 22 (4 dynamic) |
| `backoffice_pos_split.go` | 8 |

## Build and inventory
- `go build -p 2 ./...` gave EXIT=0 (inherited CPU 200% / 6G limits). No tests, vet, fixtures or Playwright.
- Inventory r63, run from the repo root:
  - Canonical 2475 = native 2021 (includes 4 provisional) + blocked 3 + unwrapped 451.
  - Dynamic 74; audit failures 0.
- POS: 230/564 ported, 334 remaining (32 dynamic), 16/35 files done.
- Lexical check: intentional arity flags only, on the changed-rows predicates at `kitchen.go:109` and `split.go:57`.

## Semantics (compile-only)
### Station and category PATCH
- MySQL counts only changed rows, so an unchanged save returns a 404.
- The PG twins add a typed `ROW(...) IS DISTINCT FROM` predicate, using `posChangedRowArgs`.

### Routes
- The `INSERT…SELECT` has typed parameters and uses `InsertSelectID`: no active station gives 0 rows and a 404 on both engines.
- **Route lookup:** `SELECT DISTINCT station_id … ORDER BY priority, id` is invalid on PG, because it orders by a column it does not select.
  - MySQL accepts it (`ONLY_FULL_GROUP_BY` is off), but which row's priority is used is undefined.
  - The PG twin uses `GROUP BY station_id ORDER BY MIN(priority), MIN(id)`.
  - The result is only appended into a map keyed by station, so the order does not affect it.

### Dispatch transaction
- The insert runs in `TxStatementScope`, so the duplicate re-read (by command key or sequence) works on PG. The id comes from `InsertID`.
- Both course filters (`NULLIF(course, '')`) get the PAD SPACE `rtrim` twin.
- The ack and ready times are the naive UTC statement time.
- `CASE ?` parameters are typed as text; enums are read as text.

### Activation readiness
- `SUM(EXISTS)` becomes `SUM(CAST(EXISTS AS integer))`.
- `HAVING expected<>actual` refers to SELECT aliases, which only MySQL allows. The PG twin repeats the expressions.
- **Cross-enum comparison:** `service_type` is a different enum type in `stock_affluence_daily`, `pos_cover_adjustments` and `pos_visits`. PG cannot compare distinct enum types directly, so the twin compares them as text, matching MySQL's ENUM string comparison.

### Acceptance and void
- The acceptance insert writes `snapshot_json` as jsonb and uses `InsertID`.
- Ticket void sets `voided_at` to the naive UTC statement time.
- The void audit is ignored inside the transaction, so it gets a savepoint.
- The category in-use `EXISTS` is cast to integer.

## New gates
- `MIN(priority)` route ordering in place of MySQL's undefined order; it does not affect the outcome.

## Carried gates (all open)
- Repair batch (technical, needs QA): guest-label `NULLIF`; catalog `EXISTS` scope bug. Parity kept for now.
- Source anomalies: PIN columns are dump-only.
- Fiscal hash-chain runtime QA.
- Import proof: 1117 pending, ready:false; 4 NOT VALID FKs with 731 orphans.
- Import; gap locks; NUL / JSON audit drops.
- `IGNORE` truncation (suppressions, categories, fiscal series); claim batch size; routing.
- Search (FTS); TIME range; numeric and collation; UUIDs; SEO and multi-key upserts.

## Next POS checkpoint: about 30 sites (small files)
| File | Sites |
|---|---|
| `backoffice_pos_courses` | 3 |
| `backoffice_manual_billing` | 4 (1 dynamic) |
| `backoffice_postres` | 4 (1 dynamic) |
| `stripe_connect` | 4 |
| `backoffice_invoices_billing` | 5 |
| `site_builder_billing` | 5 |
| `backoffice_invoice_comments` | 6 |
| **Total** | **31** |

`stripe_connect` is SQL only; no Stripe calls will be made.

