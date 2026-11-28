# result-native-pos9 — POS billing small files (31 sites)

Status: code only. Build OK. Runtime-verified: 0. PG readyfalse; server refuses DB_DRIVER=postgres; MySQL default unchanged.

## Counts (canonical inventory r64, dbmigrate-r14, repo root)
- 2475 sites = native 2052 (incl 5 provisional) + blocked 3 + unwrapped 420; dynamic 72 (was 74); audit failures 0; helper-internal 9 / prepared 3 excluded.
- Delta vs r63: +31 native (2021→2052), unwrapped 451→420, 1 new provisional (stripe_connect.go:117).
- POS: 261/564 syntax-ported, 303 remaining (30 dynamic), 23/35 files done.

## Files done
| file | sites | notes |
|---|---|---|
| backoffice_pos_courses | 3 | status enum text; PAD SPACE NULLIF rtrim twin; course-list GROUP BY resolves to column l.course on MySQL, ORDER BY alias -> PG explicit GROUP BY l.course ORDER BY octet_length(label),label |
| backoffice_manual_billing | 4 | to_char/EXTRACT MONTH; bool->smallint cast; InsertID |
| backoffice_postres | 4 | quoted POSTRES/NUM/DESCRIPCION; dynamic PATCH changed-rows via pgChangedRowsPredicate; delete quoted (was not portable) |
| stripe_connect (SQL only) | 4 | upsert PROVISIONAL (PK restaurant_id + unique account_hash); checkout count timestamptz statement time |
| backoffice_invoices_billing | 5 | enums/currency char(3) read as text; line items portable inside tx (errors returned) |
| site_builder_billing | 5 | ON CONFLICT (domain) single arbiter; stripe_events jsonb + timestamptz; recurring amount numeric cast + padSpaceNullIfArg; CURDATE UTC |
| backoffice_invoice_comments | 6 | InsertID; update changed-rows ROW(content C, updated_at) IS DISTINCT FROM |

Build: `go build -p 2 ./...` exit 0 (inherited CPU200%/6G). Lexical check: one intentional ?-arity flag (invoice_comments:199 changed-rows). One self-caught bug fixed before the final build: the recurring_invoices PG twin briefly had 5 placeholders for 4 args.

## New gates/semantic notes (separate from 1117 import proof)
- stripe_connect multi-key upsert: if a Stripe account is reused across tenants, MySQL overwrites the other tenant's row, while PG returns 23505. Provisional; not equivalent.
- restaurant_domains ON CONFLICT(domain) keeps the source cross-tenant row update (tenant/routing gate). Domain uniqueness depends on ci_ai collation (collation gate).
- Course grouping: NULL/''/space course values can show up as duplicate '1' groups on both engines; collation affects course = ?.
- invoice date/timestamp-to-string RFC3339 JSON text: runtime QA pending.
- stripe_events payload jsonb normalisation; updated_at ON UPDATE (136-onupdate gate).

## Still open (unwaived)
- Import proof: 1117 pending, readyfalse; 4 NOT VALID FKs / 731 orphans.
- IGNORE truncation: suppressions, outbox, category, fiscal series.
- Gap locks and claim size; routing / UUID v1 / SEO / multi-key upserts.
- NUL/JSON, TIME, collation/PAD SPACE/accent, FTS search, numeric/float, fiscal hash chain QA.
- Repair batch for dedicated QA: guest-label NULLIF and catalog EXISTS scope.
- Dump-only (not confirmed live): PIN columns and the other dump-only source anomalies.

## Next (~30 sites)
stripe_connect_fees 6 + admin_postres 9 + invoice_numbering 10 = 25. Optionally add pos_reports 12 for 37. Invoice numbering needs care on counter locking and idempotency.

