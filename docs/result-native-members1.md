# Native PostgreSQL port: members_staff checkpoint 1 (part A)

**Status:** this code compiles. No SQL was executed, and no realtime events, notifications, workers or providers were run. The app is **not** PG-ready: `ready:false`, MySQL stays the default and the server refuses to start on PG.

## Counts (canonical inventory r35, dbmigrate-r12 basis)

| Scope | Value |
|---|---|
| Call sites (repo) | 2472 |
| Native (repo) | 1179 (+44 since r34) |
| Dynamic (repo) | 192 |
| Portable-audit failures | 0 |
| Excluded | 5 helper-internal calls, 3 prepared-statement executions |
| members_staff | 44/84 native, 40 remaining (0 dynamic), 3/7 files done |
| Repo remaining | 1293 |

- Inventory: `/tmp/villacarmen-bot-20261111/sql-inventory-r35.json`
- Ledger: `docs/dbmigrate/native-ledger-members.json` (new)
- Semantic notes: `/tmp/villacarmen-bot-20261111/members-semantics1.json`

## Build
`go build -p 2 ./...` exited 0 under the inherited cgroup (200% CPU, 6G memory), with pipefail. No tests, vet, fixtures or Playwright were run.

## Files completed
- **backoffice_fichaje.go:** 27 sites, including the dynamic clock-member lookup.
- **backoffice_fichaje_realtime.go:** 4 sites.
- **backoffice_members.go:** 13 sites.

## Time and date semantics (grounded in the source code)
- **Fichaje timezone model:** `work_date`, `start_time`, `end_time` and the datetime parameters are naive **Europe/Madrid wall-clock** values, built in Go with `time.Now().In(boMadridTZ)`. The PG schema stores them naive too (`date` and `time(0)`). I did not convert them to UTC or timestamptz.
- **DST:** MySQL `TIMESTAMPDIFF(MINUTE, CONCAT(date,' ',time), ?)` and PG `trunc(EXTRACT(EPOCH FROM (CAST(? AS timestamp) - (work_date + start_time)))/60)` both compute wall-clock differences. A shift that spans a DST change is therefore off by ±60 real minutes, identically on both engines.
- **Truncation:** `trunc` reproduces TIMESTAMPDIFF's toward-zero truncation.
- **GREATEST with NULL:** MySQL `GREATEST(0, NULL)` is NULL, which violates the NOT NULL column (a strict-mode error). PG `GREATEST` ignores NULLs and would return 0. I preserved MySQL's behaviour with a CASE inside a correlated subselect, so the `?` arity is unchanged.
- **Entry patch:** the untyped `? IS NULL` becomes `CAST(? AS text) IS NULL`. The minutes formula is end minus start, with the parameter order kept.
- **Timestamps:**
  - `updated_at` (timestamptz) is rendered as `to_char(... AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')`.
  - The schedule-update `CURRENT_TIMESTAMP` becomes `date_trunc('second', statement_timestamp())`.
  - Token `invalidated_at = NOW()` (naive timestamp) becomes the UTC naive statement second.
- **Realtime:** `HOUR(pos_tickets.opened_at)` uses the naive **UTC** value, as in the source; it is not converted to Madrid time. The 5-minute bucket is hour + floor(minute/5)·5, which equals `FLOOR(TIME_TO_SEC/300)*300`.
- **Other date handling:** `TIME_FORMAT` and `DATE_FORMAT` become `to_char`. Compensation validity uses `CAST(? AS date) BETWEEN ... COALESCE(effective_to, DATE '9999-12-31')`.

## Other semantics
- **Member PATCH:** `RowsAffected==0` returns 404. With MySQL's CHANGED-rows count, re-saving an unchanged member returns 404; this is a source quirk. PG keeps it with `ROW(8 cols COLLATE "C") IS DISTINCT FROM ROW(params)`.
- **Contract upsert:** `ON CONFLICT (restaurant_member_id)` (`uniq_member_contracts_member`), the only key that can collide. It is not tenant-scoped, as in the source.
- **Insert IDs:** clock start (×2) and schedule assign use `sqldb.InsertID`.
- **Labour-cost money:** MySQL's per-division scale (`div_precision_increment` 4) is reproduced:
  - `minutes/60` is rounded to 4 decimals.
  - `gross/monthly_hours` is rounded to 6.
  - `pct/100` is rounded to 6.
- **Nullable sort:** `start_time` is sorted `ASC NULLS FIRST`, which is MySQL's order.
- **Hints:** `FORCE INDEX` is dropped on PG; it is an optimizer hint only.
- **CONCAT:** PG treats NULL as '' where MySQL returns NULL. Every CONCAT here is over NOT NULL or COALESCE'd values, so the difference is unreachable.
- **Gates that match on both engines:** the transitions `end_time NULL→value` (auto-cut) and `is_active 1→0` (delete) always change the row, so matched and changed counts are equal.

## Open (not accepted)
- **DST arithmetic:** the wall-clock behaviour is identical but not real elapsed time. That is the source's behaviour, documented rather than fixed.
- **Labour-cost rounding:** half-up and half-away rounding agree for non-negative values. Negative minutes are not constrained by the schema, so that path is unverified.
- **Float to decimal:** `weekly_hours` is written from a float64 into `numeric(6,2)`; rounding QA is pending.
- **Concurrency:** clock start reads the active entry and then inserts, with no lock or unique key on either engine, so a double-start race is possible. Stop has no `end_time IS NULL` guard (as in the source). Code review only.
- **Order and collation:**
  - The hour-bucket queries have no ORDER BY on either engine.
  - Name ordering and equality are subject to the CI/accent gap.
  - `LOWER(TRIM(email))` matching in the clock-member lookup runs under ci_ai.
- **Blocker from campaigns checkpoint 2 (unchanged, still cutover-blocking):** bounce diagnostics containing invalid UTF-8. No sanitizing was implemented. Nothing in this repo documents what MySQL stores for invalid utf8mb4 under `INSERT IGNORE`; it needs read-only source evidence or a coordinator decision.
- **Lexical check:** the extraction check of the PG halves (no MySQL functions, `?` arity) used a regex over backtick literals. It is not a proof, and the lexical edge cases remain fail-closed.

## Carried over (none waived)
- **Import and schema gaps:**
  - 1117 pending items, `ready:false` (separate from the semantic-gap count).
  - 4 FKs left NOT VALID, covering 731 orphan rows.
  - CI, accent and PAD SPACE collation semantics.
  - FULLTEXT.
  - 190 range narrowings.
  - ON UPDATE on 136 tables and 353 schema defaults.
  - The broken `recent_conversations` view.
  - NUL adapter QA.
  - Source anomalies.
- **Earlier app-level gaps:** catalog and campaign numeric, JSON, insert-ID and locking gaps.

## Next: members_staff part B (40 sites, 0 dynamic)
| File | Sites |
|---|---|
| backoffice_member_access_flows.go | 34 |
| pre_shift_reminders.go | 4 |
| schedule_notifications.go | 1 |
| backoffice_members_avatar.go | 1 |

Access flows cover invitation and reset tokens, expiry times and claims. The reminders are worker paths: SQL only, never run.

## Not done
No commit, merge, deploy, DB writes, providers, email, WhatsApp or sends, and no `.env`, source or proof-data edits.

