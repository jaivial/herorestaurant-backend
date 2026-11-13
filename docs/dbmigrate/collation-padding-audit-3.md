# collation-padding-audit-3: next 30 text unique keys (remaining code keys, then opaque keys)

This is a docs-only audit. No code changed in this batch, so no build was needed. No SQL was executed and no values were dumped.

**Selection:** from `collation-unique-keys-r93.tsv`, the keys not covered by audits 1 or 2, with code keys first and opaque keys second, taking the first 30. That is 10 code keys and 20 opaque keys. **33 keys remain unaudited.**

## Shared facts

- All columns are `utf8mb4_unicode_ci` (PAD SPACE), with two exceptions:
  - `admin_sessions.session_id`: `utf8mb4_general_ci`, also PAD SPACE.
  - `bot_inbound_queue.message_id`: `utf8mb4_0900_ai_ci`, which is **NO PAD**. The PG ci_ai behaviour already matches it on trailing spaces.
- PG plan r4 uses `COLLATE dbmigrate.ci_ai` for every column, except the ENUMs, which map to PG enum types.
- **Status meanings:**
  - "generated": the value is built in code with no spaces. Evidence is given per row.
  - "code-canonical": every known Go writer trims.
  - **Legacy and imported rows are not settled** by either status. No key here gets full parity.

## Rows and statuses (30, disjoint)

