# result-repair-booking-atomic1: booking payment crash window

Everything here is code only. `go build -p 2 ./...` exits 0. I ran no tests and no SQL, wrote nothing to the DB, and made no Stripe, email or WhatsApp calls. Runtime-verified is 0, PG is still ready:false and the server still refuses PG. R5 is untouched and **no schema change** was needed or applied.

## Inventory r88

`/tmp/villacarmen-bot-20261111/sql-inventory-r88.json` covers 2490 sites in 282 files, all classified. Blocked 0, provisional 0.

| Category | Sites |
|---|---|
| native | 846 |
| portable | 1476 |
| selected | 82 |
| pgOnly | 42 |
| mysqlOnly | 44 |

Two statements were replaced and two added, so the count is unchanged.

## Source bug

`completeCheckout` worked in this order:

1. Conditional pending→paid. Its error was ignored.
2. Conditional **paid→completed**.
3. Replay validation.
4. `commitFrontBooking`: its own insert tx, followed by notifications.
5. A separate `UPDATE booking_id`.

A crash after step 2 left the checkout **completed with no booking**. Retries (webhook or success page) saw "completed" and did nothing, so a paid booking was lost.

## Schema used (existing columns only)

`booking_checkouts`: `public_id` UNIQUE, `status` varchar(16) (pending/paid/completed/cancelled/failed), `booking_id` integer nullable, `completed_at`, `error_message` varchar(512).

`bookings` has no checkout-reference column. The checkout→booking guard is therefore the checkout row itself: row lock, status re-check, and `booking_id` written atomically.

## New flow (`booking_atomic_v1`)

1. **Pending→paid.** This is still conditional, but its error now returns "No se pudo registrar el pago" so the caller retries. Before, the flow continued on an unpaid row.
2. **Replay validation.** `prepareFrontBooking` is unchanged: the same non-locking source policy for availability and extras.
   - If it fails, a conditional update moves paid→failed only while the row is still paid, so a concurrent completion is never overwritten. The case goes to manual refund, as in the source.
3. **One transaction** (`completeCheckoutBookingTx`):
   - `SELECT status FROM booking_checkouts WHERE id=? AND restaurant_id=? FOR UPDATE`. This locks an existing row.
   - If the status is not paid, nothing is written and the caller returns the fresh row (completed by someone else).
   - `insertBookingTx` runs: the exact source booking INSERT plus `applyBookingLocationOccupancy`. It is now shared: `insertBooking` wraps it with its own tx, and every other caller is unchanged.
   - `UPDATE … SET status='completed', completed_at, booking_id=? WHERE id=? AND status='paid'`, which must affect exactly 1 row. Then commit.
   - **Concurrency:** a duplicate event, or the webhook racing the success page, blocks on the row lock and then sees completed, so a double booking is impossible. MySQL FOR UPDATE is a locking (current) read; PG RC re-reads after the lock.
4. **After commit only**, through `finishFrontBooking` (extracted from `commitFrontBooking` with the same body):
   - QR, receipt PDF build and CDN upload, then `setBookingReceiptURL`.
   - Broadcast.
   - WhatsApp and email notifications.
   - n8n webhook.
   - None of these run inside a DB transaction.
   - A notification failure still writes `error_message` and the booking stays completed.
   - Finally the `receipt_url` update.

## Crash points

| Crash point | Result |
|---|---|
| Before commit | Full rollback, no booking, the row stays paid. A webhook retry (503, not marked) or a success-page reload completes it. |
| After commit, before notifications | Completed with `booking_id`. Retries return the booking and create no duplicate. **Notifications not yet sent are lost (at-most-once).** |
| After commit, before the Stripe event is marked processed | The retry sees completed and marks it. Same booking. |
| Insert error inside the tx | Rollback, the row stays paid, the webhook retries (non-terminal). Before, the source marked it failed. |

## Legacy and ambiguous rows

A status of completed with `booking_id` NULL comes from the source crash window. It is logged as `completed_without_booking_review`, never re-created and never refunded automatically, and goes to manual reconciliation. Unknown past source events get no automatic financial action.

## Notifications: durable outbox gate (not invented)

The only durable outbox is `message_deliveries`, and it is WhatsApp-only. Today it is used just to retry a **failed** send, keyed `booking_confirmation:rid:bid`.

Enqueueing inside the tx and also sending inline would risk a double send. Exactly-once across the provider ack is not supported because there is no provider dedupe. Email has no outbox.

So the gate stays open: post-commit notifications are at-most-once, and a crash in that window means the customer gets no confirmation (the booking exists). One proposal would be a pending notification row in the same tx, drained by a worker that marks it sent before calling the provider. That needs a schema change (a notification state for email) and is **not applied**, so I add no code that assumes it.

## Caller audit

| Caller | Path |
|---|---|
| `handleCheckoutComplete` (success page) | `completeCheckout` |
| `handleStripeConnectWebhook` | `completeCheckout`; a non-terminal error leaves the event retryable (stripe-audit1) |
| `booking_insert.go:162` (front booking without payment) | `commitFrontBooking`, same behaviour |
| `booking_insert.go:752` (admin) | `insertBooking`, same behaviour |
| demo checkout | sets paid on its page, then the same `completeCheckout` |

## Gates (not waived)

- **Notifications at-most-once:** no outbox for email.
- **Capacity:** checked non-locking in `prepareFrontBooking`, as in the source. Two concurrent bookings can both pass; this is the gap-lock/capacity gate.
- **Concurrency claims:** code review only.
- **Price:** domain checkout returns 503 (no server price, the parent was informed); that endpoint is blocked only.
- **Stripe:** review rows are not a durable queue.
- **Remaining work:** the 10 guarded IGNORE sites, gap locks, TIME, NUL/JSON, collation, the 1117 import proof (ready:false), the 4 NOT VALID FKs with 731 orphans, and the other general gates.

## QA plan (later, local, Stripe test mode, no real sends)

1. Kill the process between insert and commit: no booking, the checkout stays paid, and a retry creates exactly one booking.
2. Kill after commit: the booking exists and a retry makes no duplicate.
3. Fire the webhook and success page concurrently: one booking.
4. Invalid replay: the checkout moves to failed.
5. A legacy completed row without `booking_id` is logged for review.

## Next

1. Gap locks / capacity.
2. TIME, NUL and collation adapters.
3. Notification outbox proposal.
4. Manual read-only PG QA.

## Files

`internal/api/booking_checkout.go`, `internal/api/booking_insert.go`. Backup: `/tmp/bk1.bak.tar`.

