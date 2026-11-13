# collation-padding-audit-1: the 28 human/unknown text unique keys

This is a docs-only audit. I executed no SQL, read no data, and made no code changes.

## Scope and coverage

- **Source of the key list:** `docs/dbmigrate/collation-unique-keys-r93.tsv`.
- **Rows in this audit:** every row whose heuristic class includes `human` or `unknown`. There are **28** such rows; the request said the first 30, and only 28 exist. This covers all of them.
- **Not audited:** the other **93** text unique keys (opaque, code and contact classes).

## Common facts (schema evidence)

- **MySQL side:** every column below is `utf8mb4_unicode_ci`, which is **PAD SPACE**. Trailing ASCII spaces are ignored in `=`, in unique checks, and in GROUP BY. Comparison is also case- and accent-insensitive.
  - The only exception is `stock_margin_scope_bands.zone`, which is an ENUM.
  - Source: `full-export/schema.json`.
- **PG plan r4:** the same width, `COLLATE dbmigrate.ci_ai` (ICU level1, nondeterministic), and a plain unique index on the same columns. Measured on 05:43:
  - case: equal
  - accent: equal
  - ß = ss: equal
  - **trailing ASCII space: NOT equal**

  So any writer that can store, or any reader that can probe, a value with trailing spaces diverges from MySQL.
- **No sub-part or expression indexes** in any of these keys. sub_part is null for every column.
- **Empty tables do not prove safety.** The only evidence that counts is whether every writer canonicalizes.

Remedy vocabulary used below:

| Code | Meaning |
|---|---|
| **W-TRIM-OK** | Every writer already applies `strings.TrimSpace` (Unicode trim) before the write, so a stored trailing ASCII space is impossible from this code. Readers still need checking. |
| **R-PROBE** | A lookup by user input must apply the same normalization the writer uses, or compare with `rtrim(col,' ') = rtrim(?,' ')` on PG. |
| **IDX-RTRIM** | Forward PG migration: unique index on `(…, rtrim(col,' '))` with ci_ai, plus every ON CONFLICT target and every lookup moved to the same expression. This is the only remedy that reproduces PAD SPACE **without changing stored values**. It needs a per-key code change. |
| **TRIM-CHANGE** | Normalize at write time. This changes the stored value compared with MySQL, which kept the raw value. It is **flagged** wherever it would also trim leading spaces or Unicode whitespace, because MySQL treats those as distinct (TrimSpace ≠ PAD SPACE). |

## Per key

1. **assistant_memory_nodes.uq_memory_scope_label** `(restaurant_id,user_id,label)`, label varchar(190) NOT NULL.
   - **Writer:** `assistant_memory.go:80`, `label := strings.ToLower(strings.TrimSpace(n.Label))`, then upsert at `:120/:123`. The label comes from LLM extraction.
   - **Reader:** `:131`, `WHERE … label = ?`, with the same normalized label.
   - **Status:** W-TRIM-OK and R-PROBE-OK (same variable). No change needed.
   - **Caveat:** TrimSpace also removes leading and Unicode spaces. This is the source's own behavior and equal on both engines.
2. **pos_kitchen_stations.uq_pos_kitchen_station_name** `(restaurant_id,name)`.
   - **Writers:** `backoffice_pos_kitchen.go:98` (insert) and `:109` (update), both with `strings.TrimSpace(in.Name)`.
   - **Readers:** id-based only (`:62`, `:130`, `:445` select by restaurant or id).
   - **Status:** W-TRIM-OK, no name probes. No change needed.
3. **pos_modifier_groups.uq_pos_modifier_group_name**.
   - **Writers:** insert at `backoffice_pos_modifiers.go:420/429` (TrimSpace); update at `:487/495` (TrimSpace).
   - **Readers:** id-based.
   - **Status:** W-TRIM-OK.
4. **pos_modifier_options.uq_pos_modifier_option_name** `(restaurant_id,group_id,name)`.
   - **Writers:** insert at `:595/604` and update at `:638/646`, both TrimSpace.
   - **Readers:** id-based.
   - **Status:** W-TRIM-OK.
5. **pos_packs.uq_pos_pack_name**.
   - **Writers:** insert at `backoffice_pos_packs.go:315/323` and update at `:359/367`, both TrimSpace.
   - **Readers:** id-based.
   - **Status:** W-TRIM-OK.
