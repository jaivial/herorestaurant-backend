# collation-padding-audit-2: next 30 text unique keys (contact, then code, then opaque)

This is a docs audit. I executed no SQL and dumped no values. Selection rule: from `collation-unique-keys-r93.tsv`, take the rows not among the 28 human/unknown keys already audited, ordered contact first, then code, then opaque, and keep the first 30. Rows 1–8 are contact and rows 9–30 are code; no pure-opaque row made it into this batch.

## Shared facts

- Every column below is `utf8mb4_unicode_ci` (PAD SPACE). The one exception is `no_marketing.contact_phone`, which is `utf8mb4_general_ci`, also PAD SPACE.
- In PG plan r4 every column is `COLLATE dbmigrate.ci_ai`, unless it is marked as an enum.
- Each key has a plain unique index on the same columns. Hashed index names are noted where present.
- In PG, ICU ci_ai does **not** pad: trailing ASCII spaces make values different (measured 05:43).
- "Code-canonical" means only that the **known Go writers** trim. It says nothing about legacy or imported rows, external writers, or query probes, so no key here is marked as having full parity.
- "Generated" means the value is built in code from constants or ids (format evidence given), so a trailing space cannot come from this code.

## Rows and statuses (30, disjoint)

### Contact keys

| # | Key | Status | Evidence | Remedy if parity is required |
|---|---|---|---|---|
| 1 | bo_users.uniq_bo_users_email (email NOT NULL) | **code-canonical, partial** | `backoffice_platform.go:477` lower+TrimSpace; `backoffice_member_access_flows.go` normalizedEmail (`:361/:410`); `backoffice_roles.go:662` TrimSpace (**not lower**: ci_ai is case-insensitive, so this is OK); bootstrap `backoffice_auth.go:521`. Login probe: `backoffice_auth.go:59` lower+TrimSpace. Dynamic update at `backoffice_platform.go:583`: **whether email is in the set list was not verified.** | IDX-RTRIM on (rtrim(email)), and login probe `rtrim(email,' ') = ?`. Do not trim stored values. |
| 2 | bo_users.uniq_bo_users_username (NULL-able) | code-canonical (generated/resolved usernames at `backoffice_member_access_flows.go:380/457`; base from `usernameBaseFromIdentity`) | NULLs are distinct on both engines | IDX-RTRIM for legacy rows |
| 3 | campaign_recipients.uq_campaign_target (campaign_id, channel, target) | code-canonical | target goes through `campaignSuppressionKey` (ignore1/2) at `backoffice_campaigns.go:1290`; channel is a constant | IDX-RTRIM with ON CONFLICT moved (`backoffice_campaigns.go:1294` PG text) |
| 4 | campaign_suppressions.uq_suppression | code-canonical, plus a DB-collation probe (suppression2) | writers at `campaign_bounce_sync.go:292/298/312`, `campaign_email_validation.go:117`, `public_unsubscribe.go:175`, `whatsapp_bot_unsubscribe.go:117`, `whatsapp_unreachable.go:76`, all canonical targets. 05:43: 0/1007 trailing. | IDX-RTRIM; the collation probe would become `rtrim(s.target,' ') = p.probe` |
| 5 | no_marketing.idx_no_marketing_phone (general_ci) | **no Go writer** (legacy table; read in `campaignSuppressionQuery`) | legacy and imported rows only | reader already trims (`TRIM(contact_phone)` in the union); no index action needed unless a writer appears |
| 6 | pos_customers.uq_pos_customers_email (NULL-able) | code-canonical | `backoffice_pos_customers.go:52` lower+TrimSpace; insert `:159`, update `:186`; duplicate probe `:85` uses the same value | IDX-RTRIM; probe `rtrim(email,' ') = ?` |
| 7 | pos_customers.uq_pos_customers_phone (NULL-able) | code-canonical (`posCustomerPhone` normalizer `:51`) | same paths | IDX-RTRIM |
| 8 | restaurant_members.uniq_restaurant_members_restaurant_email (NULL-able; hashed index `…restau_c9377ba9`) | code-canonical, partial | `backoffice_members.go:271` lower+TrimSpace for update `:319`; insert `backoffice_member_access_flows.go:183` takes normalizedEmail (**source not re-read in this pass**) | IDX-RTRIM; verify the insert path |

