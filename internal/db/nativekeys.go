package db

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
)

// Coordination id: pgnative_keys_v1 (pgnative-B). Native PostgreSQL key
// gate: every originally UNIQUE text index is a UNIQUE expression index over
// app.norm / app.norm_ascii (docs/pgnative/native-ddl-inventory.tsv, generated
// by dbmigrate -mode native-inventory). Read-only catalog checks; a business
// DB that misses one, or still carries the old MySQL emulation objects
// (__wk/__pad columns, dbmigrate collations), is refused.

// NativeKeyIndex is one expected ux__ index: Expr is the key list as in the DDL.
type NativeKeyIndex struct{ Table, Index, Expr string }

var RequiredNativeKeyIndexes = []NativeKeyIndex{
	{"POSTRES", "ux__POSTRES__uq_postres_restaurant_ui_data", "\"restaurant_id\", app.norm_ascii(\"ui_data_id\")"},
	{"accounting_exports", "ux__accounting_exports__uq_accounting_export_hash", "\"restaurant_id\", \"export_type\", \"period_from\", \"period_to\", app.norm_ascii(\"payload_hash\")"},
	{"admin_sessions", "ux__admin_sessions__uniq_admin_sessions_session_id", "app.norm_ascii(\"session_id\")"},
	{"ai_image_models", "ux__ai_image_models__uniq_ai_image_models_provider_slug", "app.norm_ascii(\"provider_slug\"), app.norm_ascii(\"slug\")"},
	{"ai_image_providers", "ux__ai_image_providers__uniq_ai_image_providers_slug", "app.norm_ascii(\"slug\")"},
	{"analytics_customer_sources", "ux__analytics_customer_sources__uq_analytics_customer_source", "\"restaurant_id\", app.norm(\"source_type\"), app.norm(\"source_ref\")"},
	{"analytics_customers", "ux__analytics_customers__uq_analytics_customers_identity", "\"restaurant_id\", app.norm_ascii(\"identity_key\")"},
	{"analytics_stock_facts", "ux__analytics_stock_facts__uq_analytics_stock_fact_source", "\"restaurant_id\", app.norm(\"source_type\"), \"source_id\""},
	{"app_settings", "ux__app_settings__uniq_app_settings_k", "app.norm_ascii(\"k\")"},
	{"assistant_memory_edges", "ux__assistant_memory_edges__uq_edge", "\"from_node\", \"to_node\", app.norm(\"rel\")"},
	{"assistant_memory_nodes", "ux__assistant_memory_nodes__uq_memory_scope_label", "\"restaurant_id\", \"user_id\", app.norm(\"label\")"},
	{"assistant_sessions", "ux__assistant_sessions__uq_assistant_sessions_token", "app.norm_ascii(\"session_token\")"},
	{"bo_member_invitation_tokens", "ux__bo_member_invitation_tokens__uniq_bo_member_invita_fb789f77", "app.norm_ascii(\"onboarding_guid\")"},
	{"bo_member_invitation_tokens", "ux__bo_member_invitation_tokens__uniq_bo_member_invita_3228cb22", "app.norm_ascii(\"token_sha256\")"},
	{"bo_password_reset_tokens", "ux__bo_password_reset_tokens__uniq_bo_password_reset_t_2c2d611a", "app.norm_ascii(\"token_sha256\")"},
	{"bo_sessions", "ux__bo_sessions__uniq_bo_sessions_token", "app.norm_ascii(\"token_sha256\")"},
	{"bo_users", "ux__bo_users__uniq_bo_users_email", "app.norm_ascii(\"email\")"},
	{"bo_users", "ux__bo_users__uniq_bo_users_username", "app.norm(\"username\")"},
	{"booking_checkouts", "ux__booking_checkouts__uq_booking_checkouts_public", "app.norm_ascii(\"public_id\")"},
	{"booking_reminder_deliveries", "ux__booking_reminder_deliveries__uq_booking_reminder_key", "app.norm_ascii(\"delivery_key\")"},
	{"bot_inbound_queue", "ux__bot_inbound_queue__uq_inbound_msg", "\"restaurant_id\", app.norm_ascii(\"message_id\")"},
	{"campaign_bounce_events", "ux__campaign_bounce_events__uq_bounce_event", "\"restaurant_id\", app.norm_ascii(\"event_key\")"},
	{"campaign_recipients", "ux__campaign_recipients__uq_campaign_target", "\"campaign_id\", app.norm(\"channel\"), app.norm(\"target\")"},
	{"campaign_suppressions", "ux__campaign_suppressions__uq_suppression", "\"restaurant_id\", app.norm(\"channel\"), app.norm(\"target\")"},
	{"campaigns", "ux__campaigns__uq_campaign_coord", "app.norm_ascii(\"coord_id\")"},
	{"chatgpt_plugin_tokens", "ux__chatgpt_plugin_tokens__uq_chatgpt_plugin_tokens_hash", "app.norm_ascii(\"token_hash\")"},
	{"comida_bebida_categories", "ux__comida_bebida_categories__uniq_comida_bebida_categ_a3a807da", "\"restaurant_id\", app.norm_ascii(\"slug\")"},
	{"comida_bulk_link_batches", "ux__comida_bulk_link_batches__uq_comida_bulk_link_idem", "\"restaurant_id\", app.norm_ascii(\"idempotency_key\")"},
	{"comida_categories", "ux__comida_categories__uniq_comida_categories_restaura_3bf90644", "\"restaurant_id\", app.norm(\"food_type\"), app.norm_ascii(\"slug\")"},
	{"comida_items", "ux__comida_items__uq_comida_items_restaurant_ui_data", "\"restaurant_id\", app.norm_ascii(\"ui_data_id\")"},
	{"comida_plato_categories", "ux__comida_plato_categories__uniq_comida_plato_categor_09c635ef", "\"restaurant_id\", app.norm_ascii(\"slug\")"},
	{"discount_tokens", "ux__discount_tokens__idx_token", "app.norm_ascii(\"token\")"},
	{"dish_translations", "ux__dish_translations__uniq_dish_translation", "\"restaurant_id\", app.norm(\"entity_type\"), \"entity_id\", app.norm(\"field_name\"), app.norm(\"lang\")"},
	{"forky_confirmation_tokens", "ux__forky_confirmation_tokens__uq_forky_conf_token_hash", "app.norm_ascii(\"token_hash\")"},
	{"group_menu_section_dishes_v2", "ux__group_menu_section_dishes_v2__uq_group_menu_dishes_59909d62", "\"restaurant_id\", app.norm_ascii(\"ui_data_id\")"},
	{"invoice_wa_sends", "ux__invoice_wa_sends__uq_invoice_wa_sends_key", "\"restaurant_id\", app.norm_ascii(\"idempotency_key\")"},
	{"legal_pages", "ux__legal_pages__uniq_legal_pages_restaurant_slug", "\"restaurant_id\", app.norm_ascii(\"slug\")"},
	{"mcp_oauth_clients", "ux__mcp_oauth_clients__uq_mcp_oauth_clients_client_id", "app.norm_ascii(\"client_id\")"},
	{"mcp_oauth_codes", "ux__mcp_oauth_codes__uq_mcp_oauth_codes_hash", "app.norm_ascii(\"code_hash\")"},
	{"mcp_oauth_tokens", "ux__mcp_oauth_tokens__uq_mcp_oauth_tokens_hash", "app.norm_ascii(\"token_hash\")"},
	{"member_time_allocations", "ux__member_time_allocations__uq_member_time_allocation_7756dbb9", "\"restaurant_id\", app.norm_ascii(\"idempotency_key\")"},
	{"menu_visibility", "ux__menu_visibility__uniq_menu_visibility_restaurant_key", "\"restaurant_id\", app.norm_ascii(\"menu_key\")"},
	{"menu_weekday_availability", "ux__menu_weekday_availability__uq_menu_weekday_availability", "\"restaurant_id\", \"menu_id\", app.norm(\"weekday\")"},
	{"menus", "ux__menus__idx_menus_restaurant_seo_slug", "\"restaurant_id\", app.norm_ascii(\"seo_slug\")"},
	{"message_deliveries", "ux__message_deliveries__uniq_message_deliveries_delivery_key", "app.norm_ascii(\"delivery_key\")"},
	{"migration_runs", "ux__migration_runs__run_key", "app.norm_ascii(\"run_key\")"},
	{"no_marketing", "ux__no_marketing__idx_no_marketing_phone", "app.norm_ascii(\"contact_phone\")"},
	{"pos_cash_closures", "ux__pos_cash_closures__uq_pos_cash_closure_idempotency", "\"restaurant_id\", app.norm_ascii(\"idempotency_key\")"},
	{"pos_cash_movements", "ux__pos_cash_movements__uq_pos_cash_movement_idempotency", "\"restaurant_id\", app.norm_ascii(\"idempotency_key\")"},
	{"pos_cover_adjustments", "ux__pos_cover_adjustments__uq_pos_cover_adjustment_idempotency", "\"restaurant_id\", app.norm_ascii(\"idempotency_key\")"},
	{"pos_customers", "ux__pos_customers__uq_pos_customers_email", "\"restaurant_id\", app.norm_ascii(\"email\")"},
	{"pos_customers", "ux__pos_customers__uq_pos_customers_phone", "\"restaurant_id\", app.norm(\"phone\")"},
	{"pos_drawer_events", "ux__pos_drawer_events__uq_pos_drawer_event_idempotency", "\"restaurant_id\", app.norm_ascii(\"idempotency_key\")"},
	{"pos_fiscal_documents", "ux__pos_fiscal_documents__uq_pos_fiscal_documents_full", "\"restaurant_id\", app.norm(\"full_number\"), \"copy_number\""},
	{"pos_fiscal_series", "ux__pos_fiscal_series__uq_pos_fiscal_series_scope", "\"restaurant_id\", app.norm_ascii(\"terminal_key\"), \"series_type\""},
	{"pos_kitchen_dispatches", "ux__pos_kitchen_dispatches__uq_pos_kitchen_dispatch_command", "\"restaurant_id\", \"station_id\", app.norm_ascii(\"command_key\")"},
	{"pos_kitchen_stations", "ux__pos_kitchen_stations__uq_pos_kitchen_station_name", "\"restaurant_id\", app.norm(\"name\")"},
	{"pos_modifier_groups", "ux__pos_modifier_groups__uq_pos_modifier_group_name", "\"restaurant_id\", app.norm(\"name\")"},
	{"pos_modifier_options", "ux__pos_modifier_options__uq_pos_modifier_option_name", "\"restaurant_id\", \"group_id\", app.norm(\"name\")"},
	{"pos_packs", "ux__pos_packs__uq_pos_pack_name", "\"restaurant_id\", app.norm(\"name\")"},
	{"pos_payments", "ux__pos_payments__uq_pos_payment_idempotency", "\"restaurant_id\", app.norm_ascii(\"idempotency_key\")"},
	{"pos_payments", "ux__pos_payments__uq_pos_payment_provider_ref", "\"restaurant_id\", app.norm(\"provider\"), app.norm(\"provider_reference\")"},
	{"pos_product_categories", "ux__pos_product_categories__uq_pos_product_category_name", "\"restaurant_id\", app.norm(\"name\")"},
	{"pos_products", "ux__pos_products__uq_pos_product_sku", "\"restaurant_id\", app.norm(\"sku\")"},
	{"pos_refunds", "ux__pos_refunds__uq_pos_refund_idempotency", "\"restaurant_id\", app.norm_ascii(\"idempotency_key\")"},
	{"pos_service_periods", "ux__pos_service_periods__uq_pos_service_period_name", "\"restaurant_id\", app.norm(\"name\")"},
	{"pos_shifts", "ux__pos_shifts__uq_pos_shift_open_terminal", "\"restaurant_id\", app.norm_ascii(\"open_terminal_key\")"},
	{"pos_stock_exceptions", "ux__pos_stock_exceptions__uq_pos_stock_exception_replay", "\"restaurant_id\", app.norm_ascii(\"replay_idempotency_key\")"},
	{"pos_tags", "ux__pos_tags__uq_pos_tag_name", "\"restaurant_id\", app.norm(\"name\")"},
	{"pos_ticket_adjustments", "ux__pos_ticket_adjustments__uq_pos_ticket_adjustment_i_791a04bf", "\"restaurant_id\", app.norm_ascii(\"idempotency_key\")"},
	{"pos_ticket_lines", "ux__pos_ticket_lines__uq_pos_ticket_line_idempotency", "\"restaurant_id\", app.norm_ascii(\"idempotency_key\")"},
	{"pos_tickets", "ux__pos_tickets__uq_pos_ticket_checkout", "\"restaurant_id\", app.norm_ascii(\"checkout_idempotency_key\")"},
	{"pos_tickets", "ux__pos_tickets__uq_pos_ticket_creation", "\"restaurant_id\", app.norm_ascii(\"creation_idempotency_key\")"},
	{"pos_tickets", "ux__pos_tickets__uq_pos_ticket_number", "\"restaurant_id\", app.norm(\"ticket_number\")"},
	{"pos_visit_merges", "ux__pos_visit_merges__uq_pos_visit_merges_restaurant_key", "\"restaurant_id\", app.norm_ascii(\"idempotency_key\")"},
	{"pos_visits", "ux__pos_visits__uq_pos_visit_open_idempotency", "\"restaurant_id\", app.norm_ascii(\"open_idempotency_key\")"},
	{"restaurant_areas", "ux__restaurant_areas__uniq_restaurant_areas_restaurant_name", "\"restaurant_id\", app.norm(\"name\")"},
	{"restaurant_beverage_options", "ux__restaurant_beverage_options__uq_restaurant_beverag_3b105e08", "\"restaurant_id\", app.norm_ascii(\"slug\")"},
	{"restaurant_booking_extras", "ux__restaurant_booking_extras__uq_restaurant_booking_e_3b0fedbe", "\"restaurant_id\", app.norm_ascii(\"slug\")"},
	{"restaurant_domains", "ux__restaurant_domains__uniq_restaurant_domains_domain", "app.norm_ascii(\"domain\")"},
	{"restaurant_floors", "ux__restaurant_floors__uniq_restaurant_floors_scope", "\"restaurant_id\", \"floor_number\", app.norm_ascii(\"scope_key\")"},
	{"restaurant_members", "ux__restaurant_members__uniq_restaurant_members_restau_626b2cd2", "\"restaurant_id\", app.norm_ascii(\"email\")"},
	{"restaurant_salons", "ux__restaurant_salons__uniq_restaurant_salons_scope", "\"restaurant_id\", \"floor_id\", app.norm(\"name\"), app.norm_ascii(\"scope_key\")"},
	{"restaurant_stripe_connect", "ux__restaurant_stripe_connect__uq_restaurant_stripe_co_3c39895c", "app.norm_ascii(\"account_hash\")"},
	{"restaurant_tables", "ux__restaurant_tables__idx_restaurant_numero", "\"restaurant_id\", app.norm(\"numero_mesa\")"},
	{"restaurant_tables", "ux__restaurant_tables__uniq_restaurant_tables_restaurant_name", "\"restaurant_id\", app.norm(\"name\")"},
	{"restaurant_tables", "ux__restaurant_tables__uniq_restaurant_tables_restaurant_numero", "\"restaurant_id\", app.norm(\"numero_mesa\")"},
	{"restaurant_uazapi_instances", "ux__restaurant_uazapi_instances__uniq_restaurant_uazap_6bdb2e7b", "app.norm(\"instance_name\")"},
	{"restaurants", "ux__restaurants__uniq_restaurants_slug", "app.norm_ascii(\"slug\")"},
	{"seo_item_routes", "ux__seo_item_routes__uniq_seo_item_route", "\"restaurant_id\", app.norm(\"entity_type\"), \"entity_id\""},
	{"seo_item_routes", "ux__seo_item_routes__uniq_seo_item_slug", "\"restaurant_id\", app.norm(\"entity_type\"), app.norm_ascii(\"slug\")"},
	{"site_builder_collections", "ux__site_builder_collections__uk_site_name", "app.norm_ascii(\"site_id\"), app.norm(\"name\")"},
	{"site_builder_component_registry", "ux__site_builder_component_registry__type", "app.norm(\"type\")"},
	{"site_builder_domain_mappings", "ux__site_builder_domain_mappings__uk_domain", "app.norm_ascii(\"domain\")"},
	{"site_builder_pages", "ux__site_builder_pages__uk_site_slug", "app.norm_ascii(\"site_id\"), app.norm_ascii(\"slug\")"},
	{"site_builder_sites", "ux__site_builder_sites__uk_custom_domain", "app.norm_ascii(\"custom_domain\")"},
	{"site_builder_sites", "ux__site_builder_sites__uk_subdomain", "app.norm_ascii(\"subdomain\")"},
	{"site_builder_versions", "ux__site_builder_versions__uk_site_version", "app.norm_ascii(\"site_id\"), \"version_number\""},
	{"stock_categories", "ux__stock_categories__uq_stock_category_name", "\"restaurant_id\", app.norm(\"name\")"},
	{"stock_document_scans", "ux__stock_document_scans__uq_stock_document_hash", "\"restaurant_id\", app.norm_ascii(\"file_hash\")"},
	{"stock_item_units", "ux__stock_item_units__uq_stock_item_unit_code", "\"restaurant_id\", \"stock_item_id\", app.norm(\"code\")"},
	{"stock_items", "ux__stock_items__uq_stock_item_sku", "\"restaurant_id\", app.norm(\"sku\")"},
	{"stock_margin_scopes", "ux__stock_margin_scopes__uq_stock_margin_scope", "\"restaurant_id\", \"scope_kind\", app.norm_ascii(\"scope_key\")"},
	{"stock_movements", "ux__stock_movements__uq_stock_movement_idempotency", "\"restaurant_id\", app.norm_ascii(\"idempotency_key\")"},
	{"stock_production_orders", "ux__stock_production_orders__uq_stock_production_idempotency", "\"restaurant_id\", app.norm_ascii(\"idempotency_key\")"},
	{"stock_recipe_step_image_jobs", "ux__stock_recipe_step_image_jobs__uq_step_image_job_idem", "\"restaurant_id\", app.norm_ascii(\"idempotency_key\")"},
	{"stock_supplier_aliases", "ux__stock_supplier_aliases__uq_stock_supplier_alias", "\"restaurant_id\", app.norm(\"supplier_name\"), app.norm(\"supplier_code\"), app.norm(\"normalized_description\")"},
	{"stock_suppliers", "ux__stock_suppliers__uq_stock_suppliers", "\"restaurant_id\", app.norm(\"name\")"},
	{"stock_vat_rates", "ux__stock_vat_rates__uq_stock_vat_name", "\"restaurant_id\", app.norm(\"name\")"},
	{"stock_warehouses", "ux__stock_warehouses__uq_stock_warehouse_code", "\"restaurant_id\", app.norm(\"code\")"},
	{"stripe_events", "ux__stripe_events__uniq_stripe_events_event_id", "app.norm_ascii(\"event_id\")"},
	{"uazapi_servers", "ux__uazapi_servers__uniq_uazapi_servers_base_url", "app.norm_ascii(\"base_url\")"},
	{"user_menu_preferences", "ux__user_menu_preferences__uq_user_menu_pref", "\"user_id\", \"restaurant_id\", \"menu_id\", app.norm_ascii(\"pref_key\")"},
	{"user_preferences", "ux__user_preferences__uq_user_pref", "\"user_id\", \"restaurant_id\", app.norm_ascii(\"pref_key\")"},
	{"website_components", "ux__website_components__uk_component_type", "app.norm(\"component_type\")"},
	{"website_pages", "ux__website_pages__uk_website_slug", "\"website_id\", app.norm_ascii(\"slug\")"},
	{"website_templates", "ux__website_templates__slug", "app.norm_ascii(\"slug\")"},
}

