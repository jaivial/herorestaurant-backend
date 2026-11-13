-- wk-ddl-proposal-vitess6.sql: PROPOSAL ONLY, NOT APPLIED, NOT FOR R5/production.
-- Future weight-key columns (bytea, MySQL collation key from internal/mysqlcoll)
-- and unique indexes mirroring the 134 ixpad indexes (internal/db/padkeys.go),
-- PAD parts replaced by <col>__wk; plain parts unchanged; NULL key when text NULL.
-- Order for any future scratch apply (coordinator review first):
--   1. ADD COLUMN (nullable)  2. backfill via Go WriterKey in a reviewed tool
--   3. readiness CheckStoredKeys = 0 mismatches  4. CREATE UNIQUE INDEX
--   5. switch callers' lookups/ON CONFLICT to __wk  6. only then retire ixpad.
-- Primary keys stay raw (FK targets); the 18 PK entries get supplementary uniques.
BEGIN;
ALTER TABLE "restaurant_domains" ADD COLUMN "domain__wk" bytea; -- key of domain (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__restaurant_domains__uniq_restaurant_domains_domain" ON "restaurant_domains" ("domain__wk");
ALTER TABLE "site_builder_domain_mappings" ADD COLUMN "domain__wk" bytea; -- key of domain (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__site_builder_domain_mappings__uk_domain" ON "site_builder_domain_mappings" ("domain__wk");
ALTER TABLE "site_builder_sites" ADD COLUMN "subdomain__wk" bytea; -- key of subdomain (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__site_builder_sites__uk_subdomain" ON "site_builder_sites" ("subdomain__wk");
ALTER TABLE "site_builder_sites" ADD COLUMN "custom_domain__wk" bytea; -- key of custom_domain (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__site_builder_sites__uk_custom_domain" ON "site_builder_sites" ("custom_domain__wk");
ALTER TABLE "restaurant_uazapi_instances" ADD COLUMN "instance_name__wk" bytea; -- key of instance_name (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__restaurant_uazapi_instances__uniq_restaurant_ua_96a63182" ON "restaurant_uazapi_instances" ("instance_name__wk");
ALTER TABLE "member_time_allocations" ADD COLUMN "idempotency_key__wk" bytea; -- key of idempotency_key (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__member_time_allocations__uq_member_time_allocat_b4151cea" ON "member_time_allocations" ("restaurant_id", "idempotency_key__wk");
ALTER TABLE "pos_cover_adjustments" ADD COLUMN "idempotency_key__wk" bytea; -- key of idempotency_key (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__pos_cover_adjustments__uq_pos_cover_adjustment__f16820e6" ON "pos_cover_adjustments" ("restaurant_id", "idempotency_key__wk");
ALTER TABLE "pos_payments" ADD COLUMN "idempotency_key__wk" bytea; -- key of idempotency_key (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__pos_payments__uq_pos_payment_idempotency" ON "pos_payments" ("restaurant_id", "idempotency_key__wk");
ALTER TABLE "pos_refunds" ADD COLUMN "idempotency_key__wk" bytea; -- key of idempotency_key (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__pos_refunds__uq_pos_refund_idempotency" ON "pos_refunds" ("restaurant_id", "idempotency_key__wk");
ALTER TABLE "pos_stock_exceptions" ADD COLUMN "replay_idempotency_key__wk" bytea; -- key of replay_idempotency_key (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__pos_stock_exceptions__uq_pos_stock_exception_replay" ON "pos_stock_exceptions" ("restaurant_id", "replay_idempotency_key__wk");
ALTER TABLE "pos_tickets" ADD COLUMN "checkout_idempotency_key__wk" bytea; -- key of checkout_idempotency_key (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__pos_tickets__uq_pos_ticket_checkout" ON "pos_tickets" ("restaurant_id", "checkout_idempotency_key__wk");
ALTER TABLE "pos_tickets" ADD COLUMN "creation_idempotency_key__wk" bytea; -- key of creation_idempotency_key (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__pos_tickets__uq_pos_ticket_creation" ON "pos_tickets" ("restaurant_id", "creation_idempotency_key__wk");
ALTER TABLE "pos_visits" ADD COLUMN "open_idempotency_key__wk" bytea; -- key of open_idempotency_key (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__pos_visits__uq_pos_visit_open_idempotency" ON "pos_visits" ("restaurant_id", "open_idempotency_key__wk");
ALTER TABLE "stock_movements" ADD COLUMN "idempotency_key__wk" bytea; -- key of idempotency_key (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__stock_movements__uq_stock_movement_idempotency" ON "stock_movements" ("restaurant_id", "idempotency_key__wk");
ALTER TABLE "stock_production_orders" ADD COLUMN "idempotency_key__wk" bytea; -- key of idempotency_key (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__stock_production_orders__uq_stock_production_idempotency" ON "stock_production_orders" ("restaurant_id", "idempotency_key__wk");
ALTER TABLE "pos_cash_closures" ADD COLUMN "idempotency_key__wk" bytea; -- key of idempotency_key (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__pos_cash_closures__uq_pos_cash_closure_idempotency" ON "pos_cash_closures" ("restaurant_id", "idempotency_key__wk");
ALTER TABLE "pos_ticket_lines" ADD COLUMN "idempotency_key__wk" bytea; -- key of idempotency_key (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__pos_ticket_lines__uq_pos_ticket_line_idempotency" ON "pos_ticket_lines" ("restaurant_id", "idempotency_key__wk");
ALTER TABLE "stock_item_units" ADD COLUMN "code__wk" bytea; -- key of code (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__stock_item_units__uq_stock_item_unit_code" ON "stock_item_units" ("restaurant_id", "stock_item_id", "code__wk");
ALTER TABLE "restaurant_areas" ADD COLUMN "name__wk" bytea; -- key of name (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__restaurant_areas__uniq_restaurant_areas_restaurant_name" ON "restaurant_areas" ("restaurant_id", "name__wk");
ALTER TABLE "restaurant_tables" ADD COLUMN "name__wk" bytea; -- key of name (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__restaurant_tables__uniq_restaurant_tables_resta_4d031b62" ON "restaurant_tables" ("restaurant_id", "name__wk");
ALTER TABLE "restaurant_tables" ADD COLUMN "numero_mesa__wk" bytea; -- key of numero_mesa (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__restaurant_tables__uniq_restaurant_tables_resta_efc1635a" ON "restaurant_tables" ("restaurant_id", "numero_mesa__wk");
CREATE UNIQUE INDEX "ixwk__restaurant_tables__idx_restaurant_numero" ON "restaurant_tables" ("restaurant_id", "numero_mesa__wk");
ALTER TABLE "site_builder_pages" ADD COLUMN "site_id__wk" bytea; -- key of site_id (utf8mb4_unicode_ci)
ALTER TABLE "site_builder_pages" ADD COLUMN "slug__wk" bytea; -- key of slug (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__site_builder_pages__uk_site_slug" ON "site_builder_pages" ("site_id__wk", "slug__wk");
ALTER TABLE "stock_supplier_aliases" ADD COLUMN "supplier_name__wk" bytea; -- key of supplier_name (utf8mb4_unicode_ci)
ALTER TABLE "stock_supplier_aliases" ADD COLUMN "supplier_code__wk" bytea; -- key of supplier_code (utf8mb4_unicode_ci)
ALTER TABLE "stock_supplier_aliases" ADD COLUMN "normalized_description__wk" bytea; -- key of normalized_description (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__stock_supplier_aliases__uq_stock_supplier_alias" ON "stock_supplier_aliases" ("restaurant_id", "supplier_name__wk", "supplier_code__wk", "normalized_description__wk");
ALTER TABLE "bo_users" ADD COLUMN "email__wk" bytea; -- key of email (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__bo_users__uniq_bo_users_email" ON "bo_users" ("email__wk");
ALTER TABLE "bo_users" ADD COLUMN "username__wk" bytea; -- key of username (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__bo_users__uniq_bo_users_username" ON "bo_users" ("username__wk");
ALTER TABLE "campaign_recipients" ADD COLUMN "channel__wk" bytea; -- key of channel (utf8mb4_unicode_ci)
ALTER TABLE "campaign_recipients" ADD COLUMN "target__wk" bytea; -- key of target (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__campaign_recipients__uq_campaign_target" ON "campaign_recipients" ("campaign_id", "channel__wk", "target__wk");
ALTER TABLE "campaign_suppressions" ADD COLUMN "channel__wk" bytea; -- key of channel (utf8mb4_unicode_ci)
ALTER TABLE "campaign_suppressions" ADD COLUMN "target__wk" bytea; -- key of target (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__campaign_suppressions__uq_suppression" ON "campaign_suppressions" ("restaurant_id", "channel__wk", "target__wk");
ALTER TABLE "no_marketing" ADD COLUMN "contact_phone__wk" bytea; -- key of contact_phone (utf8mb4_general_ci)
CREATE UNIQUE INDEX "ixwk__no_marketing__idx_no_marketing_phone" ON "no_marketing" ("contact_phone__wk");
ALTER TABLE "pos_customers" ADD COLUMN "email__wk" bytea; -- key of email (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__pos_customers__uq_pos_customers_email" ON "pos_customers" ("restaurant_id", "email__wk");
ALTER TABLE "pos_customers" ADD COLUMN "phone__wk" bytea; -- key of phone (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__pos_customers__uq_pos_customers_phone" ON "pos_customers" ("restaurant_id", "phone__wk");
ALTER TABLE "restaurant_members" ADD COLUMN "email__wk" bytea; -- key of email (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__restaurant_members__uniq_restaurant_members_res_bcbe3c17" ON "restaurant_members" ("restaurant_id", "email__wk");
ALTER TABLE "POSTRES" ADD COLUMN "ui_data_id__wk" bytea; -- key of ui_data_id (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__POSTRES__uq_postres_restaurant_ui_data" ON "POSTRES" ("restaurant_id", "ui_data_id__wk");
ALTER TABLE "accounting_exports" ADD COLUMN "payload_hash__wk" bytea; -- key of payload_hash (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__accounting_exports__uq_accounting_export_hash" ON "accounting_exports" ("restaurant_id", "export_type", "period_from", "period_to", "payload_hash__wk");
ALTER TABLE "admin_sessions" ADD COLUMN "session_id__wk" bytea; -- key of session_id (utf8mb4_general_ci)
CREATE UNIQUE INDEX "ixwk__admin_sessions__uniq_admin_sessions_session_id" ON "admin_sessions" ("session_id__wk");
ALTER TABLE "ai_image_models" ADD COLUMN "provider_slug__wk" bytea; -- key of provider_slug (utf8mb4_unicode_ci)
ALTER TABLE "ai_image_models" ADD COLUMN "slug__wk" bytea; -- key of slug (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__ai_image_models__uniq_ai_image_models_provider_slug" ON "ai_image_models" ("provider_slug__wk", "slug__wk");
ALTER TABLE "ai_image_providers" ADD COLUMN "slug__wk" bytea; -- key of slug (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__ai_image_providers__uniq_ai_image_providers_slug" ON "ai_image_providers" ("slug__wk");
ALTER TABLE "analytics_customer_sources" ADD COLUMN "source_type__wk" bytea; -- key of source_type (utf8mb4_unicode_ci)
ALTER TABLE "analytics_customer_sources" ADD COLUMN "source_ref__wk" bytea; -- key of source_ref (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__analytics_customer_sources__uq_analytics_customer_source" ON "analytics_customer_sources" ("restaurant_id", "source_type__wk", "source_ref__wk");
ALTER TABLE "analytics_customers" ADD COLUMN "identity_key__wk" bytea; -- key of identity_key (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__analytics_customers__uq_analytics_customers_identity" ON "analytics_customers" ("restaurant_id", "identity_key__wk");
ALTER TABLE "analytics_stock_facts" ADD COLUMN "source_type__wk" bytea; -- key of source_type (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__analytics_stock_facts__uq_analytics_stock_fact_source" ON "analytics_stock_facts" ("restaurant_id", "source_type__wk", "source_id");
ALTER TABLE "app_settings" ADD COLUMN "k__wk" bytea; -- key of k (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__app_settings__uniq_app_settings_k" ON "app_settings" ("k__wk");
ALTER TABLE "assistant_memory_edges" ADD COLUMN "rel__wk" bytea; -- key of rel (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__assistant_memory_edges__uq_edge" ON "assistant_memory_edges" ("from_node", "to_node", "rel__wk");
ALTER TABLE "assistant_memory_nodes" ADD COLUMN "label__wk" bytea; -- key of label (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__assistant_memory_nodes__uq_memory_scope_label" ON "assistant_memory_nodes" ("restaurant_id", "user_id", "label__wk");
ALTER TABLE "assistant_sessions" ADD COLUMN "session_token__wk" bytea; -- key of session_token (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__assistant_sessions__uq_assistant_sessions_token" ON "assistant_sessions" ("session_token__wk");
ALTER TABLE "bo_member_invitation_tokens" ADD COLUMN "onboarding_guid__wk" bytea; -- key of onboarding_guid (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__bo_member_invitation_tokens__uniq_bo_member_inv_b1aeb2d9" ON "bo_member_invitation_tokens" ("onboarding_guid__wk");
ALTER TABLE "bo_member_invitation_tokens" ADD COLUMN "token_sha256__wk" bytea; -- key of token_sha256 (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__bo_member_invitation_tokens__uniq_bo_member_inv_a551d2cc" ON "bo_member_invitation_tokens" ("token_sha256__wk");
ALTER TABLE "bo_password_reset_tokens" ADD COLUMN "token_sha256__wk" bytea; -- key of token_sha256 (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__bo_password_reset_tokens__uniq_bo_password_rese_abfbaead" ON "bo_password_reset_tokens" ("token_sha256__wk");
ALTER TABLE "bo_sessions" ADD COLUMN "token_sha256__wk" bytea; -- key of token_sha256 (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__bo_sessions__uniq_bo_sessions_token" ON "bo_sessions" ("token_sha256__wk");
ALTER TABLE "booking_checkouts" ADD COLUMN "public_id__wk" bytea; -- key of public_id (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__booking_checkouts__uq_booking_checkouts_public" ON "booking_checkouts" ("public_id__wk");
ALTER TABLE "booking_reminder_deliveries" ADD COLUMN "delivery_key__wk" bytea; -- key of delivery_key (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__booking_reminder_deliveries__uq_booking_reminder_key" ON "booking_reminder_deliveries" ("delivery_key__wk");
ALTER TABLE "campaign_bounce_events" ADD COLUMN "event_key__wk" bytea; -- key of event_key (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__campaign_bounce_events__uq_bounce_event" ON "campaign_bounce_events" ("restaurant_id", "event_key__wk");
ALTER TABLE "campaigns" ADD COLUMN "coord_id__wk" bytea; -- key of coord_id (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__campaigns__uq_campaign_coord" ON "campaigns" ("coord_id__wk");
ALTER TABLE "chatgpt_plugin_tokens" ADD COLUMN "token_hash__wk" bytea; -- key of token_hash (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__chatgpt_plugin_tokens__uq_chatgpt_plugin_tokens_hash" ON "chatgpt_plugin_tokens" ("token_hash__wk");
ALTER TABLE "comida_bebida_categories" ADD COLUMN "slug__wk" bytea; -- key of slug (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__comida_bebida_categories__uniq_comida_bebida_ca_d53427b6" ON "comida_bebida_categories" ("restaurant_id", "slug__wk");
ALTER TABLE "comida_bulk_link_batches" ADD COLUMN "idempotency_key__wk" bytea; -- key of idempotency_key (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__comida_bulk_link_batches__uq_comida_bulk_link_idem" ON "comida_bulk_link_batches" ("restaurant_id", "idempotency_key__wk");
ALTER TABLE "comida_categories" ADD COLUMN "food_type__wk" bytea; -- key of food_type (utf8mb4_unicode_ci)
ALTER TABLE "comida_categories" ADD COLUMN "slug__wk" bytea; -- key of slug (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__comida_categories__uniq_comida_categories_resta_83a5cbf6" ON "comida_categories" ("restaurant_id", "food_type__wk", "slug__wk");
ALTER TABLE "comida_items" ADD COLUMN "ui_data_id__wk" bytea; -- key of ui_data_id (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__comida_items__uq_comida_items_restaurant_ui_data" ON "comida_items" ("restaurant_id", "ui_data_id__wk");
ALTER TABLE "comida_plato_categories" ADD COLUMN "slug__wk" bytea; -- key of slug (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__comida_plato_categories__uniq_comida_plato_cate_9b85ca31" ON "comida_plato_categories" ("restaurant_id", "slug__wk");
ALTER TABLE "discount_tokens" ADD COLUMN "token__wk" bytea; -- key of token (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__discount_tokens__idx_token" ON "discount_tokens" ("token__wk");
ALTER TABLE "dish_translations" ADD COLUMN "entity_type__wk" bytea; -- key of entity_type (utf8mb4_unicode_ci)
ALTER TABLE "dish_translations" ADD COLUMN "field_name__wk" bytea; -- key of field_name (utf8mb4_unicode_ci)
ALTER TABLE "dish_translations" ADD COLUMN "lang__wk" bytea; -- key of lang (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__dish_translations__uniq_dish_translation" ON "dish_translations" ("restaurant_id", "entity_type__wk", "entity_id", "field_name__wk", "lang__wk");
ALTER TABLE "forky_confirmation_tokens" ADD COLUMN "token_hash__wk" bytea; -- key of token_hash (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__forky_confirmation_tokens__uq_forky_conf_token_hash" ON "forky_confirmation_tokens" ("token_hash__wk");
ALTER TABLE "group_menu_section_dishes_v2" ADD COLUMN "ui_data_id__wk" bytea; -- key of ui_data_id (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__group_menu_section_dishes_v2__uq_group_menu_dis_823c83d1" ON "group_menu_section_dishes_v2" ("restaurant_id", "ui_data_id__wk");
ALTER TABLE "invoice_wa_sends" ADD COLUMN "idempotency_key__wk" bytea; -- key of idempotency_key (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__invoice_wa_sends__uq_invoice_wa_sends_key" ON "invoice_wa_sends" ("restaurant_id", "idempotency_key__wk");
ALTER TABLE "legal_pages" ADD COLUMN "slug__wk" bytea; -- key of slug (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__legal_pages__uniq_legal_pages_restaurant_slug" ON "legal_pages" ("restaurant_id", "slug__wk");
ALTER TABLE "mcp_oauth_clients" ADD COLUMN "client_id__wk" bytea; -- key of client_id (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__mcp_oauth_clients__uq_mcp_oauth_clients_client_id" ON "mcp_oauth_clients" ("client_id__wk");
ALTER TABLE "mcp_oauth_codes" ADD COLUMN "code_hash__wk" bytea; -- key of code_hash (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__mcp_oauth_codes__uq_mcp_oauth_codes_hash" ON "mcp_oauth_codes" ("code_hash__wk");
ALTER TABLE "mcp_oauth_tokens" ADD COLUMN "token_hash__wk" bytea; -- key of token_hash (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__mcp_oauth_tokens__uq_mcp_oauth_tokens_hash" ON "mcp_oauth_tokens" ("token_hash__wk");
ALTER TABLE "menu_visibility" ADD COLUMN "menu_key__wk" bytea; -- key of menu_key (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__menu_visibility__uniq_menu_visibility_restaurant_key" ON "menu_visibility" ("restaurant_id", "menu_key__wk");
ALTER TABLE "menu_weekday_availability" ADD COLUMN "weekday__wk" bytea; -- key of weekday (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__menu_weekday_availability__uq_menu_weekday_availability" ON "menu_weekday_availability" ("restaurant_id", "menu_id", "weekday__wk");
ALTER TABLE "menus" ADD COLUMN "seo_slug__wk" bytea; -- key of seo_slug (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__menus__idx_menus_restaurant_seo_slug" ON "menus" ("restaurant_id", "seo_slug__wk");
ALTER TABLE "message_deliveries" ADD COLUMN "delivery_key__wk" bytea; -- key of delivery_key (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__message_deliveries__uniq_message_deliveries_delivery_key" ON "message_deliveries" ("delivery_key__wk");
ALTER TABLE "migration_runs" ADD COLUMN "run_key__wk" bytea; -- key of run_key (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__migration_runs__run_key" ON "migration_runs" ("run_key__wk");
ALTER TABLE "pos_cash_movements" ADD COLUMN "idempotency_key__wk" bytea; -- key of idempotency_key (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__pos_cash_movements__uq_pos_cash_movement_idempotency" ON "pos_cash_movements" ("restaurant_id", "idempotency_key__wk");
ALTER TABLE "pos_drawer_events" ADD COLUMN "idempotency_key__wk" bytea; -- key of idempotency_key (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__pos_drawer_events__uq_pos_drawer_event_idempotency" ON "pos_drawer_events" ("restaurant_id", "idempotency_key__wk");
ALTER TABLE "pos_fiscal_documents" ADD COLUMN "full_number__wk" bytea; -- key of full_number (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__pos_fiscal_documents__uq_pos_fiscal_documents_full" ON "pos_fiscal_documents" ("restaurant_id", "full_number__wk", "copy_number");
ALTER TABLE "pos_fiscal_series" ADD COLUMN "terminal_key__wk" bytea; -- key of terminal_key (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__pos_fiscal_series__uq_pos_fiscal_series_scope" ON "pos_fiscal_series" ("restaurant_id", "terminal_key__wk", "series_type");
ALTER TABLE "pos_kitchen_dispatches" ADD COLUMN "command_key__wk" bytea; -- key of command_key (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__pos_kitchen_dispatches__uq_pos_kitchen_dispatch_command" ON "pos_kitchen_dispatches" ("restaurant_id", "station_id", "command_key__wk");
ALTER TABLE "pos_kitchen_stations" ADD COLUMN "name__wk" bytea; -- key of name (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__pos_kitchen_stations__uq_pos_kitchen_station_name" ON "pos_kitchen_stations" ("restaurant_id", "name__wk");
ALTER TABLE "pos_modifier_groups" ADD COLUMN "name__wk" bytea; -- key of name (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__pos_modifier_groups__uq_pos_modifier_group_name" ON "pos_modifier_groups" ("restaurant_id", "name__wk");
ALTER TABLE "pos_modifier_options" ADD COLUMN "name__wk" bytea; -- key of name (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__pos_modifier_options__uq_pos_modifier_option_name" ON "pos_modifier_options" ("restaurant_id", "group_id", "name__wk");
ALTER TABLE "pos_packs" ADD COLUMN "name__wk" bytea; -- key of name (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__pos_packs__uq_pos_pack_name" ON "pos_packs" ("restaurant_id", "name__wk");
ALTER TABLE "pos_payments" ADD COLUMN "provider__wk" bytea; -- key of provider (utf8mb4_unicode_ci)
ALTER TABLE "pos_payments" ADD COLUMN "provider_reference__wk" bytea; -- key of provider_reference (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__pos_payments__uq_pos_payment_provider_ref" ON "pos_payments" ("restaurant_id", "provider__wk", "provider_reference__wk");
ALTER TABLE "pos_product_categories" ADD COLUMN "name__wk" bytea; -- key of name (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__pos_product_categories__uq_pos_product_category_name" ON "pos_product_categories" ("restaurant_id", "name__wk");
ALTER TABLE "pos_products" ADD COLUMN "sku__wk" bytea; -- key of sku (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__pos_products__uq_pos_product_sku" ON "pos_products" ("restaurant_id", "sku__wk");
ALTER TABLE "pos_service_periods" ADD COLUMN "name__wk" bytea; -- key of name (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__pos_service_periods__uq_pos_service_period_name" ON "pos_service_periods" ("restaurant_id", "name__wk");
ALTER TABLE "pos_shifts" ADD COLUMN "open_terminal_key__wk" bytea; -- key of open_terminal_key (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__pos_shifts__uq_pos_shift_open_terminal" ON "pos_shifts" ("restaurant_id", "open_terminal_key__wk");
ALTER TABLE "pos_tags" ADD COLUMN "name__wk" bytea; -- key of name (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__pos_tags__uq_pos_tag_name" ON "pos_tags" ("restaurant_id", "name__wk");
ALTER TABLE "pos_ticket_adjustments" ADD COLUMN "idempotency_key__wk" bytea; -- key of idempotency_key (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__pos_ticket_adjustments__uq_pos_ticket_adjustmen_610cf45e" ON "pos_ticket_adjustments" ("restaurant_id", "idempotency_key__wk");
ALTER TABLE "pos_tickets" ADD COLUMN "ticket_number__wk" bytea; -- key of ticket_number (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__pos_tickets__uq_pos_ticket_number" ON "pos_tickets" ("restaurant_id", "ticket_number__wk");
ALTER TABLE "pos_visit_merges" ADD COLUMN "idempotency_key__wk" bytea; -- key of idempotency_key (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__pos_visit_merges__uq_pos_visit_merges_restaurant_key" ON "pos_visit_merges" ("restaurant_id", "idempotency_key__wk");
ALTER TABLE "restaurant_beverage_options" ADD COLUMN "slug__wk" bytea; -- key of slug (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__restaurant_beverage_options__uq_restaurant_beve_2945eb03" ON "restaurant_beverage_options" ("restaurant_id", "slug__wk");
ALTER TABLE "restaurant_booking_extras" ADD COLUMN "slug__wk" bytea; -- key of slug (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__restaurant_booking_extras__uq_restaurant_bookin_038fec0f" ON "restaurant_booking_extras" ("restaurant_id", "slug__wk");
ALTER TABLE "restaurant_floors" ADD COLUMN "scope_key__wk" bytea; -- key of scope_key (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__restaurant_floors__uniq_restaurant_floors_scope" ON "restaurant_floors" ("restaurant_id", "floor_number", "scope_key__wk");
ALTER TABLE "restaurant_salons" ADD COLUMN "name__wk" bytea; -- key of name (utf8mb4_unicode_ci)
ALTER TABLE "restaurant_salons" ADD COLUMN "scope_key__wk" bytea; -- key of scope_key (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__restaurant_salons__uniq_restaurant_salons_scope" ON "restaurant_salons" ("restaurant_id", "floor_id", "name__wk", "scope_key__wk");
ALTER TABLE "restaurant_stripe_connect" ADD COLUMN "account_hash__wk" bytea; -- key of account_hash (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__restaurant_stripe_connect__uq_restaurant_stripe_261cbce4" ON "restaurant_stripe_connect" ("account_hash__wk");
ALTER TABLE "restaurants" ADD COLUMN "slug__wk" bytea; -- key of slug (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__restaurants__uniq_restaurants_slug" ON "restaurants" ("slug__wk");
ALTER TABLE "seo_item_routes" ADD COLUMN "entity_type__wk" bytea; -- key of entity_type (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__seo_item_routes__uniq_seo_item_route" ON "seo_item_routes" ("restaurant_id", "entity_type__wk", "entity_id");
ALTER TABLE "seo_item_routes" ADD COLUMN "slug__wk" bytea; -- key of slug (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__seo_item_routes__uniq_seo_item_slug" ON "seo_item_routes" ("restaurant_id", "entity_type__wk", "slug__wk");
ALTER TABLE "site_builder_collections" ADD COLUMN "site_id__wk" bytea; -- key of site_id (utf8mb4_unicode_ci)
ALTER TABLE "site_builder_collections" ADD COLUMN "name__wk" bytea; -- key of name (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__site_builder_collections__uk_site_name" ON "site_builder_collections" ("site_id__wk", "name__wk");
ALTER TABLE "site_builder_component_registry" ADD COLUMN "type__wk" bytea; -- key of type (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__site_builder_component_registry__type" ON "site_builder_component_registry" ("type__wk");
ALTER TABLE "site_builder_versions" ADD COLUMN "site_id__wk" bytea; -- key of site_id (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__site_builder_versions__uk_site_version" ON "site_builder_versions" ("site_id__wk", "version_number");
ALTER TABLE "stock_categories" ADD COLUMN "name__wk" bytea; -- key of name (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__stock_categories__uq_stock_category_name" ON "stock_categories" ("restaurant_id", "name__wk");
ALTER TABLE "stock_document_scans" ADD COLUMN "file_hash__wk" bytea; -- key of file_hash (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__stock_document_scans__uq_stock_document_hash" ON "stock_document_scans" ("restaurant_id", "file_hash__wk");
ALTER TABLE "stock_items" ADD COLUMN "sku__wk" bytea; -- key of sku (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__stock_items__uq_stock_item_sku" ON "stock_items" ("restaurant_id", "sku__wk");
ALTER TABLE "stock_margin_scopes" ADD COLUMN "scope_key__wk" bytea; -- key of scope_key (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__stock_margin_scopes__uq_stock_margin_scope" ON "stock_margin_scopes" ("restaurant_id", "scope_kind", "scope_key__wk");
ALTER TABLE "stock_recipe_step_image_jobs" ADD COLUMN "idempotency_key__wk" bytea; -- key of idempotency_key (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__stock_recipe_step_image_jobs__uq_step_image_job_idem" ON "stock_recipe_step_image_jobs" ("restaurant_id", "idempotency_key__wk");
ALTER TABLE "stock_suppliers" ADD COLUMN "name__wk" bytea; -- key of name (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__stock_suppliers__uq_stock_suppliers" ON "stock_suppliers" ("restaurant_id", "name__wk");
ALTER TABLE "stock_vat_rates" ADD COLUMN "name__wk" bytea; -- key of name (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__stock_vat_rates__uq_stock_vat_name" ON "stock_vat_rates" ("restaurant_id", "name__wk");
ALTER TABLE "stock_warehouses" ADD COLUMN "code__wk" bytea; -- key of code (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__stock_warehouses__uq_stock_warehouse_code" ON "stock_warehouses" ("restaurant_id", "code__wk");
ALTER TABLE "stripe_events" ADD COLUMN "event_id__wk" bytea; -- key of event_id (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__stripe_events__uniq_stripe_events_event_id" ON "stripe_events" ("event_id__wk");
ALTER TABLE "uazapi_servers" ADD COLUMN "base_url__wk" bytea; -- key of base_url (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__uazapi_servers__uniq_uazapi_servers_base_url" ON "uazapi_servers" ("base_url__wk");
ALTER TABLE "user_menu_preferences" ADD COLUMN "pref_key__wk" bytea; -- key of pref_key (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__user_menu_preferences__uq_user_menu_pref" ON "user_menu_preferences" ("user_id", "restaurant_id", "menu_id", "pref_key__wk");
ALTER TABLE "user_preferences" ADD COLUMN "pref_key__wk" bytea; -- key of pref_key (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__user_preferences__uq_user_pref" ON "user_preferences" ("user_id", "restaurant_id", "pref_key__wk");
ALTER TABLE "website_components" ADD COLUMN "component_type__wk" bytea; -- key of component_type (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__website_components__uk_component_type" ON "website_components" ("component_type__wk");
ALTER TABLE "website_pages" ADD COLUMN "slug__wk" bytea; -- key of slug (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__website_pages__uk_website_slug" ON "website_pages" ("website_id", "slug__wk");
ALTER TABLE "website_templates" ADD COLUMN "slug__wk" bytea; -- key of slug (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__website_templates__slug" ON "website_templates" ("slug__wk");
ALTER TABLE "whatsapp_bot_sessions" ADD COLUMN "user_phone__wk" bytea; -- key of user_phone (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__whatsapp_bot_sessions__PRIMARY" ON "whatsapp_bot_sessions" ("restaurant_id", "user_phone__wk");
ALTER TABLE "bo_role_permissions" ADD COLUMN "role_slug__wk" bytea; -- key of role_slug (utf8mb4_unicode_ci)
ALTER TABLE "bo_role_permissions" ADD COLUMN "section_key__wk" bytea; -- key of section_key (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__bo_role_permissions__PRIMARY" ON "bo_role_permissions" ("role_slug__wk", "section_key__wk");
ALTER TABLE "bo_roles" ADD COLUMN "slug__wk" bytea; -- key of slug (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__bo_roles__PRIMARY" ON "bo_roles" ("slug__wk");
ALTER TABLE "conversation_sessions" ADD COLUMN "id__wk" bytea; -- key of id (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__conversation_sessions__PRIMARY" ON "conversation_sessions" ("id__wk");
ALTER TABLE "platform_settings" ADD COLUMN "setting_key__wk" bytea; -- key of setting_key (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__platform_settings__PRIMARY" ON "platform_settings" ("setting_key__wk");
ALTER TABLE "pos_daily_sequences" ADD COLUMN "sequence_type__wk" bytea; -- key of sequence_type (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__pos_daily_sequences__PRIMARY" ON "pos_daily_sequences" ("restaurant_id", "business_date", "sequence_type__wk");
ALTER TABLE "pos_role_permissions" ADD COLUMN "role_slug__wk" bytea; -- key of role_slug (utf8mb4_unicode_ci)
ALTER TABLE "pos_role_permissions" ADD COLUMN "permission_key__wk" bytea; -- key of permission_key (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__pos_role_permissions__PRIMARY" ON "pos_role_permissions" ("restaurant_id", "role_slug__wk", "permission_key__wk");
ALTER TABLE "restaurant_ai_provider_keys" ADD COLUMN "provider__wk" bytea; -- key of provider (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__restaurant_ai_provider_keys__PRIMARY" ON "restaurant_ai_provider_keys" ("restaurant_id", "provider__wk");
ALTER TABLE "schema_migrations" ADD COLUMN "id__wk" bytea; -- key of id (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__schema_migrations__PRIMARY" ON "schema_migrations" ("id__wk");
ALTER TABLE "site_builder_assets" ADD COLUMN "id__wk" bytea; -- key of id (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__site_builder_assets__PRIMARY" ON "site_builder_assets" ("id__wk");
ALTER TABLE "site_builder_bindings" ADD COLUMN "id__wk" bytea; -- key of id (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__site_builder_bindings__PRIMARY" ON "site_builder_bindings" ("id__wk");
ALTER TABLE "site_builder_collections" ADD COLUMN "id__wk" bytea; -- key of id (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__site_builder_collections__PRIMARY" ON "site_builder_collections" ("id__wk");
ALTER TABLE "site_builder_domain_mappings" ADD COLUMN "id__wk" bytea; -- key of id (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__site_builder_domain_mappings__PRIMARY" ON "site_builder_domain_mappings" ("id__wk");
ALTER TABLE "site_builder_pages" ADD COLUMN "id__wk" bytea; -- key of id (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__site_builder_pages__PRIMARY" ON "site_builder_pages" ("id__wk");
ALTER TABLE "site_builder_publish_queue" ADD COLUMN "id__wk" bytea; -- key of id (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__site_builder_publish_queue__PRIMARY" ON "site_builder_publish_queue" ("id__wk");
ALTER TABLE "site_builder_sites" ADD COLUMN "id__wk" bytea; -- key of id (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__site_builder_sites__PRIMARY" ON "site_builder_sites" ("id__wk");
ALTER TABLE "site_builder_versions" ADD COLUMN "id__wk" bytea; -- key of id (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__site_builder_versions__PRIMARY" ON "site_builder_versions" ("id__wk");
ALTER TABLE "stock_role_permissions" ADD COLUMN "role_slug__wk" bytea; -- key of role_slug (utf8mb4_unicode_ci)
ALTER TABLE "stock_role_permissions" ADD COLUMN "permission_key__wk" bytea; -- key of permission_key (utf8mb4_unicode_ci)
CREATE UNIQUE INDEX "ixwk__stock_role_permissions__PRIMARY" ON "stock_role_permissions" ("restaurant_id", "role_slug__wk", "permission_key__wk");
ROLLBACK; -- proposal: never COMMIT from this file