6. **pos_product_categories.uq_pos_product_category_name**.
   - **Writers:**
     - UI at `backoffice_pos_split.go:47/57` (TrimSpace).
     - Carta import at `backoffice_pos_catalog.go:213/226`: raw `categoria`/`tipo` SELECTed from comida_items / group_menu_wines_v2. The **PG** text uses `rtrim(x,' ')` (collation1); the MySQL text is the source.
   - **Readers:** the link joins at `:262/:267`. The PG text uses `pc.name = rtrim(c.categoria,' ')`.
   - **Status:** handled for the existing writers.
   - **Residual:** a row whose stored name has a trailing space would still be distinct on PG. That needs IDX-RTRIM for full parity on imported data. Note the r5 import enforced ci_ai uniqueness, and the 05:43 counts were 0/0.
7. **pos_service_periods.uq_pos_service_period_name**.
   - **Writers:** `backoffice_pos_operations.go:63` (insert) and `:74/79` (update), both TrimSpace.
   - **Status:** W-TRIM-OK.
8. **pos_tags.uq_pos_tag_name** (varchar(80)).
   - **Writer:** `backoffice_pos_rail.go:759` (TrimSpace). I found no name update path; the grep shows only the insert and deletes/reads.
   - **Status:** W-TRIM-OK.
9. **restaurant_areas.uniq_restaurant_areas_restaurant_name** (varchar(128)).
   - **Create:** `backoffice_premium.go:2132` (TrimSpace) to `:2148–2166`.
   - **Update:** `updateBOPremiumArea` at `:2204`, `nameArg := nullableString(stringPtrOr(req.Name, ""))`. **This is NOT trimmed**, so the update at `:2223–2259` can store a trailing space.
   - **Status:** **GAP on update.**
   - **Remedy:** either IDX-RTRIM, or TRIM-CHANGE on update for consistency with create. The latter is **flagged**: it changes the stored value, since MySQL kept the raw value but treated it as PAD-equal.
10. **restaurant_domains.uniq_restaurant_domains_domain** `(domain)`. This is global (cross-tenant): the ownership key behind `domain_reservation.go` / `domainOwnedByOther`.
    - **Writers:**
      - `backoffice_premium.go:1142/1150` uses `normalizePremiumDomain` (`:4139`, ToLower+TrimSpace+scheme/path strip).
      - Registrar `site_builder_registrar.go:83` uses ToLower+TrimSpace, then `claimOwnedDomain`.
      - `instatic_manager.go:953/957` is the instatic subdomain (generated).
      - **Billing checkout `site_builder_billing.go:158–239` passes `req.Domain` RAW** (only a TrimSpace emptiness check at `:158`) to `claimOwnedDomain`, `reserveCheckoutAttempt`, the Stripe metadata and the UPDATEs.
    - **Impact on PG:** `"example.com "` (trailing space) is a different unique row from `"example.com"` owned by another tenant. On MySQL, PAD SPACE makes the claim conflict, which returns errDomainUnavailable. On PG the claim would **succeed as a new owner**, which is a **cross-tenant ownership bypass**. Case is not a problem because ci_ai is case-insensitive. Checkout is currently disabled with a 503 (no price authority), so this is not reachable now, but it is critical before re-enabling.
    - **Remedy (preferred, and source-consistent with the registrar and premium paths):** normalize `req.Domain` with the registrar rule at handler entry. **Flag:** this also drops leading spaces and lowercases. MySQL already compared case-insensitively, and leading or interior whitespace is not a valid DNS name, so the identity change is acceptable but must be stated. Also add IDX-RTRIM or a CHECK `domain = lower(btrim(domain))` in a forward migration (not applied).
11. **restaurant_salons.uniq_restaurant_salons_scope** `(restaurant_id,floor_id,name,scope_key)`.
    - **scope_key** is a GENERATED column: `COALESCE(date_iso(specific_date),…)`, computed and opaque, with no PAD risk.
    - **name:** `normalizeSalonWrite` (`backoffice_salons.go:154` TrimSpace) for insert `:315` and update `:401`.
    - **Other writers:** `backoffice_config.go:1780/1819` updates restaurant_salons. These are capacity/flag updates; **the name column was not verified there** → open item.
    - **Status:** W-TRIM-OK for salons.go; config.go needs re-checking.
