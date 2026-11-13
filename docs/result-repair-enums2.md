# Result: repair-enums2 (pos_visits.service_type closed, 30 more enum columns, temporal catalog gate; code-only; nothing executed)

## Manual temporal QA proof (coordinator 10:11, scratch only, network none, ROLLBACK; recorded with its exact scope)
- **Table:** restaurant_areas, 1 existing row. Observed:
  - the no-op left it unchanged;
  - a real change gave 09:11:18 UTC, the truncated statement time;
  - an explicit SAME timestamp plus another change kept 09:11:18;
  - an explicit different timestamp (2001-…) was kept;
  - UPDATE … FROM with duplicate join rows updated once, with the GUC empty after;
  - the savepoint mark was cleared.
- **Trigger order** for the 2 QA tables: mark before refresh.
- **NOT verified:** multi-row (the table has 1 row, so the "multi-row" steps updated 1 row) and the generated-column table (pos_visits is empty, so UPDATE 0).
- 136 is not closed. No fixtures were created; R5 is untouched.

## pos_visits.service_type: closed (source)
- **Writer:** backoffice_pos.go:847 binds `moment.ServiceType` from resolvePOSBusinessMoment (pos.go:247-275).
- **Source of the value:** either the literal "OTHER", or `period.ServiceType` read from pos_service_periods (pos.go:480).
- **That column's writers** (operations.go:50 create/update) require validPOSMode(in.ServiceType, "LUNCH","DINNER","OTHER") with an exact canonical match. The assistant (assistant_pos_tools.go:48) writes the literal 'OTHER'.
- **Result:** always a canonical label; no PG risk from app writers.

## Code fixes (bind the label MySQL stores; MySQL results identical; leading spaces still fail on both engines)
- **New in sql_native.go:** mysqlLowerEnumArg (ToLower + TrimRight " ") and mysqlLowerEnumPtr (nil → NULL). Used only for all-lower-case ASCII label sets.
- **backoffice_config.go normalizeEncryption / normalizeProvider:** these validated ToLower(TrimSpace(v)) but returned the RAW v. 'TLS' / 'SMTP' would be stored canonically by MySQL and rejected by a PG enum. They now return mysqlLowerEnumArg(v).
- **conversation_history.go:313:** message_type/message_role are bound via mysqlLowerEnumArg. The source applies TrimSpace only, so 'User' is accepted by MySQL as 'user'.
- **backoffice_invoices.go:547/551 and :673/:677:** payment_method and discount_type use mysqlLowerEnumPtr. The raw client *string was bound before; invoice status already uses an exact switch.
- **Not used:** no Go EqualFold, and no non-ASCII folding. All affected label sets are ASCII.

