# result-repair-vitess10

Model: claude-merge/claude-opus-5-5-acc2 (low).
- Source and build only. No tests or Playwright, no SQL writes, no activation, no network.
- go.mod and go.sum are unchanged.
- `go build -p 2 ./...` OK with /home/linuxbrew/.linuxbrew/bin/go.

## 0. Flag moved to a shared place
- The `VC_PG_WK_KEYS` flag now lives in `internal/mysqlcoll/flag.go`: `WKFlag`, `SetWKFlag`, `KeyArgs`, and `keyText` for string, *string, int and nil values.
- The api `wkOn` and `SetWKKeys` delegate to it.
- New `internal/analytics/wk_keys.go` gives analytics the same `wkOn`, `wkPG` and `wkKeyArgs`, using its own dialect.
- Flag off: SQL text and args are byte-identical to before.

## 1. Writers: a mechanical generator wired 21 PG inserts
- The generator is `/tmp/wkgen.py`, kept outside the worktree. It parses the PG INSERT literal:
  - the column list, then the VALUES or SELECT tokens;
  - it asserts one token per column;
  - it maps each key column to its placeholder ordinal, or to a literal.
- It then:
  - appends `<col>__wk` columns and `?`;
  - rewrites the ON CONFLICT rtrim/ci_ai key expressions to `<col>__wk`;
  - adds `col__wk = EXCLUDED.col__wk` wherever `col = EXCLUDED.col` is set;
  - passes the matching Go argument expression to `wkKeyArgs`.
- It refuses VALUES forms that have placeholders after VALUES, and slice-variadic args.

**Wired:**
- **analytics/service.go:** analytics_customers.identity_key; analytics_customer_sources (source_ref, source_type); analytics_stock_facts.source_type.
- **backoffice_premium.go:** restaurant_areas.name (3 variants); restaurant_tables name and numero_mesa (9 variants) plus name only (1).
- **comida.go:** comida_plato_categories.slug (339, 443, 705); comida_bebida_categories.slug (363).
  - Manual correction: at 443 and 705 the generator had mistaken `sqldb.InsertID`'s RETURNING-expression parameter for a bind arg. I fixed both to `[]any{slug}` with the correct position.
- **site_builder.go:1744:** site_builder_domain_mappings (domain, id).

**Not wired:**
- site_builder_registrar.go:303 and instatic_manager.go:948: `mapArgs...` slice args (mapping domain mappings to sites), which need a manual edit.
- comida.go:2404: a different call shape.
- restaurant_areas and restaurant_tables UPDATE variants (premium.go:2234-2896): these are UPDATE statements, not dynamic builders, and need a SET-item edit per variant.

## 2. Dynamic SET builders: `wkSetItems(table, sets, args)` in wk_keys.go
- With the flag on, each SET item on a registered column (`col = ?` or `col = NULL`) gets `col__wk = ?` or `col__wk = NULL` right after it, with the key argument right after the column's argument. The set arguments precede the WHERE arguments in every builder (checked).
- Any other shape on a registered column, or a key error, adds an item naming a non-existent column (`wk_key_error__`), so the statement fails closed.
- Applied as one line before the query build at all 8 builder sites:
  - backoffice_postres.go: POSTRES. The builder never sets ui_data_id, so it is a no-op, and pgChangedRowsPredicate is unaffected;
  - backoffice_uazapi_servers_admin.go: base_url;
  - site_builder.go, sites: subdomain and custom_domain;
  - site_builder.go, pages: slug and site_id;
  - site_builder.go, bindings: no registered SET column (id is in WHERE only);
  - backoffice_platform.go, restaurants: slug is not in its SET list, so no-op;
  - backoffice_platform.go, bo_users: email and username are not set, so no-op;
  - website_builder_handlers.go: website_pages is not registered, so no-op.

## 3. LIKE sites (same flag)
- **Wired: backoffice_pos_customers.go search.**
  - display_name, email and tax_id use the escaped `%q%` pattern (backslash escape, the same as the source).
  - The phone `%digits%` branch is a separate candidate pass, combined as OR.
  - Result: an id set, then `c.id = ANY(?)`; 422 on too broad.
- LIKE sites wired in total: 5 (postres, stock list, comida postres, invoices list, POS customers).
- **Reclassified:** backoffice_pos.go:689 `ticket_number LIKE 'PREFIX-YYYYMMDD-%'` is a generated ASCII prefix (group B). No collation wiring is needed.
- **Not done:**
  - assistant catalog (per-table candidates);
  - invoices 782/786 (bookings search inside the invoice handler, with a dynamic query);
  - POS reservations 31;
  - checkcancel 126/139;
  - the rest of the vitess8 list.

## 4. The 11 "needs reading" rows (read)
- **10 are not key writers.** Their SETs do not touch a registered column: pos_visits parked_*, pos_ticket_lines comp, comida_items ai_generating, message_deliveries status, group_menu_section_dishes_v2 active, campaign_recipients status/error ×2, POSTRES insert/update/active. The POSTRES INSERT at admin_postres.go:203 sets NUM and DESCRIPCION but not ui_data_id.
- **1, internal/db/migrations/migrations.go:83:** `mysqlOnlySQL` schema_migrations bookkeeping (MySQL migrations only), not PG runtime. No wiring.

## Coverage
Columns with every known PG INSERT wired:
- restaurant_domains.domain
- restaurant_uazapi_instances.instance_name
- campaign_suppressions (channel, target)
- analytics identity_key, source_ref and source_type (on both tables)
- comida slug, partly: one bebida site is missing
- restaurant_areas.name, insert side only
- restaurant_tables (name, numero_mesa), insert side only
- site_builder_domain_mappings, partly

This is still far from all 149 columns, and the UPDATE paths are partial. No column is claimed complete across INSERT and UPDATE unless stated.

## Inventory r117
2532 sites: 2511 native, unchanged.

## Gates
- `__wk` DDL unapplied; partial writer coverage; LIKE 5 sites.
- Sort parity; provisional bounds.
- GPL/provenance still open.
- Shadow FK.
- PG disabled, checkout 503, 0 handler QA, 1117 pending, 731 orphans, R5 untouched.