12–14. **restaurant_tables:** `idx_restaurant_numero` and `uniq_restaurant_tables_restaurant_numero` (both `(restaurant_id,numero_mesa)`, duplicate indexes in the source), plus `uniq_restaurant_tables_restaurant_name` (name is NULL-able).
    - **Create:** `backoffice_premium.go:2339–2342` trims numero_mesa; the name is trimmed at `:2332`. The INSERTs are at `:2401–2435`.
    - **Update:** `:2560` `nameArg := nullableString(stringPtrOr(req.Name, ""))` is **untrimmed**. The update for numero_mesa (`validateTableNumero` `:1588` trims only for length) still needs checking.
    - **NULL name:** NULL is distinct in both engines' unique checks (MySQL InnoDB allows multiple NULLs, and PG does too), so NULL behaves the same.
    - **Status:** **GAP on update**, same remedy and flag as areas.
    - The derived next-number reader (`:2308`) parses numbers in Go, so it is not affected.
15. **restaurant_uazapi_instances.uniq_restaurant_uazapi_instances_name** `(instance_name)`. Global; this is a provider identifier.
    - **Writer:** `backoffice_uazapi_provisioning.go:613` generates `fmt.Sprintf("nv-%d-%d", restaurantID, UnixNano)`, or takes `prov.InstanceName` from the provider response (`:623`). Insert at `:643/656`.
    - **Status:** this is an **opaque exact provider value, not a logical name**. The generated form has no spaces. A provider-returned value is untrusted: a trailing space there would make a PG-distinct duplicate where MySQL would conflict. Cross-tenant isolation (isolation2) relies on this key.
    - **Remedy:** IDX-RTRIM, or reject a provider name whose `TrimRight(" ")` differs from the original (fail closed). Do **not** trim silently, because the provider looks instances up by the exact name.
    - **Index note:** the PG plan index name did not match the `ix__` pattern in my grep, so its existence needs confirming in plan r4 (open).
16. **site_builder_collections.uk_site_name** `(site_id,name)`.
    - **Go writers:** none found (sites=0). Writers are unknown, possibly a UI not in this repo or none at all.
    - **Status:** **UNKNOWN writers.** Gate it; do not assume it is safe.
17. **site_builder_component_registry.type** `(type)`.
    - **Writer:** seed migration `internal/db/migrations/034_site_builder_component_seed.sql` (constants). Readers look up by type.
    - **Status:** constants with no trailing spaces, so this is effectively code/opaque. No change needed beyond verifying that no runtime writer exists (none found in Go).
18. **site_builder_domain_mappings.uk_domain** `(domain)`. Global; a mapping is routing.
    - **Writers:**
      - `site_builder.go:1683` checks ownership with `ToLower(TrimSpace(input.Domain))`, **but inserts the RAW `input.Domain` at `:1696/1699`**.
      - `site_builder_registrar.go:287/291` and `instatic_manager.go:944` use normalized or generated values.
    - **Impact:** on PG, `"x.com "` passes the owner check and creates a second mapping row for the same host. On MySQL, `uk_domain` PAD SPACE would reject it.
    - **Remedy:** insert the same normalized value that was checked. This is TRIM-CHANGE and is **flagged**: the stored value differs from the source raw value, but the source treated them as one domain. Also add IDX-RTRIM / CHECK as a forward step. Critical, because mapping activation is gated (DNS proof absent).
19. **site_builder_pages.uk_site_slug** `(site_id,slug)`.
    - **Writers:** `site_builder.go:297/300` (default), `:554/557` (insert: `input.Slug` RAW, emptiness check only at `:536`), `:657/695` (update: `*input.Slug` RAW).
    - **Status:** **GAP.**
    - **Remedy:** IDX-RTRIM, plus rtrim-aware slug lookups for public routing. TRIM-CHANGE is **flagged**: a slug with a trailing space would route differently.
20. **site_builder_sites.uk_custom_domain** `(custom_domain)`, NULL-able.
    - **Writers:** the dynamic UPDATE at `site_builder.go:410`. Whether `custom_domain` is among the updated fields, and whether it is normalized, was not verified in this pass → **open**.
    - **NULL:** multiple NULLs are allowed on both engines.
