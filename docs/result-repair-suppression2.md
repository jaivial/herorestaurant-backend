# result-repair-suppression2: closing the do-not-contact (suppression) safety gates

Everything here is a code change only. `go build -p 2 ./...` exits 0. I ran no tests, fixtures or harness, executed no SQL, read nothing from a DB, and sent nothing. Runtime-verified is still 0, PG is still ready:false, and the server still refuses PG. The r5 proof is untouched.

## Inventory r83

`/tmp/villacarmen-bot-20261111/sql-inventory-r83.json` covers 2486 sites in 281 files. All sites are classified:

| Category | Sites |
|---|---|
| native | 846 |
| portable | 1472 |
| selected | 82 |
| pgOnly | 42 |
| mysqlOnly | 44 |

Blocked 0, provisional 0, 13 helper sites excluded. The +1 site is the new collation lookup (nativeSQL, dynamic, rebound once).

## Coverage of every send path (actual code read)

All campaign/marketing sends gate through `campaignSuppressionFilter.blocked()`. The filter is now loaded through `loadCampaignSuppressionFilterFor`, which runs the exact Go map lookup plus the DB collation lookup.

| Path | Probes | Lookup-error behaviour |
|---|---|---|
| `campaignAudience` (manual + bookings) | all candidates, batched in chunks of 200 | returns the error, no audience |
| Delivery loop (`backoffice_campaigns` before each send) | that recipient | existing supErr path: row left pending, back off, abort after max failures; **no send** |
| Resume of paused recipients | all paused rows (load moved after the read) | 500 response, nothing requeued |
| `campaign_auto_retake` requeue | all frozen rows | warn + return, nothing requeued |
| `campaign_email_retry` | that target | rescheduled +30 min, no send |
| WhatsApp outbox drain, event='campaign' (**NEW gate**; queued campaign retries were sent without re-check) | the recipient | rescheduled, no send. If suppressed: settled as failed ("baja") without sending |
| `campaign_email_validation` (report + suppression inserts, no send) | all items | error ignored as in the source; the filter stays partial (documented) |

Transactional outbox events such as booking_confirmation were never gated in the source. They remain ungated; I did not add over-blocking.

Unstorable targets fail closed: `blocked()` returns true when `campaignSuppressionKey` rejects a target (invalid UTF-8, NUL, more than 190 characters, or empty). Every writer skips such a target and never truncates it.

## Accent and case collision (exact DB collation, no Go approximation)

The source column is `utf8mb4_unicode_ci` (schema.json, table and column). The PG plan uses `dbmigrate.ci_ai` (ICU und-u-ks-level1, nondeterministic). `uq_suppression` uses that collation on each engine.

For each probe, `campaignSuppressionCollationHits` runs:

```
SELECT p.i FROM (SELECT 0 AS i, ? AS probe UNION ALL ...) p
WHERE EXISTS (SELECT 1 FROM campaign_suppressions s
              WHERE s.restaurant_id = ? AND s.channel = ? AND s.target = <probe in column collation>)
```

- MySQL uses `CONVERT(p.probe USING utf8mb4) COLLATE utf8mb4_unicode_ci`.
- PG uses `p.probe COLLATE "dbmigrate"."ci_ai"`.

This blocks exactly the targets that the engine's own unique key already merges with a stored row. It does not widen matching beyond that, and it applies only to `campaign_suppressions`. The legacy tables have no such unique key and stay exact. The index literals are generated integers.

**Gate retained:** ICU level1 is not the same as MySQL UCA 4.0.0 unicode_ci. PAD SPACE and some expansions and ligatures differ. Rows distinct under MySQL can therefore collide or differ on PG. This belongs to the existing CI/AI collation gate (import/QA), so PG stays disabled.

## Bounce diagnostic decoupled from suppression

- A failed event insert no longer skips the suppression. It counts `event_store_failed` (a new additive JSON field) and the suppression is still applied. Only a proven duplicate (RowsAffected 0) skips.
- Both statements are autocommit on `s.db`. There is no shared tx, so no aborted-transaction or savepoint issue.
- `campaignSuppressBounce` now returns its error. Previously both writes were `_, _ =` and errors were silent. A failure counts `suppress_failed` and is logged without the address.
- An invalid-UTF-8 or NUL diagnostic is stored as `[raw-latin1]` + the Latin-1 reading of its bytes, which is a bijective, lossless, explicitly flagged encoding. It is cut to 288 bytes so it fits varchar(300). A NUL byte becomes U+FFFD, and that case is flagged lossy.
- Classification uses the original text. This is **not MySQL-equivalent**: what MySQL IGNORE stored there is unproven.

## Error semantics confirmed

- **Public unsubscribe:** the response is always `success:true` (no enumeration), as in the source. Invalid, NUL or overlong targets are skipped, and the filter blocks them anyway.
- **Bot unsubscribe:** `stored==0` still returns the existing error to the model.
- **`campaignUnsubscribeAlready` with NUL on PG:** the query errors and the function returns false (not suppressed). That only affects the message shown.
- **Outbox width reject:** `enqueueWhatsAppDelivery` returns the error. The booking caller already logs it and returns the cause. The campaign caller previously used `_ =` and now logs the enqueue failure (no recipient) while still returning the send error, so the recipient is marked failed and nothing is dropped silently.
- **Email validation:** the rune-based length check (counted as already suppressed) is deliberate and recorded.
- **`truncate()`:** now cuts on a character boundary within the byte budget. It is shared by outbox errors, retry errors and reasons, all of which wrote split runes that PG rejects with 22021.

## Gates (not waived)

- Collation semantic difference (MySQL unicode_ci vs PG ci_ai), as above.
- What MySQL stored for invalid diagnostics is unproven.
- NUL inside a JSON outbox payload is rejected by PG jsonb.
- The validation filter is partial on a read error, as in the source.
- 1117 import-proof ready:false, 4 NOT VALID FKs with 731 orphans, and the remaining general gates (IGNORE categories 140→120 and fiscal series, gap locks, NUL/JSON, search, TIME, UUID, multikey, collation/numeric, fiscal hash, 136 on-update, bare jsonb `?`, Stripe retention/processed, domain DNS, MySQL-only utilities).

## QA plan (later, local PG only)

1. Store `josé@x` and probe `jose@x` on both engines; it must be blocked.
2. Probe with 191 characters, a multibyte target and a NUL; each must be blocked.
3. Force a lookup error at each path in the table and confirm no send.
4. Queue a campaign outbox row, add a suppression, drain, and confirm it is not sent.
5. Run a bounce with an invalid diagnostic: the suppression must exist and the diagnostic must be flagged.
6. Run an over-wide outbox key and confirm the error is logged.

## Next

1. IGNORE categories (140→120) and fiscal series.
2. Stripe processed flag and reconciliation.
3. Gap-lock advisory locks.
4. TIME / NUL / collation adapters.
5. The read-only local PG QA plan.

## Files changed

All under `internal/api`: `campaign_suppression_target.go`, `backoffice_campaigns.go`, `campaign_auto_retake.go`, `campaign_email_retry.go`, `campaign_email_validation.go`, `campaign_bounce_sync.go`, `whatsapp_outbox.go`, `booking_whatsapp.go`.

Backup: `/tmp/ign2.bak.tar`.

