# Native PostgreSQL port: members_staff checkpoint 2 (members_staff syntax complete)

**Status:** this code compiles. No SQL was executed, and no reminders, notifications, email, WhatsApp or provider calls were run. No raw tokens or secrets were read. The app is **not** PG-ready: `ready:false`, MySQL stays the default and the server refuses to start on PG.

## Counts (canonical inventory r36, dbmigrate-r12 basis)

| Scope | Value |
|---|---|
| Call sites (repo) | 2472 |
| Native (repo) | 1219 (+40 since r35) |
| Dynamic (repo) | 192 |
| Portable-audit failures | 0 |
| Excluded | 8 helper-internal calls (was 5; +3 statements in the new SAVEPOINT helper), 3 prepared-statement executions |
| members_staff | 84/84, 7/7 files |
| Repo remaining | 1253 |

- Inventory: `/tmp/villacarmen-bot-20261111/sql-inventory-r36.json`
- Ledger: `docs/dbmigrate/native-ledger-members.json`
- Semantic notes: `/tmp/villacarmen-bot-20261111/members-semantics2.json`

## Build
`go build -p 2 ./...` exited 0 under the inherited cgroup (200% CPU, 6G memory), with pipefail. No tests, vet, fixtures or Playwright were run.

## Files completed
| File | Sites |
|---|---|
| backoffice_member_access_flows.go | 34 |
| pre_shift_reminders.go | 4 |
| schedule_notifications.go | 1 |
| backoffice_members_avatar.go | 1 |

## New foundation helpers (internal/sqldb)
- **`TxStatementScope`:** InnoDB rolls back only a failed statement and the transaction continues; PG aborts the whole transaction. The helper wraps one statement in a SAVEPOINT on PG; on MySQL it is a no-op. It is applied **only** where the source deliberately continues after an error:
  - The bo_users INSERT, where a duplicate falls through to the retry SELECT.
  - The two ignored username UPDATEs.
  - The two ignored token-invalidation UPDATEs, after the onboarding password set and after the reset confirm.
- **`NaiveUTCArg`:** for a naive timestamp column, the MySQL driver (`loc=UTC`) binds a `time.Time` as its UTC wall clock. pgx would discard the zone and store the caller's local wall clock, which I read in the pgx v5.11 source (`discardTimeZone`). On PG the helper binds `t.UTC()` instead.

## Token and access semantics (source preserved)
- **Expiry:** `expires_at = time.Now().Add(ttl)` is bound through `NaiveUTCArg`. Checks like `expires_at > NOW()`, `used_at = NOW()` and `invalidated_at = NOW()` use the UTC naive statement second.
- **Single use:** the UPDATE sets `used_at` from NULL, so it always changes the row and the `RowsAffected` gate is identical. A concurrent second confirm blocks, then re-checks against the committed row on both engines (an InnoDB locking read, or PG READ COMMITTED re-evaluation), so the loser gets 0 rows. This comes from code review only.
- **Token comparisons:** `token_sha256` and `onboarding_guid` equality runs under ci_ai. These are hex/uuid values, so case-insensitivity is harmless. Raw tokens are never read or logged.
- **Upserts:**
  - `member_contracts` uses `ON CONFLICT (restaurant_member_id)`.
  - `bo_user_restaurants` uses `ON CONFLICT (user_id, restaurant_id)`, the PRIMARY key.
- **Insert IDs:** member create and bo_users create use `sqldb.InsertID` inside the transaction.
- **Avatar upload:** re-saving the same `photo_url` returns 404 under MySQL's changed-rows count (a source quirk). PG preserves it with `photo_url COLLATE "C" IS DISTINCT FROM`.

## Worker paths (code only)
- **Pre-shift DueShifts: SOURCE QUIRK preserved, not fixed.** The query compares `TIMESTAMP(work_date, start_time)`, which is a **Madrid wall clock**, with `now`/`end` bound as the **UTC wall clock**. The reminder window is therefore shifted by the UTC offset (1h or 2h, depending on DST). PG reproduces it: `(work_date + start_time)` against `NaiveUTCArg` values.
- **Claim INSERT IGNORE:** IGNORE covers the `uniq_message_deliveries_delivery_key` duplicate plus truncation of `event` to 64 and `delivery_key` to 191. PG mirrors these with `LEFT` and `ON CONFLICT (delivery_key) DO NOTHING`. There is no FK. The payload is written with `CAST(? AS jsonb)`. With the unique key, exactly one concurrent claimer wins on both engines.
- **schedule_notifications:** `COALESCE(bool, 0)` is a type error on PG, so it becomes `CASE ... 1/0`.