### Code keys

| # | Key | Status | Evidence |
|---|---|---|---|
| 9 | accounting_exports.uq_accounting_export_hash | export_type is an **enum** (not a collation key); payload_hash is **generated** (sha256 hex, `backoffice_accounting_exports.go:341`) | none |
| 10 | ai_image_models (provider_slug, slug) | **no runtime Go writer**; seed migration `052_ai_image_provider_config.sql` | legacy/seed rows only |
| 11 | ai_image_providers.slug | **no runtime Go writer**; seed migration 052 | same as 10 |
| 12 | analytics_customer_sources (source_type, source_ref) | generated (`internal/analytics/service.go:693/697`; ON CONFLICT on the plain key `:699`) | IDX-RTRIM would need the `:699` target moved |
| 13 | analytics_sales_documents source_type | enum (plus integer source_id; hashed index `…docu_eadc31f1`) | none |
| 14 | analytics_stock_facts source_type | generated constant (`service.go:951/955`) | none |
| 15 | app_settings.k | **no Go writer** (seed migration `081`); reader uses constant `k='app_base_url'` (`instatic_manager.go:393`) | none |
| 16 | assistant_memory_edges.uq_edge rel | code-canonical (`assistant_memory.go:103` lower+TrimSpace) | none |
| 17 | comida_bebida_categories slug (hashed index `…categ_0e60fb4c`) | **partial**: slugify output at `comida.go:322` is `strings.Trim(b.String(),"-")` (no spaces); user slug `comida.go:2366` TrimSpace; lookups `:2458/:2493` compare `slug = ? OR LOWER(name) = LOWER(?)` | probe on name is PAD-sensitive; IDX-RTRIM / rtrim probe |
| 18 | comida_categories (food_type, slug) (hashed index `…restaura_758afe88`) | **not verified**: insert `backoffice_comida_categories.go:446`, update `:736`; slug source not traced in this pass | open |
| 19 | comida_plato_categories slug (hashed index `…categor_0c3c7a89`) | same as 17 (`comida.go:401/425/667`) | same as 17 |
| 20 | dish_translations (entity_type, field_name, lang) | generated (`translations.go:263/269`, `cmd/backfill-translations` constants and field names) | ON CONFLICT targets at `translations.go:269` and `backfill-translations:387` |
| 21 | legal_pages slug | allow-list (`isValidLegalSlug`, `legal_pages.go:20`) | none |
| 22 | menu_weekday_availability weekday | canonical English keys (`menu_weekday_availability.go:16–28`) | none |
| 23 | menus.idx_menus_restaurant_seo_slug (NULL-able) | writer `menu_public_seo_handlers.go:98` via `nullIfEmptyMenuSEOSlug(slug)`; **slug normalizer not traced** | open; public SEO route lookups (`seo_item_routes.go:265/291`) |
| 24 | pos_fiscal_documents full_number | generated (`prefix-date-%04d`, `backoffice_pos_fiscal.go`) | the prefix is a constant (FS/FR) |
| 25 | pos_fiscal_series (terminal_key, series_type) | terminal_key: TrimRight(" ") for the scope (ignore2); series_type: enum | none |
| 26 | pos_payments (provider, provider_reference) (NULL-able) | `stockNullableString` TrimSpace (`backoffice_pos_checkout.go:356`, `backoffice_stock.go:1866`) | **opaque provider reference is TrimSpace'd** by the source code itself (existing behaviour; not changed) |
| 27 | pos_products.uq_pos_product_sku (NULL-able) | `stockNullableString` (`backoffice_pos_catalog.go:95/104`) | IDX-RTRIM for imported rows |
| 28 | pos_products.uq_pos_product_source source_type | enum | none |
| 29 | pos_tickets.uq_pos_ticket_number | generated (`nextPOSTicketNumber` `backoffice_pos.go:701`, prefix = receipt_prefix trimmed at settings save) | the number lookup uses LIKE (ported) |
| 30 | restaurant_beverage_options slug (hashed index `…beverag_f70fbea9`) | **not verified** (`beverage_options.go:140/165`) | open |

