# Native PostgreSQL port: campaigns checkpoint 1

**Status:** this code compiles. None of the SQL has been executed or verified at runtime, and no campaign or retry worker was started, so nothing was sent. The app is **not** PG-ready: `ready:false`, MySQL stays the default and the server refuses to start on PG.

## Counts (canonical inventory r33, dbmigrate-r12 basis)

| Scope | Value |
|---|---|
| Call sites (repo) | 2472 |
| Native (repo) | 1113 (+42 since r32) |
| Dynamic (repo) | 193 |
| Portable-audit failures | 0 |
| Excluded | 5 helper-internal calls, 3 prepared-statement executions |
| campaigns_messaging | 42/64 native, 22 remaining (0 dynamic), 2/7 files done |
| Repo remaining | 1359 |

- Inventory: `/tmp/villacarmen-bot-20261111/sql-inventory-r33.json`
- Ledger: `docs/dbmigrate/native-ledger-campaigns.json` (new; it holds the per-site `semantic_open` list)

## Build
`go build -p 2 ./...` exited 0 under the inherited cgroup (200% CPU, 6G memory), with pipefail. No tests, vet, fixtures or Playwright were run.

## Files completed

### backoffice_campaigns.go (29 sites, 5 dynamic)
- **Campaign columns:** `DATE_FORMAT` becomes `to_char`. `sent_at` is a naive UTC DATETIME; `created_at` and `updated_at` are rendered with `AT TIME ZONE 'UTC'`.
- **Stats:** `SUM(bool)` becomes `SUM(CASE WHEN ... THEN 1 ELSE 0 END)`.
- **Insert ID:** campaign create now uses `sqldb.InsertID` (`RETURNING id`).
- **Suppression list (safety-critical):** the PG version keeps the same seven-branch union. MySQL's `CONVERT/COLLATE` only reconciles the tables' mixed collations; PG pins `COLLATE "C"` per branch instead. No comparison runs on the result in SQL, because Go canonicalizes every row.
- **Audience window:** `CURDATE()` in the UTC session becomes `CAST(statement_timestamp() AT TIME ZONE 'UTC' AS date) - ?`.
- **INSERT IGNORE (recipient ledger):** with no FK and a generated id, IGNORE here covers two cases:
  1. A duplicate on `uq_campaign_target` `(campaign_id, channel, target)`.
  2. A `target` or `name` wider than `varchar(190)`. MySQL truncates these to 190 characters, with a warning, and still inserts the row. Booking emails and names are `varchar(255)`; manual targets are unbounded.
  
  The PG statement handles both explicitly: `LEFT(CAST(? AS text), 190)` on those two columns, and `ON CONFLICT` on that key only. `RowsAffected` stays 0 on a duplicate, so `queued_new` is preserved. Any other error now surfaces and is counted in `insert_errors`.
- **Unsubscribed list:** `DATE(timestamp)` becomes a UTC date. The nullable `created_at` is sorted `DESC NULLS LAST`, which is MySQL's order.
- **Recipient-list and resume queries:** the full composed strings are wrapped once.
- **Resume gates:** a paused row always changes when it is updated to skipped or pending, so the requeued and skipped counters are exact on both engines.
- **Sent timestamps:** `NOW()` on the naive `sent_at` becomes `date_trunc('second', statement_timestamp()) AT TIME ZONE 'UTC'`.

### campaign_email_retry.go (13 sites)
- **INSERT IGNORE:** becomes `ON CONFLICT (recipient_id) DO NOTHING`. A duplicate on that PRIMARY key is the only case IGNORE can hit here.
- **Backoff writes:** `DATE_ADD(NOW(), INTERVAL ? MINUTE)` becomes UTC-now `+ CAST(? AS integer) * INTERVAL '1 minute'`, and the 30-minute literal becomes `INTERVAL '30 minutes'`. The backoff steps (30m/2h/6h), the per-tick limits (20 read, 5 sent) and the 3-attempt cap are unchanged.
- **Due query:** `next_at <= NOW()` uses the same UTC naive clock, at second precision.

## Locking, claims and concurrency (explicit; not runtime-verified)
- **Retry claim:** `UPDATE campaign_email_retries SET done=2 WHERE recipient_id=? AND done=0` is gated on `n == 1`.
  - Going from 0 to 2 always changes the row, so MySQL's changed-row count and PG's matched-row count agree.
  - A second claimer blocks on the row lock and re-checks `done=0` against the committed row on both engines (an InnoDB locking read, and PG's READ COMMITTED re-check). The loser gets 0 rows.
  - This is reasoned from engine semantics only; concurrency QA is still pending.
- **Campaign send runner:** the source has no DB claim (no `FOR UPDATE` or `SKIP LOCKED`). Exclusivity is the in-process `campaignRunners` map only. This is preserved unchanged: on either engine, two processes could double-send. Nothing was added or fixed.
- **No transactions:** all statements here are autocommit, with no explicit transactions. Isolation-level defaults (MySQL REPEATABLE READ vs PG READ COMMITTED) only matter through the claim re-check described above.
- **Delete is not atomic:** deleting a campaign removes the campaign and then its recipients as two separate statements, as in the source, and leaves retry rows behind. The behaviour is identical on both engines.

## Open semantic gaps (new; not accepted choices)
- **Recipient INSERT IGNORE:** how truncation to 190 characters interacts with CI uniqueness, and other formerly ignorable errors now surfacing as `insert_errors`.
- **Suppression list:** the `TRIM(...) <> ''` and `source IN (...)` filters run under CI collations; the non-ASCII `LOWER` mapping may differ (Go re-lowercases afterwards). QA required.
- **Audience GROUP BY:** CI/PAD SPACE folding decides contact dedupe, and the representative spelling is arbitrary on both engines.
- **Time windows:** backoff and due windows depend on statement time in UTC; runtime QA is required.
- **Status equality:** `status`, `channel` and `source` comparisons are subject to the CI gap.

## Carried over unchanged (none waived)
- **App-level gaps:**
  - Reconciliation read locks.
  - The stock digest's arbitrary `any_value(warehouse_id)`.
  - The category and same-day dish upserts return id 0 on conflict (provisional, unproven).
  - The wine `float(10,2)` rounding/range fix is a proposal only.
  - Double rounding in averages.
  - Nested JSON containment.
  - JSON changed-row equality, text rendering and timestamp QA.
  - Dessert mirror insert-ID ordering.
  - Percentage and `adelanto_amount` float QA.
- **Import and schema gaps:**
  - 1117 pending items, `ready:false`. That number is the import-proof count, not a count of all semantic gaps.
  - 4 FKs left NOT VALID, covering 731 orphan rows.
  - CI, accent and PAD SPACE collation semantics.
  - FULLTEXT.
  - 190 range narrowings.
  - ON UPDATE on 136 tables and 353 schema defaults.
  - The broken `recent_conversations` view.
  - NUL adapter QA.
  - Source anomalies.

## Next checkpoint: the rest of campaigns_messaging (22 sites, 0 dynamic)
| File | Sites |
|---|---|
| campaign_auto_retake.go | 6 |
| campaign_bounce_sync.go | 5 |
| campaign_tools.go | 5 |
| campaign_email_guard.go | 4 |
| campaign_email_validation.go | 2 |

Bounce sync and auto-retake are worker paths. Their claim and idempotency semantics need the same explicit review, and no workers will be run.

## Not done
No commit, merge, deploy, DB writes, providers, email, WhatsApp or sends, and no `.env`, source or proof-data edits.

