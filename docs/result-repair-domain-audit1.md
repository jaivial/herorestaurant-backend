# result-repair-domain-audit1: audit of repair-domain2 (schema states, Stripe idempotency, site selection, webhook, mappings, instatic)

Code only. go build -p 2 ./... exits 0. Runtime-verified 0. No SQL, Stripe, registrar or process was executed. PG readyfalse, server refuses PG, MySQL default. The r5 proof is untouched.

## Schema facts (MySQL schema.json and PG plan r4)
| column | type (both engines) | consequence |
|---|---|---|
| restaurant_domains.registration_status | varchar(32) NOT NULL DEFAULT 'pending', no CHECK, no enum | `pending`, `registering` and `register_uncertain` (18 chars) are valid. No migration needed. |
| restaurant_domains.stripe_payment_status | enum('pending','paid','failed') | Code only writes these three values. |
| restaurant_domains.stripe_checkout_session_id | varchar(255) NULL | `creating:` / `uncertain:<uuid>` (≤ 46 chars) fits. |
| instatic_instances.status | enum('provisioning','running','stopped','error') | `provisioning` and `error` are valid. |

No immutable proof or schema was edited.

## Fixes (both dialects)
1. **Stripe checkout idempotency (`checkout_idempotency_v1`).**
   - The API supports `Idempotency-Key` through the existing `doIdempotent`. I added `CreateCheckoutSessionKeyed` (same form as CreateCheckoutSession).
   - **Per-attempt key:** stored as `creating:<key>` in stripe_checkout_session_id and sent as `domain-checkout:<key>`.
   - **Ambiguous error** (timeout or 5xx can still create the session): the reservation becomes `uncertain:<key>` and is never cleared. The next request, or a concurrent loser of the reservation update, reuses the same key, so Stripe returns its original session. No second session, no double charge.
   - **Success:** the real session id is stored, scoped to our creating/uncertain key.
   - **Earlier repair-domain2 behaviour (now removed):** it cleared the marker on error, which could allow a duplicate.
2. **Site selection before the wallet path.**
   - `resolveMappingSite` runs before the registrar purchase, before provision DNS, and before Stripe session creation. Several sites without a `site_id` returns 400 "site_id requerido". A foreign site_id returns 400.
   - Checkout writes `site_id` into the signed session metadata, and the webhook passes it through (`checkoutRequest.SiteID`, `registerRequest.SiteID`: optional JSON strings).
   - A restaurant with no site keeps the source result: no mapping.
3. **Webhook.**
   - **Replay:** VerifyWebhookSignature is replaced by `VerifyWebhookSignatureWithTolerance(…, 5m)`, so an old signed event is refused.
   - **Dedupe:** the INSERT on unique `event_id` is the claim. A duplicate key means "already handled". The source read-then-insert let two concurrent deliveries both process.
   - **Payment status:** only `payment_status == "paid"` marks the domain paid; delayed-method "unpaid" completions are logged and ignored.
   - **No state downgrade:** the paid upsert only moves `registration_status` from pending or failed. It never overwrites registered, active, registering or uncertain, which previously could reopen a purchase.
4. **Same-owner claim:** a no-op conflict branch. It never mutates paid or other state. A foreign owner is only read.
5. **Premium verify:** `registering` and `register_uncertain` display as pending and are never shown as verified or active.
6. **instatic port claim.**
   - **Start failure:** the own claimed row becomes `error` and keeps the port for this restaurant, so a retry claims it again.
   - **Foreign port:** the foreign owner is recorded in the in-memory map, so allocation skips that port and doesn't retry it forever.
   - **Safety:** only a confirmed DB owner starts a process, on every process, because the claim is checked against the unique port key before start.

## Reported, not fixed (actual blockers)
- **Custom mapping TOCTOU.**
  - A mapping for an unowned domain reserves nothing in restaurant_domains. A later buyer owns the domain row, and the old unverified mapping then blocks that buyer's mapping (`skipped_mapped_to_other_site`).
  - I did not claim restaurant_domains from the mapping endpoint. Without DNS proof, that would let any site owner squat a domain.
  - Safe path: DNS proof first, then an owner claim at verification time.
- **DNS proof (source TODO).** `handleVerifyDomain` marks verified without DNS. The token exists but no resolver is wired. Mapping routing stays inactive: the router's mapping lookup queries a non-existent column, so it is never activated.
- **Abandoned real sessions (`cs_…`):** they still block new checkouts. Resolving this requires RetrieveCheckoutSession or a `checkout.session.expired` webhook (external API).
- **Webhook processing failure after the event row insert:** Stripe retries become duplicates (source behaviour).

## Counts (inventory r79)
- **Call sites:** 2490, all classified.
  - nativeSQL 849
  - portableSQL 1472
  - selectedSQL 82
  - pgOnlySQL 41
  - mysqlOnlySQL 46 (port or retire)
- **Provisional / blocked:** 0 / 0.
- **Runtime-verified / semantic QA:** 0 / 0.
- **Classification does not validate these state changes.**

## Gates (unwaived)
- Everything above in "Reported, not fixed".
- Orphaned provider resources (no external cleanup).
- Demo Stripe Connect shared hash.
- 1117 import proof readyfalse; 4 NOT VALID FKs / 731 orphans.
- IGNORE, gap locks, NUL/JSON, search, TIME, UUID node identity, multikey, collation/numeric, fiscal hash, 136 onupdate.
- Botstore Rebind quote awareness; unwired tools.
- Guest-label/catalog-scope repairs pending dedicated QA; PIN columns dump-only.

## Next
1. **Cutover wiring:**
   - Single DB_DRIVER dialect source.
   - PG pools for ops-check and cdn-sweep (UTC pin, PG schema name).
   - Analytics wiring.
   - Botstore schemaless UTC pin.
   - Keep the server PG refusal until runtime QA.
2. **Quote-aware Rebind:** sqldb.Rebind and botstore Rebind fail closed on string literals, comments, dollar-quotes and jsonb `?` operators; then a caller audit.
3. **Shared owner-guard helper** for the domain, identity and port claims.
4. **DNS TXT verification design:** a resolver interface plus a claim at verification. Implement only after review.

