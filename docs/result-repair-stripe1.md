# result-repair-stripe1: durable Stripe event processing, checkout attempt window, session reconciliation

Everything here is code-only. `go build -p 2 ./...` exits 0. I ran no tests or harness, executed no SQL, wrote nothing to any database, and made no Stripe, Cloudflare or other provider call. Runtime verification is 0. PG is still ready:false and the server still refuses PG. The r5 proof and plan r4 are untouched, and **no schema change is applied**.

## Inventory r85

`/tmp/villacarmen-bot-20261111/sql-inventory-r85.json`: 2493 sites in 282 files, all classified. Blocked 0, provisional 0.

| Kind | Sites |
|---|---|
| native | 848 |
| portable | 1477 |
| selected | 82 |
| pgOnly | 42 |
| mysqlOnly | 44 |

## Schema used (existing columns only)

**`stripe_events`** (MySQL 081 / PG plan): `event_id` varchar(128) UNIQUE, `type`, `payload` JSON/jsonb, `processed_at` nullable, `created_at` defaulting to CURRENT_TIMESTAMP.

| Row state | Meaning |
|---|---|
| `processed_at` NULL | claimed, in progress, or failed and retryable |
| `processed_at` set | effects done, or terminal |
| `created_at` on an unprocessed row | claim lease time, re-stamped when a lease is taken over (10 min) |

`docs/dbmigrate/proposed-stripe-events-claim.sql` is a **proposal only** (applied: false). It adds `claimed_at`, `attempts` and `last_error` so `created_at` no longer has to double as the lease.

**`restaurant_domains`**: `domain` is UNIQUE. `stripe_payment_status` is an enum: pending, paid, failed. `stripe_checkout_session_id` is varchar(255). `registration_cost` is numeric(10,2).

**`recurring_invoices`**: no unique key.

## Source bug fixed

Both webhooks (`/stripe/webhook` for domains, `/stripe/connect-webhook` for booking checkout and `account.updated`) inserted the event with `processed_at = NOW()` **before** processing. After any failure past that insert, Stripe's retries were dropped as duplicates, and the domain webhook only logged insert errors before continuing.

The new flow, `stripe_event_processing_v1` in `stripe_event_claim.go`, works like this:

1. **Claim.** The event is inserted with `processed_at` NULL. MySQL uses an ODKU no-op and PG uses `ON CONFLICT DO NOTHING`. Only a duplicate is a no-op; any other error returns 503 so Stripe retries.
   - RowsAffected 1 means claimed.
   - Otherwise a conditional UPDATE takes the lease only if the row is unprocessed and its lease has expired. That gives exactly one winner, with no lock on a missing row.
   - Otherwise a processed row returns 200 duplicate, and a fresh lease returns 503 (busy) so Stripe retries later.
2. **Process.** A returned error leaves the event unprocessed and answers 503, so Stripe retries for up to about 3 days.
3. **Mark processed** only after success, or when the event is terminal. If the mark fails, the answer is 503 and the retry repeats only idempotent effects.

Old rows written by the source keep `processed_at` set. Events already lost that way cannot be told apart from processed ones and need manual review.

## Domain `checkout.session.completed` / `async_payment_succeeded` (retry-safe)

- **Signed object checks:** `payment_status` must be `paid`.
  - The metadata carries `domain`, `restaurant_id`, `site_id` (as before), plus two new keys: `attempt` (the reservation key) and `amount_cents`.
  - When `amount_cents` is present, it must equal `amount_total`, and the currency must be `eur`. A mismatch is terminal and logged for manual review, with nothing provisioned.
- **One transaction:**
  - Claim insert (no-op on an existing row).
  - `SELECT … FOR UPDATE` on the now-existing domain row.
  - Owner check: a foreign owner means nothing is written; this is terminal and goes to manual refund review.
  - **Duplicate payment** (already paid by a different session): terminal and logged, with no effects.
  - **Session not the recorded attempt** (a paid but older or pre-v2 session of the same owner): accepted, because the payment is verified, and logged for review.
  - UPDATE the owner row to paid with this session id, then insert the recurring invoice **once per domain** using `NOT EXISTS` under the row lock. MySQL uses `INSERT…SELECT FROM DUAL`; PG uses typed casts.
  - Commit.
- **Provisioning** (Cloudflare Ensure* plus the mapping upsert, both idempotent) runs after commit.
  - A crash before the processed mark only repeats these idempotent steps.
  - There is no registrar purchase in the webhook, so there is no double purchase, and the invoice cannot be duplicated.
