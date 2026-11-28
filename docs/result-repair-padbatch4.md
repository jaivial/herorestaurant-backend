# Result: repair-padbatch4 (final 24 PAD unique keys; code-only, nothing applied; NOT functional completion)

## Selection and final sets
- The 24 keys are pad-key-sets-padbatch3.txt REMAINING_24, from restaurant_floors through website_templates.
- 92 + 24 = 116 patched and 0 non-PK remaining, re-derived from the padplan against RequiredPadKeyIndexes. The final list is in docs/dbmigrate/pad-key-sets-padbatch4.txt.
- This closes source caller coverage for the non-PK PAD unique keys only. General collation parity, the PK/FK design and runtime QA remain open.

## Keys and callers (PG text only; MySQL texts unchanged; no new Go identity bug found)
| key (parts) | callers / treatment |
|---|---|
| restaurant_floors (rid, floor_number, scope_key GENERATED) | backoffice_config.go:514, :608 ON CONFLICT on the expression over the generated column PATCHED; generated-expression parity stays gated |
| restaurant_salons (rid, floor_id, name, scope_key GENERATED) | reads/writes by id only (salons.go:390/402/440/471, booking_insert.go:1017); future index only |
| restaurant_stripe_connect.account_hash char (HMAC) | booking_checkout.go:629 and stripe_connect.go:144 owner lookups PATCHED; stripe_connect.go:137 IsUniqueViolationOn adds ixpad__…_261cbce4; ON CONFLICT (restaurant_id) unchanged |
| restaurants.slug | instatic_proxy.go:161 host→tenant lookup PATCHED; other restaurants reads are by id |
| seo_item_routes (rid, entity_type, entity_id) / (rid, entity_type, slug) | seo_item_routes.go:727 ON CONFLICT (expression), :739 (pgOnlySQL fallback UPDATE) and :945 slug check PATCHED; :734 IsUniqueViolationOn adds ixpad__seo_item_routes__uniq_seo_item_slug; :657 UPDATE SET by id raw |
| site_builder_collections (site_id, name) | no Go writer (legacy); future index only |
| site_builder_component_registry.type | site_builder.go:1252 (PG twin of :1248) PATCHED; seed-only writer |
| site_builder_versions (site_id, version_number) | site_builder.go:1018 list, :1067 and :1499 MAX(version) PATCHED |
| stock_categories (rid, name) | stock_import.go:384 ON CONFLICT and :390 lookup PATCHED; UPDATE stock.go:468 by id |
| stock_document_scans (rid, file_hash char NULL) | stock_documents.go:240 and stock_analytics.go:926 duplicate lookups PATCHED |
| stock_items (rid, sku NULL) | stock_import.go:350, technical_sheet_link.go:86, cmd/comida-stock-backfill:225 PATCHED; stock.go:611 LIKE search unchanged |
| stock_margin_scopes (rid, scope_kind enum raw, scope_key) | stock_margin_scopes.go:202 and comida_categories.go:943 PATCHED |
| stock_recipe_step_image_jobs (rid, idempotency_key) | technical_sheet_images.go:188 replay lookup PATCHED |
| stock_suppliers (rid, name) | stock_suppliers.go:87 and :129 duplicate checks PATCHED |
| stock_vat_rates (rid, name), stock_warehouses (rid, code NULL) | UPDATE SET by id only; no name/code lookup (stock.go:942 joins warehouses by id); future index only |
| stripe_events.event_id (provider id, not trimmed) | stripe_event_claim.go:65 ON CONFLICT, :71 read and :87 processed-mark PATCHED. Same pinned connection, advisory lock and claim/progress semantics (text only); nothing executed |
| uazapi_servers.base_url | admin SET only (uazapi_servers_admin.go:350); no lookup; future index only |
| user_preferences / user_menu_preferences (…, pref_key) | backoffice_preferences.go:161 and :200 ON CONFLICT, :175 and :212 reads PATCHED |
| website_components.component_type / website_pages (website_id, slug) / website_templates.slug | reads by id/website_id only (website_builder_handlers.go); future index only |

- **Coverage method:** the table-literal scanner plus broad column greps outside table literals (domain/URL/slug/base_url/event_id) and greps for unique-index names.
- **Provider and Stripe ids are never trimmed:** stored and bound values stay raw.

