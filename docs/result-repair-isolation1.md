# result-repair-isolation1: domain ownership tenant-isolation fix (pos13 and all equivalent writers)

Code only. go build -p 2 ./... exits 0 (after go clean -cache freed the /tmp quota). Runtime-verified 0. PG readyfalse; the server refuses PG; MySQL stays the default. Nothing was run. The r5 proof is untouched.

## The bug (source, both engines)
restaurant_domains.domain is UNIQUE and is the Host → tenant routing key. Four writers upserted on it:

| writer | source conflict action | effect for a domain owned by restaurant B |
|---|---|---|
| backoffice_premium register (tx) | ODKU restaurant_id=VALUES(restaurant_id), is_primary=… | A took over B's domain. The pre-check FOR UPDATE locks nothing for an absent row on PG; MySQL gap locks can deadlock. |
| site_builder_registrar register | ODKU registration_status='registered' | B's row rewritten; DNS then provisioned for A |
| site_builder_billing Stripe webhook | ODKU registration_status/stripe_payment_status='paid' | B's row marked paid; provisioning + recurring invoice for A |
| instatic ProvisionSubdomain | ODKU registration_status/cf_zone_id | B's row rewritten |
| site_builder_domain_mappings (registrar provision, instatic MySQL branch) | ODKU status/ssl_status='active' | another site's mapping rewritten |

## The fix (explicit reviewed bug fix, both dialects)
- **New helper:** `internal/api/domain_ownership.go` (`domain_owner_isolation_v1`) provides `restaurantDomainOwner` / `domainOwnedByOther` (optional FOR UPDATE).
- **Conflict branches:**
  - MySQL: `col = IF(restaurant_id = VALUES(restaurant_id), new, col)`. restaurant_id is never assigned, so the IF condition is stable across the assignment list.
  - PG: `ON CONFLICT (domain) DO UPDATE … WHERE restaurant_domains.restaurant_id = EXCLUDED.restaurant_id`.
  - Mappings use the same pattern on `site_id`.
- **Ownership re-read after the upsert** (and before external effects where possible):
  - **Premium:** re-read FOR UPDATE inside the tx. A foreign owner returns the existing 409 DOMAIN_UNAVAILABLE; the deferred rollback undoes the is_primary reset, so there are no partial writes.
  - **Registrar:** a foreign owner is rejected before CheckDomain/RegisterDomain with the existing 409 "dominio no disponible". The post-upsert re-check covers the race (409, not provisioned).
  - **provisionDomainSite:** skips DNS and mapping for a foreign-owned row. An absent row keeps the source path.
  - **Stripe webhook:** for a foreign-owned row it skips provisioning and the recurring invoice, and logs `[domain_owner_isolation_v1]` for manual refund review.
  - **instatic:** a pre-check runs before DNS; a foreign owner (also on the post-check) returns an error instead of claiming success.
- **Same-owner path:** unchanged values. The conflict update still applies to the owner's row.
- **RowsAffected:** not used by any of these callers. On MySQL a no-change ODKU returns 0 or 1 and PG returns 0 or 1, so there is no observable difference.

## Race argument
- **Ownership invariant:** after the fix, no writer assigns restaurant_id on conflict, and no UPDATE or DELETE of restaurant_domains.restaurant_id exists anywhere (grep audit). The owner is therefore the first committed inserter and never changes.
- **Concurrent first insert (A, B):** the unique index serialises them on both engines.
  - The second INSERT waits for the first. If the first commits, the second takes the conflict path. The guard leaves the row unchanged, but the row is locked on both engines (PG locks the conflicting row even when the DO UPDATE WHERE is false; MySQL ODKU takes an X lock). The re-read then sees owner A, and B gets "unavailable".
  - If the first rolls back, B inserts and owns the row.
- **MySQL premium gap-lock deadlock:** MySQL premium can still deadlock on the absent-row FOR UPDATE gap locks. That fails closed (500 DOMAIN_REGISTER_FAILED, rollback) and never hands the domain to the wrong owner.
- **Not claimed:** FOR UPDATE on a missing row is not relied on for safety.

## Caller audit (all restaurant_domains / domain-mapping writers)
- **Premium:** both fallback variants (of the 49 behind 39 sites) are guarded. The website and recurring variants don't touch domains.
- **Registrar:** register and provision both go through provisionDomainSite, which is guarded, and the mapping is guarded.
- **Billing:** the webhook is guarded. The checkout UPDATE is `WHERE restaurant_id=? AND domain=?`, already scoped, and unchanged.
- **instatic:** the subdomain row is guarded. The MySQL mapping is guarded; its PG side stays blocked on UUID.
- **site_builder handleCreateDomain:** a plain INSERT into mappings; the unique key returns the existing 409, no upsert. Unchanged.
- **Gate:** handleCreateDomain doesn't check restaurant_domains ownership (mapping vs domain table). This is a policy gap I recorded but did not fix (no owner-policy guess).

## Inventory r75 status
| class | count |
|---|---|
| call sites | 2476 (+1 owner-check helper call) |
| syntax-classified | 2473: nativeSQL 841, portableSQL 1460, selectedSQL 82, pgOnlySQL 39, provisional 5 |
| MySQL-only: port or retire | 46 |
| unwired PG twins | ops-check, cdn-sweep |
| blocked | 3: assistant_pos_tools:68 UUID v1, instatic_manager:903 UUID v1, instatic_manager:211 multi-unique (restaurant_id, port), which is NOT UUID |
| semantically validated / runtime-verified | 0 / 0 |

## Next concrete repair (proposal, same pattern)
1. **stripe_connect.go:117** (PK restaurant_id + UNIQUE account_hash):
   - In a tx, FOR UPDATE on account_hash. If another restaurant owns it, return the existing "No se pudo conectar"-class error and change no rows.
   - Then the owner-scoped upsert on restaurant_id. Map a racing 23505/1062 on account_hash (IsUniqueViolationOn) to the same error. MySQL changes the same way: no cross-tenant overwrite.
2. **uazapi:637** (UNIQUE restaurant_id + UNIQUE instance_name): same shape on instance_name. A foreign instance_name returns a provisioning conflict, and the provider instance stays unassigned.
3. **instatic:211** (restaurant_id + port): same shape on port. A foreign port returns an error, and the caller picks another port. This would unblock the PG twin.
4. **SEO 707/715:** both keys include restaurant_id, so there is no cross-tenant risk. Make it atomic with one tx: INSERT … ON CONFLICT DO NOTHING inside a savepoint, then the route-key-first UPDATE in the same tx. Keep it provisional until reviewed.
5. **UUID v1, the 2 true sites:** generate google/uuid v1.6.0 NewUUID (v1, already in go.mod) in Go and bind it on both dialects, after a consumer grep for ordering/time reliance.

## Gates (unwaived)
- 1117 import proof readyfalse; 4 NOT VALID FKs / 731 orphans.
- IGNORE truncation; gap locks; NUL/JSON; search; TIME; UUID; SEO; multikey; collation/numeric; fiscal hash; 136 onupdate.
- Unwired tools; botstore Rebind quote awareness.
- Guest-label/catalog-scope technical repairs pending dedicated QA; PIN columns dump-only.
- **New:** a Stripe payment for a foreign-owned domain is now logged and not provisioned, but is not refunded automatically (manual review).