| # | Key | Type / nullability | Status | Evidence |
|---|---|---|---|---|
| 1 | restaurant_booking_extras.uq_…_slug | varchar(140) NOT NULL | generated | `beverageOptionSlug(name)` at `booking_extras.go` around :65; ON CONFLICT (restaurant_id, slug) on the PG text; the bot catalog `whatsapp_bot_catalog_extras.go:203` uses the same slug rule; lookups by slug use the same value |
| 2 | restaurants.uniq_restaurants_slug (global) | varchar(64) NOT NULL | code-canonical | create at `backoffice_platform.go` (around :190) uses lower+TrimSpace; the dynamic update at `:296` set list has no slug (source read) |
| 3 | seo_item_routes.uniq_seo_item_route (entity_type) | varchar(16) | generated constant | `seo_item_routes.go:701/725`; route upsert tx (seo_route_upsert_tx_v1) |
| 4 | seo_item_routes.uniq_seo_item_slug (entity_type, slug) | varchar(160) | generated | `slugifySeoItem` (`seo_item_routes.go:172/204`, Trim "-"), including the manual edit at `:941` (`slugifySeoItem(*body.Slug)`) with pre-check `:945`; the PG slug conflict is handled with a savepoint |
| 5 | stock_affluence_daily.uq_stock_affluence_day (service_type) | ENUM | enum | `backoffice_stock_analytics.go:221`, `backoffice_pos_checkout.go:65` |
| 6 | stock_items.uq_stock_item_sku | varchar(64) NULL | code-canonical (`stockNullableString` TrimSpace) | `backoffice_stock.go:693/744`; import `backoffice_stock_import.go:88/396`; technical-sheet inserts (`backoffice_technical_sheet_*.go`) do **not** set sku (NULL); `cmd/comida-stock-backfill` generated sku |
| 7 | stock_margin_scopes (scope_kind, scope_key) | ENUM + varchar(64) | scope_kind is an enum; scope_key is **validated, not trimmed** | `backoffice_stock_margin_scopes.go:172` length check plus `validateMarginScopeKey` against existing category keys (exact); lookup `:200` exact; a padded key that matches no existing category is rejected by validation (fail closed). **Open:** if validation compares through the ci_ai DB lookup, a padded key could fail on PG but pass on MySQL. |
| 8 | website_components.uk_component_type | varchar(50) | no runtime Go writer (seed) | — |
| 9 | website_pages.uk_website_slug (website_id, slug) | varchar(200) NOT NULL | code-canonical | `website_builder_handlers.go` create (around :433, TrimSpace), update (around :499, TrimSpace), default ''. **Note:** '' is a valid slug (homepage) |
| 10 | website_templates.slug | varchar(100) | no runtime Go writer | — |
| 11 | POSTRES.uq_postres_restaurant_ui_data | varchar(64) NULL | opaque, client- or code-supplied, **source not traced** | writers `admin_postres.go:202`, `backoffice_postres.go:133`, `comida.go:1670` (ui_data_id not in the insert lists shown, so presumably NULL); open |
| 12 | admin_sessions.uniq_…_session_id | char(64) general_ci | no Go writer (legacy table) | — |
| 13 | analytics_customers.uq_…_identity | varchar(255) | generated identity key (`internal/analytics/service.go:663/673`; ON CONFLICT (restaurant_id, identity_key) `:675`; lookup `:687`); builder **not traced** in this pass | open |
| 14 | assistant_sessions.uq_assistant_sessions_token | varchar(64) NULL | opaque **client** token, TrimSpace'd by the source (`assistant_ws.go:284`) before the lookup `:289` and insert `:295` | already canonical by source (existing behaviour, not changed) |
| 15 | bo_member_invitation_tokens.onboarding_guid | char(36) NULL | generated UUID (`backoffice_member_access_flows.go:635`) | — |
| 16 | bo_member_invitation_tokens.token_sha256 | char(64) | generated sha256 hex | — |
| 17 | bo_password_reset_tokens.token_sha256 | char(64) | generated sha256 hex (`:673`) | — |
| 18 | bo_sessions.token_sha256 | char(64) | generated sha256 hex (`backoffice_auth.go:180`, `mcp_oauth_store.go:185`, `whatsapp_bot_service_session.go:105`); 05:43: 219 rows, all lowercase | — |
| 19 | booking_checkouts.uq_…_public | varchar(64) | generated (`newCheckoutPublicID`, `booking_checkout.go:188`) | — |
| 20 | booking_reminder_deliveries.uq_booking_reminder_key | varchar(128) | generated (`bookingReminderDeliveryKey`; width guard from ignore3) | — |
| 21 | bot_inbound_queue.uq_inbound_msg (message_id) | varchar(120), **0900_ai_ci NO PAD** | provider message id (opaque exact); **no PAD gap**, because the source collation is NO PAD | width guard (ignore3) |
| 22 | campaign_bounce_events.uq_bounce_event | varchar(190) | generated (`campaignBounceKey`) | — |
| 23 | campaigns.uq_campaign_coord | varchar(64) | generated (`"camp-"+uuid`, `backoffice_campaigns.go:422`) | — |
| 24 | chatgpt_plugin_tokens.uq_…_hash | char(64) | generated sha256 hex (`chatgpt_plugin_tokens.go:75`) | — |
| 25 | comida_bulk_link_batches.uq_comida_bulk_link_idem | varchar(120) | **client idempotency key**, TrimSpace'd by the source (`backoffice_technical_sheet_bulk.go` around :131) before the lookup and insert `:191` | already canonical by source |
| 26 | comida_items.uq_comida_items_restaurant_ui_data | varchar(64) NULL | opaque; the insert `comida.go:1607` with a client-supplied `ui_data_id` (`comida.go:175` JSON field) was **not traced for trim** | open; **do not trim** (UI correlation id); propose IDX-RTRIM or refuse edge spaces |
| 27 | discount_tokens.idx_token | varchar(64) | no Go writer | — |
| 28 | forky_confirmation_tokens.uq_…_hash | char(64) | generated sha256 (`assistant_confirmation.go:50`) | — |
| 29 | group_menu_section_dishes_v2 ui_data_id | varchar(64) NULL | opaque UI id; insert `backoffice_group_menus_v2.go:404/409`; **source not traced** | open (same as 26) |
| 30 | invoice_wa_sends.uq_invoice_wa_sends_key | varchar(80) | **client idempotency key**, TrimSpace'd by the source (`backoffice_invoices.go:1069`), used for lookup and insert | already canonical by source |

**Totals (disjoint, 30):**

| Status | Count | Rows |
|---|---|---|
| generated | 14 | 1, 3, 4, 13 (builder not traced), 15, 16, 17, 18, 19, 20, 22, 23, 24, 28 |
| code-canonical | 3 | 2, 6, 9 |
| enum / enum + validated | 2 | 5, 7 |
| no runtime Go writer | 4 | 8, 10, 12, 27 |
| source-trimmed client opaque | 3 | 14, 25, 30 |
| NO PAD source (no gap) | 1 | 21 |
| opaque, source not traced | 3 | 11, 26, 29 |

