# Native PG port: catalog_stock group 10 (public_menus.go + backoffice_stock_suppliers.go; compile only, NOT runtime-verified, NOT PG-ready)

## Counts (inventory r27: /tmp/villacarmen-bot-20261111/sql-inventory-r27.json, dbmigrate-r12)
- **Repo:** 2472 call sites, 918 native (+35), 240 dynamic. portable_audit_failures is 0.
  - Excluded: 5 helper-internal calls and 3 prepared-stmt calls.
- **Core:** 406/406. **Bookings:** 161/161.
- **catalog_stock:** 351/504 native, 153 remaining (42 dynamic). 12/43 files are done:
  - **public_menus.go 18/18 (10 dynamic)**;
  - **backoffice_stock_suppliers.go 17/17 (2 dynamic)**.
  - Ledger: `docs/dbmigrate/native-ledger-catalog.json`.
- **Build:** `go build -p 2 ./...` exits 0 under the inherited cgroup (CPU 200%, 6G). No SQL was run, and no tests, vet, fixtures or Playwright.

## backoffice_stock_suppliers.go (17)
- **Supplier list:** the scalar subquery DATE_FORMAT becomes `to_char`.
- **Create:**
  - The `EXISTS` pre-check becomes CASE 0/1 → 409.
  - The INSERT uses `InsertID`.
  - The source maps no named unique error: a racing duplicate on `uq_stock_suppliers` answers 500 on both. This breadth is **kept** and no mapping was added.
- **PATCH:**
  - The clash `EXISTS` becomes CASE.
  - The UPDATE keeps the changed-rows 404 gate (source quirk), using `IS DISTINCT FROM` over (name/notes `COLLATE "C"`, is_active).
- **Delete and alias reads:** portable. `FOR UPDATE` is kept.
- **Alias upsert:** `ON CONFLICT (restaurant_id,supplier_name,supplier_code,normalized_description)`. All four columns are NOT NULL.
- **Alias prune:** the dynamic NOT IN list is rebound once.
- **Price summary:**
  - `AVG(decimal(18,6))` has scale 6+4=10, so PG uses `ROUND(AVG, 10)`.
  - The latest price, `SUBSTRING_INDEX(GROUP_CONCAT(… ORDER BY effective_at DESC, id DESC),'|',1)+0`, becomes `CAST((array_agg(… ORDER BY …))[1] AS double precision)`.
    - group_concat_max_len only truncates the tail, never the first element.
  - DATE_FORMAT becomes to_char.
  - The window is `<statement UTC naive> - make_interval(days)`.
- **Recent prices by other suppliers (dynamic):** the head/tail are sqlFragment'd (`ROUND(AVG,10)` and the same window) around the generated IN list, rebound once.

## public_menus.go (18)
- **Page visibility read:** composed from the fixed column list, portable.
- **Page visibility upsert (dynamic):** `ON CONFLICT (restaurant_id)` on `uniq_rest_page_vis_restaurant`. `col = COALESCE(?, restaurant_page_visibility.col)` reads the existing row, as MySQL does.
- **Slider:** `slider_mode` enum is cast to text. The composed slider-image query is rebound once.
- **Special sections and special-date link:** DATE_FORMAT becomes to_char.
- **Weekday availability, menus list, sections/dishes IN-lists:** composed and rebound once.
  - `cta_json` and `allergens_json` are `CAST AS text` via sqlFragment inside the Sprintf templates.
- **Public menu detail / v2 sections / dishes:** JSON is cast to text. `created_at` is scanned into a string, which depends on the UTC ScanLocation (timestamp QA).
- **activePublicMenuWhere/OrderBy queries ×3 (dynamic):** fixed portable fragments, rebound once.

**Source anomalies:** none in these files; no DIA/FINDE and no menu_dishes_catalog.name.

## App-semantic gaps, still explicit (separate ledger, NOT part of the 1117 import-proof count; marker ready:false)
- **Category ODKU insert id:** provisional "0 on conflict → source fallback". The MySQL path is unproven and this is not equivalent.
- **Wine precio float(10,2):** rounding/range proposal only; imported proofs are untouched.
- **Quotient/AVG double-rounding edge:** usage avg_daily, plus this phase's supplier AVG ×2. Pending numeric QA.
- **JSON_CONTAINS** nested-array containment.
- **jsonb vs binary JSON equality** in changed-row checks; JSON text render; JSON timestamp QA.

**Unchanged:**
- CI/accent/PAD SPACE (ordering and equality).
- FULLTEXT.
- 190 range narrowings.
- ON UPDATE (136) / DEFAULT (353) time semantics.
- The recent_conversations view.
- NUL adapter QA.
- 4 NOT VALID FKs covering 731 orphans.
- Source anomalies.

Compilation is not equivalence. MySQL stays the default and the server still refuses PG. No DB writes, activation, providers, payments, email, WhatsApp, sends, .env/proof edits, commits or deploys.

## Next remaining files (r27)
| file | remaining | dynamic |
|---|---|---|
| group_menus.go | 14 | 6 |
| menu_section_dessert_source.go | 11 | 1 |
| backoffice_group_menus.go | 10 | 2 |
| backoffice_stock_margin_scopes.go | 9 | 0 |
| menu_visibility_admin.go | 8 | 0 |
| admin_menus.go | 8 | 5 |
| special_menu_principales.go | 7 | 0 |
| backoffice_vinos.go | 7 | 7 |
| backoffice_stock_import.go | 6 | 0 |
| backoffice_menus.go | 6 | 6 |
| wines_v2.go | 5 | 0 |
| menu_weekday_availability.go | 5 | 2 |
| others | … | … |

The rest of the 153 sites are in the ledger.

