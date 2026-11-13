# Native PostgreSQL port: catalog checkpoint 14

**Status:** the code compiles. No SQL was executed and none of it has been verified at runtime. The app is not PG-ready: `ready:false`, MySQL stays the default and the server refuses to start on PG.

## Counts (canonical inventory r31, dbmigrate-r12 basis)

| Scope | Value |
|---|---|
| Call sites (repo) | 2472 |
| Native (repo) | 1044 (+31 since r30's 1013) |
| Dynamic (repo) | 202 |
| Portable-audit failures | 0 |
| Excluded | 5 helper-internal calls, 3 prepared-statement executions |
| catalog_stock | 477/504 native, 27 remaining (4 dynamic), 31/43 files done |
| Core / bookings | 406/406 and 161/161, unchanged |

- Inventory: `/tmp/villacarmen-bot-20261111/sql-inventory-r31.json`
- Ledger: `docs/dbmigrate/native-ledger-catalog.json` (deltas are in `/tmp/villacarmen-bot-20261111/catalog-deltas10.json`)

## Build

`go build -p 2 ./...` exited 0 under the inherited cgroup (200% CPU, 6G memory), with pipefail. No tests, vet, fixtures or Playwright were run.

## Files ported (31 sites)

### backoffice_stock_import.go (6 sites)
- **EXISTS checks:** `EXISTS` becomes `CASE WHEN EXISTS ... THEN 1 ELSE 0 END`, because PG returns a boolean and the code scans into an int. As in the source, errors are still ignored.
- **Category upsert:** the ODKU becomes `ON CONFLICT (restaurant_id, name) DO UPDATE SET name = EXCLUDED.name`. That is the only key that can collide, since `id` is generated.
- **Item insert ID:** `LastInsertId` is replaced by `sqldb.InsertID` (`RETURNING id`). Execution errors still take the same error branch.

### backoffice_dish_same_day_booking.go (5 sites)
- **Upsert:** the ODKU becomes `ON CONFLICT (dish_id, menu_id)`. That is the real unique key, which is not tenant-scoped (same as the source).
- **date_modified:** set to `date_trunc('second', statement_timestamp())`; the column is a timestamp, i.e. `timestamptz` in PG.
- **Insert ID on PG:** `RETURNING CASE WHEN xmax = 0 THEN id ELSE 0 END`. **This is provisional.** What MySQL's `LastInsertId` returns on the update branch of an ODKU is unproven; this is the same gap as the category upsert.

### backoffice_menu_visibility.go (4 sites)
- **INSERT IGNORE:** becomes `ON CONFLICT (restaurant_id, menu_key) DO NOTHING`. The table has no FK and the constant values fit the column widths, so a duplicate is the only thing IGNORE could ever skip.
- **Changed-rows gate:** when `RowsAffected` is 0 the code falls back to an insert. MySQL counts only changed rows, so the PG statement adds `is_active IS DISTINCT FROM CAST($1 AS smallint)` to keep that behaviour.

### backoffice_vinos_ws.go (4 dynamic sites)
- Each final assembled string is wrapped exactly once.
- The `num` expression is chosen with `sqlFragment(vinoV2NumExpr, vinoV2NumExprPG)`.
- **"Wine not found" gate:** the PG statement adds `ROW(ai_requested_img, ai_generating_img) IS DISTINCT FROM ROW(1, 1)`. This keeps a source quirk: a wine that is already generating reports "not found".

### special_menu_cta.go (4 sites)
- JSON is read with `CAST(special_cta AS text)` (the consumer is `json.Unmarshal`) and written with `CAST(? AS jsonb)`.
- `DATE_FORMAT` becomes `to_char(date, 'YYYY-MM-DD')`.

### stock_digest.go (4 sites)
- **Enum column:** `base_unit` gets `CAST(... AS text)`.
- **Time:** `NOW()` on the naive `expires_at` column becomes `date_trunc('second', statement_timestamp() AT TIME ZONE 'UTC')`. `DATE_ADD(..., INTERVAL ? DAY)` becomes `CAST(? AS integer) * INTERVAL '1 day'`.
- **Ungrouped columns:** in the subquery, `m.restaurant_id` and `m.stock_item_id` become `i.restaurant_id` and `i.id`. They are equal through the join and functionally dependent on the primary key.
- **FLAGGED (GROUP BY nondeterminism):** `m.warehouse_id` is not grouped. Without `ONLY_FULL_GROUP_BY`, MySQL takes an arbitrary row's value. PG uses `any_value(m.warehouse_id)`, which has the same indeterminate contract. Nothing proves both databases pick the same row for an item stocked in several warehouses.
- No digest or WhatsApp send was run.

### backoffice_comida_ai.go (4 sites)
- All 4 are portable.
- The AI and provider code was not touched and not run.

## Static wrap audit (assembled strings)
- **Search 1:** wrapper calls followed by an outside `+` concatenation. Found 0.
- **Search 2:** backtick-close followed by `+`. The only SQL hit is `bookings_admin.go:127`, where an `sqlFragment` pair is then wrapped once by `portableSQL(sqlQuery)`. That is correct.
- **Search 3:** variables assigned from a wrapper and later extended with `+=`. The only hit is `server.go:1680`: `nativeSQL` is used on fragments that contain no placeholders, and the final query is wrapped once by `portableSQL(query)`. That is correct, though `sqlFragment` would be cleaner there.
- **Limit:** these searches are regex/line-based. They are not an AST proof.

## New or updated semantic gaps (not part of the 1117 import-proof count; none waived)
- **ODKU insert ID:** `dish_same_day_booking` returns 0 on conflict on PG. This is provisional (the category gap now has a second site).
- **Stock digest:** the `any_value(warehouse_id)` result may differ from MySQL's pick.
- **CI collation:** equality and ordering on `sku`, `name`, `source_type` and `role`, and the `<> ''` comparison under PAD SPACE.
- **Timestamp scanned to string:** `date_modified` and `updated_at` arrive as RFC3339 text on both drivers. This comes from reading the code only; JSON timestamp QA is still needed.

## Carried over unchanged
- **App-level gaps:**
  - The category ODKU insert ID returns 0 on conflict (provisional).
  - The wine `float(10,2)` rounding/range fix is a proposal only.
  - Double rounding in averages and quotients.
  - Nested JSON containment.
  - JSON changed-row equality, text rendering and timestamp QA.
  - Dessert mirror insert-ID order.
  - Percentage float rounding QA.
  - FULLTEXT search.
- **Import and schema gaps:**
  - 1117 pending items, `ready:false`.
  - 4 FKs left NOT VALID, covering 731 orphan rows.
  - CI, accent and PAD SPACE collation semantics.
  - 190 range narrowings.
  - ON UPDATE on 136 tables and 353 schema defaults.
  - The broken `recent_conversations` view.
  - NUL adapter QA.
- **Source anomalies, preserved as failures:**
  - The DIA/FINDE tables are absent.
  - `menu_dishes_catalog.name` is absent.
  - `bookings.created_at` is absent.
  - `modification_history.restaurant_id` is absent.

## Remaining catalog files (12 files, 27 sites, 4 dynamic): next checkpoint
| File | Sites | Dynamic |
|---|---|---|
| backoffice_stock_reconciliation.go | 3 | |
| backoffice_stock_margin_targets.go | 3 | 1 |
| special_menu_group_booking.go | 3 | |
| special_date_menu_sections.go | 3 | |
| menu_public_seo_handlers.go | 3 | 1 |
| menu_section_cta.go | 2 | |
| menu_public_seo.go | 2 | |
| backoffice_vinos_cutout.go | 2 | 2 |
| backoffice_stock_permissions.go | 2 | |
| backoffice_comida_upload.go | 2 | |
| group_menus_party_size.go | 1 | |
| backoffice_comida_counts.go | 1 | |

All 27 fit in one checkpoint, which would complete the catalog_stock domain.

## Not done
No commit, merge, deploy, DB writes, providers or sends, and no `.env`, source or proof-data edits.

