# Result: repair-padbatch1 (next 20 PAD unique keys + probe script; code-only, nothing applied)

## Selection (exact, not heuristic)
- Source: derived local padplan (/tmp/villacarmen-bot-20261111/derived-localmeta-20261112/padplan.json).
- 116 non-PRIMARY proposed_gated unique keys, minus the 32 already in RequiredPadKeyIndexes, leaves 84.
- The first 20 by (table, index) were taken.
- **The counts agree:** 32 + 20 = 52 patched, and 116 - 52 = 64 remaining.
- The exact sets are in docs/dbmigrate/pad-key-sets-padbatch1.txt.

## The 20 keys (part kinds from padplan; pad = rtrim+ci_ai)
| key | parts | callers / treatment |
|---|---|---|
| POSTRES.uq_postres_restaurant_ui_data | restaurant_id, ui_data_id(NULL) | no writer or lookup repo-wide (admin_postres never touches ui_data_id); future index only |
| accounting_exports.uq_accounting_export_hash | restaurant_id, export_type(enum raw; enum input parity gated), period_from, period_to, payload_hash char(64) | accounting_exports.go:352 ON CONFLICT → PATCHED; MySQL ODKU :348 unchanged |
| admin_sessions.uniq_admin_sessions_session_id | session_id char general_ci | no Go writer or reader (legacy); future index only; general_ci approximation gated |
| ai_image_models.uniq_…provider_slug | provider_slug, slug | config_ai_image.go:359 PATCHED; seed-only writers (no Go INSERT) |
| ai_image_providers.uniq_…slug | slug | config_ai_image.go:353, :407 PATCHED |
| analytics_customer_sources.uq_analytics_customer_source | restaurant_id, source_type, source_ref | analytics/service.go:699 ON CONFLICT PATCHED |
| analytics_customers.uq_analytics_customers_identity | restaurant_id, identity_key | service.go:675 ON CONFLICT and :687 lookup PATCHED (builder NormalizeCustomerIdentity is canonical; old rows still need the probe) |
| analytics_stock_facts.uq_analytics_stock_fact_source | restaurant_id, source_type, source_id | service.go:957 ON CONFLICT PATCHED |
| app_settings.uniq_app_settings_k | k | instatic_manager.go:393 constant lookup PATCHED; no Go writer |
| assistant_memory_edges.uq_edge | from_node, to_node, rel | assistant_memory.go:112 ON CONFLICT PATCHED |
| assistant_memory_nodes.uq_memory_scope_label | restaurant_id, user_id, label | assistant_memory.go:125 ON CONFLICT and :131 lookup PATCHED |
| assistant_sessions.uq_assistant_sessions_token | session_token(NULL) | assistant_ws.go:291 PATCHED |
| bo_member_invitation_tokens.…guid | onboarding_guid char(NULL) | member_access_flows.go:1304 (PG twin of :1277) PATCHED; :1355 SET … WHERE id unchanged |
| bo_member_invitation_tokens.…token | token_sha256 char | member_access_flows.go:1202 (twin of :1175) PATCHED |
| bo_password_reset_tokens.…token | token_sha256 char | member_access_flows.go:1837 (twin of :1810) PATCHED |
| bo_sessions.uniq_bo_sessions_token | token_sha256 char | middleware.go:163 (twin of :136), backoffice_auth.go:217 logout, mcp_oauth_routes.go:308 PATCHED |
| booking_checkouts.uq_booking_checkouts_public | public_id | booking_checkout.go:104, 229, 233, 279 (twin of 277), 284 PATCHED |
| booking_reminder_deliveries.uq_booking_reminder_key | delivery_key | booking_reminders.go:349 ON CONFLICT, :361 DELETE, :370 (twin of :368) PATCHED |
| campaign_bounce_events.uq_bounce_event | restaurant_id, event_key | campaign_bounce_sync.go:352 ON CONFLICT PATCHED |
| campaigns.uq_campaign_coord | coord_id | INSERT only (backoffice_campaigns.go:424, generated "camp-<uuid>"); every read is by id; future index only |

- **How the callers were found:** (1) a scan of every Go string literal that names the table (any length, not a 25-line window), (2) repo greps for the key columns outside table-named literals, and (3) unique-index-name greps. None of the 20 keys has an IsUniqueViolationOn call or a name check.
- **Generated or hash keys were still patched** (bo_sessions and token hashes, public_id, delivery_key, coord_id lookups): old or imported rows can only be settled by the probes.
- **char(64) keys:** PG bpchar already ignores trailing spaces in comparison, so for these the pad expression is redundant but consistent. The padplan char_bpchar_semantics gate stays.
- **No changes on MySQL:** MySQL texts and the default workflow are unchanged. Every PG edit converts portableSQL→nativeSQL or changes only the PG literal, so each statement is still rebound once.