**Totals (disjoint):**

| Status | Count | Rows |
|---|---|---|
| code-canonical | 9 | 2, 3, 4, 6, 7, 16, 21, 22, 25 |
| code-canonical partial (one path unverified) | 2 | 1, 8 |
| generated | 6 | 9, 12, 14, 20, 24, 29 |
| enum | 2 | 13, 28 (row 9's export_type enum is counted with row 9 under generated) |
| no runtime Go writer | 4 | 5, 10, 11, 15 |
| partial (PAD-sensitive probe) | 2 | 17, 19 |
| not verified | 3 | 18, 23, 30 |
| existing-trim opaque | 1 | 26 |
| canonical via stockNullableString | 1 | 27 |

Total: **30** keys, each with exactly one status.

## Four remaining human GAP keys (from audit-1), values retained

These are proposals only (not applied), and nothing here normalizes values:

| Key | Index | Callers to change with it |
|---|---|---|
| restaurant_areas | `(restaurant_id, (rtrim(name,' ') COLLATE dbmigrate.ci_ai))` | update `backoffice_premium.go:2204–2259`. Pre-check uniqueness by `rtrim(name,' ') = rtrim(?,' ')` before writing, and map 23505 on the new index to the existing conflict error. |
| restaurant_tables name (NULL-able) | `(restaurant_id, (rtrim(name,' ') COLLATE dbmigrate.ci_ai))` | update `:2560`. Same pre-check. NULL stays distinct. |
| site_builder_pages | `(site_id, (rtrim(slug,' ') COLLATE dbmigrate.ci_ai))` | insert `site_builder.go:554` and update `:657/695`. Public page resolution by slug must compare `rtrim(slug,' ') = rtrim(?,' ')`. |
| stock_supplier_aliases | `(restaurant_id, rtrim(supplier_name,' '), rtrim(supplier_code,' '), rtrim(normalized_description,' '))` each with explicit `COLLATE dbmigrate.ci_ai` | ON CONFLICT targets at `backoffice_stock_suppliers.go:300` and `backoffice_stock_documents.go:686` must use the identical expression list. Lookup `:161` uses rtrim on both sides. |

## Legacy padded rows vs the global ownership checks

- **The lookup misses padded rows.** `restaurantDomainOwner` (`domain_ownership.go:33`) runs `WHERE domain = ?`. With canonical input on PG it will **not** match an imported row stored as `"x.com "`, so a new claim inserts a second row and becomes an owner. MySQL PAD SPACE would have matched.
- **Canonical writers alone do not close this.** The same applies to:
  - `site_builder_domain_mappings.domain` (the mapping create owner check);
  - `site_builder_sites.subdomain`/`custom_domain` (unique checks rely only on the plain index);
  - `restaurant_uazapi_instances.instance_name` (exact lookup `backoffice_uazapi_provisioning.go:679`).
- **Evidence:** only the 05:43 counts exist, and they do not include restaurant_domains or the site tables.
- **Proposal (source only, not applied):**
  1. **Read-only pre-check:** `SELECT count(*) FROM <t> WHERE <col> <> rtrim(<col>,' ')` for restaurant_domains.domain, site_builder_domain_mappings.domain, site_builder_sites.subdomain/custom_domain, and restaurant_uazapi_instances.instance_name.
  2. **Global unique expression index:** `(rtrim(col,' ') COLLATE dbmigrate.ci_ai)` on each. Any existing PAD-equal pair is a manual merge decision, never automatic.
  3. **Lookups:** `restaurantDomainOwner` becomes `WHERE rtrim(domain,' ') = ?` (input is already canonical). The mapping and uazapi lookups change the same way.
  4. **ON CONFLICT targets:** `domain_reservation.go:58`, `backoffice_premium.go:1146/1154`, `site_builder_registrar.go:305` and `instatic_manager.go:950` move to the expression.
  5. **Until then:** PG activation of domain ownership stays gated. Neither the code nor the plan proves that legacy rows are free of padding.

## Remaining unaudited

**63** keys remain (121 − 28 − 30): the rest of the code class and all opaque keys. Open items from this batch: rows 1 (dynamic email update), 8 (insert path), 18, 23 and 30.

