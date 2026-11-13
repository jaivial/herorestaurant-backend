# Native PG port: catalog_stock group 5 (backoffice_group_menus_v2.go part B; compile only, NOT runtime-verified, NOT PG-ready)

## Counts (inventory r22: /tmp/villacarmen-bot-20261111/sql-inventory-r22.json, dbmigrate-r10)
- **Repo:** 2475 call sites, 769 native (+41 vs r21's 728), 280 dynamic, 5 helper-internal calls excluded. portable_audit_failures is 0.
- **Tooling fix (dbmigrate-r10):**
  - The r9 audit flagged 2 false positives: the string literal `'POSTRES'` matched the mixed-case table identifier `POSTRES`.
  - r10 strips single-quoted literals **only for the identifier check**. The MySQL-only function check is unchanged.
- **Core:** 406/406. **Bookings:** 161/161.
- **catalog_stock:** 202/506 native, 304 remaining (81 dynamic). 4/43 files are done.
  - **backoffice_group_menus_v2.go is 70/70** (part A 29 + part B 41).
  - Ledger: `docs/dbmigrate/native-ledger-catalog.json`.
- **Build:** `go build -p 2 ./...` exits 0 under the inherited cgroup (CPU 200%, 6G). No SQL was run, and no tests, vet, fixtures or Playwright.

## Part A InsertID claim, re-checked
- **MySQL:**
  - Exec errors are returned and handled; only the LastInsertId error is ignored.
  - go-sql-driver/mysql's `Result.LastInsertId` returns the OK-packet id and cannot fail after a successful exec, so the ignored error has no reachable case.
- **PG:**
  - `INSERT … RETURNING id` is **one statement**. Its error is the same genuine execution error MySQL also reports.
  - There is no separate id-retrieval step that could fail independently, and a VALUES insert cannot return zero rows.
- **Conclusion (by code review):** no externally observable departure identified. Genuine PG errors are not swallowed.
- **Applies to:** part A (draft menu, sections) and part B (dishes, catalog dish, slider images ×2, postres menu).

## Changed functions (part B, 41 sites)
- **PutSectionDishes:**
  - Ownership COUNT and existing-dishes read are portable.
  - Dish UPDATE/INSERT use `CAST(? AS jsonb)` for allergens_json; the INSERT uses `InsertID`.
  - The composed `DELETE … NOT IN` is rebound once.
- **PatchSectionDish:** `CAST(allergens_json AS text)` on read and CAST jsonb on write. RowsAffected is not checked.
- **DishesCatalogSearch** (comida_items):
  - `alergenos_json` is cast to text.
  - LIKE becomes `LOWER(col) COLLATE "C"` with a Go-lowered pattern. This is case-insensitive but **accent-sensitive**, a CI search gap.
  - `ORDER BY updated_at DESC NULLS LAST` (MySQL sorts NULL last in DESC).
  - `updated_at` is scanned into a string; it renders RFC3339 "…Z" only because of the UTC ScanLocation, so QA is needed.
- **DishesCatalogUpsert:**
  - The UPDATE keeps MySQL's changed-rows contract: an unchanged save answers "Dish not found" (source quirk).
  - PG uses `IS DISTINCT FROM` over (title/description `COLLATE "C"`, allergens jsonb, smallint, numeric(10,2)).
  - JSON equality differs: MySQL compares binary JSON, jsonb compares semantically. They match for normalized string arrays, but this needs QA.
  - The INSERT uses `InsertID`.
- **Publish, ToggleActive, image uploads (dish foto, preview), Delete, special menu image:** portable. The delete RowsAffected is matched rows on both.
- **Slider:**
  - `COALESCE(CAST(slider_mode AS text),'default')`.
  - `IF(slider_mode IN …,'both',slider_mode)` becomes CASE; the literal resolves to the enum.
  - Inserts use `InsertID`.
  - MAX(position), GREATEST counter, reorder and delete are portable.
- **AI slider job:** SQL only; the provider code is untouched and nothing was executed.
- **ResolvePostres:**
  - Uses the existing `legacy_source_table='POSTRES'` column.
  - The menu INSERT uses `InsertID`; the section INSERT is portable.

**Source anomalies:**
- This file does not reference `menu_dishes_catalog.name` or DIA/FINDE.
- The catalog uses `title`; comida search uses `nombre`/`titulo`, which both exist.
- The known anomalies elsewhere remain documented and unfixed.

## Pending risks (none waived)
- **Search:** CI/accent search and ordering.
- **JSON:** text render and jsonb-vs-binary equality in changed-row checks.
- **Timestamps:** JSON timestamp rendering QA (UTC ScanLocation from code reading).
- **ON UPDATE:** time semantics (136 tables / 353 defaults).
- **Unchanged:**
  - 190 range narrowings.
  - The recent_conversations view.
  - NUL QA.
  - 4 NOT VALID FKs covering 731 orphans.
  - 1117 pending, ready:false.
  - PAD SPACE.
  - FULLTEXT.
  - Source anomalies.

MySQL stays the default and the server still refuses PG. No DB writes, activation, providers, payments, email, WhatsApp, sends, .env/proof edits, commits or deploys.

## Next bounded group (~37 sites, group-menus satellites)
| file | sites | dynamic |
|---|---|---|
| backoffice_group_menus_v2_special_sections.go | 19 | 4 |
| backoffice_group_menus_v2_ws.go | 10 | 0 |
| backoffice_group_menus_v2_items.go | 8 | 1 |

**Then:**
| file | sites | dynamic |
|---|---|---|
| comida.go | 31 | 12 |
| backoffice_stock_analytics.go | 26 | 0 |
| backoffice_comida_categories.go | 22 | 10 |
| public_menus.go | 18 | 10 |
| others | … | … |

