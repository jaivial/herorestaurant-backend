# Result: repair-enums3 (35 more enum columns, 113-row audit TSV, reader scan; code-only; nothing executed)

## Exact set
- **Batch:** the next 35 of the 73 unaudited enum columns, in (table, column) order, from pos_fiscal_series.series_type through site_builder_domain_mappings.ssl_status.
- **Audited:** 10 + 30 + 35 = 75. **Remaining:** 38 unverified.
- **TSV:** docs/dbmigrate/enum-columns-audit.tsv, 113 rows with unique keys (script-asserted: 113 keys, 113 distinct, every audited key present in the schema).

| status | columns |
|---|---|
| canonical | 55 |
| fixed | 12 |
| no-writer | 7 |
| seed | 1 |
| unverified | 38 |

## Writers (this batch; file:line evidence per row in the TSV)
- **Canonical by server literals/constants:** pos_fiscal_series.series_type, pos_kitchen_dispatch_lines.action, pos_products.source_type, pos_shifts/pos_stock_anomalies/pos_stock_exceptions(code,status)/pos_ticket_lines/pos_tickets(status,stock_status)/pos_visits.status, pos_ticket_line_stock.status, reservation_location_occupancy.scope, restaurant_ads.image_generation_status (typed constants), restaurant_domains.stripe_payment_status, site_builder_domain_mappings.ssl_status, recurring_invoices.frequency.
- **Canonical by entry normalization plus an allowlist:**
  - ToUpper(TrimSpace) + validPOSMode or an exact check: pos_kitchen_dispatches.status (+transition), pos_modifier_groups.kind, pos_service_periods.service_type, pos_settings.{covers_mode,stock_mode}, pos_tags.scope, pos_ticket_adjustments.{mode,type};
  - restaurant_info.clasificacion: exact match or derived;
  - restaurant_tables.status: normalizePremiumTableStatus.
- **No writer:** pos_payments.status, pos_refunds.status, pos_ticket_adjustments.status (default only); recurring_invoices.payment_method, site_builder_collections.source_type (no Go writer).
- **FIXED here (3):** each bind is now the label MySQL stores (mysqlLowerEnumArg, all-lower-case ASCII labels; leading spaces still fail on both engines; MySQL result identical).
  - **restaurant_websites.status:** website_builder_handlers.go:254 bound TrimSpace only. is_published still uses the existing EqualFold, which is unchanged.
  - **site_builder_assets.type:** site_builder.go:902 bound the raw input.Type. The reader at :814 already compares CAST(type AS text) COLLATE ci_ai.
  - **site_builder_bindings.refresh_mode:** site_builder.go:1369 bound the raw input.RefreshMode.
- The existing 9 fixes are unchanged.

## Readers / comparisons / ORDER BY (literal scan; docs/dbmigrate/enum-readers-scan.json)
- **46 `enum = ?` / `IN (?` comparisons over 32 enum columns** in both dialect texts.
  - On PG, a native enum compared with a bound parameter coerces the parameter to the enum: a non-canonical or unknown value raises 22P02, where MySQL compares under ci and returns no match.
  - The asset-type reader already uses the text/ci_ai pattern. The other 45 sites are NOT individually reviewed in this batch and are **gated**.
- **38 ORDER BY on 14 enum columns.** MySQL orders by declaration index and the PG native enum orders by declaration order, so they are equivalent. No PG twin orders by CAST(col AS text) (grep: 0). The plan keeps native enums.
- **Still gated:**
  - numeric ordinal input (MySQL k → k-th label);
  - external writers;
  - dynamic SQL writers outside literal INSERT/UPDATE text (the scan covers literal statements; dynamic fragments such as website_builder_handlers.go:520 `status = ?` were traced by hand only where found). A per-column "no-writer" status means none was found by the literal scan plus targeted greps; that is NOT a proof of absence.

## Build / inventory
- go build -p 2 ./... OK.
- Inventory r111: 2511 (bind args only), all native.

## Gates (not claimed)
- **Enums:** 38 unverified; 45 enum comparison reader sites unreviewed; the ordinal/external adapter (design only).
- **Temporal:** the forward DDL is scratch-pending, applied by the coordinator. RequireOnUpdateTriggers is not assumed to pass until the coordinator's facts are in.
- **Still pending:** collation 774/771, unsigned 190/91, storage 1, view 1.
- 731 orphans; checkout replay payload (awaiting approval).
- R5 untouched; PG server disabled; checkout 503; 0 handler QA.
