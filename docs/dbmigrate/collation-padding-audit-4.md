# Collation padding audit 4 — final 33 of 121 unique text keys (source-only)

Scope: the 33 keys in r93 TSV not covered by audits 1-3 (exact list: /tmp/sel4.tsv). Source: full-export/schema.json (MySQL metadata), Go source in this worktree. No SQL executed, no DB/schema/code changes. Coverage below is **source-only**; it is NOT full parity: imported/legacy rows, external writers and query-probe semantics (PAD SPACE equality on lookups) are not settled by any writer trim.

All 33 columns: collation utf8mb4_unicode_ci (schema.json). PAD attribute is not stored in schema.json; utf8mb4_unicode_ci is a UCA-4.0.0 collation that MySQL documents as PAD SPACE, but this audit records it as "per source collation, not re-observed".

Coordinator read-only 06:44: padded counts on global keys 0 — current data only, not a future guard.

## Disjoint statuses
- S-GEN-HEX: server-generated fixed-width hex; trailing space impossible from known writers.
- S-GEN-FMT: server-built fmt key; no user trailing space unless an embedded component carries one.
- S-GEN-COL: MySQL GENERATED column; PG must render equivalent expression.
- S-CLIENT-TRIM: client key, TrimSpace before store and lookup.
- S-CLIENT-RAW: client key stored raw (emptiness checked via TrimSpace only) → PAD SPACE gap for "k" vs "k ".
- S-CLIENT-MIXED: one path trims, another stores raw.
- S-PROVIDER: provider-issued opaque ID.
- S-ALLOWLIST: normalized to fixed allowlist/constants.
- S-NO-WRITER: no writer found repo-wide (Go/SQL/JS, excluding binaries/.git).
- S-UNTRACED: origin not fully traced — open.

