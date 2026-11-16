# result-native-tool1: internal/botstore (37 sites)

Code only. go build -p 2 ./... exits 0. Runtime-verified 0. PG readyfalse, server refuses PG, MySQL default. No migration, copy, import or rollback was re-run; proof data untouched.

## Counts (inventory r72)
- **Repo:** 2475 = native 2392 (incl 5 provisional) + blocked 3 + unwrapped 80; dynamic 21; audit failures 0.
- **Delta vs r71:** +37.
- **Syntax complete:** POS 564/564, WhatsApp 316/316, botstore 37/37.
- **Remaining tooling (80):**
  - internal/dbmigrate 18
  - cmd/ops-check 15
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

## Work: marker-only, no SQL text changed
The new package file `internal/botstore/dialect_markers.go` adds two identity markers:
- **selectedSQL:** caller-selected text, or the SQLite half of an explicit branch.
- **pgOnlySQL:** reached only on a Postgres handle.

| file | sites | selectedSQL | pgOnlySQL | basis |
|---|---|---|---|---|
| botstore.go | 11 | 7 | 4 | wrappers (ExecContext/QueryContext/QueryRowContext/Exec/InsertID SQLite) carry caller text; per-dialect schema list; SQLite ALTER; PG CREATE SCHEMA, RETURNING id, migration markers |
| migrate.go | 14 | 10 | 4 | readRows/count/insertSQL/txQuerier are dialect-selected; sqlite_master/sqlite_sequence/rowid statements are the SQLite branch; pg_get_serial_sequence/sequence state/setval/meta upsert are the PG branch |
| mysql.go | 12 | 0 | 12 | MySQL→PG importer and rollback: target is always PG |

The botstore callers in api (context_sqlite, outbox, history, rag) were ported in the WhatsApp checkpoints. Here the store is the passthrough.

## Gates (unwaived)
- **Schemaless PG open:** legacy separate-DB mode uses plain pgx without the UTC session pin, so the default-session timezone gap remains. The unified bot_context schema path pins UTC (confirmed in pos1).
- **Full-text search:** SQLite FTS5 vs PG tsvector/FTSFold ranking mismatch.
- **Rebind:** not quote-aware (documented contract: no literal '?').
- **Proof cutover:** the r5 copy/import/rollback proof is a pre-cutover snapshot; drain/re-verify at cutover is still required.
- **Prior gates still open:**
  - 1117 import proof readyfalse; 4 NOT VALID FKs / 731 orphans.
  - IGNORE truncation, routing, gap locks, NUL/JSON, search, TIME, UUID, SEO, multikey, collation/numeric, fiscal hash, 136 onupdate.
  - pos13 domain-register cross-tenant takeover (unaccepted).
  - Guest-label/catalog-scope technical repairs pending dedicated QA.
  - PIN columns dump-only.

## Next
- internal/dbmigrate 18 + cmd/ops-check 15 = 33 (one checkpoint).
- Then the remaining 47 cmd/* and db tool sites.