## End-of-domain static review (all 7 members_staff files)
- A regex pass over every `nativeSQL` PG half found no MySQL-only function, no `?` inside single-quoted literals, and no raw unwrapped backtick SQL calls. The result was 0 findings.
- **Limit:** this is a lexical pass over backtick literals. It is not an AST or SQL-parser proof; lexical uncertainties remain fail-closed.
- **Time-parameter check:** every part A BETWEEN and `=` date parameter is a formatted string, not a `time.Time`, so the pgx zone-discard issue does not affect them.

## Open (none accepted)
1. **SYSTEMIC, new:** "error swallowed, then the same transaction continues" was not audited in the earlier ported domains (core, bookings, catalog, campaigns). On PG that pattern aborts the transaction. A repo-wide review is needed before cutover.
2. **SYSTEMIC, new:** `time.Time` arguments bound to naive columns in earlier domains need the same `NaiveUTCArg` review, because pgx discards the zone where the MySQL driver converts to UTC. Earlier sites that used `.UTC()` explicitly, such as `bo_sessions`, are fine; the rest were not audited.
3. **bo_users race retry:** PG READ COMMITTED can reuse a concurrently committed user; MySQL REPEATABLE READ would return the error. This is an isolation delta.
4. **Fractional-second ties:** both engines round into `timestamp(0)`, but an exact .5s tie (MySQL half-up vs PG rint) is unverified.
5. **Source quirks kept:** the pre-shift UTC-offset window and the DST wall-clock arithmetic in fichaje (members checkpoint 1).
6. **Negative labour-cost rounding:** pending, not accepted.
7. **Bounce diagnostics with invalid UTF-8:** this suppression gap remains **cutover-blocking**, and no sanitizing was implemented.

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

## Remaining domains (remaining-callers.json × r36)
| Domain | Files | Sites left | Dynamic |
|---|---|---|---|
| pos_billing | 35 | 564 | 53 |
| whatsapp_bot | 53 | 316 | 53 |
| site_analytics | 17 | 201 | 22 |
| tools_cmd | 12 | 57 | 9 |
| assistant | 8 | 55 | 22 |
| migration_tooling_native | 7 | 55 | 31 |
| db_runner | 2 | 5 | 2 |
| **Total** | | **1253** | |

Syntax-complete domains: core 406, bookings 161, catalog_stock 504, campaigns 64, members_staff 84.

## Recommended next: site_analytics (201 sites, 17 files), three bounded checkpoints
- **A (~68 sites): website builder.**
  - website_builder_handlers.go: 48 (4 dynamic)
  - website_builder_generator.go: 4
  - website_builder_public.go: 4
  - site_builder_registrar.go: 3
  - legal_pages.go: 3
  - seo_item_routes.go: 10 (4 dynamic)
  - Total: 72.
- **B (~74 sites): site builder and Instatic.**
  - site_builder.go: 47 (5 dynamic)
  - instatic_manager.go: 24
  - instatic_proxy.go: 3
  - Total: 74.
- **C (~55 sites): analytics, ads and AI usage.**
  - analytics/service.go: 27
  - analytics/breakdowns.go: 6
  - backoffice_ads.go: 9 (2 dynamic)
  - backoffice_ai_usage.go: 7 (4 dynamic)
  - ai_usage_logging.go: 2 (1 dynamic)
  - cafes_page_seo.go: 2 (2 dynamic)
  - public_ads.go: 1
  - public_ads_embed.go: 1
  - Total: 55.
- **Order:** I suggest running the two systemic audits (items 1 and 2 under Open) before or alongside A, because they may require fixes in already-ported domains.

## Not done
No commit, merge, deploy, DB writes, providers, email, WhatsApp or sends, and no `.env`, source or proof-data edits.

