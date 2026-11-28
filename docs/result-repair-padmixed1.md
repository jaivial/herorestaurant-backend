# Result: repair-padmixed1 (mixed raw/trim keys + 4 human-text keys; code-only, nothing applied)

## Exact keys (schema.json; all utf8mb4_unicode_ci = PAD SPACE per local derived metadata). 7 tables, 9 unique indexes
| # | index | key order | nullable | known writers' trim |
|---|---|---|---|---|
| 1 | pos_cash_closures.uq_pos_cash_closure_idempotency | restaurant_id, idempotency_key varchar(120) | NN | client keys trimmed; generated cash-day-close:/legacy-shift-close: |
| 2 | pos_ticket_lines.uq_pos_ticket_line_idempotency | restaurant_id, idempotency_key varchar(120) | NN | raw pos.go:1105/1238; trimmed line_move/courses; generated compKey |
| 3 | stock_item_units.uq_stock_item_unit_code | restaurant_id, stock_item_id, code varchar(48) | NN | raw stock.go:698, technical sheets/import/backfill; TrimSpace stock.go:861 |
| 4 | restaurant_areas.uniq_restaurant_areas_restaurant_name | restaurant_id, name varchar(128) | NN | premium.go:2148-2166 |
| 5-7 | restaurant_tables: uniq_…_name (name varchar(255) NULL), uniq_…numero and idx_restaurant_numero (numero_mesa varchar(32); two duplicate unique indexes in the source) | restaurant_id, col | name NULL | numero TrimSpace premium.go:2341 |
| 8 | site_builder_pages.uk_site_slug | site_id varchar(36), slug varchar(255) (BOTH text, no restaurant_id; tenant is via site) | NN | site_builder.go:304/588/729 |
| 9 | stock_supplier_aliases.uq_stock_supplier_alias | restaurant_id, supplier_name(180), supplier_code(100), normalized_description(255) | NN | name/code TrimSpace (suppliers.go:81/278); description = stockAliasDescription (lower+TrimSpace) |

Writers that trim and writers that store raw both stay as they are. Nothing new is normalized and no stored value is trimmed.

## Required future DDL (NOT executed; additive; raw R5 indexes kept until QA)
```
CREATE UNIQUE INDEX "ixpad__pos_cash_closures__uq_pos_cash_closure_idempotency" ON "pos_cash_closures" ("restaurant_id", (rtrim("idempotency_key", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__pos_ticket_lines__uq_pos_ticket_line_idempotency" ON "pos_ticket_lines" ("restaurant_id", (rtrim("idempotency_key", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__stock_item_units__uq_stock_item_unit_code" ON "stock_item_units" ("restaurant_id", "stock_item_id", (rtrim("code", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__restaurant_areas__uniq_restaurant_areas_restaurant_name" ON "restaurant_areas" ("restaurant_id", (rtrim("name", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__restaurant_tables__uniq_restaurant_tables_resta_4d031b62" ON "restaurant_tables" ("restaurant_id", (rtrim("name", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__restaurant_tables__uniq_restaurant_tables_resta_efc1635a" ON "restaurant_tables" ("restaurant_id", (rtrim("numero_mesa", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__restaurant_tables__idx_restaurant_numero" ON "restaurant_tables" ("restaurant_id", (rtrim("numero_mesa", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__site_builder_pages__uk_site_slug" ON "site_builder_pages" ((rtrim("site_id", ' ')) COLLATE "dbmigrate"."ci_ai", (rtrim("slug", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__stock_supplier_aliases__uq_stock_supplier_alias" ON "stock_supplier_aliases" ("restaurant_id", (rtrim("supplier_name", ' ')) COLLATE "dbmigrate"."ci_ai", (rtrim("supplier_code", ' ')) COLLATE "dbmigrate"."ci_ai", (rtrim("normalized_description", ' ')) COLLATE "dbmigrate"."ci_ai");
```
- The two numero_mesa indexes reproduce the source duplicate; retiring one needs review.
- A NULL restaurant_tables.name stays distinct.
- Collision probes are still pending.

## Caller catalogue and treatment (PG text only; MySQL text unchanged)
| site | operation | treatment |
|---|---|---|
| backoffice_pos_cash.go:488, :541 | closure replay lookup | PATCHED: pad expression scoped by restaurant_id |
| pos_cash.go:536, cash_days.go:435 | INSERT closure | raw; "duplicate" substring (source) |
| backoffice_pos.go:1105/1140/1238, line_move.go:90, recall.go:183/235 | INSERT ticket lines | raw; "duplicate" substring handling (source); no key lookup exists |
| stock.go:698/861, stock_import.go:401, technical_sheet_{duplicate:69,ensure:115,sheets:157}, cmd/comida-stock-backfill:241 | INSERT units | raw / mixed (source); errors propagate; no code lookup exists |
| premium.go:2148-2166 / 2223-2259 | INSERT / UPDATE areas (WHERE id) | raw; no name lookup exists |
| premium.go:2350, :2552 | numero_mesa duplicate pre-check | PATCHED: pad expression |
| premium.go:2401-2475 / 2664-2854 / 2885 | INSERT / UPDATE tables (WHERE id) | raw |
| site_builder.go:304/588/729 + uk_site_slug substring :596/:732 | INSERT / UPDATE pages | raw; the substring check already matches the ixpad name |
| site_builder.go:509/620/1082/1514, whatsapp_bot_admin_reads.go:843 | page reads | by site_id/id only; no slug lookup in Go (public routing does not query pages by slug) |
| stock_documents.go:161 | OCR alias match | PATCHED: pad on name, code and description, plus the ORDER BY code-match rank |
| stock_documents.go:686, stock_suppliers.go:300 | upsert ON CONFLICT | PATCHED: the target is the identical pad expression list |
| stock_suppliers.go:306 | kept-id readback | PATCHED |
| stock_suppliers.go:315 / :319 | DELETE by supplier_name | PATCHED |
| stock_suppliers.go:206 | alias list by supplier_name | PATCHED |
| stock_suppliers.go:31-32 | per-supplier alias counts (sa.supplier_name=sup.name, both PAD columns) | PATCHED on both sides |