21. **site_builder_sites.uk_subdomain** `(subdomain)`. Global; this is routing.
    - **Writer:** `site_builder.go:277/280`, `input.Subdomain` RAW (emptiness check only at `:268`). Update via `:410` is unverified.
    - **Status:** **GAP**, cross-tenant routing on PG (`"abc "` ≠ `"abc"`).
    - **Remedy:** normalize as a DNS label (lower + trim; leading spaces invalid) — **flagged** TRIM-CHANGE — plus IDX-RTRIM / CHECK.
22. **site_builder_versions.uk_site_version** `(site_id,version_number)`.
    - **site_id** is a UUID generated in Go (opaque); version_number is an integer.
    - **Status:** opaque exact, no PAD risk. No change needed.
23. **stock_categories.uq_stock_category_name**.
    - **Writers:** `backoffice_stock.go:440` (insert, TrimSpace), `:468` (update, TrimSpace), import `backoffice_stock_import.go:88` (TrimSpace) to `:384` (upsert).
    - **Status:** W-TRIM-OK.
24. **stock_margin_scope_bands.uq_stock_margin_scope_band** `(restaurant_id,scope_id,zone)`.
    - **zone** is MySQL ENUM, PG enum type.
    - **Writer:** `backoffice_stock_margin_scopes.go:235` (code constants).
    - **Status:** an enum, not a collation key. PG enum input is case-sensitive, while MySQL ENUM is case-insensitive on input. Writers use constants, so no PAD or case risk; this is outside the padding scope. Recorded.
25. **stock_supplier_aliases.uq_stock_supplier_alias** `(restaurant_id,supplier_name,supplier_code,normalized_description)`.
    - **Writers:**
      - `backoffice_stock_suppliers.go:278`: the code is TrimSpace, the description goes through `stockAliasDescription` (`backoffice_stock_documents.go:71`, lower+TrimSpace+255-byte cut), and the supplier_name comes from the supplier row (trimmed at creation, `:81/:123`).
      - `backoffice_stock_documents.go:686`: `supplier` comes from the scan's `supplier_name`. That value is set by the OCR/LLM extraction and by the review UPDATE at `:400`; **trimming was not verified**.
    - **Readers:** `:161` matches `supplier_name = ?` from the extraction.
    - **Status:** **partial GAP** (supplier_name from OCR/review).
    - **Remedy:** R-PROBE + IDX-RTRIM, or trim the review input — **flagged** if that alters supplier identity.
    - **Separate issue:** the 255-byte cut in `stockAliasDescription` can split UTF-8. That is not part of PAD.
26. **stock_suppliers.uq_stock_suppliers**.
    - **Writers:** `backoffice_stock_suppliers.go:81/95` and `:123/137`, both TrimSpace.
    - **Status:** W-TRIM-OK. Readers resolve aliases by this name (see 25).
27. **stock_vat_rates.uq_stock_vat_name**.
    - **Writers:** `backoffice_stock_analytics.go:468` (insert, TrimSpace) and `:508` (update, TrimSpace in args).
    - **Status:** W-TRIM-OK.
28. **uazapi_servers.uniq_uazapi_servers_base_url** `(base_url)`. Global; a provider endpoint.
    - **Writer:** `normalizeBOUAZAPIServerBaseURL` (`backoffice_uazapi_servers_admin.go:562`: TrimSpace, TrimRight "/", parsed and re-serialized) for insert `:219/224`. The update at `:426` is dynamic; it uses the same normalizer when baseUrl is given (unverified).
    - **Status:** an opaque URL, normalized. W-TRIM-OK for insert; the update is open.

## Summary: 28 disjoint primary statuses (revised in repair-padding1)

The audit-1 version gave a bucket total of 35 and mixed open issues with keys; that table is wrong and is replaced by this one.

"Code-canonical" means only that every **known Go writer** trims before writing. It does **not** settle:
- legacy or imported stored rows (checked only by the 05:43 read-only counts, and only for 6 tables);
- external or non-Go writers (repo-wide search below);
- query-probe semantics for lookups by user input.

No key below is claimed to have **full MySQL PAD SPACE parity**.