- **Caveats:**
  - A Cloudflare failure inside `provisionDomainSite` is still not surfaced, so the event is marked processed with the domain not active. This is the existing gate.
  - A legitimate re-purchase of the same domain later gets no new invoice row (fail-safe; recorded).

## Expired or abandoned sessions

`checkout.session.expired` and `async_payment_failed` move the domain row from pending to failed, only while the row still holds **that** session id or that attempt's `creating:` / `uncertain:` marker. An old session can never clear a newer attempt. This is idempotent: a second event matches 0 rows.

## Checkout attempt (`checkout_idempotency_v2`)

- **Window.** The key is now `<uuid>@<unix>`, the reservation time. It is persisted in the existing `creating:` / `uncertain:` marker and sent as the Idempotency-Key `domain-checkout:<key>`.
  - Reusing a key is allowed only within **23 hours**. The Stripe client (`doIdempotent`) sends only the header; Stripe's documented minimum retention is 24 hours.
  - An older key, or one with no timestamp, is refused with `errCheckoutNeedsReconcile` (409, logged). It needs manual Stripe reconciliation; there is no session listing or heuristic matching.
- **Stable-id reconciliation.** When the row holds a real `cs_…` id that is still pending, the request calls `RetrieveCheckoutSession(that id)` (existing client code, not executed here).
  - `expired`: a conditional pending→failed update on that id, after which a new attempt may start.
  - `complete`: 409, "pending confirmation".
  - Open, or any lookup error: fail closed with "pago en curso".
  - It never charges or refunds.
- **Same-owner retry.** If an attempt is pending or a session was created, there is no second session. A paid row is refused before Stripe is called, as before.

## Connect webhook

- `completeCheckout` is a conditional state machine (pending→paid→completed) that re-reads the session from Stripe. An unknown checkout, or one that is cancelled or failed, is terminal. Other errors are retried.
- `account.updated` retries when the refresh fails; the refresh is idempotent.

## Caller audit

| Caller | Change |
|---|---|
| `server.go` routes `/stripe/webhook`, `/stripe/connect-webhook` | none |
| `handleBillingCheckout` → `reserveCheckoutAttempt` | edited |
| `handleStripeCheckoutCompleted` | now returns an error |
| `handleStripeCheckoutClosed` | new |
| `backoffice_platform` payments list (reads `stripe_events` ordered by `created_at`, shown as `paid_at`) | unchanged; see note |

Note on the payments list: `created_at` is now the last claim time, which differs from first receipt only on retries. The list also shows unprocessed events. Recorded; the proposal fixes it.

## Diagnostic caveat (correction)

`[raw-b64]` stores base64 of only the **first 216 bytes**, and `[raw-latin1]` stores only the first 288 bytes. Only that prefix is preserved losslessly; any tail beyond it is lost. It is **not** a full lossless raw diagnostic.

## Gates (not waived)

- Lease and claim concurrency claims are from code review only.
- MySQL RR vs PG RC: the claim relies on the unique key and conditional UPDATEs, not on snapshot reads.
- The `created_at` overload is temporary, pending the proposal.
- Cloudflare provisioning failure is not retried.
- Events dropped by the source and old `uncertain` attempts need manual reconciliation.
- Events past Stripe's ~3-day retry period stay unprocessed and visible for reconciliation.
- 12 IGNORE sites remain: accounting_exports, menu_visibility, provision×6 / bo_user_restaurants×3, booking_reminder_deliveries, campaign_email_retries/recipients, pre_shift, bot_inbound_queue. The stripe_events site is now fixed.
- Gap locks, TIME, NUL/JSON, collation (ci_ai vs unicode_ci, PAD SPACE), 1117 import proof ready:false, 4 NOT VALID FKs with 731 orphans, and all other general gates.

## Manual QA plan (later, local only, Stripe test mode, never live)

1. Replay the same signed event: first 200, then duplicate.
2. Force a DB error mid-processing: 503, the row stays unprocessed, and a retry after the lease processes it.
3. Run two concurrent deliveries: one is processed, the other gets 503 busy.
4. Simulate an expired session for an old attempt while a newer attempt exists: the newer attempt is untouched.
5. A 23h+ uncertain attempt returns 409 reconcile.
6. An amount mismatch is not provisioned.
7. A second paid session gives no second invoice.

## Next

1. The 12 remaining IGNORE sites.
2. Gap-lock advisory locks.
3. TIME / NUL / collation adapters.
4. Read-only local PG QA plan.

## Files

- `internal/api/stripe_event_claim.go` (new)
- `internal/api/site_builder_billing.go`
- `internal/api/booking_checkout.go`
- `docs/dbmigrate/proposed-stripe-events-claim.sql` (proposal)

Backup: `/tmp/str1.bak.tar`.

