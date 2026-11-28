# result-repair-vitess17

Model: claude-merge/claude-opus-5-5-acc2 (low).
- Source and build only. No tests or Playwright, no SQL writes, no activation, no network.
- go.mod and go.sum are unchanged.
- `go build -p 2 ./...` OK with /home/linuxbrew/.linuxbrew/bin/go.
- Backup: /tmp/v17-pre.bak.tar.
- backoffice_stock_documents.go:680 is untouched.

Input (coordinator 15:33): scratch has wk-sortkeys-vitess16.sql applied and backfilled (stock_items.name 479, stock_recipes.name 0). `pad-gate-check` with PADQA_WK=1 passes wk_keys.

## 1. The 3 remaining LIKE paths: all wired (flag VC_PG_WK_KEYS; off = unchanged)
- **assistant `catalog_list` (generic resources):**
  - new `assistantCatalogNameCollation` map (source schema): comida_items, BEBIDAS, CAFES, menus, pos_products, stock_items and restaurant_members, all with the name column utf8mb4_unicode_ci;
  - candidates are `SELECT <idCol>, <name> FROM <table> WHERE restaurant_id=?`, then `AND <idCol> = ANY(?)`;
  - a table missing from the map keeps the old path.
- **assistant vinos:** group_menu_wines_v2.nombre under **utf8mb4_0900_ai_ci**, then `AND v.id = ANY(?)`.
- **group_menus.go:940 join.** With the flag on (PG):
  - the dish query runs **without** the comida_items LEFT JOIN, with the ci columns as NULL;
  - `gmJoinComidaItems` loads the tenant's platos (ordered by ci.id) and `(*Coll).TitleJoin` (utf8mb4_unicode_ci, the collation of both title_snapshot and nombre) re-creates the join;
  - it keeps the three OR predicates (`=`, LOWER()=LOWER() with the collation's own case map, and `LIKE nombre || ' (+%'` with the source quirk of wildcards inside nombre);
  - multiplicity is preserved (N rows per dish, or one row with NULL ci fields);
  - the order follows the dish ORDER BY; ties between matches follow item id (engine-arbitrary in the source).
- **Refactor with the flag off:** rows are first collected into `[]gmDishRow`, then processed by the unchanged body. The SQL text and args are identical; only buffering changed.

All LIKE sites of the vitess4 catalogue are now wired (22 paths). Bounds are still the provisional floor (§4).

## 2. Lookups
None converted this batch. 63 remain (`docs/dbmigrate/wk-lookups-remaining-vitess15.tsv`). They are not broken with the flag on; they keep the ci_ai approximation.

## 3. Review pass of the generated wiring
**Off-path identity holds by construction:**
- `wkPG(off, on)` returns `off` unless `mysqlcoll.WKFlag()` AND `sqlDialect == Postgres`;
- `wkKeyArgs`, `wkArgs`, `wkSupArgs` and `wkSetItems` return their input unchanged when off;
- `wkKeyArg` returns `v`.

**Static checks over all 328 `wkPG` sites in 96 files** (/tmp/v17r3.py):
- no OFF text contains `__wk`;
- every ON text adds `__wk` or `ANY(?)`;
- no `wkPG` sits in the MySQL position (`nativeSQL(wkPG(…`, `sqlFragment(wkPG(…`, `mysqlOnlySQL(wkPG(…`: 0), and none is under cmd/;
- placeholders added by the ON text equal the keys added by the enclosing call's helpers.

**6 flagged, all checked by hand and OK** (`docs/dbmigrate/wk-review-vitess17.tsv`):
- premium.go 1151/1161: args via the `wkDomainArgs` helper.
- whatsapp_bot_history:53 and human_takeover:90: the query is held in a variable, and the key is added at the Exec.
- seo_item_routes:726: `pgOnlySQL` wrapper, keys present.
- backoffice_stock.go:652 (stock options): the ON text has fewer placeholders (LIKE ×3 becomes `ANY(?)`), and `optArgs` is rebuilt accordingly.

**Caveat:** this is a structural check. Per-site value correctness (which Go expression feeds which key) was spot-checked in vitess12 and is not exhaustively proven. Scratch QA with the flag on remains the real test.

## 4. Read-only measuring queries (not run): docs/dbmigrate/like-bounds-measure-vitess17.sql
- In `BEGIN TRANSACTION READ ONLY … ROLLBACK`, aggregates only.
- The maximum candidates per restaurant for each LIKE site's candidate predicate: POSTRES, stock_items, invoices, pos_customers, the checkcancel key, stock_recipes, comida_items per type, platos, group menu dishes, wines, bookings (all, non-cancelled, per day), analytics_customers and the catalog tables.
- For AI usage (whatsapp_message_logs): the maximum rows in any 1, 7 or 31-day window per tenant, plus content bytes over 31 days.
- Bound per site = 4 × max (`mysqlcoll.Bound`), with the AI usage memory budget (avg bytes × bound ≤ 64 MB) checked against `max_bytes_31d`.

## Inventory r124
- 2535 sites: 2512 native, 9 static, 14 dynamic.
- The group-menus dish query moved into a variable, so the scanner now counts it as dynamic (native −1, dynamic +1). The new gmJoinComidaItems candidate query is dynamic (+1).

## Gates
- Flag off.
- 63 lookups unconverted (approximation only).
- Bounds provisional until the measuring file is run.
- Stock document insert bug (documented).
- GPL/provenance still open.
- Shadow FK.
- PG disabled, checkout 503, 0 handler QA, 1117 pending, 731 orphans, R5 untouched.
