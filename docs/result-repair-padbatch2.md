# Result: repair-padbatch2 (next 20 PAD unique keys; code-only, nothing applied)

## Selection
- The 20 keys are pad-key-sets-padbatch1.txt REMAINING_64, from chatgpt_plugin_tokens through migration_runs inclusive, in deterministic (table, index) order.
- 52 + 20 = 72 patched and 44 remaining. Both exact sets are in docs/dbmigrate/pad-key-sets-padbatch2.txt.

## Keys and callers (PG text only; MySQL texts and default workflow unchanged)
| key (parts; pad = rtrim+ci_ai) | callers / treatment |
|---|---|
| chatgpt_plugin_tokens.token_hash char | chatgpt_plugin_tokens.go:150 lookup PATCHED |
| comida_bebida_categories (rid, slug) | comida.go:365, :2401 ON CONFLICT; :2463 and :2502 slug-or-name lookups PATCHED (slug part only; LOWER(name) unchanged) |
| comida_bulk_link_batches (rid, idempotency_key) | technical_sheet_bulk.go:143 replay lookup PATCHED; INSERT :191 raw |
| comida_categories (rid, food_type, slug) | comida_categories.go:313/318 food_type filter (incl. the '' constant), :538 slug check plus the COALESCE(food_type,'') IN list PATCHED; UPDATE SET :745 raw |
| comida_items (rid, ui_data_id NULL) | writer comida.go:1608 (TrimSpace+length); no key lookup or conflict; future index only |
| comida_plato_categories (rid, slug) | comida.go:341, :440, :702 ON CONFLICT; :407 lookup PATCHED |
| discount_tokens.token | no Go reader or writer (legacy/import only); future index only |
| dish_translations (rid, entity_type, entity_id, field_name, lang char(2)) | translations.go:223 (dynamic IN list, sqlFragment), :270/275 ON CONFLICT, :532, :552 PATCHED; cmd/backfill-translations:389 ON CONFLICT and :404 lookup PATCHED |
| forky_confirmation_tokens.token_hash char | assistant_confirmation.go:76 DELETE and :112 lookup (PG twins) PATCHED |
| group_menu_section_dishes_v2 (rid, ui_data_id NULL) | no writer or lookup repo-wide; future index only |
| invoice_wa_sends (rid, idempotency_key) | backoffice_invoices.go:1434 replay lookup PATCHED; INSERT :1454 raw |
| legal_pages (rid, slug) | legal_pages.go:36 lookup and :195/200 ON CONFLICT, whatsapp_bot_admin_reads.go:741, cmd/seed-legal-pages:93 (PG report branch) PATCHED; :103 is MySQL-only |
| mcp_oauth_clients.client_id | mcp_oauth_store.go:66 ON CONFLICT and :82 redirect lookup PATCHED |
| mcp_oauth_codes.code_hash char | store.go:133 lookup and :152 consume PATCHED |
| mcp_oauth_tokens.token_hash char | store.go:219 lookup, routes.go:306 revoke PATCHED; also store.go:279 revoke by client_id (non-key column, same identity) PATCHED |
| menu_visibility (rid, menu_key) | backoffice_menu_visibility.go:59 ON CONFLICT and :159 ($n-placeholder update); menu_visibility_admin.go:37/46/60/97/195/206/225 PATCHED |
| menu_weekday_availability (rid, menu_id, weekday) | menu_weekday_availability.go:123 ON CONFLICT PATCHED |
| menus (rid, seo_slug NULL) | menu_public_seo_handlers.go:31 public lookup PATCHED (sqlFragment for the PG fragment, portableSQL rebinds once); UPDATE SET :98 raw; unique error stays generic (:101) |
| message_deliveries.delivery_key NULL | pre_shift_reminders.go:53 and whatsapp_outbox.go:100 ON CONFLICT PATCHED |
| migration_runs.run_key | no writer or reader repo-wide; future index only |

Hash, generated and allowlisted keys were not skipped: legacy or imported padded rows can only be settled by the probes.

### MCP code→client binding (reviewed bugfix, both dialects)
- **Before:** mcp_oauth_store.go compared the stored code client_id to the token request's client_id with Go == (exact). Authorize trims client_id; the token endpoint passes it raw.
- **Now:** the code SELECT returns the DB comparison as sameClient:
  - MySQL: COALESCE(client_id = ?, 0) under the column collation;
  - PG: the pad expression.
- **Effect:** the binding matches the unique client identity of the source DB. The PKCE, expiry and used checks are unchanged. There are no extra provider calls.

