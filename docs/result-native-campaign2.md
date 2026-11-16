# Native PostgreSQL port: campaigns checkpoint 2 (campaigns_messaging syntax complete)

**Status:** all campaigns_messaging SQL now has an explicit PostgreSQL variant. It compiles, but none of it has been executed. No workers, bounce polls, IMAP, DNS lookups, providers, email or WhatsApp were run. The app is **not** PG-ready: `ready:false`, MySQL stays the default and the server refuses to start on PG.

## Counts (canonical inventory r34, dbmigrate-r12 basis)

| Scope | Value |
|---|---|
| Call sites (repo) | 2472 |
| Native (repo) | 1135 (+22 since r33) |
| Dynamic (repo) | 193 |
| Portable-audit failures | 0 |
| Excluded | 5 helper-internal calls, 3 prepared-statement executions |
| campaigns_messaging | 64/64, 7/7 files |
| Repo remaining | 1337 |

- Inventory: `/tmp/villacarmen-bot-20261111/sql-inventory-r34.json`
- Ledger: `docs/dbmigrate/native-ledger-campaigns.json` (21 `semantic_open` entries)
- Semantic notes: `/tmp/villacarmen-bot-20261111/campaign-semantics2.json`

## Build
`go build -p 2 ./...` exited 0 under the inherited cgroup (200% CPU, 6G memory), with pipefail. No tests, vet, fixtures or Playwright were run.

## Key fact used: the imported r4 schema
In the imported r4 plan, every `_ci` text column is `COLLATE dbmigrate.ci_ai`: an ICU level-1 collation, nondeterministic, case- and accent-insensitive.
- **Equality and unique keys:** these fold case and accent on PG as they do in MySQL; `ON CONFLICT` on such a key folds the same way.
- **LIKE and regex:** PG17 **rejects** LIKE and regex on a nondeterministic collation. Every LIKE/REGEXP on these columns therefore runs on `LOWER(col) COLLATE "C"`.

## Sites ported (22)

### campaign_auto_retake.go (6)
- **Frozen-row clause:** gets a PG twin, `LOWER(error) COLLATE "C" LIKE 'pausa bounce%'`. The equality branch stays on the ci_ai column.
- **Quiescence window:** `created_at >= date_trunc('second', statement_timestamp()) - ? hours`; `created_at` is timestamptz.
- **Requeue/skip gates:** a paused row always changes when updated, so the exact counters hold.
- **Orphan-runner restart:** uses only the in-process `campaignRunners` map, as in the source. Two processes could each start a runner and double-send; this is unchanged.

### campaign_bounce_sync.go (5)
- **invalid_emails insert:** select-list parameters are typed. **Source quirk preserved:** an aggregate without GROUP BY always returns one row, so the `NOT EXISTS` in WHERE never stops the insert. Duplicates and `id_booking=0` happen on both engines.
- **Hard-bounce upsert:** `ON CONFLICT (restaurant_id, channel, target)` with CASE expressions on the old row. This equals MySQL's left-to-right assignment, where `reason` is assigned while `source` still holds its old value.
- **pending_verify INSERT IGNORE:** IGNORE here covers the `uq_suppression` duplicate plus truncation of `target` to 190 and `reason` to 255. PG mirrors exactly these with `LEFT` and `DO NOTHING`.
- **Bounce events INSERT IGNORE:** the `uq_bounce_event` duplicate (RowsAffected 0 means "known") plus truncation of every string column to its declared width, applied after `NULLIF` as MySQL does. See the first open risk below for the gap.
- **Provider list:** portable. The `smtp_host <> ''` comparison is subject to the PAD SPACE gap.

### campaign_email_guard.go (4)
- **Systemic breaker:** `LIKE` and `REGEXP` become `LOWER(col) COLLATE "C"` with `LIKE` and `~`. NULL three-valued logic is unchanged.
- **Optional-group rewrite:** `(is )?` is written `(is ){0,1}`, because `sqldb.Rebind` turns every `?`, even inside a literal, into a placeholder.
- **Other queries:** the `MAX(id)` baseline, the new-event count and the pause UPDATE are portable.

### campaign_email_validation.go (2)
- **GROUP BY LOWER(TRIM(email)):** inherits ci_ai, so case and accent variants fold together and the representative spelling is arbitrary.
- **INSERT IGNORE:** covers only the duplicate (Go guards `len <= 190`, reasons are constants, no FK). As in MySQL, a duplicate returns no error and is still counted as `stored`.