| # | table.index (column) | type/null | status | evidence |
|---|---|---|---|---|
| 1 | mcp_oauth_clients.uq_mcp_oauth_clients_client_id (client_id) | varchar(128) NN | S-GEN-HEX + lookup gap | generated newBOSessionToken mcp_oauth_routes.go:120; upsert ON CONFLICT(client_id) mcp_oauth_store.go:66; authorize lookup TrimSpace routes.go:181, token lookup raw PostFormValue routes.go:272; padmeta1: no configured client_id source in internal/config or cmd/server; mcpRedirectURIAllowed store.go:76 accepts ANY unregistered client-supplied id, so the "static" id is client-chosen text |
| 2 | mcp_oauth_codes.uq_mcp_oauth_codes_hash (code_hash) | char(64) NN | S-GEN-HEX | sha256 hex insert store.go:114; CHAR padding: PG char(64) bpchar semantics differ, hex fixed 64 so no pad |
| 3 | mcp_oauth_tokens.uq_mcp_oauth_tokens_hash (token_hash) | char(64) NN | S-GEN-HEX | store.go:172 |
| 4 | member_time_allocations.uq_member_time_allocation_idempotency | varchar(120) NN | S-CLIENT-RAW | production_labour.go:203 |
| 5 | menu_visibility.uniq_menu_visibility_restaurant_key (menu_key) | varchar(50) NN | S-ALLOWLIST | constants; ON CONFLICT(restaurant_id,menu_key) backoffice_menu_visibility.go:59 |
| 6 | message_deliveries.uniq_message_deliveries_delivery_key | varchar(191) NULL | S-GEN-FMT | campaigns.go:1106, booking_whatsapp:205, pre_shift_reminders.go:53, whatsapp_outbox.go:100 (width guard) |
| 7 | migration_runs.run_key | varchar(191) NN | S-NO-WRITER | only r93 TSV references; external/legacy writer possible |
| 8 | pos_cash_closures.uq_pos_cash_closure_idempotency | varchar(120) NN | S-CLIENT-MIXED(trim)+S-GEN-FMT | client TrimSpace backoffice_pos_cash.go:358/471; generated cash-day-close: cash_days.go:434, legacy-shift-close: operations.go:515 |
| 9 | pos_cash_movements.uq_pos_cash_movement_idempotency | varchar(120) NN | S-CLIENT-TRIM | backoffice_pos_cash.go:358/471 |
| 10 | pos_cover_adjustments.uq_pos_cover_adjustment_idempotency | varchar(120) NN | S-CLIENT-RAW | checkout.go:910 |
| 11 | pos_drawer_events.uq_pos_drawer_event_idempotency | varchar(120) NN | S-CLIENT-TRIM | backoffice_pos_rail.go:390 |
| 12 | pos_kitchen_dispatches.uq_pos_kitchen_dispatch_command (command_key) | varchar(120) NN | S-CLIENT-TRIM | backoffice_pos_kitchen.go:388 TrimSpace(key)+":"+stationID |
| 13 | pos_payments.uq_pos_payment_idempotency | varchar(120) NN | S-CLIENT-RAW | assistant_pos_tools.go:83, checkout.go:356 |
| 14 | pos_refunds.uq_pos_refund_idempotency | varchar(120) NN | S-CLIENT-RAW | assistant:92, checkout.go:700 |
| 15 | pos_shifts.uq_pos_shift_open_terminal (open_terminal_key) | varchar(64) NULL GEN | S-GEN-COL | schema expr CASE status='OPEN' THEN terminal_key; padmeta1: only writer INSERT operations.go:468 TrimSpace(terminalKey), default "main"; UPDATEs (cash.go:550, cash_days.go:360/451, operations.go:511) never write terminal_key |
| 16 | pos_stock_exceptions.uq_pos_stock_exception_replay | varchar(120) NULL | S-CLIENT-RAW | reports.go:285 |
| 17 | pos_ticket_adjustments.uq_pos_ticket_adjustment_idempotency | varchar(120) NN | S-CLIENT-TRIM | rail.go:549 |
| 18 | pos_ticket_lines.uq_pos_ticket_line_idempotency | varchar(120) NN | S-CLIENT-MIXED | raw backoffice_pos.go:1105/1238; trimmed line_move :146, courses :76; compKey pos.go:1140 generated |
| 19 | pos_tickets.uq_pos_ticket_checkout (checkout_idempotency_key) | varchar(120) NULL | S-CLIENT-RAW (padmeta1) | handler raw in.IdempotencyKey checkout.go:431 (TrimSpace emptiness only), :480; batch path in.IdempotencyKey+":t:"+id :603; Go replay check exact == :319 (stricter than MySQL ci/PAD on both dialects) |
| 20 | pos_tickets.uq_pos_ticket_creation | varchar(120) NN | S-CLIENT-RAW | pos.go:870, operations.go:317 |
| 21 | pos_visit_merges.uq_pos_visit_merges_restaurant_key | varchar(120) NN | S-CLIENT-TRIM | rail.go:181 |
| 22 | pos_visits.uq_pos_visit_open_idempotency | varchar(120) NN | S-CLIENT-RAW | pos.go:847, assistant:48 |
| 23 | restaurant_floors.uniq_restaurant_floors_scope (scope_key) | varchar(10) NULL GEN | S-GEN-COL | expr IFNULL(DATE_FORMAT(specific_date,'%Y-%m-%d'),'global'); ON CONFLICT backoffice_config.go:514/608 needs PG generated column present |
| 24 | restaurant_stripe_connect.uq_restaurant_stripe_connect_hash | char(64) NN | S-GEN-HEX | HMAC hex stripe_connect.go:56; unique-err stripe_connect.go:137 |
| 25 | stock_document_scans.uq_stock_document_hash | char(64) NULL | S-GEN-HEX | stockDocumentHash stock_documents.go:229 |
| 26 | stock_item_units.uq_stock_item_unit_code | varchar(48) NN | S-CLIENT-MIXED | trim stock.go:861; create raw in.DisplayUnitCode stock.go:698 |
| 27 | stock_movements.uq_stock_movement_idempotency | varchar(120) NN | S-CLIENT-RAW (+generated realtime keys stock_realtime.go:179) | stock.go:968/1147/1460; unique-err stock.go:1463 |
| 28 | stock_production_orders.uq_stock_production_idempotency | varchar(120) NN | S-CLIENT-RAW | recipes.go:499; unique-err recipes.go:502 |
| 29 | stock_recipe_step_image_jobs.uq_step_image_job_idem | varchar(120) NN | S-CLIENT-TRIM (or generated) | technical_sheet_images.go:111/183 |
| 30 | stock_warehouses.uq_stock_warehouse_code | varchar(32) NULL | S-CLIENT-TRIM | stockNullableString TrimSpace stock.go:1866 |
| 31 | stripe_events.uniq_stripe_events_event_id | varchar(128) NN | S-PROVIDER | ON CONFLICT(event_id) stripe_event_claim.go:65 |
| 32 | user_menu_preferences.uq_user_menu_pref | varchar(64) NN | S-ALLOWLIST | normalizeBOPreference preferences.go:111; ON CONFLICT :200 |
| 33 | user_preferences.uq_user_pref | varchar(64) NN | S-ALLOWLIST | preferences.go:111; ON CONFLICT :161 |

