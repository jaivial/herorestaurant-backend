# result-native-pos6 — POS checkpoint 6: catalog + PIN (30 sites)

**Runtime-verified: 0 sites. Syntax port only; ready:false.** MySQL stays the default and the server still refuses PG. No login, PIN attempt, counter mutation, secret read, SQL or external effect.

## Scope (2 files complete)
| File | Sites |
|---|---|
| `backoffice_pos_catalog.go` | 18 (3 dynamic) |
| `backoffice_pos_pin.go` | 12 |

## Build and inventory
- `go build -p 2 ./...` gave EXIT=0 (inherited CPU 200% / 6G limits). No tests, vet, fixtures or Playwright.
- Inventory r61, run from the repo root:
  - Canonical 2475 = native 1960 (includes 4 provisional) + blocked 3 + unwrapped 512.
  - Dynamic 79; audit failures 0.
- POS: 169/564 ported, 395 remaining (37 dynamic), 12/35 files done.
- Lexical check: clean.

## Semantics (compile-only)
### Catalog
- **Product CRUD:**
  - Create uses `InsertID`.
  - The PATCH/DELETE n==0 → 404 gate is safe: `version+1` and the `deleted_at IS NULL` filter guarantee the row actually changes.
  - `deleted_at` (timestamptz) uses the statement time.
  - Ordering by category gets `NULLS FIRST` (left-joined categories).
- **Import preview:**
  - The `COLLATE utf8mb4_unicode_ci` clauses are dropped on PG, because both sides already use `ci_ai`.
  - `EXISTS` is cast to integer for the int scan.
  - `precio` is cast to `double precision` for the float64 scan.
- **Import confirm (transaction):**
  - `INSERT IGNORE` categories become `ON CONFLICT (restaurant_id, name) DO NOTHING`; their ignored errors run in savepoints.
  - The `pos_products` upsert becomes `ON CONFLICT (restaurant_id, source_type, source_id)`. That is the only collidable key here: `sku` is NULL and the id is generated.
  - **Affected-row count:** MySQL reports 1 for an insert and 2 for a changed update (`version+1` always changes the row); PG reports 1. The `count++` condition (`> 0`) gives the same result either way.
  - **Wine price `ROUND(precio*100)`:** MySQL promotes FLOAT to DOUBLE before multiplying, so the PG twin casts `precio` to `double precision`. Otherwise PG would compute in float4.
  - `UPDATE … JOIN` becomes `UPDATE … FROM`, keeping the LEFT JOIN so that a missing category sets NULL; ignored errors run in savepoints.
- **Stock rules:**
  - The `INSERT…SELECT` select-list parameters are typed; the quantity goes float64 → `numeric(18,4)` through `double precision`.
  - RowsAffected==0 still means "warehouse not found".
  - `deduction_source` (an enum) is read as text.
  - Readiness `SUM(EXISTS)` becomes `SUM(CAST(EXISTS AS integer))`.

### PIN (`countPINFailure` upsert)
- MySQL applies the SET assignments left to right, so `locked_until` reads the `failed_attempts` value just written.
- In PG's `ON CONFLICT`, the right-hand sides read the old row. The PG twin therefore repeats the new-count `CASE` inside `locked_until`.
- `updated_at` is an instant (timestamptz); `locked_until` is naive UTC.
- The lock window is still interpolated as a server integer constant, as in the source.

### Other PIN statements
- `NULLIF(name, '')` gets the PAD SPACE `rtrim` twin.
- `TRIM(CONCAT(...))` becomes `trim(... || ...)`; both columns are NOT NULL.
- The member insert uses `InsertID`, with the race fallback outside any transaction.

## New source findings (preserved, not fixed)
- **Import-preview `imported` flag bug:** inside the correlated `EXISTS`, the unqualified `source_type` and `source_id` resolve to `p.*` (inner scope first, on both engines). So the flag is true whenever the restaurant has any sourced product.
  - Repair candidate: qualify the columns with `sources.*`.
  - It goes to the dedicated QA batch together with the guest-label fix.
- **Missing PIN columns:** `restaurant_members.pos_pin_hash` and `pos_pin_set_at` are absent from the restored dump schema and from `internal/db/migrations`. The PIN statements fail on both engines.
  - This is dump-only; production has not been checked.

## New gates
- `IGNORE` truncation of category names (`categoria` varchar(140) vs `name` varchar(120)).
- Import-preview ordering: collation differences.

## Carried gates (all unwaived)
- Guest-label repair: QA batch pending.
- Import proof: 1117 pending, ready:false; 4 NOT VALID FKs with 731 orphans.
- Import; suppression `IGNORE` truncation; claim batch size; routing; gap locks.
- NUL / JSON audit drops; search (FTS); TIME range; UUIDs; SEO and multi-key upserts.
- Collation; numeric QA.

## Next POS checkpoint: about 30 sites
- `backoffice_pos_fiscal.go` 23 (1 dynamic) + `backoffice_pos_recall.go` 8 = 31 sites, or
- `backoffice_pos_kitchen.go` 22 + `backoffice_pos_split.go` 8 = 30 sites.

