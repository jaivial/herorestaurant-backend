# Result: repair-padbatch3 (next 20 PAD unique keys; code-only, nothing applied; NOT functional completion)

## Selection
- The 20 keys are the first 20 of pad-key-sets-padbatch2.txt REMAINING_44, from pos_cash_movements through restaurant_booking_extras.
- 72 + 20 = 92 patched and 24 remaining. The exact sets are in docs/dbmigrate/pad-key-sets-padbatch3.txt.

## Keys and callers (PG text only; MySQL texts unchanged; no Go identity bug found in this batch)
| key (parts; pad = rtrim+ci_ai, others raw) | callers / treatment |
|---|---|
| pos_cash_movements (rid, idempotency_key) | backoffice_pos_cash.go:391 pre-check and :401 duplicate re-read PATCHED; INSERT :398 raw |
| pos_drawer_events (rid, idempotency_key) | backoffice_pos_rail.go:398 (nativeSQL PG) and :422 PATCHED |
| pos_fiscal_documents (rid, full_number, copy_number int) | INSERT fiscal.go:396 and :503 (copy via INSERT…SELECT); every read is by id/ticket; no full_number lookup. Future index only. full_number is built from the locked series counter (unchanged); nothing fiscal was executed |
| pos_fiscal_series (rid, terminal_key, series_type enum raw) | fiscal.go:107 ON CONFLICT (PG target = exact expression list, enum raw) and :112 lookup PATCHED. terminal_key is already TrimRight(" ") at :104 (source); counter locking and chain unchanged |
| pos_kitchen_dispatches (rid, station_id, command_key) | kitchen.go:399 replay lookup PATCHED (key = TrimSpace(key)+":"+station) |
| pos_kitchen_stations (rid, name) | UPDATE SET kitchen.go:109 raw; no name lookup; "duplicate" substring generic |
| pos_modifier_groups / pos_modifier_options (rid[, group_id], name) | UPDATE SET modifiers.go:495/:646 raw; no name lookup |
| pos_packs (rid, name) | UPDATE SET packs.go:367 raw; no name lookup |
| pos_payments (rid, provider NULL, provider_reference NULL) | INSERT checkout.go:362 and assistant tools; accounting export read only; no key lookup; NULL parts stay distinct. Future index only |
| pos_product_categories (rid, name) | catalog.go:213 and :226 ON CONFLICT PATCHED; catalog.go:262 and :267 category-link joins PATCHED: pc.name vs rtrim(categoria/tipo) are now both sides rtrim+ci_ai (was a one-sided rtrim); UPDATE split.go:57 raw |
| pos_products (rid, sku NULL) | UPDATE SET catalog.go:104 raw; no sku lookup on pos_products |
| pos_service_periods (rid, name) | UPDATE SET operations.go:79 raw; no name lookup |
| pos_shifts (rid, open_terminal_key GENERATED, NULL when not OPEN) | no lookup on open_terminal_key; the "duplicate" substring on the shift-open INSERT is generic. Index over the generated column; the PG plan's generated expression is still the generated_column gate |
| pos_tags (rid, name) | no name lookup |
| pos_ticket_adjustments (rid, idempotency_key) | rail.go:485 replay lookup PATCHED |
| pos_tickets (rid, ticket_number) | generated numbers; pos.go:689 is a LIKE prefix scan for the next number (LIKE is not PAD-affected, unchanged); no equality lookup |
| pos_visit_merges (rid, idempotency_key) | rail.go:190 replay lookup PATCHED |
| restaurant_beverage_options (rid, slug) | beverage_options.go:146 ON CONFLICT and :156 lookup PATCHED |
| restaurant_booking_extras (rid, slug) | booking_extras.go:75 ON CONFLICT and :85; whatsapp_bot_catalog_extras.go:109, :191 (FOR UPDATE), :203 ON CONFLICT and :206 PATCHED |

- None of these 20 keys has an IsUniqueViolationOn or index-name check (repo grep).
- Go maps in these files key on ids or slot groups, not on these identities.
- Idempotency, replay and drain identity is DB-decided. A replay still does not compare payload (source contract, recorded).