**Total 30.**

## Audit-2 open items, closed by source

| Item | Finding |
|---|---|
| bo_users dynamic update | `backoffice_platform.go:561–578` sets only name, is_superadmin and must_change_password. **No email or username** is written there, so row 1 of audit-2 is code-canonical. |
| restaurant_members insert | `backoffice_member_access_flows.go`: `normalizedEmail := ToLower(TrimSpace(in.Email))` (`:350`) feeds the insert (around :183). Code-canonical. |
| comida_categories slug | `validateComidaCategoryName` returns `slugifyCategoryName(name)` (`backoffice_comida_categories.go:159`, only [a-z0-9-]). Insert `:446` and update `:736` use it, and `comidaCategorySlugTaken` pre-checks the same value. Generated. |
| menus.seo_slug | `normalizeMenuSEOSlug` (`menu_public_seo.go:73`) applies lower+TrimSpace and the pattern `menuSEOSlugPattern` ("sin espacios") before the update `menu_public_seo_handlers.go:98`. Code-canonical. |
| restaurant_beverage_options slug | `beverageOptionSlug(name)` (`beverage_options.go` around :135), ON CONFLICT (restaurant_id, slug); the lookup uses the same value. Generated. |

## Comprehensive source-only plan: PAD SPACE in the import planner (not implemented)

1. **Classification input.** For each MySQL unique or PK index, take each text column with its collation.
   - PAD SPACE collations: utf8mb4_unicode_ci, utf8mb4_general_ci, utf8_*_ci.
   - NO PAD collations: utf8mb4_0900_*, which already match PG on trailing spaces.
   - ENUM columns become PG enums with exact labels; padding does not apply.
   - BINARY/_bin collations map to "C". The MySQL _bin collations are also PAD SPACE, and that must be recorded.
2. **Planner output, per PAD SPACE text unique key.** The planner keeps the raw column and the raw values, and emits:
   `CREATE UNIQUE INDEX … ON t (<non-text cols>, (rtrim(<col>,' ') COLLATE dbmigrate.ci_ai), …)`
   - The plain index is kept only while code still names it in ON CONFLICT or in `IsUniqueViolationOn`.
   - A read-only pre-check reports PAD-equal collisions in the imported data. A collision is a manual decision; the planner never merges rows automatically.
3. **Go catalogue that must change together with the indexes** (a generated list, same coverage as the inventory):
   - **ON CONFLICT targets** must name the expression. Known: domains (`domain_reservation.go:58`, `backoffice_premium.go:1146/1154`, `site_builder_registrar.go:305`, `instatic_manager.go:950`), `analytics/service.go:675/699`, beverage and booking extras, `translations.go:269`, `backfill-translations`, stock supplier aliases, `pos_catalog` categories, `campaign_*` and every other `ON CONFLICT (…)` on a PAD key.
   - **`IsUniqueViolationOn` constraint names** must list the new index name.
   - **Equality lookups on user or provider input** must use `rtrim(col,' ') = rtrim(?,' ')`. Generated keys need no change.
4. **What it does not cover.** ORDER BY, GROUP BY and DISTINCT, LIKE, joins on raw columns, unicode_ci vs general_ci character equivalences (general_ci compares per character, so ß ≠ ss there; ICU makes them equal), and `ignorable` characters. Uniqueness plus the listed lookups are **not** full MySQL equivalence.
5. **Activation dependency.** This plan stays unapplied. Every PG activation gate remains.

## Critical items still unapplied

- **Padded global owner lookups:**
  - `restaurantDomainOwner`, used by the custom_domain guard added in audit-2 and still exact-match;
  - the mapping, subdomain and instance_name lookups.

  New canonical writers do not stop a PG claim of an identity whose imported owner row is padded. Domain ownership on PG stays **gated**.
- **The four human GAP keys:** proposals only.

## Remaining unaudited

**33 keys** remain. They are the rest of the opaque class, mainly the POS idempotency keys, which are client-supplied (as in rows 25 and 30) and **must be checked one by one**. Open items from this batch: rows 7 (validation comparison), 11, 13 (builder), 26 and 29.

