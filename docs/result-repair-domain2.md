# result-repair-domain2: domain reservation before purchase and checkout, site selection, mapping guard, instatic claim-before-start

Code only. go build -p 2 ./... exits 0. Runtime-verified 0. Nothing external was executed: no registrar, no Stripe, no process start. PG readyfalse, server refuses PG, MySQL default. The r5 proof is untouched.

## State model (restaurant_domains, inspected)
- **Columns:**
  - registration_status: varchar(32), default 'pending'.
  - stripe_payment_status: enum pending|paid|failed, default pending.
  - stripe_checkout_session_id, registration_cost, cf_zone_id.
- **Values written in the source:** registered (registrar), paid (webhook), provisioned (instatic), active (DNS done). Premium verify reads active / failed / pending.
- **New in-flight values (`domain_reservation_v1`):**
  - `registering`: a purchase is in flight.
  - `register_uncertain`: the provider call failed or was ambiguous.
  - Premium verify maps unknown values through its default branch (gate, read only).

## Owner claim (shared helper `internal/api/domain_reservation.go`)
- **Claim statement:**
  - MySQL: `INSERT … VALUES (rid, domain, 0, 'pending') ON DUPLICATE KEY UPDATE registration_status = registration_status` (no-op).
  - PG: `… ON CONFLICT (domain) DO NOTHING`.
- **Owner re-read:** returns the owner, status, payment status and session. A foreign owner returns `errDomainUnavailable`, and that foreign row is never modified.
- **Atomicity:** comes from the unique index (the first committed inserter wins on both engines). No missing-row lock is assumed.
- **Rows are never deleted:** an external outcome may be uncertain.

## Registrar register (before purchase)
1. Claim. A foreign owner returns 409 "dominio no disponible".
2. Same-owner state:
   - `registered` / `active` → success `already_registered:true`, with no purchase.
   - `registering` → 409 "ya en curso".
   - `register_uncertain` → 409 "pendiente de conciliación manual".
3. CheckDomain / TLD / registrable checks run unchanged. A failure here leaves an owned `pending` row (no wallet obligation).
4. `UPDATE … SET registration_status='registering' WHERE own AND status IN ('pending','paid','failed')`. RowsAffected ≠ 1 → 409. Concurrent same-owner retries lose here and do not buy.
5. RegisterDomain:
   - Error → `register_uncertain`, only from `registering`. No automatic retry and no double buy.
   - Success → `registered`, only from `registering`. A failed DB write leaves `registering` (retries refused, manual recovery) and is logged.
6. Provisioning via `provisionDomainSite(…, req.SiteID)`. The response adds `site_mapping`.

## Stripe checkout (before session create)
1. Claim. A foreign owner returns 409. `stripe_payment_status='paid'` → 409 "ya está pagado".
2. Reserve marker `creating:<uuid4>` on the own row: `WHERE own AND payment<>'paid' AND (session IS NULL OR payment='failed')`. RowsAffected ≠ 1 → 409 "ya hay un pago en curso", so there is no second concurrent checkout and no double charge.
3. CreateCheckoutSession:
   - Error → release our own marker only. The buyer never got a URL, so nothing is payable.
   - Success → store the session id and registration_cost, scoped to our marker. If that write fails, the marker remains (further checkouts refused, manual recovery). The webhook still marks the own row paid.
4. **Abandoned sessions** keep blocking new checkouts. Recovery is a manual reset, or future `checkout.session.expired` handling (not implemented).
5. **The existing webhook owner guard stays.** A post-payment foreign owner is logged as a manual-refund candidate, with no automatic refund.

## Registrar site selection (provisional resolved)
- `mapDomainToSite` (`site_mapping_selection_v1`), with three cases:
  - **Explicit request `site_id`:** optional field in the registrar JSON; it must belong to the restaurant.
  - **No site_id:** exactly one site is used.
  - **0 or several sites:** `site_id_required` / `skipped_no_site`, no mapping, fail-closed.
