# result-repair-vitess24

Model: claude-merge/claude-opus-5-5-acc2 (low).
- Source and build only. No tests, no SQL, no network.
- go.mod unchanged; build OK.
- backoffice_stock_documents.go:680 is untouched.

## 1. Duplicate-key classifiers (internal/)
**Name-based** (`sqldb.IsUniqueViolationOn(err, mysqlKey, pgNames...)`; PG matches SQLSTATE 23505 plus ConstraintName). Every site already lists the legacy `ix__`, `ixpad__` and `ixwk__` names:

| site | mysql key | PG names checked |
|---|---|---|
| backoffice_stock_recipes.go:502 | uq_stock_production_idempotency | ix__/ixpad__/ixwk__stock_production_orders__uq_stock_production_idempotency |
| seo_item_routes.go:740 | uniq_seo_item_slug | ix__/ixpad__/ixwk__seo_item_routes__uniq_seo_item_slug |
| backoffice_uazapi_provisioning.go:695 | uniq_restaurant_uazapi_instances_name | ix__…_496278c3, ixpad__…_96a63182, ixwk__… |
| stripe_connect.go:142 | uq_restaurant_stripe_connect_hash | ix__…_cb13e095, ixpad__…_261cbce4, ixwk__… |
| backoffice_stock.go:1504 | uq_stock_movement_idempotency | ix__/ixpad__/ixwk__stock_movements__uq_stock_movement_idempotency |

**Hardening (sqldb.go):** any `ixpad__` entry now also matches its derived `ixwk__` twin (prefix swap, truncated to 63, the same rule as `db.WKIndexName`), so a hand-typed ixwk name cannot drift.

**Generic** (any unique violation, engine-neutral; no index names, so all unique indexes are covered):
- `isSQLDuplicateError` (message "duplicate entry", "unique constraint" or "already exists"): premium.go 939/1399/2544/3021/4049, uazapi_servers_admin.go 235/436;
- `sqldb.IsUniqueViolation` (1062 / 23505): salons.go 322/412, production_labour.go 205, comida_categories.go 144;
- menu_public_seo_handlers.go:102.

## 2. The area 500 is source parity, not a PG classifier gap
- `handleBOPremiumTablesCreate` with `entity=area` (premium.go ~1638) maps **every** error from `createBOPremiumArea` to 500 TABLES_CREATE_FAILED. There is no duplicate classification on either engine.
- On MySQL the same duplicate (1062 on uniq_restaurant_areas_restaurant_name) also gives **500**.
- So the PG 500 equals MySQL. **No change was made**, to preserve the contract. A 409 would be a new behaviour for both engines (product decision).
- Compare: the table create path classifies numero_mesa duplicates (`isSQLDuplicateError` → posTableConflictError, 409).

**Which index fired, and why first:**
- `ix__restaurant_areas__uniq_restaurant_areas_restaurant_name` is the R5 import's **raw legacy** unique index on (restaurant_id, name), with the column collation `dbmigrate.ci_ai`.
- A nondeterministic ICU collation makes even the raw index case- and accent-insensitive, so "qa area" collides with "QA Área" there.
- PG checks unique indexes in index OID order. The raw index was created by the import before the ixpad and ixwk indexes, so it reports first.
- Implication: the raw index enforces ci_ai, not MySQL's unicode_ci. Where they differ (ß, æ, trailing space), the raw index can still reject values that MySQL accepts.
- Retiring or recreating the raw indexes on the `__wk` columns is the pending step 6 of wk-ddl-proposal-vitess6.sql ("only then retire ixpad"; this extends to the raw ci_ai uniques). It is not applied.

## 3. cmd/qa-http (static: /tmp/villacarmen-bot-20261111/qa-http)
- sha256 dac985b00d04b0f6a0babde05f7fd45c6da593be0a052b85cde440bf974b6714.
- Usage: `qa-http -X PATCH -url http://127.0.0.1:18080/api/admin/stock/categories/<id> -cookie-file /tmp/qa-cookie < body.json`.
- Behaviour:
  - loopback URLs only;
  - the cookie is read from the file (value or `name=value`) and never printed;
  - the body comes from stdin (JSON content type);
  - `-H` adds extra headers;
  - prints `status=… bytes=<first -n>`.

## 4. Duplicate stock category: 400 on both engines
`handleBOStockCategoryCreate` (backoffice_stock.go:446) returns **400 "Category could not be created"** for any insert error, including MySQL 1062 and PG 23505. The PG path matches MySQL. Supplier duplicate → 409 on both (stock_suppliers.go:97).

## Binaries
- vc-server-qa rebuilt (static), sha256 088928cc7f35b51372a3ce9dd6b4b82f983a0ec5e95807cde5068394ded3cff1.

## Gates
- Raw ci_ai unique indexes are still active (they can over-reject compared with MySQL); retirement is unapplied.
- About 29 approximate lookups.
- Stock document insert bug.
- GPL/provenance still open.
- 1117 pending and 731 orphans untouched.
