# result-repair-stripe-audit1: concurrency and trust audit of stripe1, with fixes

All of this is code-only. `go build -p 2 ./...` exits 0. I ran no tests and no SQL, wrote nothing to any DB, and called no Stripe or Cloudflare endpoint. Runtime verification is 0. PG is still ready:false and the server still refuses PG. R5 and plan r4 are untouched. Schema applied: **false**.

## Inventory r86

`/tmp/villacarmen-bot-20261111/sql-inventory-r86.json`: 2491 sites in 282 files, all classified. Blocked 0, provisional 0.

| Kind | Sites |
|---|---|
| native | 846 |
| portable | 1477 |
| selected | 82 |
| pgOnly | 42 |
| mysqlOnly | 44 |

The total is 2 fewer than r85 because the lease UPDATE and the separate mark helper were removed.

## Findings in stripe1, and fixes

### 1. A stale lease could complete work, and `created_at` was repurposed

**Problem.** The 10-minute `created_at` lease had no fencing. After a takeover, the old worker could still mark the event processed or repeat effects. Re-stamping `created_at` also changed the paid_at value shown in the payments list (a regression).

**Fix (v2).**
- Each event takes a **per-event session advisory lock on one pinned connection**. A new helper, `sqldb.TryNamedLockConn`, provides it. The existing `TryNamedLock` wraps it and reuses the ErrBadConn discard-on-unconfirmed-unlock cleanup.
  - MySQL uses `GET_LOCK` with a lock name `se:` + 40 hex characters of sha256, which stays within the 64-character limit.
  - PG uses `pg_try_advisory_lock`.
- With the lock held, on that same connection:
  - The event is inserted with `processed_at` NULL. A duplicate is a no-op that raises no error. The statement is autocommit, so no PG aborted-transaction or savepoint handling is needed.
  - `processed_at` is then read. If it is already set, the request returns a 200 duplicate.
- **Fencing.** The domain-effects transaction and the processed mark run **on the pinned connection**, and the mark must change exactly one row. If the session is lost, the lock is lost and those statements fail, so a stale holder cannot commit effects or mark the event.
- The lock is a session lock, not a long transaction. No transaction is open during the Stripe re-read.
- If another holder has the lock, the request returns 503 and Stripe retries.
- `created_at` is never written again.
- **Residual risk:** external Cloudflare calls cannot be fenced. They are `Ensure*` calls and the mapping is an upsert, so repeating them is idempotent.

### 2. Effect idempotency, proven from the code

| Effect | Why repeating it is safe |
|---|---|
| Domain paid | conditional on the row lock; a second run sees paid with the same session and skips |
| Invoice | `NOT EXISTS` under the row lock |
| CF zone/DNS | `Ensure*` |
| Mapping | upsert scoped to the same site |
| Expired/failed | conditional on the session or attempt marker |
| Connect `completeCheckout` | conditional pending→paid→completed; only the winner of paid→completed inserts the booking and sends notifications, so there is no double send |

**Open source gap:** if the process crashes after the completed claim but before the booking insert, the booking is lost (at-most-once). Retries see "completed" and do nothing. Fixing this needs a durable booking outbox step. Gate.

### 3. Trust and price authority

- **Livemode.** Both webhooks now refuse an event whose `livemode` differs from the mode of the configured key.
- **Provider re-read.** The domain session is now **re-read from Stripe by id** (`RetrieveCheckoutSession`). Every check uses the provider object, not the event payload.
- **Expected amount on the server.** It is now persisted at reservation time: `registration_cost` holds this attempt's expected amount in euros (numeric(10,2)).
  - The source stored the cents figure in that euros column. This is a fixed bug, and it also changes the platform list display.
  - Reusing an uncertain or creating key with a different amount is refused (409), because the same key must mean the same Stripe request.