- The arbitrary first site is gone.
- **Insert:** explicit `VALUES (UUID() | bound Go v1, site, domain, …)`, with the site-owner-guarded conflict branch, then a mapped-site re-read.
- **Stripe webhook:** passes "" (single-site restaurants are mapped; multi-site ones are not).

## Custom domain mapping guard (site_builder handleCreateDomain)
- **Principal check (unchanged):** `canAccessSiteBySiteID`.
- **New check:** the site's restaurant_id is read, and a restaurant_domains row owned by another restaurant → the existing 409 "Domain already in use".
- **DNS proof is not implemented:** `handleVerifyDomain` marks verified with no check (source TODO). A verification_token is generated, but no code reads DNS.
- **Next plan:**
  1. Verify a TXT `_menustudio-verify.<domain>` equal to the token via a resolver interface, before setting status='verified'.
  2. Route only verified mappings, joined through site_builder_sites.restaurant_id. The current router queries a non-existent mappings.restaurant_id column (source anomaly), so the mapping route is dead today.

## instatic: claim before start (`instatic_port_claim_v1`)
- **Audit of the previous fix:** the pre-check, process start and post-check left a window. The process listened on P before the DB claim, and another process holding P for restaurant B in its map could proxy B's traffic to it.
- **Now:**
  1. The owner-guarded upsert claims the row (status `provisioning`) before `cmd.Start`.
  2. The owner is re-read. Only the confirmed owner starts; otherwise the port is forgotten and an error is returned.
  3. After start, `status='running'` is set on the own row only.
- **Effect:** no process ever listens on a port whose DB row belongs to another restaurant.
- **Residual window:** another process's stale in-memory map from before its own claim fails. That process would refuse to start B on P and forget P.

## Counts (inventory r78)
| class | count |
|---|---|
| call sites | 2490 (2475 canonical + 15 added by the repairs) |
| syntax-classified | 2490: nativeSQL 850, portableSQL 1471, selectedSQL 82, pgOnlySQL 41, mysqlOnlySQL 46 (port/retire) |
| provisional | 0 |
| blocked | 0 |
| unwired PG twins | ops-check, cdn-sweep |
| runtime-verified / semantic QA | 0 / 0 |

All reclassifications are code-reviewed fixes with QA pending, not validated equivalence.

## Gates (unwaived)
- **New:**
  - Abandoned Stripe checkout blocking (manual reset / expiry handler).
  - `registering` / `register_uncertain` manual reconciliation.
  - Premium verify handling of the new status values.
  - Multi-site webhook mapping skipped.
  - DNS verification missing; mapping router anomaly.
- **Still open:**
  - Orphaned provider resources (no external cleanup).
  - Demo Stripe Connect shared hash.
  - Domain isolation fixes not runtime-validated.
- **Repo-wide:**
  - 1117 import proof readyfalse; 4 NOT VALID FKs / 731 orphans.
  - IGNORE, gap locks, NUL/JSON, search, TIME, UUID node identity, multikey, collation/numeric, fiscal hash, 136 onupdate.
  - Botstore Rebind quote awareness; unwired tools.
  - Guest-label/catalog-scope repairs pending dedicated QA; PIN columns dump-only.

## Next
1. **Cutover wiring:** single DB_DRIVER dialect; PG pools for ops-check and cdn-sweep; analytics; botstore schemaless UTC pin. Keep the server refusal until runtime QA.
2. **Quote-aware Rebind:** sqldb.Rebind and botstore Rebind fail closed on literals, comments, dollar-quotes and jsonb `?`; then a caller audit.
3. **Shared owner-guard helper:** consolidate domain/identity/port claims.
4. **Source-bug QA batch:**
   - Guest-label NULLIF.
   - Catalog EXISTS scope.
   - Ticket catch-up casts.
   - Refund-sum fan-out.
   - Demo connect identity.
   - DNS TXT verification.