var normDefRE = regexp.MustCompile(`::[a-z_ ]+(\([0-9, ]+\))?|"|\(|\)|\s+|\bpublic\.`)

// NormIndexDef reduces an index key list (DDL or pg_get_indexdef rendering)
// to a comparable form: no quotes, parentheses, ::type casts, spaces or
// public. schema prefix.
func NormIndexDef(s string) string {
	if i := strings.Index(s, " USING btree "); i >= 0 {
		s = s[i+len(" USING btree "):]
	}
	return strings.ToLower(normDefRE.ReplaceAllString(s, ""))
}

// RowQuerier is *sql.DB or *sql.Tx.
type RowQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// CheckNativeKeyIndex: exists in current_schema(), unique, valid, ready,
// non-partial, on the expected table, same normalised key list. "" when OK.
func CheckNativeKeyIndex(ctx context.Context, q RowQuerier, ix NativeKeyIndex) string {
	var def, table string
	var unique, valid, ready, partial bool
	err := q.QueryRowContext(ctx, pgOnlySQL(`SELECT pg_get_indexdef(i.indexrelid), t.relname, i.indisunique, i.indisvalid, i.indisready, i.indpred IS NOT NULL
		FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid JOIN pg_class t ON t.oid = i.indrelid JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = current_schema() AND c.relname = $1`), ix.Index).Scan(&def, &table, &unique, &valid, &ready, &partial)
	switch {
	case err == sql.ErrNoRows:
		return "missing"
	case err != nil:
		return err.Error()
	case table != ix.Table:
		return "on table " + table
	case !unique || !valid || !ready || partial:
		return fmt.Sprintf("unique=%v valid=%v ready=%v partial=%v", unique, valid, ready, partial)
	case NormIndexDef(def) != NormIndexDef(ix.Expr):
		return "key differs"
	}
	return ""
}

