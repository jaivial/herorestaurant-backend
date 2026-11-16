# Native PostgreSQL port — group-2 delta fixes + core_backoffice group 3 (technical sheets)

Status: **built** (`go build -p 2 ./...` exit 0, static `dbmigrate-r8` exit 0). No PG statement has been executed, all semantics are unverified until manual QA, and the server still refuses `DB_DRIVER=postgres`. The MySQL path is unchanged. The staging marker stays `ready:false`.

## Part A — group-2 delta fixes (PG branches only)

| delta | fix | status |
|---|---|---|
| Stripe JSON null vs missing | `jsonb_typeof(p #> path)='null'` gives `'null'`, a missing path gives NULL and then the default, anything else uses `#>>`. The `CAST AS UNSIGNED` replica parses MySQL's leading-digit prefix (`'12abc'` → 12, otherwise 0). | reasoned equivalent; number-text rendering still open |
| IGNORE over-length `role` / `brand_name` | source widths: `role varchar(32)` NOT NULL default 'admin', `brand_name varchar(255)`. An explicit PG `CAST(? AS varchar(n))` truncates like IGNORE, and only at those sites. | reasoned |
| IGNORE FK race | the parents in each `EXISTS` are locked `FOR KEY SHARE`, mirroring the InnoDB FK shared lock | needs a PG17 runtime check (locking inside an EXISTS sub-select) |
| MRR scale | per MySQL rules `decimal(10,2)/n` gives scale 6 rounded per row and the `SUM` is exact; PG `ROUND(.,6)` per row then exact `SUM`, scanned to float64 | same by type rules, runtime-unverified |

## Part B — technical sheets: 91 sites converted, 0 left

Files: `backoffice_technical_sheet_{bulk,components,cost,duplicate,ensure,image_worker,images,link,step_upload,steps}.go`, `backoffice_technical_sheets.go` and `backoffice_technical_sheets_ws.go`. All dynamic sites are included. Two new PG-only statements replace `UPDATE … ORDER BY`.

Decisions (detail in `docs/dbmigrate/native-semantic-notes-core.json`):
- **Step renumbering:** PG parks the rows above `stepReorderParkOffset` and then brings them down (unique per row plus `CHECK step_no > 0`). The MySQL statement is kept behind `mysqlOnlySQL`; the PG statements are `pgOnlySQL` (each panics if used on the wrong dialect).
- **Enum CASE:** CASE into the enum column is cast to `"enum__stock_recipe_steps__generation_status"`.
- **JSON:** writes are bound `CAST(? AS jsonb)`; `COALESCE(json,'')` reads become `::text`.
- **Time:** naive datetime columns use `(now() AT TIME ZONE 'UTC')`; `DATE_SUB(NOW(), INTERVAL ? MINUTE)` becomes `make_interval`.
- **UNION collations:** these become `::text COLLATE dbmigrate.ci_ai`; the wine `vinoV2NumExprPG` uses `CAST AS bigint`; `"POSTRES"`, `"NUM"` and `"DESCRIPCION"` are quoted. The ensure/link table and condition twins are also quoted.
- **Insert ids:** 9 `LastInsertId` calls become `InsertID`.
- **Locks:** `FOR UPDATE [SKIP LOCKED]` is kept.

## Ledger (`docs/dbmigrate/native-ledger-core.json`, `sql-inventory-r8.json`)

| | value |
|---|---|
| core_backoffice sites | 405 (403 + 2 PG-only) in 63 files |
| native | **261** |
| remaining | **144** (6 dynamic) in 29 files |
| files fully native | 34 |
| portable audit failures | 0 |
| whole repo | 2476 sites, 261 native |

## Open semantics (not waived)

- **Search:** `sheetListFrom` uses `COLLATE "C" ILIKE` on PG, which is case-insensitive but **not accent-insensitive** like MySQL `unicode_ci` LIKE. This is pending.
- **Sort order:** ICU `ci_ai` ORDER BY may differ from `unicode_ci`.
- **RowsAffected:** at `steps.go:83`, `sheets.go:268` and `sheets.go:450`, MySQL returns 0 changed rows (a 404) on same-value updates while PG returns matched rows (a 200). This behaviour delta needs a decision.
- **Earlier blockers:** CI/PAD SPACE, unsigned narrowing (190), the ON UPDATE same-value gap (136 tables), the view, the storage adapter QA and the 4 NOT VALID historical-orphan FKs all still block cutover.

## Next group (core_backoffice group 4; not started)

Labour and hours (`backoffice_production_labour` 16, `backoffice_labour_cost` 12, `hours` 9, `hour_split` 8, `hour_percentages` 8, `occupancy` 5, `calendar_data`/`backoffice_calendar` 6, `backoffice_affluence` 3 (1 dynamic), `backoffice_dashboard` 2) plus `backoffice_salons` 10, about 79 sites. Group 5 (≈65) covers the remaining modifications/events/ads/accounting/widget/misc files.