### campaign_tools.go (5)
- **Revision insert and deletes:** portable. The prune uses the derived-table `NOT IN` (MySQL's workaround for LIMIT in a subquery), which is also valid in PG.
- **Latest revision:** `DATE_FORMAT(created_at)` becomes `to_char(created_at AT TIME ZONE 'UTC', ...)`.

## Prior-checkpoint bug fixed
`backoffice_bookings.go:943`: the PG regex literal contained `?`, which Rebind would have turned into `$n` placeholders. That is a latent runtime failure from bookings checkpoint 2. It now uses `{0,1}`. A line-based scan of all PG literals found no other `?` inside a literal. That scan is not a proof.

## Recipient INSERT IGNORE: classification of newly surfaced PG errors (from checkpoint 1)
Checked against the actual imported r4 schema: no FK, and no CHECK on the inserted columns. `channel` is a constant; `target` and `name` are cut to 190 characters with `LEFT`, matching MySQL's character truncation. Identity sequences were reset and verified in r4.
- **Impossible:** NOT NULL, range, FK and over-width errors.
- **Implausible:**
  - Invalid UTF-8: the values come from `json.Unmarshal` or the database.
  - NUL bytes: emails must match `^[a-z0-9][a-z0-9._%+-]...`, WhatsApp numbers are digits-only, and booking names in PG are already NUL-free.
- **Residual (OPEN):** any other PG failure is now surfaced as `insert_errors` rather than hidden. The interaction between truncation and CI uniqueness has not been QA'd.

Nothing is silently swallowed in the PG branch.

## Open risks (none accepted, all need decision or QA)
1. **Bounce events with invalid UTF-8 (safety-relevant):** `Diag` is cut at 300 bytes, so a multibyte character can be split, and IMAP text may not be UTF-8. MySQL IGNORE turns this into a warning; PG raises 22021, the notice is skipped and **no suppression is applied**. The value MySQL actually stores is unproven, so I did not sanitize it. This needs a source-grounded decision.
2. **Case-insensitive matching:** LOWER versus ICU simple case folding in the breaker regex and LIKE. A few non-ASCII foldings (for example U+017F) can differ.
3. **Frozen-row LIKE:** loses MySQL's accent-insensitivity.
4. **Do-not-contact union and audience:** the case, accent, PAD SPACE and source filters remain under the general collation gap (see campaigns checkpoint 1).
5. **Claim concurrency:** the retry claim result, and the absence of any DB claim for campaign and orphan runners, come from code review only.
6. **Source quirk:** the duplicate inserts into invalid_emails described above.
7. **Time windows:** backoff, quiescence and due windows depend on statement time in UTC; runtime QA is required.

## Carried over unchanged (none waived)
- **App-level gaps:**
  - Reconciliation read locks.
  - The stock digest's arbitrary warehouse pick.
  - The category and same-day dish upserts return id 0 on conflict (provisional, unproven).
  - The wine `float(10,2)` rounding/range fix is a proposal only.
  - Double rounding in averages.
  - Nested JSON containment.
  - JSON changed-row equality, text rendering and timestamp QA.
  - Dessert mirror insert-ID ordering.
  - Percentage and `adelanto_amount` float QA.
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

## Remaining domains (remaining-callers.json × r34)
| Domain | Files left | Sites left | Dynamic |
|---|---|---|---|
| pos_billing | 35 | 564 | 53 |
| whatsapp_bot | 53 | 316 | 53 |
| site_analytics | 17 | 201 | 22 |
| members_staff | 7 | 84 | 1 |
| tools_cmd | 12 | 57 | 9 |
| assistant | 8 | 55 | 22 |
| migration_tooling_native | 6 | 55 | 31 |
| db_runner | 2 | 5 | 2 |
| **Total** | | **1337** | |

Syntax-complete domains: core 406, bookings 161, catalog_stock 504, campaigns_messaging 64.

## Recommended next: members_staff (84 sites), split into two checkpoints
- **Part A (44 sites):**
  - backoffice_fichaje.go: 27 (1 dynamic)
  - backoffice_fichaje_realtime.go: 4
  - backoffice_members.go: 13
- **Part B (40 sites):**
  - backoffice_member_access_flows.go: 34
  - pre_shift_reminders.go: 4
  - schedule_notifications.go: 1
  - backoffice_members_avatar.go: 1

Time-clock (fichaje) work is heavy on datetime/UTC and interval arithmetic. The reminder and notification paths are workers: SQL only, never run.

No new domain was started.

## Not done
No commit, merge, deploy, DB writes, providers, email, WhatsApp or sends, and no `.env`, source or proof-data edits.

