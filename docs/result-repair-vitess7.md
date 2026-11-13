# result-repair-vitess7

Model: claude-merge/claude-opus-5-5-acc2 (low).
- Source and build only. Nothing was run; no SQL, no network, no tests or Playwright, no activation.
- go.mod and go.sum are byte-identical. No fix to the x/text and x/sys "// indirect" markers was needed: the build passes with -mod=readonly. The markers are stale but harmless.
- Business-pool and public-schema wiring is untouched.
- `go build -p 2 ./...` OK.

Input: vitess6 was verified live at 12:55. Metadata matched 149/149 and the 9 composite columns were clean.

## 1. cmd/wk-backfill (wk_backfill_v1), built, NOT run
- **Default dry-run:** opens a read-only session (`SHOW transaction_read_only` must be on). Per column it reports `need_key` (text NOT NULL and `__wk` NULL) and `invalid_utf8`.
- **Apply** requires all of:
  - `-apply`;
  - `-target-db` equal to the connected `current_database()`;
  - a database name without "prod";
  - **no `onupd%` trigger on the table.**
- **Why the trigger check matters:** the forward temporal refresh trigger (already COMMITted on scratch) would treat a `__wk` change as a real change and bump updated_at. That would be a data mutation. The trigger helper in `internal/dbmigrate/plan.go` must first exclude `__wk` columns; that is a NEW required change, and it alters the onupdate function SHA and gate.
- **Resumable:**
  - only rows with a NULL key are selected;
  - each UPDATE is guarded by `col::text = original` and `__wk IS NULL`; a row that changed meanwhile counts as `skipped_changed`;
  - batches commit separately, so a rerun continues where it stopped.
- **Invalid UTF-8 rows are never written** and stay NULL, so readiness blocks.
- Output: JSON counts only, never the DSN (env `WKB_PG_DSN`).
- Composite PKs are supported (text comparison per PK part).

## 2. Writer wiring (flag `VC_PG_WK_KEYS=1`, OFF by default; internal/api/wk_keys.go)
- `wkOn()` = flag AND PG. `wkPG(plain, withWK)` picks the PG text. `wkArgs(args, at, table, col, v)` inserts `mysqlcoll.ColumnKey` only when the flag is on.
- **With the flag off**, the SQL texts and argument lists are byte-identical to before. MySQL is never affected.
- With the flag on but no `__wk` DDL, the statements fail on the unknown column (fail closed).
- New registry `internal/mysqlcoll/wkcolumns.go` (generated, 149 entries) plus `registry.go` (`ForColumn`, `ColumnKey`, cached Coll).
- **Wired in this batch:** the 4 PG insert statements of `restaurant_domains.domain`. With the flag on they set `domain__wk` and use `ON CONFLICT (domain__wk)`:
  - `domain_reservation.go` claimOwnedDomain;
  - `backoffice_premium.go` both domain insert variants. A key error yields nil args, so the statement fails and nothing is stored without a key;
  - `instatic_manager.go` provisioned subdomain insert. A key error now returns an error, but only when the flag is on.
- **Remaining:** the inventory-based lexical scan finds 651 INSERT/UPDATE/REPLACE lines over 107 of the 119 tables, listed in `/tmp/villacarmen-bot-20261111/wk-writer-candidate-lines-vitess7.txt`. These are MySQL and PG texts together, and they include UPDATEs that do not touch key columns. Each needs a per-site decision: does it write a registered column? If so, add the wkPG and wkArgs variant and the ON CONFLICT target. So **writer coverage is 1 of 149 columns**, not complete.
- Still missing:
  - dynamic SQL builders (column lists assembled at runtime);
  - the readers and lookups switching to `__wk`;
  - a startup gate requiring the ixwk indexes plus `CheckStoredKeys` before the flag may be on.

## 3. LIKE sites (the same flag)
- New helper `(*Server).wkLikeIntIDs`:
  - runs the tenant-scoped candidate query (the same non-text predicates, no LIKE, no LIMIT);
  - applies `mysqlcoll.Like` on the original bytes;
  - fails closed above the bound with `mysqlcoll.ErrTooBroad`.
- `wkLikeMinBound` = 5000 is a provisional technical floor until `max_rows_per_tenant` is measured for the site's table (`mysqlcoll.Bound`).
- **Wired:** `admin_postres.go` POSTRES.DESCRIPCION search (utf8mb4_unicode_ci PAD per source metadata).
  - With the flag on it uses the MySQL pattern `%term%` unlowered, returns ids in NUM order (the source has no ORDER BY), and keeps the same JSON shape.
  - "Search too broad" maps to **HTTP 422**, `{"status":"error","message":"Search too broad, please refine the filters","matchingIds":[]}`.
  - With the flag off the code is unchanged.
- **Remaining group A sites** (from vitess4):
  - admin_menus.go:450
  - assistant_catalog_tools.go:575
  - backoffice_invoices.go:228-237, 782, 786
  - backoffice_pos.go:689
  - backoffice_pos_customers.go:105, 112
  - backoffice_pos_reservations.go:31
  - checkcancel.go:126, 139
  - whatsapp_bot_dish_allergens_tools.go:56
  - backoffice_ai_usage.go:281
  - backoffice_technical_sheet_link.go:378
  - backoffice_bookings_search.go:152-162
  - backoffice_bookings.go:368-375
  - backoffice_stock.go:530, 613
  - backoffice_group_menus_v2.go:2487
  - comida.go:818, 1007, 1111
  - group_menus.go:940 (TitleJoin helper ready, not wired)
- The multi-column and paginated sites also need SortByKey and Go-side count and pagination.

## Inventory r114
- 2531 sites = 2511 native (unchanged; the 4 rewired PG texts stay native through nativeSQL) + 20 non-native.
- The 20 are, by my attribution: collkey-compare 10, wk-backfill and the wkLikeIntIDs candidate query the rest. The inventory summary gives no per-file breakdown.

## Gates (new and remaining)
- **NEW:** the temporal trigger helper must exclude `__wk` before any backfill or flag-on write. Otherwise updated_at is bumped falsely.
- `__wk` DDL is unapplied. Writer coverage is 1 of 149 columns; LIKE coverage is 1 site.
- Startup gate for the flag; measured bounds.
- GPL/provenance still open.
- PG disabled, checkout 503, 0 handler QA, 1117 pending, 731 orphans, R5 untouched.
