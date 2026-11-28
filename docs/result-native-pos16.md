# result-native-pos16: backoffice_pos_rail (53 sites, 2 dynamic). POS domain syntax-complete

Code only. go build -p 2 ./... exits 0. Runtime-verified: 0. PG is readyfalse, the server refuses PG, and MySQL stays the default. No rail, checkout or provider path was invoked.

## Counts (inventory r71)
- **Repo:** 2475 = native 2355 (incl 5 provisional, none new) + blocked 3 + unwrapped 117. Dynamic: 42. Audit failures: 0. Delta vs r70: +53.
- **POS billing:** 564/564 syntax-ported, 35/35 files. Syntax coverage only; no semantic validation.
- **Nested-wrapper audit:** repo-wide grep for nativeSQL/portableSQL nesting returns 0. This phase used exact whole-literal wraps with count assertions instead of prefix anchors.

## Work
- **Merge tx:**
  - The merge-key duplicate re-read is savepoint-scoped.
  - The JSON source list is cast to jsonb.
  - Four MySQL UPDATE..JOIN statements became UPDATE..FROM with the same argument order. The moved-lines count is unchanged: every matched row changes.
  - The ignored line-stock re-point and the MERGE audit run in savepoints.
  - INSERT IGNORE tags (INSERT..SELECT) became a typed parameter plus ON CONFLICT on the PK, DO NOTHING.
  - voided_at and closed_at use naive UTC statement time.
- **Audits:** JSON_OBJECT became typed jsonb_build_object. Go bools (parked, comped) become JSON 1/0 on PG, matching go-mysql's TINYINT binding (code review only).
- **Drawer and tag ids:** both use InsertID RETURNING.
- **Adjustment and COMP audits:** ignored inside the tx, so they run in savepoints.
- **Operator and tag-validity queries:** dynamic statements rebound once.
- **Line and ticket tag INSERT IGNORE:** ON CONFLICT on the PK.
- **Enums:** read as text.

## Gates (unwaived)
- **New in this file:**
  - **INSERT IGNORE on tags:** MySQL also downgrades FK errors to warnings; on PG they raise.
  - **Bool→JSON 1/0 audits:** need runtime JSON QA.
  - **Tag name ordering:** collation gate.
- **Carried over:**
  - **pos13:** concurrent domain-register cross-tenant takeover (unaccepted, must be resolved before activation).
  - **pos14:** lenient UNSIGNED ticket catch-up cast; duplicate re-read isolation.
  - **pos10–15:** all gates.
  - **Repo-wide:** 1117 import proof readyfalse; 4 NOT VALID FKs / 731 orphans; collation, search, UUID, SEO, multikey, routing, NUL, TIME, numeric, fiscal hash, 136 onupdate.
  - **Repairs and dump-only:** guest-label/catalog-scope technical repairs pending dedicated QA; PIN columns are dump-only.

## Next: tooling, 117 sites
Split into checkpoints in this order:
1. internal/botstore: 37
2. internal/dbmigrate: 18
3. cmd/ops-check: 15
4. The rest:
   - backfill-translations 9
   - cdn-sweep 8
   - comida-stock-backfill 8
   - group-menu-arroz-cleanup 5
   - update-restaurant-info 4
   - db/migrations 4
   - migrate-wine-images 3
   - botstore-migrate 2
   - chatgpt-plugin-token 1
   - cmd/dbmigrate 1
   - seed-legal-pages 1
   - internal/db 1

