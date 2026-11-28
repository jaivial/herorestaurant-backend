# result-native-pos5 — POS checkpoint 5: customers + line move (29 sites)

**Runtime-verified: 0 sites. Syntax port only; ready:false.** MySQL stays the default and the server still refuses PG. No SQL and no external effects.

## Scope (2 files complete)
| File | Sites |
|---|---|
| `backoffice_pos_customers.go` | 16 (1 dynamic) |
| `backoffice_pos_line_move.go` | 13 |

## Build and inventory
- `go build -p 2 ./...` gave EXIT=0 (inherited CPU 200% / 6G limits). No tests, vet, fixtures or Playwright.
- Inventory r60:
  - Canonical 2475 = native 1930 (includes 4 provisional) + blocked 3 + unwrapped 542.
  - Dynamic 83; audit failures 0.
- POS: 139/564 ported, 425 remaining (41 dynamic), 10/35 files done.
- Lexical check: clean; no double-wrapped statements.
- **Process note:** one intermediate inventory run went to the wrong directory (after a failed chained step) and printed 2475 → 2325. It was rerun from the repo root; r60 is the correct run.

## Semantics (compile-only)
### Customers
- **Search:**
  - The LIKE on `ci_ai` columns becomes `LOWER(col) COLLATE "C"` with a lowered pattern.
  - The source escapes `%` and `_` with a backslash, and backslash is the default escape character on both engines.
  - The phone LIKE uses collation C.
- **Duplicate detection:**
  - The `? <> ''` checks are typed as text.
  - The unique keys on (restaurant, phone) and (restaurant, email) use `ci_ai`, so case-insensitive collisions are kept; accent and PAD SPACE differences remain open.
  - After a duplicate insert, the conflict re-read runs outside any transaction, so no savepoint is needed.
- **Writes:**
  - Create uses `InsertID`.
  - PATCH with n==0 then checks existence, so matched vs changed rows give the same response.
  - Anonymise uses the naive UTC statement time.
  - The audit inserts run outside a transaction and their errors are ignored, as in the source.
  - The customer link writes `jsonb_build_object` with a typed bigint.
- Ticket `status` is read as text; `SUM(numeric)` is scanned into float64.

### Line move (inside a transaction)
- **Split:**
  - `ROUND(CAST(? AS double precision) * cents) - CAST(? AS bigint)`.
  - The new line's id comes from `InsertID`.
  - The modifier and tag copies (`INSERT…SELECT`) cast the `?` in the select list explicitly.
- The kitchen sent-by-station `UNION ALL` adds `numeric(12,3)` quantities and is scanned into float64.
- Every transaction error propagates; no statement errors are ignored.
- **Idempotency read:**
  - `CAST(JSON_UNQUOTE(JSON_EXTRACT(..)) AS UNSIGNED)` becomes `CAST(after_json->>'movedLineId' AS bigint)`. This code always writes that value as an integer, so the result matches.
  - The general `CAST AS UNSIGNED` edge-case gate still applies.
  - The JSON-null key difference (`'null'` on MySQL vs NULL on PG) cannot occur, because this code never writes a null key.
- **Audit JSON:**
  - `full` is a Go bool. go-sql-driver sends it as 1/0, so MySQL stores the number 1/0; the PG twin keeps 1/0 with `CAST(CAST(? AS boolean) AS integer)`.
  - `quantity` is a double. Its JSON number text can differ (`2.0` vs `2`); this needs QA. The idempotency lookup does not read it.

## New gates
- Customer search: accent and PAD SPACE behaviour.
- JSON number text of the audit `quantity`.
- Customer-name ordering: collation differences.

## Carried gates (all unwaived)
- Guest-label repair: dedicated QA batch (technical fix); parity kept for now.
- Import proof: 1117 pending, ready:false; 4 NOT VALID FKs with 731 orphans.
- Import; NUL / JSON audit drops; gap locks.
- `IGNORE` truncation in suppressions; claim batch size; routing.
- Search (FTS); TIME range; collation; numeric QA.
- UUIDs; SEO and multi-key upserts.

## Next POS checkpoint: about 30 sites
- `backoffice_pos_catalog.go` 18 (3 dynamic) + `backoffice_pos_pin.go` 12 = 30 sites.