| # | Key | Primary status |
|---|---|---|
| 1 | assistant_memory_nodes.uq_memory_scope_label | code-canonical (lower+TrimSpace, same value probed) |
| 2 | pos_kitchen_stations.uq_pos_kitchen_station_name | code-canonical; id-only readers |
| 3 | pos_modifier_groups.uq_pos_modifier_group_name | code-canonical |
| 4 | pos_modifier_options.uq_pos_modifier_option_name | code-canonical |
| 5 | pos_packs.uq_pos_pack_name | code-canonical |
| 6 | pos_product_categories.uq_pos_product_category_name | PG text rtrim on import/join (collation1); residual: stored rows |
| 7 | pos_service_periods.uq_pos_service_period_name | code-canonical |
| 8 | pos_tags.uq_pos_tag_name | code-canonical |
| 9 | restaurant_areas.uniq_restaurant_areas_restaurant_name | **GAP** (update path untrimmed) |
| 10 | restaurant_domains.uniq_restaurant_domains_domain | **FIXED in code** (canonical identity check + store) |
| 11 | restaurant_salons.uniq_restaurant_salons_scope | code-canonical (config.go updates only capacity/is_active, verified) |
| 12 | restaurant_tables.idx_restaurant_numero | code-canonical (create and update TrimSpace, verified `backoffice_premium.go:2340/2545`) |
| 13 | restaurant_tables.uniq_restaurant_tables_restaurant_name | **GAP** (update path untrimmed) |
| 14 | restaurant_tables.uniq_restaurant_tables_restaurant_numero | code-canonical (same as 12; duplicate index of the same columns) |
| 15 | restaurant_uazapi_instances.uniq_restaurant_uazapi_instances_name | **FIXED in code** (provider name with a trailing ASCII space is refused) |
| 16 | site_builder_collections.uk_site_name | no runtime writer in the repo (only migration 033 DDL); stored and external rows unknown |
| 17 | site_builder_component_registry.type | seed constants (migration 034); no runtime writer |
| 18 | site_builder_domain_mappings.uk_domain | **FIXED in code** (same canonical value checked and stored) |
| 19 | site_builder_pages.uk_site_slug | **GAP** (raw slug on create and update) |
| 20 | site_builder_sites.uk_custom_domain | **FIXED in code** (non-empty custom_domain uses canonical identity) |
| 21 | site_builder_sites.uk_subdomain | **FIXED in code** (edge ASCII spaces refused, not trimmed) |
| 22 | site_builder_versions.uk_site_version | opaque (Go UUID site_id + integer) |
| 23 | stock_categories.uq_stock_category_name | code-canonical (UI and import) |
| 24 | stock_margin_scope_bands.uq_stock_margin_scope_band | enum, not a collation key |
| 25 | stock_supplier_aliases.uq_stock_supplier_alias | **GAP** (supplier_name from OCR/review not verified trimmed) |
| 26 | stock_suppliers.uq_stock_suppliers | code-canonical |
| 27 | stock_vat_rates.uq_stock_vat_name | code-canonical |
| 28 | uazapi_servers.uniq_uazapi_servers_base_url | code-canonical (create and update both use `normalizeBOUAZAPIServerBaseURL`, verified `:136/:345`) |

Totals: code-canonical 15; FIXED in code 5; **GAP 4**; no runtime writer 2; opaque 1; enum 1. That adds up to **28**.

### Sub-issues (separate from the key statuses)

- **Imported unique index names (plan r4, read from `plan.sql`):**
  - `ix__restaurant_uazapi_instances__uniq_restaurant_uazap_496278c3` on `(instance_name)`. The name is hashed, which is why the earlier `ix__table__name` grep missed it. `IsUniqueViolationOn` already names it.
  - Every other key keeps `ix__<table>__<mysql_index>`.
- **Collections writers (repo-wide, not only Go):** only `internal/db/migrations/033_site_builder_tables.sql` (DDL) and prebuilt binaries (`bin/`, `server_new`, `tmp/main`, `newvillacarmen-backend`) match.
- **Webhook replay:** Stripe sessions created before this fix carry the raw metadata domain. The completed handler's `FOR UPDATE … WHERE domain = ?` then finds no PG row and goes to review. That is fail-closed, with no automatic action.
- **custom_domain ownership:** `site_builder_sites.custom_domain` is a global unique key but is **not** checked against `restaurant_domains` ownership. This is an existing gate, not changed here.
- **UAZAPI fail-closed path:** a refused provider name leaves an already-created provider instance orphaned. This is the existing provisioning gate, with no cleanup call.


## Remaining unaudited

There are 93 opaque/code/contact keys left. They should be done in chunks of about 30.