- **Webhook acceptance.** The webhook proceeds only when all of these hold:
  - the session is paid and the currency is eur;
  - the metadata owner matches the row owner;
  - the session is the row's recorded attempt or its stored session id;
  - `amount_total` equals the server-recorded expected amount, and also the metadata `amount_cents` when present.
- **Previously** a paid session that was not the recorded attempt was accepted. **Now** it goes to REVIEW. The same applies to:
  - pre-v2 metadata;
  - a missing row or foreign owner;
  - a domain already paid by another session;
  - ownership or site conflicts.
- **CRITICAL OPEN GATE: price authority.** `handleBillingCheckout` still takes `amount_eur` from the authenticated backoffice client. No server-side EUR price exists for this flow. The premium flow's `premiumDomainBasePrice×1.5` and Cloudflare registrar USD `pricing` are different sources, and I picked neither, because choosing one would invent a price. Until a price source is decided, the stored amount only proves consistency, not correctness. Activation is blocked.

### 4. Durable states (no silent terminal drop)

| State | Meaning |
|---|---|
| `processed_at` set | done |
| `processed_at` NULL + 503 | retryable; Stripe retries for about 3 days |
| `processed_at` NULL + 200 `review` | durable manual-review state; listed by `processed_at IS NULL` |

The optional outcome/attempts/last_error columns are proposed in `docs/dbmigrate/proposed-stripe-events-claim.sql` (applied: false).

### 5. Cloudflare failure

- `provisionDomainSite` results `skipped_dns` and `skipped_error` are now **retryable errors**. The domain stays `paid`, not active, which is a durable pending state, and Stripe retries.
- Only `mapped` or `skipped_no_site` (the source behaviour) complete the event.
- `site_id_required`, `skipped_mapped_to_other_site` and `skipped_not_owned` go to REVIEW.
- **Open:** an unknown Cloudflare outcome (timeout after creation) relies on `Ensure*` idempotency. After the retry period runs out, the event stays NULL for reconciliation.

### 6. The 23-hour window

The 24-hour figure is Stripe's documented minimum key retention. It is **not** present in repo source or config: `doIdempotent` only sets the header. The window is a modelled assumption, kept as a QA gate.

### 7. Diagnostic caveat (unchanged)

Only a prefix of the raw diagnostic is preserved, not the full value: 216 bytes in b64, 288 bytes in latin1.

## Gates (not waived)

- **Price authority** (critical).
- **Booking at-most-once** after the completed claim.
- **Concurrency claims are from code review only:** advisory lock semantics, MySQL RR vs PG RC.
- **Lock connection per in-flight event:** pool pressure.
- **Rows the source marked processed before processing:** need manual review.
- **The 12 remaining IGNORE sites.**
- **Adapters:** gap locks, TIME, NUL/JSON, collation.
- **Import proof:** 1117 entries, ready:false; 4 NOT VALID FKs with 731 orphans.
- **All other general gates.**

## QA plan (later, local only, Stripe test mode)

1. Concurrent deliveries: one is processed, the other gets 503.
2. Kill the lock connection mid-run: the mark fails, and a retry completes.
3. A paid session that is not the recorded attempt goes to review with `processed_at` NULL.
4. An amount mismatch goes to review.
5. A Cloudflare failure returns 503, the domain stays paid, and a retry maps it.
6. A livemode mismatch returns 400.
7. Payments-list `created_at` stays unchanged after a retry.

## Next

1. Decide the price source (business decision; all technical parts are ready).
2. The 12 remaining IGNORE sites.
3. Gap-lock advisory locks.
4. TIME/NUL/collation adapters.
5. Read-only local PG QA plan.

## Files

- `internal/sqldb/sqldb.go` (TryNamedLockConn)
- `internal/api/stripe_event_claim.go` (rewritten)
- `internal/api/site_builder_billing.go`
- `internal/api/booking_checkout.go`
- `internal/api/domain_reservation.go`
- `docs/dbmigrate/proposed-stripe-events-claim.sql`

Backup: `/tmp/str2.bak.tar`.