// RequireNativeKeys is the PG startup gate replacing the PAD/shadow/__wk gates.
func RequireNativeKeys(ctx context.Context, db *sql.DB) error {
	var n int
	if err := db.QueryRowContext(ctx, pgOnlySQL(`SELECT count(*) FROM pg_attribute a JOIN pg_class c ON c.oid = a.attrelid JOIN pg_namespace ns ON ns.oid = c.relnamespace
		LEFT JOIN pg_collation co ON co.oid = a.attcollation LEFT JOIN pg_namespace cn ON cn.oid = co.collnamespace
		WHERE ns.nspname = current_schema() AND a.attnum > 0 AND NOT a.attisdropped
		  AND (a.attname LIKE '%\_\_wk' OR a.attname LIKE '%\_\_pad' OR cn.nspname = 'dbmigrate')`)).Scan(&n); err != nil {
		return fmt.Errorf("postgres business DB refused: native key gate: %w", err)
	}
	if n != 0 {
		return fmt.Errorf("postgres business DB refused: %d columns still use the MySQL emulation (__wk/__pad or dbmigrate collation)", n)
	}
	var imm int
	if err := db.QueryRowContext(ctx, pgOnlySQL(`SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace WHERE n.nspname = 'app' AND p.proname IN ('norm', 'norm_ascii') AND p.provolatile = 'i'`)).Scan(&imm); err != nil || imm != 2 {
		return fmt.Errorf("postgres business DB refused: app.norm/app.norm_ascii missing or not IMMUTABLE (%d, %v)", imm, err)
	}
	for _, ix := range RequiredNativeKeyIndexes {
		if why := CheckNativeKeyIndex(ctx, db, ix); why != "" {
			return fmt.Errorf("postgres business DB refused: native unique index %s on %s: %s", ix.Index, ix.Table, why)
		}
	}
	return nil
}