## Future DDL (NOT executed; additive; raw R5 kept)
```
CREATE UNIQUE INDEX "ixpad__chatgpt_plugin_tokens__uq_chatgpt_plugin_tokens_hash" ON "chatgpt_plugin_tokens" ((rtrim("token_hash", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__comida_bebida_categories__uniq_comida_bebida_ca_d53427b6" ON "comida_bebida_categories" ("restaurant_id", (rtrim("slug", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__comida_bulk_link_batches__uq_comida_bulk_link_idem" ON "comida_bulk_link_batches" ("restaurant_id", (rtrim("idempotency_key", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__comida_categories__uniq_comida_categories_resta_83a5cbf6" ON "comida_categories" ("restaurant_id", (rtrim("food_type", ' ')) COLLATE "dbmigrate"."ci_ai", (rtrim("slug", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__comida_items__uq_comida_items_restaurant_ui_data" ON "comida_items" ("restaurant_id", (rtrim("ui_data_id", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__comida_plato_categories__uniq_comida_plato_cate_9b85ca31" ON "comida_plato_categories" ("restaurant_id", (rtrim("slug", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__discount_tokens__idx_token" ON "discount_tokens" ((rtrim("token", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__dish_translations__uniq_dish_translation" ON "dish_translations" ("restaurant_id", (rtrim("entity_type", ' ')) COLLATE "dbmigrate"."ci_ai", "entity_id", (rtrim("field_name", ' ')) COLLATE "dbmigrate"."ci_ai", (rtrim("lang", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__forky_confirmation_tokens__uq_forky_conf_token_hash" ON "forky_confirmation_tokens" ((rtrim("token_hash", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__group_menu_section_dishes_v2__uq_group_menu_dis_823c83d1" ON "group_menu_section_dishes_v2" ("restaurant_id", (rtrim("ui_data_id", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__invoice_wa_sends__uq_invoice_wa_sends_key" ON "invoice_wa_sends" ("restaurant_id", (rtrim("idempotency_key", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__legal_pages__uniq_legal_pages_restaurant_slug" ON "legal_pages" ("restaurant_id", (rtrim("slug", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__mcp_oauth_clients__uq_mcp_oauth_clients_client_id" ON "mcp_oauth_clients" ((rtrim("client_id", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__mcp_oauth_codes__uq_mcp_oauth_codes_hash" ON "mcp_oauth_codes" ((rtrim("code_hash", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__mcp_oauth_tokens__uq_mcp_oauth_tokens_hash" ON "mcp_oauth_tokens" ((rtrim("token_hash", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__menu_visibility__uniq_menu_visibility_restaurant_key" ON "menu_visibility" ("restaurant_id", (rtrim("menu_key", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__menu_weekday_availability__uq_menu_weekday_availability" ON "menu_weekday_availability" ("restaurant_id", "menu_id", (rtrim("weekday", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__menus__idx_menus_restaurant_seo_slug" ON "menus" ("restaurant_id", (rtrim("seo_slug", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__message_deliveries__uniq_message_deliveries_delivery_key" ON "message_deliveries" ((rtrim("delivery_key", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__migration_runs__run_key" ON "migration_runs" ((rtrim("run_key", ' ')) COLLATE "dbmigrate"."ci_ai");
```
The gate (internal/db/padkeys.go) now has 72 entries (padbatch2 adds 20).

## Gate review (CHAR / bpchar)
- For char(n) keys, the index expression rtrim("col", ' ') implicitly casts bpchar→text, which strips the padding. pg_get_indexdef is expected to render it as rtrim((col)::text, ' '::text). The validator regex accepts an optional (col) and ::text.
- The index key collation is the explicit dbmigrate.ci_ai, checked via indcollation, so implicit cast collations do not matter.
- The opclass must be the default btree text opclass, which suits an expression of type text.
- This is still unverified on a real catalog (source-only) and needs manual QA; it refuses on mismatch.
- The R5 schema/plan hashes are not touched (the gate reads the live catalog only).

## Probes (regenerated; read-only, NOT executed; the coordinator runs them separately)
- docs/dbmigrate/pad-collision-probes.sql (PG):
  - BEGIN READ ONLY … COMMIT;
  - 72 unique keys, 18 PKs and 11 FK children = 101 aggregate SELECTs;
  - NULL-filtered GROUP BY / HAVING collisions, padded rows via octet_length (NULL for CHAR), and pad-only FK parent matches.
- docs/dbmigrate/pad-collision-probes-mysql.sql (new separate file): 101 plain count SELECTs, no transaction statements.

## Build / inventory
- go build -p 2 ./... OK.
- Inventory r101: 2501 sites (conversions only), all native, 0 portable audit failures.

## Gates (not claimed)
- ci_ai is only an approximation (general_ci keys are ungrounded).
- The gate's rendering regex is unverified.
- All 72 indexes are unapplied.
- PK/FK future-writer gate is open.
- Checkout replay still does not compare payload.
- PG server disabled; tools report-only and index-gated.
- R5 untouched; 1117 pending / 731 orphans; checkout 503; 0 handler QA; 5 query shapes; 17 legitimate MySQL-only.
- No SQL, DB writes or provider calls.

## Next
The next 20 of the remaining 44, starting at pos_cash_movements.
