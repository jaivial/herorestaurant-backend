# result-repair-ignore3: remaining IGNORE sites and domain price authority

All of this is code only. `go build -p 2 ./...` exits 0. I ran no tests or SQL, wrote to no DB, and called no provider. No sends were made, and the reminder and outbox workers were not run. Runtime-verified count is 0. PG is still ready:false and the server still refuses PG. R5 is untouched.

## Inventory r87

`/tmp/villacarmen-bot-20261111/sql-inventory-r87.json`: 2490 sites in 282 files, all classified. Blocked 0, provisional 0.

| Category | Sites |
|---|---|
| native | 846 |
| portable | 1476 |
| selected | 82 |
| pgOnly | 42 |
| mysqlOnly | 44 |

The total is one lower than before because the unsafe pre-shift fallback COUNT was removed.

## Exact recount (grep, non-comment statements)

- **Before this batch: 24 `INSERT IGNORE` statements.**
  - 10 already had repair guards: suppression ×5, bounce event, WA outbox, POS category ×2, fiscal series. These remain as IGNORE, with Go-side guards; the remaining risk for each is documented in ignore1/ignore2/suppression2.
  - 14 were unaddressed. This batch fixes all 14.
- The earlier "12" was an inaccurate claim: it included the Stripe site, which stripe1 already fixed, and it missed 3 `bo_user_restaurants` sites plus others.
- **Now: 10 remain**, all the guarded ones listed above.

## The 14 fixed sites

Pattern on both dialects: a duplicate is the only no-op (MySQL `ON DUPLICATE KEY UPDATE k=k`, PG `ON CONFLICT (actual key) DO NOTHING`). Every other error propagates or is logged, and nothing is silently truncated.

| Site | Key / width / FK | Source risk | Fix and caller |
|---|---|---|---|
| `booking_reminders.claimBookingReminder` | uq delivery_key varchar(128); no FK | MySQL IGNORE and the PG `CAST(varchar(128))` both cut the key, so two keys sharing a prefix collide and the second reminder is lost | Over 128 runes is an error. Duplicate is a no-op. The caller now logs claim errors instead of silently `continue`. No send. |
| `pre_shift_reminders.Claim` | event varchar(64), delivery_key 191 | The event was cut by LEFT, so Release (`WHERE event=key`) missed its row. The error fallback answered "claimed" when no row existed, so it could **send without a durable claim**. | Over 64 runes is an error. The fallback is removed and errors are returned. `reminders.worker` already stops on error. |
| `botInboundEnqueue` (MySQL bot store) | uq (restaurant_id, message_id); chat_key 190 / message_id 120 | A cut message_id collided, so a message was never queued for replay | Over-width is refused on MySQL (checkpoint log; best effort as in the source, the live turn is not blocked). Duplicate is a no-op. The PG bot store uses TEXT columns. Quarantine and replay are unchanged. |
| `provisionRestaurantDefaults` ×5 | PK/uniq restaurant_id; FK restaurants on branding, info, bot_ai | IGNORE and the PG `EXISTS` guard skipped an FK failure, so admin got "success" with missing satellite rows | FK errors now fail provisioning; the caller returns success:false. ON CONFLICT now names the actual key. brand_name has no cut: `restaurants.name` is varchar(255), the same width. |
| `provisionRestaurantOwner` | PK (user_id, restaurant_id); FKs | Same as above | Same. An existing membership keeps its role. |
| `backoffice_auth` bootstrap | Same | The `_, _` silent result, and FK skip when restaurant 1 is missing | Still best effort, but now logged. |
| `handlePlatformUserCreate` assignment | role varchar(32) | The role (a permission identity) was cut; FK was skipped; success was always reported | Over 32 runes or an FK failure now answers `success:false` with "user created, assignment failed". |
| `accounting_exports` | uq of 5 columns; FK restaurants | An FK skip meant the export was served unaudited | FK errors give 500. Duplicate is a no-op. |
| `menu_visibility` seed | uq (restaurant_id, menu_key); constants | Silent `_, _` | Errors logged. |
| `campaign_email_retries` | PK recipient_id | Errors swallowed (return false) | Errors logged. `truncate` is rune-safe since ignore1. |
| `campaign_recipients` | uq (campaign_id, channel, target); target 190 | PG `LEFT(target,190)` / MySQL IGNORE cut the target into **another recipient** | The target is never cut. It is re-checked with `campaignSuppressionKey`, and an unstorable target counts as an insert error. The name keeps an explicit 190-character rune cut in Go. |

## Domain price authority (read-only grounding)

**Where I looked:**
- Worktree backend: `internal/api/site_builder_billing.go`, `server.go` route `/site-builder/billing/checkout` (ajustes gate), `forky_admin_ops.json`, `docs/USER_STORIES.md` US-0483 (endpoint listing only).
- `/home/jaime/newvillacarmen/backoffice/src`: **no caller** of `billing/checkout` or `amount_eur`.
- `/home/jaime/newvillacarmen/backend`: same code as the worktree.

**What I found:**
- `integrations/cloudflare.go RegistrarDomain.PriceEUR` is declared but **never populated**. Cloudflare returns `pricing.currency` and `registration_cost`, shown raw by `handleRegistrarSearch`.
- The webhook reads metadata `price_eur`, but no code ever sets it.
- `backoffice_premium.go` prices a **different** flow: `premiumDomainBasePrice × boPremiumDomainMarkupFactor 1.5`, in EUR.
- No config, DB table or doc defines the site-builder checkout price.

**Decision: fail closed, checkout only.**
- `domainCheckoutPriceAuthority()` returns false, so `handleBillingCheckout` answers 503 before reservation or Stripe.
- Nothing else is blocked. The webhook still processes existing sessions under the audit1 rules.
- Pending owner decision. The two candidate sources are recorded, and neither was chosen.

## Carried gates

- **Manual review is not a durable queue.** Review means 200 with `processed_at` NULL. That is evidence only, with no queue.
- **CRITICAL next repair: booking paid→completed crash loses the booking.** The fix needs an atomic DB booking plus outbox, using the source architecture. No sends.
- **Pinned SQL connection fencing** needs QA.
- **Diagnostic** keeps only a prefix.
- **Remaining adapters:** gap locks, TIME, NUL/JSON, collation (ci_ai/PAD SPACE).
- **Import proof:** 1117 ready:false; 4 NOT VALID FKs, 731 orphans.
- All other general gates stay open.

## QA plan (later, local only, no sends)

1. Reminder key over 128 characters gives an error and nothing is sent.
2. Pre-shift DB error: the claim fails and nothing is sent.
3. Inbound message_id over 120 characters on MySQL is refused and logged.
4. Provisioning with the parent deleted gives success:false.
5. A role of 33 characters gives success:false.
6. A campaign target over 190 characters counts as an insert error.
7. Domain checkout returns 503.

## Next

1. Booking atomicity (critical).
2. Gap locks.
3. TIME / NUL / collation adapters.
4. Manual read-only PG QA.

## Files and backup

Files changed, all under `internal/api`:
- `booking_reminders.go`
- `pre_shift_reminders.go`
- `whatsapp_bot_inbound_queue.go`
- `backoffice_provision.go`
- `backoffice_auth.go`
- `backoffice_platform.go`
- `backoffice_accounting_exports.go`
- `backoffice_menu_visibility.go`
- `campaign_email_retry.go`
- `backoffice_campaigns.go`
- `site_builder_billing.go`

Backup: `/tmp/ig3.bak.tar`.

