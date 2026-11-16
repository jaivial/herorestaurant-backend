# result-native-pos10 — POS small files (37 sites)

Code only. Build OK. Runtime-verified 0. PG readyfalse, server refuses DB_DRIVER=postgres, MySQL default unchanged.

## Counts (inventory r65, repo root)
- 2475 sites: native 2089 (5 provisional, none new), blocked 3, unwrapped 383, dynamic 72, audit failures 0.
- Delta vs r64: +37 native.
- POS: 298/564 ported, 266 left (30 dynamic), 27/35 files done.

## Files
| file | sites | key points |
|---|---|---|
| stripe_connect_fees | 6 | single-PK ON CONFLICT upserts (setting_key, restaurant_id); created_at to_char UTC |
| admin_postres | 9 | quoted POSTRES/NUM/DESCRIPCION; changed-rows twins for update/toggle; LIKE lower-C; explicit-NUM insert advances PG identity via setval in same statement |
| invoice_numbering | 10 | ON CONFLICT (restaurant_id,year), GREATEST on existing row; EXTRACT YEAR; PAD SPACE <>'' twin; FOR UPDATE kept |
| backoffice_pos_reports | 12 | enums as text; UNION of two enum types as text; replay: savepoints for skipped/ignored errors, INSERT IGNORE -> ON CONFLICT snapshot key DO NOTHING + InsertSelectID, resolved_at timestamptz statement time |

Build: go build -p 2 ./... exit 0. Lexical check: 2 intentional changed-rows ?-arity flags (admin_postres 249, 343).

## New gates (separate from 1117 import proof, unwaived)
- **POSTRES explicit NUM vs identity:** setval is non-transactional. If the sequence has never been called and the max rows were deleted, PG can end up behind MySQL's counter. The per-restaurant MAX+1 on a global PK can collide across tenants (source bug, same on both engines).
- **Invoice numbering first use:** under MySQL RR a FOR UPDATE on a missing row takes a gap lock; PG takes none. With two concurrent first uses, PG can let both run the backfill, and the second overwrites the numbers the first assigned. Gap-lock gate; not equivalent. invoice_number has no unique index on either engine.
- **Replay snapshot:** INSERT IGNORE also downgraded range and FK errors; on PG these raise (IGNORE gate).
- **Report ordering and collation:** ORDER BY on enums assumes PG enum labels follow the source declaration order (needs QA). COUNT(DISTINCT provider_reference) depends on ci_ai collation. The card refund sum fans out across payment rows (source behaviour kept).
- updated_at onupd triggers (136 gate) on platform_settings, invoice settings and fee overrides.

## Still open
- 1117 import proof readyfalse; 4 NOT VALID FKs / 731 orphans.
- IGNORE truncation, routing, gap locks, UUID, SEO, multi-key upserts (incl. stripe_connect provisional), domain cross-tenant upsert.
- NUL/JSON, search, TIME, collation/PAD SPACE, numeric, fiscal hash chain.
- Guest-label/catalog-scope repairs waiting for dedicated QA; PIN columns are dump-only.

## Next: 8 large files, 266 sites
- pos_admin 13 + pos_stock_realtime 25 = 38 (next checkpoint).
- Then invoices 21 + pos_packs 23.
- Then premium 39 (16 dynamic), pos 44, checkout 48, rail 53, each as its own checkpoint or split.