## Future DDL (NOT executed; additive; current R5 raw indexes unchanged and unapplied)
```
CREATE UNIQUE INDEX "ixpad__pos_cash_movements__uq_pos_cash_movement_idempotency" ON "pos_cash_movements" ("restaurant_id", (rtrim("idempotency_key", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__pos_drawer_events__uq_pos_drawer_event_idempotency" ON "pos_drawer_events" ("restaurant_id", (rtrim("idempotency_key", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__pos_fiscal_documents__uq_pos_fiscal_documents_full" ON "pos_fiscal_documents" ("restaurant_id", (rtrim("full_number", ' ')) COLLATE "dbmigrate"."ci_ai", "copy_number");
CREATE UNIQUE INDEX "ixpad__pos_fiscal_series__uq_pos_fiscal_series_scope" ON "pos_fiscal_series" ("restaurant_id", (rtrim("terminal_key", ' ')) COLLATE "dbmigrate"."ci_ai", "series_type");
CREATE UNIQUE INDEX "ixpad__pos_kitchen_dispatches__uq_pos_kitchen_dispatch_command" ON "pos_kitchen_dispatches" ("restaurant_id", "station_id", (rtrim("command_key", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__pos_kitchen_stations__uq_pos_kitchen_station_name" ON "pos_kitchen_stations" ("restaurant_id", (rtrim("name", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__pos_modifier_groups__uq_pos_modifier_group_name" ON "pos_modifier_groups" ("restaurant_id", (rtrim("name", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__pos_modifier_options__uq_pos_modifier_option_name" ON "pos_modifier_options" ("restaurant_id", "group_id", (rtrim("name", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__pos_packs__uq_pos_pack_name" ON "pos_packs" ("restaurant_id", (rtrim("name", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__pos_payments__uq_pos_payment_provider_ref" ON "pos_payments" ("restaurant_id", (rtrim("provider", ' ')) COLLATE "dbmigrate"."ci_ai", (rtrim("provider_reference", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__pos_product_categories__uq_pos_product_category_name" ON "pos_product_categories" ("restaurant_id", (rtrim("name", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__pos_products__uq_pos_product_sku" ON "pos_products" ("restaurant_id", (rtrim("sku", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__pos_service_periods__uq_pos_service_period_name" ON "pos_service_periods" ("restaurant_id", (rtrim("name", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__pos_shifts__uq_pos_shift_open_terminal" ON "pos_shifts" ("restaurant_id", (rtrim("open_terminal_key", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__pos_tags__uq_pos_tag_name" ON "pos_tags" ("restaurant_id", (rtrim("name", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__pos_ticket_adjustments__uq_pos_ticket_adjustmen_610cf45e" ON "pos_ticket_adjustments" ("restaurant_id", (rtrim("idempotency_key", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__pos_tickets__uq_pos_ticket_number" ON "pos_tickets" ("restaurant_id", (rtrim("ticket_number", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__pos_visit_merges__uq_pos_visit_merges_restaurant_key" ON "pos_visit_merges" ("restaurant_id", (rtrim("idempotency_key", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__restaurant_beverage_options__uq_restaurant_beve_2945eb03" ON "restaurant_beverage_options" ("restaurant_id", (rtrim("slug", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__restaurant_booking_extras__uq_restaurant_bookin_038fec0f" ON "restaurant_booking_extras" ("restaurant_id", (rtrim("slug", ' ')) COLLATE "dbmigrate"."ci_ai");
```
- The gate now has 92 entries; the 20 new ones are under "padbatch3".
- Until these indexes exist, PG tools are refused (incompatible = blocked).

## Probes (regenerated; read-only; NOT executed)
- PG: docs/dbmigrate/pad-collision-probes.sql (BEGIN READ ONLY … COMMIT).
- MySQL: docs/dbmigrate/pad-collision-probes-mysql.sql.
- Each has 121 count-only SELECTs: 92 unique, 18 PK and 11 FK.
- Coordinator 08:10 (earlier 52-key version): zero collisions and zero padding except the CHAR-unknown rows. That is current data only, not a guarantee for the future.

## PK/FK future-writer design (still gated)
- **Decision:** keep the raw PKs and FKs. Readers rtrim/ci_ai, plus a supplementary unique ixpad index per PAD PK.
- **Future writers:** each PK/FK writer must canonicalize (generated UUIDs/constants/validated slugs) or refuse edge spaces before the value can cross an FK. That is only verified for the paths listed in padcontact1/padbatch1.
- **Open writers:** conversation_sessions.id (sender-derived) and whatsapp_bot_sessions.user_phone (provider-derived) are not proven canonical for the future.
- **Fallback:** the generated canonical shadow key (option B) stays open.
- **Not proof:** zero probe data is not a future guarantee. The gate is kept.

## Build / inventory
- go build -p 2 ./... OK.
- Inventory r102: 2501 sites (conversions only), all native, 0 portable audit failures.

## Gates (not claimed)
- ci_ai is only an approximation.
- The gate's rendering regex is unverified.
- All 92 indexes are unapplied.
- Generated-column and enum input parity are gated.
- PG server disabled; tools report-only and index-gated.
- R5 untouched; 1117 pending / 731 orphans; checkout 503; 0 handler QA; 17 legitimate MySQL-only.
- No SQL, DB writes, fiscal, payment or provider calls.
- This is not functional completion.

## Next
The last 24 keys, starting at restaurant_floors.uniq_restaurant_floors_scope.
