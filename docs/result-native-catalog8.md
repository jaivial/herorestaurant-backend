# Native PG port: catalog_stock group 8 (backoffice_comida_categories.go + comida insert-id/float follow-ups; compile only, NOT runtime-verified, NOT PG-ready)

## Counts (inventory r25: /tmp/villacarmen-bot-20261111/sql-inventory-r25.json, dbmigrate-r12)
- **Repo:** 2472 call sites, 857 native (+22), 252 dynamic. portable_audit_failures is 0.
  - Excluded: 5 helper-internal calls and 3 prepared-stmt calls.
- **Core:** 406/406. **Bookings:** 161/161.
- **catalog_stock:** 290/504 native, 214 remaining (54 dynamic). 9/43 files are done; **backoffice_comida_categories.go is 22/22 (10 dynamic)**.
  - Ledger: `docs/dbmigrate/native-ledger-catalog.json`.
- **Build:** `go build -p 2 ./...` exits 0 under the inherited cgroup (CPU 200%, 6G). No SQL was run, and no tests, vet, fixtures or Playwright.

## 1. comida.go custom-category upsert insert id (from the last checkpoint)
**Static grounding:**
- The three upserts contain **no `LAST_INSERT_ID(id)`**. Only backoffice_pos.go uses `LAST_INSERT_ID(expr)`.
- go-sql-driver/mysql (v1.7.1/1.9.3/1.10.1 in the module cache) returns the OK-packet `insert_id` verbatim.
- So the value on the ODKU **update/no-change path is purely server behaviour** and cannot be established from this repository without a server run.

**Source-faithful pathway now implemented:**
- PG uses `RETURNING CASE WHEN xmax = 0 THEN id ELSE 0 END`.
  - An insert returns the id, the same as MySQL.
  - A conflict returns 0, so the source's own fallback (`getCategoryBySlugOrName`: slug OR LOWER(name), LIMIT 1, no ORDER BY) runs exactly as written.
- This replaces last checkpoint's deterministic slug-row id.

**Gap, not claimed equivalent:**
- The two match only if MySQL reports 0 on that path.
- If MySQL reports the existing id, MySQL skips the lookup while PG may return a name-matching row.
- This needs a coordinator read-only or staging probe.

## 2. Wine price `group_menu_wines_v2.precio float(10,2)`
- **Source:** this is the only FLOAT(M,D) column in the 253-table source.
- **Plan mapping:** `real`, with no D=2 rounding and no M range check.
- **Known MySQL behaviour:** it rounds to 2 decimals before storing as float32.
- **Not established statically:** the exact rounding mode.
- **Proposal only:** `docs/dbmigrate/plan-revision-proposal-float-md.json`.
  - Option A, recommended: a trigger rounding `round(numeric,2)::real` plus a range CHECK, after confirming the rounding mode.
  - Option B: rounding at app write sites, which are spread across 5 files.
  - Option C: rejected, because numeric changes the float32 read-back values.
- **No app rounding was applied**, to avoid arbitrary double rounding. The imported proof schema, data and plan.sql are not modified.
- This is a schema gap, kept distinct from app behaviour.

## 3. backoffice_comida_categories.go (22 sites)
- **Static statements:** all 11 are portable text — plain SELECT/UPDATE/DELETE, `FOR UPDATE`, `ORDER BY CASE`, and COALESCE on varchar columns.
  - comida_categories has no JSON, enum or decimal columns.
- **Create:**
  - Uses `InsertID`, a single statement. Genuine errors still surface.
  - Duplicate detection: `isDuplicateKeyErr` now calls `sqldb.IsUniqueViolation`, which matches MySQL 1062 (unchanged) or PG 23505, the same breadth.
  - Before this, a PG duplicate would have answered 500 instead of 409.
  - The unused mysql import was removed.
- **Dynamic statements (10):**
  - The legacy table comes from the fixed `comidaCategoryLegacyTables` map, the where fragments are internal parameterized predicates, and the `?` lists are generated.
  - Each composed statement is wrapped once with `portableSQL`.
  - `FOR UPDATE` locking and the twin-reconcile ordering `ORDER BY active DESC, id` are unchanged.
- **RowsAffected:** no gates in this file.
- **Pending:** name/slug equality, `ORDER BY name` and the twin predicate `name = ?` depend on utf8mb4_unicode_ci (CI/accent/PAD SPACE). **Not accepted as equivalent.**

## Unchanged pending (none waived)
- **Search and collation:** CI/accent/PAD SPACE; FULLTEXT.
- **Ranges:** 190 range narrowings.
- **Schema time semantics:** ON UPDATE (136) / DEFAULT (353).
- **The recent_conversations view.**
- **QA items:** NUL; JSON text; JSON timestamps.
- **Schema gaps:** JSON_CONTAINS nested-array semantics; the float(M,D) gap.
- **Integrity and readiness:** 4 NOT VALID FKs covering 731 orphans; 1117 pending, ready:false.
- **Source anomalies.**

MySQL stays the default and the server still refuses PG. No DB writes, activation, providers, payments, email, WhatsApp, sends, .env/proof edits, commits or deploys.

## Next
1. **backoffice_stock_analytics.go:** 26 sites, 0 dynamic.
2. **Then:**
   | file | remaining | dynamic |
   |---|---|---|
   | public_menus.go | 18 | 10 |
   | backoffice_stock_suppliers.go | 17 | 2 |
   | group_menus.go | 14 | 6 |
   | menu_section_dessert_source.go | 11 | 1 |