## 30 more enum columns (first 30 of the 103 unaudited in (table, column) order; exact writers from the literal scan of INSERT/UPDATE)
| column (labels) | writers (file:line) | value source | status |
|---|---|---|---|
| POSTRES.production_type (RAW,MANUFACTURED) | technical_sheet_link.go:119/123 | ToUpper(TrimSpace)+exact check (link.go:37-38) | canonical |
| accounting_exports.export_type | accounting_exports.go:348/352 | ToUpper(TrimSpace) + switch (:320-324) | canonical |
| accounting_exports.status | none (default only) | — | no writer |
| ai_image_models.mode (t2i,i2i) | none in Go (seed) | — | no writer |
| analytics_sales_documents.source_type (INVOICE,POS) | analytics/service.go:709/713 | literals "INVOICE"/"POS" (service.go:163/168/510) | canonical |
| analytics_sync_runs.status | service.go:377/441/450 | SQL literals | canonical |
| assistant_messages.role (user,assistant) | assistant_ws.go:425/426/560 | SQL literals | canonical |
| cdn_object_sweeps.status | cmd/cdn-sweep/main.go:146/159/163 | Go constants ("SUCCEEDED", …; main.go:136) | canonical |
| comida_items.production_type | technical_sheet_bulk.go:177 literal; link.go:102 | as POSTRES | canonical |
| conversation_messages.message_role (user,assistant,system) | conversation_history.go:305/309 | TrimSpace only | FIXED (lower bind) |
| conversation_messages.message_type (user,ai,system) | same | TrimSpace + assistant→ai | FIXED |
| conversation_sessions.status | none via placeholder (SQL literals) | — | canonical |
| email_provider_config.provider (smtp,gmail) | backoffice_config.go:2797/2807 | normalizeProvider returned raw | FIXED |
| email_provider_config.smtp_encryption (none,tls,ssl) | same | normalizeEncryption returned raw | FIXED |
| group_menu_wines_v2.production_type | link.go:112 | as POSTRES | canonical |
| instatic_instances.status | instatic_manager.go:180/186/229/238/267/490 | SQL literals | canonical |
| integration_job_runs.status | cmd/ops-check/main.go:115/130 | literal + Go constants ("OK", …) | canonical |
| invoices.discount_type (percentage,fixed) | backoffice_invoices.go:519/531/646/658 | raw *string | FIXED |
| invoices.payment_method (efectivo,tarjeta,transferencia,bizum,cheque) | same | raw *string | FIXED |
| invoices.status | same + :968/973/1320/1322 literals | exact switch (:476/601), default borrador | canonical |
| member_compensation_audit.action | labour_cost.go:229/273 | Go literal "CREATE"/"UPDATE" + SQL 'DELETE' | canonical |
| member_compensations.pay_type (MONTHLY,HOURLY) | labour_cost.go:206/214 | ToUpper(TrimSpace) (:65) + effectiveHourlyCost validation | canonical |
| member_time_allocation_audit.action | production_labour.go:213/257 | SQL literals | canonical |
| menus.slider_mode | group_menus_v2.go:3261 (+ SQL CASE literals 3381/3385/3674/3678) | TrimSpace + exact map boValidSliderModes (:3255) | canonical (case-variant input is rejected in Go on both) |
| pos_activation_acceptances.acceptance_type | pos_kitchen.go:586 | ToUpper(TrimSpace) + exact (:569-570) | canonical |
| pos_cash_closures.status | default only | — | no writer |
| pos_cash_days.status | cash_days.go:445 literal (and create path default) | literal | canonical |
| pos_cover_adjustments.service_type | checkout.go:916 | validPOSMode exact (:899) | canonical |
| pos_drawer_events.reason | pos_rail.go:418 | ToUpper(TrimSpace) + exact (:405-409) | canonical |
| pos_fiscal_documents.document_type | pos_fiscal.go:396 (Go constants posFiscalType*), :503 copy | constants/copy | canonical |

- **Accounting:** 40 of 113 audited (10 in timeqa1, 30 here); **73 remaining**, not audited.
- **Totals:** 9 columns fixed (3 in timeqa1, 6 here); 4 with no writer.
- **Still gated:** MySQL numeric-ordinal input and external writers (native adapter, design only).
- **Scan limit:** the scan covers literal INSERT/UPDATE only, so dynamic SQL writers of these columns beyond the scan are not claimed absent.

## Temporal readiness catalog gate (new; read-only)
- **internal/db/onupdate_gate.go RequireOnUpdateTriggers**, wired into OpenBusinessForTool and pad-gate-check `business.onupdate_triggers`. It requires:
  - **(a)** dbmigrate.on_update_ts and dbmigrate.on_update_mark: zero-arg plpgsql trigger functions whose pg_proc.prosrc sha256 equals the planner source (dbmigrate.OnUpdateFunctionSources, the text between $$). That is content identity, not just the name.
  - **(b)** all 273 triggers from internal/db/onupdate_expected.go (generated from the future-plan-timeqa1-r5compat render: 136 tables): exact name, tgtype 19 (BEFORE, ROW, UPDATE), function, NUL-separated args, and UPDATE OF attnum (mark) or none (refresh), enabled.
  - **(c)** per table, exactly the expected number of non-internal BEFORE UPDATE row triggers, so no foreign trigger can sit between mark and refresh.
- **Expected now on scratch/R5:** FAIL (old 2-part triggers, no mark functions). There is no app activation.

## Build / inventory
- go build -p 2 ./... OK.
- Inventory r110: 2511 (+4 gate catalog queries), 288 files, all native.

## Gates (not claimed)
- **Unverified temporal QA:** multi-row and generated-column cases. The forward temporal DDL is unapplied.
- **Enums:** 73 unaudited, plus the ordinal/external gap.
- **Still pending:** 774 like / 771 pad, 190/91 unsigned, storage 1, view 1.
- 731 orphans; checkout replay payload (awaiting approval).
- R5 untouched; PG server disabled; checkout 503; 0 handler QA (the manual diagnostics are not handler QA).