Counts (after padmeta1): S-GEN-HEX 5, S-GEN-FMT 1, S-GEN-COL 2, S-CLIENT-TRIM 7, S-CLIENT-RAW 10, S-CLIENT-MIXED 3, S-PROVIDER 1, S-ALLOWLIST 3, S-NO-WRITER 1, S-UNTRACED 0 = 33.

Remedy (proposal, unapplied) — CORRECTED by repair-padmeta1: the earlier COLLATE "C" text was an error. MySQL behavior is preserved, so the source column collation's equality stays in force until the user explicitly changes the contract. Opaque means stored values are never trimmed; it does NOT mean C when the source collation is unicode_ci. For S-CLIENT-RAW/MIXED/NO-WRITER the proposal is a unique expression index on (rtrim(col, ' ') COLLATE dbmigrate.ci_ai) that keeps raw values, with identical lookups and ON CONFLICT targets. ci_ai only approximates unicode_ci: UCA expansions and general_ci vs unicode_ci vs ICU are unresolved, so this stays gated and is not a new policy. No blanket type change. The generator is dbmigrate -mode padplan (repair-padmeta1). S-GEN-COL: verify PG plan renders the generated expressions (not re-verified this batch).

## Audit-3 open items closed (source)
1. validateMarginScopeKey: backoffice_stock_margin_targets.go:52; input TrimSpace backoffice_stock_margin_scopes.go:159 before validate/lookup/insert (:200/:210). Key validated against allowlist (COMIDA_TYPE lowercase compare :57) or tipo:id with category existence query; GLOBAL forced "*".
2. POSTRES ui_data_id: no writer repo-wide (grep Go/SQL/JS excluding binaries). Only migration 053 adds columns for comida_items and VINOS; POSTRES column origin is import/legacy only → S-NO-WRITER.
3. group-menu dishes ui_data_id: same, no writer repo-wide → S-NO-WRITER.
4. comida_items ui_data_id: TrimSpace + length check comida.go:1600-1602, insert :1608 (confirmed).
5. analytics identity: NormalizeCustomerIdentity internal/analytics/service.go:36 — email lower+TrimSpace :52, phone digits only :56, tax upper alnum :67, name lower+Fields collapse :77; ON CONFLICT(restaurant_id,identity_key) :675. Canonical, no trailing space from this builder; legacy rows unsettled.

## Corrected planner plan (supersedes overbroad parts of audit-3)
- Do not infer PAD attribute from collation name suffix. Use observed metadata (information_schema.COLLATIONS.PAD_ATTRIBUTE) or exact source server version+collation. Local source container differs from production (0900_ai_ci server default vs unicode_ci columns); per-column collation from schema.json governs.
- _bin collations are text (character-wise, may be PAD SPACE), not BINARY bytes; map to PG "C" + rtrim where PAD, preserve C vs ci_ai per column explicitly.
- utf8mb4_general_ci vs unicode_ci vs PG ICU und-u-ks-level1 equivalence is UNRESOLVED (e.g. ß=ss differs between general_ci and unicode_ci). No claim ICU matches source.
- ENUM: comparison and input with trailing spaces (MySQL strips trailing spaces from ENUM definitions; strict-mode input matching) still needs audit; Go-side enums do not settle it.
- Generated columns and hex/char(64): bpchar vs varchar handling must be per column.
- Exact catalogue of SQL conflict targets/unique-error names: docs/dbmigrate/padding-index-callers.tsv (351 lines). Kinds: ON_CONFLICT (PG twin targets, matched to schema index column sets), UNIQUE_ERR (IsUniqueViolationOn names), MYSQL_ODKU/MYSQL_IGNORE (MySQL applies to ANY unique key on the table). 350 rows. Statuses: key_in_121 58, table_has_121_key_all_uniques_apply 57 (MySQL ODKU/IGNORE), not_in_121_text_keys 209, blocked_unresolved 25 (lexical extraction failed: table not resolved or NO_TARGET — each needs manual read), helper_definition 1. Lexical, not complete proof: dynamic SQL built outside 25-line window is blocked.

## Set check (script)
Selections: audit1 = r93 rows with class human|unknown (28); audit2/3/4 = docs/dbmigrate/padding-audit-selections/sel2,sel3,sel4.tsv (30/30/33). Script set comparison: total 121, unique 121, duplicates none, missing none, extra none vs r93 TSV (121 rows, 121 unique). Doc text uses abbreviated key names, so membership is proven by selection files, not by grepping doc prose.

