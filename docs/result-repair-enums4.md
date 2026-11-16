# result-repair-enums4

Model: claude-merge/claude-opus-5-5-acc2 (low). This batch changed source code only. The worker ran no SQL, no tests, no DB access, no providers and no sends.

## 113-column writer audit (docs/dbmigrate/enum-columns-audit.tsv)
- The last 38 columns are now audited, so all 113 rows are covered (113 unique keys, checked with a set comparison).
- Status counts: canonical 81, fixed 17, no-writer 13, seed 2.
- Scope: this covers the writers I know about in source. It does not prove global input parity.
  - External writers and numeric ordinal input stay gated.
  - ci_ai is only an approximation of the source collation.

## New fixes in this batch
Writers now bind the stored label through mysqlUpperEnumArg / mysqlLowerEnumArg. This is ASCII-only case and trailing-space canonicalisation of fixed labels:
- backoffice_stock.go:296 and :345: stock_warehouses.type
- backoffice_stock.go:695 and :746: stock_items kind, base_dimension, deduction_source
- backoffice_stock_import.go:396: kind, dimension, deduction_source
- site_builder.go:590 (create) and :699 (update): site_builder_pages.page_type

Readers in the PG text now use:
  (rtrim(CAST(col AS text),' ') COLLATE dbmigrate.ci_ai) = (rtrim(?,' ') COLLATE dbmigrate.ci_ai)
- backoffice_stock.go:545: stock_items.kind filter
- backoffice_invoices.go:242: invoices.status filter
- With this form, unknown input gives no match, as in MySQL, instead of a PG enum cast error.
- MySQL text is unchanged. ORDER BY stays on native enum order.

## enum-readers-scan.json cmp: 46 rows (the earlier "45" was a miscount; the file has 46, including duplicates for repeated columns)
- **SET assignments (writer binds, not comparisons).** Each bound value is canonical or validated per the audit TSV:
  - invoices 651/652/655/663/664/667
  - ads 820
  - modifiers 505
  - operations 79 (x2)
  - config 2799, 2800
  - technical_sheet_link 102, 112, 119, 123
  - images 226
  - labour_cost 214
  - stock 345, 744 (x2)
  - stock_documents 429
  - group_menus_v2 3262
  - website 256 (x2, mysqlLowerEnumArg)
  - kitchen 524 (x2; the "? IN (...)" compares the parameter with literals, not the column)
  - checkout 389 (x2), 824 (x4; CASE ?='REFUNDED' is a Go constant)
- **WHERE comparisons fed by constants or literals (canonical-bound, no change):**
  - analytics/service.go:722 (source_type literal)
  - pos.go:438 (acceptance_type constants)
  - config_ai_image.go:359 x2 (t2i/i2i literals)
  - occupancy.go:33 (scope literals)
  - pos_fiscal.go:112 x2 (series_type constants)
- **WHERE comparisons fed by validated input (validated, returns 400 before SQL, which is a source error contract):**
  - stock_analytics.go:216 x2 (exact switch)
  - stock_margin_scopes.go:202 x2
  - pos_checkout.go:58/61 (service_type)
- Unresolved readers: 0 in this scan.
- The scan is lexical. Dynamic fragments outside it stay gated per the TSV dynamic notes.

## Scratch facts (coordinator, 10:22, scratch only, network none, parent GO 08:53)
- The forward temporal DDL (353 defaults, 273 triggers) was COMMITted on scratch.
- The read-only pad-gate-check results:
  - 134 indexes: PASS
  - ownership: PASS
  - business.onupdate_triggers: PASS (function source SHA, args and order checked against the real catalog)
  - shadowFK gate: FAILED, as expected (bo_roles.slug__pad is missing because the earlier shadow DDL was rolled back on orphans; no waiver)
- Still unverified: multi-row and generated-column temporal behaviour. No handler QA has been run.

## Build / inventory
- go build -p 2 ./... OK.
- Inventory r112: 2511 sites, all native, 17 legitimate MySQL-only.

## Gates still open
- R5 untouched.
- 1117 original pending; 731 orphans.
- PG server disabled; tools report-only; checkout 503.
- 0 handler QA.
- ci_ai is an approximation.
- External or numeric-ordinal enum input is gated. This needs technical research, not a policy decision.
- Shadow FK.
- Checkout replay payload comparison is still pending.
- Unsigned generator range.

## Next
1. Research a native enum ordinal adapter.
2. Shadow-FK orphan remediation plan (no waiver).
3. Manual read-only handler QA.
Order after that: full PG verified, then the functional bot, then GraphRAG.

