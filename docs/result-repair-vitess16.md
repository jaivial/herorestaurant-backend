# result-repair-vitess16

Model: claude-merge/claude-opus-5-5-acc2 (low).
- Source and build only. No tests or Playwright, no SQL writes, no activation, no network.
- go.mod and go.sum are unchanged.
- `go build -p 2 ./...` OK with /home/linuxbrew/.linuxbrew/bin/go.
- Backup: /tmp/v16-pre.bak.tar.
- backoffice_stock_documents.go:680 is untouched.

## 1. stock_items.name / stock_recipes.name sort keys: done (source)
**Registry.** Both columns are added to `mysqlcoll.WKColumns` (now 151 entries), utf8mb4_unicode_ci PAD, sort-only.
- With the flag on, the startup guard requires both columns and `CheckStoredKeys` = 0.
- So `docs/dbmigrate/wk-sortkeys-vitess16.sql` (new, NOT applied, BEGIN … ROLLBACK; supersedes the vitess14 proposal) plus a backfill must come BEFORE the flag is enabled.

**Writers wired:**
- stock_items.name:
  - technical_sheet_duplicate:60, technical_sheet_ensure:107 and technical_sheets:149 (generator);
  - stock create (backoffice_stock.go ~731) and stock import (~396). These already carried sku__wk; now both keys go in one KeyArgs call, cols {sku, name};
  - stock update (~782): `name__wk=$11` after `sku__wk=$10`.
- stock_recipes.name:
  - technical_sheet_duplicate:81, technical_sheet_ensure:124 and technical_sheets:168;
  - stock_documents:510;
  - stock_recipes:260 (insert) and :267 (UPDATE);
  - the technical_sheets dynamic SET builder (~265) through `wkSetItems("stock_recipes", …)`.
- The other stock_recipes and stock_items UPDATEs (allergens, status, pricing, soft delete) do not set name.
- Not wired: cmd/comida-stock-backfill, a maintenance tool outside app runtime.

**ORDER BY behind the flag:**
- stock list: `i.name__wk` (default and qty sorts);
- stock options: `ORDER BY i.name__wk LIMIT 500`;
- dish allergens: `ORDER BY name__wk LIMIT 5`.

## 2. LIKE sites
**Wired this batch:**
- **technical sheet:** `sheetListFrom` takes an optional id set (variadic, so existing callers compile unchanged). The REST list and WS search compute it with `wkSheetIDs` (stock_recipes.name, restaurant + is_active, the same base predicate). Results: 422 or a WS error.
- **assistant catalog,** 3 of its paths:
  - postres search (POSTRES.DESCRIPCION, `"NUM" = ANY(?)`);
  - customers_list (analytics_customers display_name, email, phone);
  - stock_items_list (stock_items.name).
- **AI usage (WhatsApp log search):**
  - candidates are the same restaurant and the same `created_at` window; user_phone and content under unicode_ci;
  - the PG flag path adds `id = ANY(?)`;
  - ErrTooBroad maps to 422 in the backoffice handler. The assistant messaging tool returns the error.

**Measured-limit method (proposal for AI usage and all LIKE sites):**
1. For each site's candidate query, run a read-only count on scratch or local source: the maximum candidate rows per restaurant over the site's window (AI usage: per 1, 7 and 31 days).
2. Set the bound per site to 4 × that maximum.
3. Record it in code next to the site, replacing the provisional `wkLikeMinBound` floor of 5000.
4. Re-measure after each import.
- AI usage reads mediumtext content into memory, so its bound must also respect a byte budget. Proposal: the measured average content size × bound ≤ 64 MB.

**Not wired:**
- **assistant catalog, generic `catalog_list` over the legacy tables:** it needs a per-table collation map for those columns.
- **assistant catalog, vinos path:** its id is the legacy num expression.
- **group_menus.go:940:** the LEFT JOIN with the `title_snapshot LIKE CONCAT(ci.nombre,…)` predicate needs the TitleJoin restructuring.
  - Plan: query 1 = the dishes without the ci join; query 2 = the tenant's platos; `(*Coll).TitleJoin` to pair them; then reuse the existing row assembly per pair.
  - Not implemented this batch.

LIKE sites wired in total: 19 (counting the 3 catalog paths, technical sheet and AI usage).

## 3. Lookups
- None converted this batch; 63 remain (`docs/dbmigrate/wk-lookups-remaining-vitess15.tsv`).
- The simplest remaining group is "query in a variable", which needs per-site manual edits.

## 4. Flag-readiness checklist: docs/dbmigrate/wk-flag-readiness-vitess16.tsv
One row per registered column (151), with the PG writers lexically still unwired and the sort notes.

The lexical re-triage of all current writers of registry tables finds 4 references:
- **backoffice_members.go:327, restaurant_members.email:** wired in vitess13 (`email__wk = $11`). A false positive: the key sits outside the 14-line window.
- **backoffice_premium.go:2730 and :2778, restaurant_tables.name:** wired in vitess11. False positives.
- **backoffice_stock_documents.go:680, stock_movements.idempotency_key:** the known source bug, untouched by decision.

So, lexically, **every PG writer of the 151 columns is wired** except the bug site. Per-site review of the generator output is still required.

**Before the flag may be turned on, still required:**
1. Apply the `__wk` sort-key DDL (wk-sortkeys-vitess16.sql) on scratch and backfill. Otherwise the guard refuses.
2. Convert or verify the 63 remaining lookups. Until then they keep the ci_ai approximation; they don't break with the flag, they are just not exact.
3. The remaining LIKE sites: generic catalog_list, vinos, group_menus.go:940.
4. Measured bounds.
5. Review of the auto-wired writers.
6. A scratch run of the guard (PADQA_WK=1) after the new DDL.

## Inventory r123
2534 sites, 2513 native, unchanged.

## Gates
- Flag off.
- Stock document insert bug (documented).
- GPL/provenance still open.
- Shadow FK (orphans).
- PG disabled, checkout 503, 0 handler QA, 1117 pending, 731 orphans, R5 untouched.
