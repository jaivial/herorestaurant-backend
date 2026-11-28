# result-repair-vitess14

Model: claude-merge/claude-opus-5-5-acc2 (low).
- Source and build only. No tests or Playwright, no SQL writes, no activation, no network.
- go.mod and go.sum are unchanged.
- `go build -p 2 ./...` OK with /home/linuxbrew/.linuxbrew/bin/go.
- Backups: /tmp/v14-pre.bak.tar and /tmp/v14-lk.bak.tar.
- The stock movement insert bug at backoffice_stock_documents.go:680 is NOT touched (user decision).

## 0. Correction of vitess13 (would have broken with the flag on)
- vitess13 switched the stock list and stock options to `ORDER BY i.name__wk`, but **stock_items.name has no `__wk` column**: only stock_items.sku is registered.
- With the flag on, those queries would have failed on an unknown column.
- **Both are reverted to `i.name`.** The sort keys are now a proposal (§2).

## 1. Writers
**The 8 changed-rows UPDATEs, hand-wired** with a dedicated transformer (/tmp/wkupd.py):
- pos_service_periods.name
- pos_product_categories.name
- pos_kitchen_stations.name
- stock_vat_rates.name
- stock_suppliers.name
- stock_warehouses.code (name is not registered)
- stock_categories.name
- stock_items.sku (name is not registered)

How they are wired:
- `?` texts get `col__wk=?` right after `col=?`, with the key inserted right after its argument. The duplicated changed-rows argument tail shifts consistently.
- `$n` texts get `col__wk=$max+1`, with the key appended at the end.
- `mysqlcoll.KeyArgs` now accepts `at < 0` to mean append.
- The changed-rows ROW predicates are unchanged. The key column is derived from the text, so it does not change RowsAffected semantics.
- pos_product_categories passes `posChangedRowArgs(...)` as a slice. It is now evaluated twice; it is a pure function, so this is harmless.

**pos_tickets (assistant_pos_tools.go:72):** still not wired (slice args built earlier). Listed as remaining.

**scope_key__wk** (STORED GENERATED `IFNULL(DATE_FORMAT(specific_date,'%Y-%m-%d'),'global')`):
- New helper `wkScopeKeyText(date)`: an empty date gives 'global', and an exact `YYYY-MM-DD` gives that date.
- Any other format returns invalid UTF-8, so the key fails and the statement is not executed. This is fail-closed rather than guessing MySQL's date coercion.
- Wired:
  - restaurant_salons create (backoffice_salons.go): `name__wk` and `scope_key__wk`;
  - restaurant_floors date-scoped create (floors_date.go): `scope_key__wk`;
  - plus the vitess13 global floor inserts.
- The salon update does not change specific_date. The other floors and salons UPDATEs (config max_aforo, has_capacity_limit) do not touch specific_date. No other specific_date writer was found by grep in internal/.

## 2. Sort keys for stock_items.name and stock_recipes.name (proposal, not applied)
`docs/dbmigrate/wk-sortkeys-proposal-vitess14.sql`, wrapped in BEGIN … ROLLBACK:
- `ADD COLUMN name__wk bytea` on both tables;
- non-unique `(restaurant_id, name__wk)` indexes;
- the full list of writers that must be wired first: 6 for stock_items.name and 7 for stock_recipes.name, including a dynamic SET builder and one cmd tool.

They are NOT added to `mysqlcoll.WKColumns` yet. Adding them would make the startup guard require columns that the scratch copy does not have. Until activation, the stock list, stock options and dish allergens keep `ORDER BY name` under ci_ai (known gap).

## 3. LIKE sites
None added this batch; still 8 wired.

Remaining:
- assistant catalog
- bookings search at runtime (including invoices 782/786)
- POS reservations
- AI usage
- technical sheet
- group menus v2
- comida 818/1111
- group_menus.go:940 TitleJoin

## 4. Lookups converted to col__wk = key (flag on)
- New `wkKeyArg(table, col, v)` (api and analytics):
  - with the flag on, the argument becomes the weight key;
  - an invalid-UTF-8 key gives NULL, so no row matches. This is a documented gap against MySQL's invalid-input behaviour.
- Converter (/tmp/v14lk.py): for each row of wk-lookups-vitess13.tsv it converted the row only when:
  - the exact unqualified pattern `(rtrim(col,' ') COLLATE dbmigrate.ci_ai) = (rtrim(?,' ') …)` appears once in the statement;
  - the statement names exactly ONE table and that table has the column registered (table confirmed);
  - the args are inline.
- Each converted text uses `wkPG(orig, … col__wk = ? …)`, with the matching argument wrapped in `wkKeyArg`.
- **Converted: 113** (`docs/dbmigrate/wk-lookups-converted-vitess14.tsv`). Top tables: restaurant_domains 14, booking_checkouts 5, site_builder_sites 4, site_builder_domain_mappings 4, restaurant_booking_extras 4.
- **Remaining: 72** (`docs/dbmigrate/wk-lookups-remaining-vitess14.tsv`), each with its reason:

| reason | rows |
|---|---|
| qualified, cast or other form | 20 |
| several key comparisons in one statement | 17 |
| inside an already-flagged writer text | 15 |
| call form (query in a variable, e.g. domain_ownership selectedSQL) | 12 |
| JOIN or table not unique | 7 |
| slice args | 1 |

## Inventory r121
2534 sites, 2513 native, unchanged.

## Gates
**The flag must stay off.** Reasons:
- pos_tickets slice insert;
- 72 lookups;
- sort keys not applied;
- LIKE 8 of about 16 sites;
- the auto-wired rows still need review.

Also open:
- stock document insert bug (user decision);
- GPL/provenance;
- shadow FK;
- PG disabled, checkout 503, 0 handler QA, 1117 pending, 731 orphans, R5 untouched.
