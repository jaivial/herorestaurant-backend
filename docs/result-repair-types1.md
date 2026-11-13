# result-repair-types1: TIME and NUL type adapters, plus the first manual read-only QA plan

All changes are code only. `go build -p 2 ./...` exits 0. No tests, no SQL, no DB access, and no edits to the proof (r5 untouched). Runtime-verified count is 0; PG is still ready:false. Schema proposals: applied false.

## Inventory r91

`/tmp/villacarmen-bot-20261111/sql-inventory-r91.json` has 2498 sites in 284 files, all classified. Counts are unchanged from r90, because this batch made no SQL changes.

## TIME: exact column classification

Evidence comes from the full-export `schema.json` and an ndjson value scan of the existing artifacts. No DB was read.

| MySQL column | PG plan | Source rows | Values | Semantics (from code) |
|---|---|---|---|---|
| bookings.reservation_time | time(0) | 2355 | all clock 00–23h, no negatives, none >=24h | clock time of day |
| member_time_entries.start_time / end_time | time(0) NULL | 0 | – | clock (fichaje `parseStrictHHMM`) |
| member_work_schedules.start_time / end_time | time(0) | 0 | – | clock (`parseStrictHHMM`) |
| pos_service_periods.start_time / end_time | time(0) | 0 | – | clock (`posClockMinutes`) |
| pos_settings.business_day_cutoff | time(0), default 05:00:00 | 0 | – | clock (`time.Parse("15:04")`) |
| cancelled_bookings.reservation_time | varchar(10) | 228 | all clock | text copy |

- There are no duration columns. The only TIME arithmetic in code is `SEC_TO_TIME(FLOOR(TIME_TO_SEC(TIME(opened_at))/300)*300)` in `fichaje_realtime`, which stays bounded within 0..86399 seconds (clock).
- **No plan revision is needed.** PG time cannot hold ±838h, but no stored value needs it.
- **The real risk was write-side.** `ensureHHMMSS`, which feeds 10 booking callers, accepted any 8-character string. MySQL TIME would store "25:00:00" or "-1:00:00" as durations; PG rejects them with 22008.
  - **Fixed on both dialects:** it now requires strict `HH:MM:SS` with 00-23 / 00-59 / 00-59 and otherwise returns the existing "invalid time" 400. The existing 5-character and >8-character truncation still applies first.
  - This is an explicit behaviour change: invalid times were stored before.
- The fichaje, POS service-period and POS settings writers already validate strictly, so they are unchanged.
- **Open gate:** booking writers that do not go through `ensureHHMMSS` have not been individually audited. These are the BO normaliser, the bot `resTime`, and self-service.
- **Scan:** pgx returns time as "HH:MM:SS" text, the same as MySQL; this was covered in earlier phases.

## NUL

- **Source evidence:** `full-source-nul-inventory.json` lists exactly 1 NUL, in assistant_messages.content (1 row). The existing bytea adapter preserves it and is unchanged.
- **jsonb:** the plan has about 75 jsonb columns, listed in `proposed-types1.md`. The source data has no NUL in any of them. New writes could add one, and PG would reject it with 22P05.
- **Code change (bounded):**
  - `jsonHasNULEscape` (`json_nul.go`) gives exact detection. `encoding/json` always emits the `\u0000` escape, and the check respects backslash parity.
  - It is used in `enqueueWhatsAppDelivery`: on PG a payload containing NUL now returns an explicit error, which callers log or propagate. It is never stripped or sanitised.
- A lossless wrapper needs every reader to round-trip it, and readers use jsonb operators in SQL, so I did not build one. The other jsonb writers stay a gate, with options (b)/(c) in the proposal.

## Collation (next, separate)

No change in this batch. There will be no claim that Go Unicode case folding equals MySQL `utf8mb4_unicode_ci` or PG ICU ci_ai. That needs a later exact per-column plan.

## First manual read-only QA plan (for later; nothing runs now)

**Preconditions.**
- The parent or user confirms, in the existing local migration scope, the local MySQL source and the local PG copy, and their read-only credentials.
- Run each query by hand as a read-only user (`SET SESSION TRANSACTION READ ONLY` / `default_transaction_read_only=on`).
- No writes, no activation, no app start.
- Never print secrets; take DSNs from the operator's environment.

**PG copy (psql, read-only):**
1. `SELECT value::text FROM dbmigrate.marker WHERE key='import_verified';` and confirm ok, ready_for_cutover, and pending=1117 versus the r5 report.
2. `SELECT conname FROM pg_constraint WHERE NOT convalidated;` should return 4 rows, matching r5.
3. `SELECT count(*) FROM bookings;` should be 2355; `SELECT count(*) FROM cancelled_bookings;` should be 228. Compare against `full-mysql-counts.json` per table.
4. TIME: `SELECT min(reservation_time), max(reservation_time) FROM bookings;`
5. NUL: `SELECT id FROM assistant_messages WHERE position('\x00'::bytea in convert_to(content,'UTF8'))>0;` uses the adapter column per the store design. Expect exactly 1 row, matching the inventory.
6. Enum spot check: `SELECT stripe_payment_status, count(*) FROM restaurant_domains GROUP BY 1;`
7. Collation: `SELECT collname, colliculocale FROM pg_collation WHERE collname='ci_ai';`
8. Timestamps: compare `SELECT created_at FROM bookings ORDER BY id LIMIT 5;` (timestamptz shown in UTC) with the MySQL source rows.
9. Fiscal hash chain (read-only): `SELECT id, series_id, previous_hash, content_hash FROM pos_fiscal_documents ORDER BY series_id, series_number LIMIT 20;` then recompute offline against the source. No document is ever issued.

**MySQL source (mysql client, read-only):**
1. `SELECT @@sql_mode, @@time_zone, @@collation_server;` compared against `villacarmen-full-mysql-source-settings-20261111.json`.
2. The same counts, and `SELECT MIN(reservation_time), MAX(reservation_time) FROM bookings;`
3. `SELECT COUNT(*) FROM assistant_messages WHERE LOCATE(CHAR(0), content) > 0;` should return 1.

## Gates (not waived)

- Booking time writers outside `ensureHHMMSS` (audit pending).
- jsonb NUL on every other writer.
- Collation.
- Notifications are at-most-once.
- Capacity: code review only.
- The 10 guarded IGNORE sites.
- 1117 import-proof entries (ready:false); 4 NOT VALID FKs with 731 orphans.
- General gates.

## Files

- `internal/api/reservations_availability.go`
- `internal/api/whatsapp_outbox.go`
- `internal/api/json_nul.go` (new)
- `docs/dbmigrate/proposed-types1.md` (proposal)

Backup: `/tmp/ty1.bak.tar`.

