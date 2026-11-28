# result-repair-vitess12

Model: claude-merge/claude-opus-5-5-acc2 (low).
- Source and build only. No tests or Playwright, no SQL writes, no activation, no network.
- go.mod and go.sum are unchanged.
- `go build -p 2 ./...` OK with /home/linuxbrew/.linuxbrew/bin/go.
- Pre-batch backup: /tmp/v12-pre.bak.tar (internal and cmd).

## 1. Writers: the 151 unwired rows
**Approach.** The PG text of a `portableSQL` statement is wrapped as `portableSQL(wkPG(orig, withWK))`:
- `wkPG` returns `orig` unless the flag is on AND the dialect is PG, so the MySQL text never changes;
- an explicit nativeSQL split is not needed for the flag path.

The generator (/tmp/wkgen.py and /tmp/wkgen2.py, outside the worktree) was extended:
- it finds the PG literal; if the line hit is the MySQL first argument, it takes the adjacent PG twin;
- UPDATE support: `col = ?` or `col = COALESCE(?, col)` gets a `col__wk` item, with the key nested right after its argument;
- the enclosing call is found by paren-walking. For `sqldb.InsertID`/`InsertSelectID`, the RETURNING id parameter is kept out of the bind-argument mapping. This was a bug in the first run, which mapped wrong values. I restored all files from the backup and re-ran before building.
- `cmd/` maintenance tools are excluded, since they are not app runtime.

**Result:** 126 statements wired automatically (`docs/dbmigrate/wk-writer-autowired-vitess12.tsv`). These include:
- POS: visits, tickets, payments, refunds, cash closures, ticket lines and others;
- stock: movements, item units, items, warehouses, categories, production;
- bo_*;
- site_builder versions, pages and assets;
- idempotency keys.

The value mapping is mechanical: column, then token, then placeholder ordinal. I spot-checked it on the pos and stock sites, but **each site still needs a review**.

Unique-violation names: every `ixpad__…` literal used in IsUniqueViolationOn got its `ixwk__` sibling (4 sites). The others use the generic duplicate check.

**Updated list:** `docs/dbmigrate/wk-writer-unwired-vitess12.tsv`, re-triaged and lexical.

| status | rows |
|---|---|
| wired | 344 |
| mysql-text | 40 |
| mysql-only | 1 |
| cmd tools (out of runtime) | 4 |
| UNWIRED | 12 |

The 12 UNWIRED rows:
- **accounting_exports.payload_hash** (:350) and **booking_reminder_deliveries.delivery_key** (:347): values come from struct fields inside expressions.
- **pos_fiscal_documents.full_number** (fiscal :500): computed SQL expression. Needs a manual Go-side key; **fiscal priority**.
- **stock_movements.idempotency_key** (stock_documents :680): token/column count mismatch, needs reading.
- **restaurant_members.email** UPDATE (:327): SET shape not matched.
- **whatsapp_bot_sessions.user_phone** ×2: call form.
- **restaurant_floors.scope_key** ×2: generated/expression column (the scope_key token is absent).
- **menus.seo_slug**: slice args.
- **restaurant_tables** premium 2732/2780: these were wired in vitess11 (false positive).

There are also 6 "no key SET item" and 5 "variadic slice args" UPDATEs whose rows the lexical re-triage marks wired because `__wk` appears nearby. Treat them as **unverified**:
- pos_service_periods, pos_product_categories, pos_kitchen_stations and menus seo_slug (slice args);
- stock_vat_rates, stock_suppliers, stock_items sku, stock_categories and stock_warehouses code (SET shape).

## 2. LIKE sites
**Wired this batch:** stock item options (`backoffice_stock.go` handleBOStockItemOptions).
- The id set uses name, sku and barcode under unicode_ci.
- The PG variant becomes `(?='%%' OR i.id = ANY(?))`, keeping ORDER BY name and LIMIT 500. Results: 422 or 500.

LIKE sites wired in total: 8.

**Not done:**
- assistant catalog
- bookings search at runtime (including invoices 782/786)
- POS reservations
- AI usage
- technical sheet
- group menus v2
- comida 818/1111 (listCatalogItems and wines)
- group_menus.go:940 TitleJoin

## 3. Sort parity of the wired LIKE sites
| site | ORDER BY | collation-relevant? | parity remedy |
|---|---|---|---|
| admin_postres search | NUM (Go helper; the source has no ORDER BY) | no (engine-arbitrary in source) | none |
| comida listPostres | active DESC, NUM DESC | no | none |
| invoices list | invoice_date / amount | no | none |
| POS customers | c.updated_at DESC LIMIT 20 | no | none |
| checkcancel | LIMIT 1, no ORDER BY | no (arbitrary in source) | none |
| stock list | i.name (default), qty+name, updated_at | **yes**: i.name sorts under PG ci_ai, not unicode_ci | `ORDER BY name__wk` once the DDL exists |
| stock options | i.name LIMIT 500 | **yes**: also decides which 500 rows are returned | `ORDER BY name__wk` |
| dish allergens | name LIMIT 5 | **yes**: also decides which 5 rows are returned | `ORDER BY name__wk` |

- **Go SortByKey is not needed** at these sites. Ordering and LIMIT stay in SQL, and `__wk` bytea byte order equals MySQL weight order (the vitess6 key design).
- The `name__wk` columns exist in the DDL proposal (stock_items.name; stock_recipes.name is NOT in the 149 registry, so dish allergens would need a new key column or a Go sort over its small candidate set).
- SortByKey stays for Go-assembled results (TitleJoin).
- Not implemented yet: the ORDER BY switch needs the DDL.

## Inventory r119
2534 sites, 2513 native, unchanged.

## Gates
- `__wk` DDL unapplied, so the guard refuses with the flag on.
- 12 + 11 writer rows unwired or unverified; 126 auto-wired rows need review.
- LIKE: 8 sites.
- Sort switch to `__wk` pending the DDL.
- GPL/provenance still open.
- Shadow FK.
- PG disabled, checkout 503, 0 handler QA, 1117 pending, 731 orphans, R5 untouched.
