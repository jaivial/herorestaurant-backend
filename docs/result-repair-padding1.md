# result-repair-padding1

These are code changes only. `go build -p 2 ./...` exits 0. I ran no tests, executed no SQL, called no provider and made no network calls. Inventory: no SQL text changed, so r95 still has 2499 sites (re-run confirms 2499). Runtime handler QA: 0. PG server disabled. Tools: report-only. 1117 pending / 731 orphans: unwaived. R5: untouched.

## Code changes (`padding_domain_identity_v1`, `padding_provider_identity_v1`)

All domain normalization reuses the existing `normalizePremiumDomain` (`backoffice_premium.go:4139`: lower, TrimSpace, scheme/path/query/port strip, trim of edge dots, `boPremiumDomainRe` format check). It returns "" for an invalid domain, and that becomes a 400. No new domain policy was added.

1. **`domain_reservation.go` `claimOwnedDomain` (the shared claim helper for checkout, the attempt re-claim and the registrar):** it now refuses any domain that is not already in canonical form (`errDomainNotCanonical`). It never normalizes silently.
2. **`site_builder_billing.go` checkout (dormant behind the 503):**
   - The price gate stays **first**, unchanged.
   - Then `req.Domain = normalizePremiumDomain(req.Domain)`, and an invalid value gets a 400. The same identity then flows to the claim, `reserveCheckoutAttempt`, the Stripe metadata and every UPDATE.
3. **`site_builder_registrar.go` register and provision:** these used lower+TrimSpace and now use `normalizePremiumDomain`, with 400 on invalid input. This is stricter: an invalid format is rejected before any purchase, and edge dots and scheme/path are stripped exactly as on the premium path.
4. **`site_builder.go` domain-mapping create:** the same normalized value is now checked by `domainOwnedByOther` **and** stored. Before, the check was normalized but the insert used the raw value.
5. **`site_builder.go` site create/update:**
   - **subdomain:** a value with edge ASCII spaces is refused (400). There is no existing subdomain normalizer, so nothing is trimmed and no new policy is added.
   - **custom_domain (update):** a non-empty value is normalized through `normalizePremiumDomain` (invalid → 400). An empty value keeps the source behaviour.
6. **`backoffice_uazapi_provisioning.go`:** a provider-returned `InstanceName` with a trailing ASCII space is refused (error, nothing stored). The exact provider value is never trimmed.
   - The generated `nv-…` and `legacy-…` names are unaffected.
   - The existing global-ownership 23505 → `errUAZAPIInstanceTaken` behaviour is unchanged.
   - The lookup at `:679` uses the same exact value.

Stored raw names (areas, tables, slugs, aliases) and provider values are preserved. Nothing is rewritten.

## Audit open items closed from source

- **Salon updates in `backoffice_config.go:1780/1819`:** these set only `has_capacity_limit`/`is_active`, not name.
- **Table numero_mesa update:** TrimSpace at `backoffice_premium.go:2545` before both the pre-check and the dedicated UPDATE at `:2885`.
- **UAZAPI base_url update:** `normalizeBOUAZAPIServerBaseURL` at `:345`, same as create.
- **Imported uazapi index name:** `ix__restaurant_uazapi_instances__uniq_restaurant_uazap_496278c3`.
- **Collections writers:** repo-wide, only migration 033 DDL plus binaries.

## Docs

- **`docs/dbmigrate/collation-padding-audit-1.md`:** the Summary is replaced by **28 disjoint primary statuses**. Totals: 15 code-canonical, 5 fixed, 4 GAP, 2 with no runtime writer, 1 opaque, 1 enum. Sub-issues are listed separately. No key is claimed to have full parity.
- **`docs/dbmigrate/proposed-padding-index-1.md`:** an unapplied proposal covering expression unique indexes with explicit `COLLATE dbmigrate.ci_ai`, a pre-check for collisions, matching ON CONFLICT targets and lookups, then the drop order. It states its limits honestly: ordering, LIKE, other equality and expansions are not made equivalent.

## Open gates

- **Remaining GAP keys:** 4 (areas name, tables name, page slug, supplier alias name). Remedy per the proposal; not applied.
- **custom_domain ownership:** not checked against restaurant_domains.
- **Provider resources:** the provider-orphan resource on the refused UAZAPI name.
- **Pre-fix Stripe sessions:** raw-domain metadata goes to review.
- **Keys not yet audited:** 93.
- **Carried gates:**
  - general: collation and LIKE; NUL/jsonb; notifications at-most-once; capacity locks reviewed in code only; 10 guarded IGNORE sites; local source environment ≠ production;
  - price authority: domain checkout stays at 503;
  - 1117 pending / 731 orphans.

## Next

1. Fix the 4 GAP keys, using value-preserving lookups or the index proposal.
2. Audit chunk 2 of the 93 keys.
3. Manual read-only app-SQL QA.

## Files

- `internal/api/{domain_reservation.go,site_builder_billing.go,site_builder_registrar.go,site_builder.go,backoffice_uazapi_provisioning.go}`
- the two docs above

Backup: `/tmp/pad1.bak.tar`.