None of these keys has an IsUniqueViolationOn call (repo grep).

## Source bug fix: stockAliasDescription
- **Bug:** the source cut at 255 BYTES with value[:255], which can split a UTF-8 sequence. MySQL strict mode (1366) and PG (22021) both reject that, so such an alias could neither be saved nor matched.
- **Fix:** keep the 255-byte bound but back off to a rune start.
- **Old keys:** every key the source could ever store (a valid cut) is reproduced byte-identically.
- **Changed inputs:** only inputs whose 255th byte fell inside a multibyte rune now get a valid key 1-3 bytes shorter instead of an error.
- **No silent identity truncation is added:** this is the existing derivation of the normalized key, not the stored user value. Both the writer and the reader use the same function.

## Gate
internal/db/padkeys.go RequiredPadKeyIndexes now has 24 entries: 15 from before plus these 9. The composite validator is unchanged and refuses on any mismatch. Its rendering regex is still unverified and needs manual QA.

## Build / inventory
- go build -p 2 ./... OK.
- Inventory r98: 2501 sites (unchanged count, since these were conversions to nativeSQL), all native, 0 portable audit failures.

## Primary-key PAD audit (no code change; design only)
18 PRIMARY keys contain PAD SPACE text; reservation_location_occupancy's PK is enum-only.

| table | PK | source writers (Go) | FK children |
|---|---|---|---|
| bo_roles | slug | backoffice_roles.go:538 | bo_member_invitation_tokens.role_slug (RESTRICT), bo_role_permissions.role_slug (CASCADE) |
| bo_role_permissions | role_slug, section_key | roles.go:547/551 | - |
| conversation_sessions | id | conversation_history.go:274/281/291 | - |
| platform_settings | setting_key | stripe_connect_fees.go:198 | - |
| pos_daily_sequences | restaurant_id, business_date, sequence_type | backoffice_pos.go:664/695 | - |
| pos_role_permissions | restaurant_id, role_slug, permission_key | pos_admin.go:220 | - |
| restaurant_ai_provider_keys | restaurant_id, provider | whatsapp_bot_ai_config.go:219/220 | - |
| schema_migrations | id | migrations.go:83 (MySQL bootstrap only) | - |
| site_builder_sites | id (uuid text) | site_builder.go:284/287/444/481/1561 | assets, bindings, collections, domain_mappings, pages, publish_queue, versions .site_id (CASCADE) |
| site_builder_pages | id | site_builder.go:304/588/729/773 | bindings.page_id (CASCADE) |
| site_builder_versions | id | site_builder.go:1119/1547 | publish_queue.version_id (CASCADE) |
| site_builder_assets / bindings / domain_mappings | id | site_builder.go / registrar / instatic | - |
| site_builder_collections, site_builder_publish_queue | id | no Go writer | - |
| stock_role_permissions | restaurant_id, role_slug, permission_key | stock_permissions.go:106/107 | - |
| whatsapp_bot_sessions | restaurant_id, user_phone | whatsapp_bot_history.go:48/53, human_takeover.go:85/90 | - |

Design (proposal only, not implemented):
- PG cannot use an expression as a PRIMARY KEY, and FKs need a plain unique or PK target, so the raw PK stays.
- PAD parity for PK equality would need a SUPPLEMENTARY unique ixpad index per PK, plus every by-key reader and upsert patched like the keys above.
- FK compatibility is the real problem. MySQL PAD SPACE lets a child 'x ' reference a parent 'x'; a PG FK on a ci_ai column does not (ICU NO PAD), so such rows fail to validate. This is part of the open 731-orphan / FK-validation gate. The value-preserving options are:
  - (a) a read-only probe proving there are no padded child or parent values, plus writers already refusing edge spaces;
  - (b) a reviewed canonical-copy migration.
- That needs a decision. No fake fix.
- Many of these PKs are server-generated (UUID ids) or constants (role/permission slugs), so the practical risk is concentrated in conversation_sessions.id, whatsapp_bot_sessions.user_phone, restaurant_ai_provider_keys.provider and platform_settings.setting_key. Their writers are not traced in this batch.

## Coverage
- Unique keys with callers patched: 24 (5 global, 10 idempotency, 9 here).
- Remaining: 92 of the 116 PAD unique-key proposals, plus the 18 PK proposals (design only).
- Runtime verification: 0.

## Gates (not claimed)
- padidem1 replay still does not compare payload (existing source flaw, recorded); there is no full payment replay QA.
- All 24 indexes are unapplied, additive, and keep the plain indexes.
- ci_ai is only an approximation of unicode_ci.
- PG server disabled; tools report-only and index-gated.
- R5 untouched; 1117 pending / 731 orphans; checkout 503; 17 legitimate MySQL-only; 0 handler QA.
- No SQL, DB writes or provider calls.

## Next
1. Coordinator review of the 24 DDL statements.
2. Read-only probes for collisions and padded FK/PK values.
3. Manual gate-rendering QA.
4. The next key group, using the padplan proposals.