## Future DDL (NOT executed; from the DERIVED local metadata; additive; original R5 untouched)
```
CREATE UNIQUE INDEX "ixpad__restaurant_floors__uniq_restaurant_floors_scope" ON "restaurant_floors" ("restaurant_id", "floor_number", (rtrim("scope_key", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__restaurant_salons__uniq_restaurant_salons_scope" ON "restaurant_salons" ("restaurant_id", "floor_id", (rtrim("name", ' ')) COLLATE "dbmigrate"."ci_ai", (rtrim("scope_key", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__restaurant_stripe_connect__uq_restaurant_stripe_261cbce4" ON "restaurant_stripe_connect" ((rtrim("account_hash", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__restaurants__uniq_restaurants_slug" ON "restaurants" ((rtrim("slug", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__seo_item_routes__uniq_seo_item_route" ON "seo_item_routes" ("restaurant_id", (rtrim("entity_type", ' ')) COLLATE "dbmigrate"."ci_ai", "entity_id");
CREATE UNIQUE INDEX "ixpad__seo_item_routes__uniq_seo_item_slug" ON "seo_item_routes" ("restaurant_id", (rtrim("entity_type", ' ')) COLLATE "dbmigrate"."ci_ai", (rtrim("slug", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__site_builder_collections__uk_site_name" ON "site_builder_collections" ((rtrim("site_id", ' ')) COLLATE "dbmigrate"."ci_ai", (rtrim("name", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__site_builder_component_registry__type" ON "site_builder_component_registry" ((rtrim("type", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__site_builder_versions__uk_site_version" ON "site_builder_versions" ((rtrim("site_id", ' ')) COLLATE "dbmigrate"."ci_ai", "version_number");
CREATE UNIQUE INDEX "ixpad__stock_categories__uq_stock_category_name" ON "stock_categories" ("restaurant_id", (rtrim("name", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__stock_document_scans__uq_stock_document_hash" ON "stock_document_scans" ("restaurant_id", (rtrim("file_hash", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__stock_items__uq_stock_item_sku" ON "stock_items" ("restaurant_id", (rtrim("sku", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__stock_margin_scopes__uq_stock_margin_scope" ON "stock_margin_scopes" ("restaurant_id", "scope_kind", (rtrim("scope_key", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__stock_recipe_step_image_jobs__uq_step_image_job_idem" ON "stock_recipe_step_image_jobs" ("restaurant_id", (rtrim("idempotency_key", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__stock_suppliers__uq_stock_suppliers" ON "stock_suppliers" ("restaurant_id", (rtrim("name", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__stock_vat_rates__uq_stock_vat_name" ON "stock_vat_rates" ("restaurant_id", (rtrim("name", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__stock_warehouses__uq_stock_warehouse_code" ON "stock_warehouses" ("restaurant_id", (rtrim("code", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__stripe_events__uniq_stripe_events_event_id" ON "stripe_events" ((rtrim("event_id", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__uazapi_servers__uniq_uazapi_servers_base_url" ON "uazapi_servers" ((rtrim("base_url", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__user_menu_preferences__uq_user_menu_pref" ON "user_menu_preferences" ("user_id", "restaurant_id", "menu_id", (rtrim("pref_key", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__user_preferences__uq_user_pref" ON "user_preferences" ("user_id", "restaurant_id", (rtrim("pref_key", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__website_components__uk_component_type" ON "website_components" ((rtrim("component_type", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__website_pages__uk_website_slug" ON "website_pages" ("website_id", (rtrim("slug", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__website_templates__slug" ON "website_templates" ((rtrim("slug", ' ')) COLLATE "dbmigrate"."ci_ai");
```
- The gate now has 116 entries; the 24 new ones are under "padbatch4".
- The probes are regenerated, read-only and not executed: 145 count SELECTs each (116 unique, 18 PK, 11 FK) in pad-collision-probes.sql (PG, BEGIN READ ONLY…COMMIT) and pad-collision-probes-mysql.sql.
- Coordinator 08:21: the earlier 72/18/11 probe set returned zero on current data, except PG CHAR (unknown). That is current data only, not full parity.

## Build / inventory
- go build -p 2 ./... OK.
- Inventory r103: 2501 sites (conversions only), all native, 0 portable audit failures.

## Next technical plan (ordered; nothing here is complete)
1. **PK/FK canonicalization (raw IDs and FK equality retained):**
   - (a) Trace and fix the future writers that are not proven canonical: conversation_sessions.id (sess_<rid>_<sender>_<ts>; the sender text comes from webhook normalization) and whatsapp_bot_sessions.user_phone (provider msg.Sender). Refuse edge spaces at the write boundary only where the source writer is shown to be able to produce them.
   - (b) Add a supplementary ixpad unique index per PAD PK plus rtrim readers.
   - (c) If any padded parent or child appears, a reviewed generated canonical shadow column (rtrim, STORED, UNIQUE) with FKs re-pointed. This is a forward migration and stays unapplied until review.
2. **Coordinator read-only:** run the 145-query probes. For CHAR columns, the MySQL twin is the authority.
3. **Integrate the 1117 pending source gates:** these are source blockers, not just documentation. Collation (general_ci/unicode_ci vs ICU), LIKE accent semantics and ENUM input trailing spaces stay gated.
4. **Apply on a scratch PG copy only after review:** 116 ixpad + 18 PK supplementary indexes, with the raw R5 indexes kept. Then verify the RequirePadKeyIndexes rendering against the real catalog (the regex is still unverified).
5. **Full manual read-only, then whole-app functional QA on PG** (handlers, bot, booking/payments in demo mode only). Only then cut over. GraphRAG after the verified functional bot.

## Gates (not claimed)
- ci_ai is only an approximation; generated columns and ENUM parity are gated.
- All 116 indexes are unapplied.
- PK/FK future writers are unverified (conversation sender, WhatsApp user_phone).
- Checkout replay still does not compare payload.
- PG server disabled; tools report-only and index-gated.
- R5 untouched; 1117 pending / 731 orphans; checkout 503; 0 handler QA; 17 legitimate MySQL-only.
- No SQL, DB writes, payment or provider calls.
- This is NOT functional completion.