## Future DDL (NOT executed; additive; raw R5 unchanged)
```
CREATE UNIQUE INDEX "ixpad__POSTRES__uq_postres_restaurant_ui_data" ON "POSTRES" ("restaurant_id", (rtrim("ui_data_id", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__accounting_exports__uq_accounting_export_hash" ON "accounting_exports" ("restaurant_id", "export_type", "period_from", "period_to", (rtrim("payload_hash", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__admin_sessions__uniq_admin_sessions_session_id" ON "admin_sessions" ((rtrim("session_id", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__ai_image_models__uniq_ai_image_models_provider_slug" ON "ai_image_models" ((rtrim("provider_slug", ' ')) COLLATE "dbmigrate"."ci_ai", (rtrim("slug", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__ai_image_providers__uniq_ai_image_providers_slug" ON "ai_image_providers" ((rtrim("slug", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__analytics_customer_sources__uq_analytics_customer_source" ON "analytics_customer_sources" ("restaurant_id", (rtrim("source_type", ' ')) COLLATE "dbmigrate"."ci_ai", (rtrim("source_ref", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__analytics_customers__uq_analytics_customers_identity" ON "analytics_customers" ("restaurant_id", (rtrim("identity_key", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__analytics_stock_facts__uq_analytics_stock_fact_source" ON "analytics_stock_facts" ("restaurant_id", (rtrim("source_type", ' ')) COLLATE "dbmigrate"."ci_ai", "source_id");
CREATE UNIQUE INDEX "ixpad__app_settings__uniq_app_settings_k" ON "app_settings" ((rtrim("k", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__assistant_memory_edges__uq_edge" ON "assistant_memory_edges" ("from_node", "to_node", (rtrim("rel", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__assistant_memory_nodes__uq_memory_scope_label" ON "assistant_memory_nodes" ("restaurant_id", "user_id", (rtrim("label", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__assistant_sessions__uq_assistant_sessions_token" ON "assistant_sessions" ((rtrim("session_token", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__bo_member_invitation_tokens__uniq_bo_member_inv_b1aeb2d9" ON "bo_member_invitation_tokens" ((rtrim("onboarding_guid", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__bo_member_invitation_tokens__uniq_bo_member_inv_a551d2cc" ON "bo_member_invitation_tokens" ((rtrim("token_sha256", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__bo_password_reset_tokens__uniq_bo_password_rese_abfbaead" ON "bo_password_reset_tokens" ((rtrim("token_sha256", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__bo_sessions__uniq_bo_sessions_token" ON "bo_sessions" ((rtrim("token_sha256", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__booking_checkouts__uq_booking_checkouts_public" ON "booking_checkouts" ((rtrim("public_id", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__booking_reminder_deliveries__uq_booking_reminder_key" ON "booking_reminder_deliveries" ((rtrim("delivery_key", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__campaign_bounce_events__uq_bounce_event" ON "campaign_bounce_events" ("restaurant_id", (rtrim("event_key", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__campaigns__uq_campaign_coord" ON "campaigns" ((rtrim("coord_id", ' ')) COLLATE "dbmigrate"."ci_ai");
```
The gate (internal/db/padkeys.go) now has 52 entries; the 20 new ones are under "padbatch1" and use exact part order and pad flags from padplan.

## Probe script (replaces padcontact1's draft probes)
- **The padcontact1 probes had two flaws:**
  - raw <> rtrim misses CHAR padding (bpchar) and is collation-ignorable under ci_ai;
  - COUNT DISTINCT over composites with NULL miscounts (it could go negative for restaurant_members).
- **New file:** docs/dbmigrate/pad-collision-probes.sql. It is read-only, NOT executed, and returns aggregate counts only.
- **Unique keys:** one row for each of the 52 required keys:
  - collision_groups and rows_in_collisions, via GROUP BY the full rtrim+ci_ai key HAVING count(*) > 1, with every nullable part filtered IS NOT NULL;
  - padded_rows via octet_length(col) <> octet_length(rtrim(col,' ')), reported as NULL for CHAR columns.
- **Primary keys:** the same check over all 18 PAD PKs (full column sets).
- **FK children:** all 11 FK children of those PKs. For each, padded_child_rows plus pad_only_parent_matches: children with no exact PG parent but a pad/ci_ai parent, i.e. rows that are valid in MySQL but invalid in PG.
- **MySQL twin:** padded rows via OCTET_LENGTH vs TRIM(TRAILING ' '). MySQL CHAR strips on read.

## PK compatibility decision (revised)
- **Preserve the MySQL lookup contract:** prefer rtrim readers plus a supplementary unique ixpad index over rejecting padded client requests (option C dropped).
- **Not complete:**
  - FK equality in PG stays raw ci_ai (NO PAD);
  - parity holds only while no padded values exist in parents or children;
  - a read-only 0 from the probes is current data only, not a guarantee for the future;
  - future writers must ensure no edge-space values cross FKs. Many already normalize (role slugs) or generate (UUIDs). The rest are unverified for the future, so the generated canonical shadow-key option (B) stays open.
- **Gates kept:** the PK/FK parity gate, the R5 hashes, and the 731-orphan gate are unchanged.

## Build / inventory
- go build -p 2 ./... OK.
- Inventory r100: 2501 sites (conversions only), all native, 0 portable audit failures.

## Gates (not claimed)
- ci_ai is only an approximation (general_ci for admin_sessions and no_marketing is ungrounded).
- The gate's rendering regex is unverified.
- All 52 indexes are unapplied.
- Checkout replay still does not compare payload.
- PG server disabled; tools report-only and index-gated.
- R5 untouched; 1117 pending / 731 orphans; checkout 503; 0 handler QA; 5 query shapes; 17 legitimate MySQL-only.
- No SQL, DB writes or provider calls.

## Next
1. The coordinator runs pad-collision-probes.sql (read-only).
2. Next 20 of the remaining 64, starting at chatgpt_plugin_tokens.
