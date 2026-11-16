# result-repair-utils1: port 3 runtime utilities to both dialects (20 sites)

All changes are code only. `go build -p 2 ./...` exits 0. I ran none of the tools: no translation provider was called, no stock backfill or arroz cleanup was executed, and no SQL was run from code. Runtime-verified app SQL remains 0. PG is still ready:false and R5 is untouched.

## Inventory r94

`/tmp/villacarmen-bot-20261111/sql-inventory-r94.json` has 2498 sites, all classified. mysqlOnly dropped from **44 to 24**.

| Kind | Sites |
|---|---|
| native | 857 |
| portable | 1492 |
| selected | 82 |
| pgOnly | 43 |
| mysqlOnly | 24 |

### Remaining mysqlOnly (24)

| Group | Sites | Status |
|---|---|---|
| Utilities: chatgpt-plugin-token 1, migrate-wine-images 3, seed-legal-pages 1, update-restaurant-info 4 | **9** | next batch |
| Legitimate MySQL-only: `internal/db/migrations` 4, `internal/db/mysql.go` 1, `dbmigrate` export 3 + schema 1, `cmd/dbmigrate` 1 | 10 | stay (source reads / bootstrap) |
| Inside `internal/api`: assistant_catalog_tools 2, technical_sheet_steps 1, booking_documents 1, seo_item_routes 1 | **5** | classified in earlier phases; re-verify that each is genuinely unreachable on PG before PG activation (gate) |

## Wiring (all three tools)

- Each tool now has a `dialect.go` with `dialect`, `nativeSQL`, `portableSQL` and `sqlFragment`, the same contract as cdn-sweep. Each statement is rebound exactly once.
- **comida-stock-backfill** and **group-menu-arroz-cleanup**:
  - They now use `db.OpenBusinessForTool`.
    - **MySQL:** `OpenMySQL`, unchanged.
    - **PG:** fails closed without `DB_PG_TOOLS_QA_APPROVED=1`, `DATABASE_URL` and the import marker. It pins UTC and schema, runs no migrations and has no MySQL fallback.
  - `--apply` is **refused on PG**; only the default report mode runs there.
- **backfill-translations**:
  - **MySQL:** keeps its own DSN env exactly as before.
  - **PG:** only via `DB_DRIVER=postgres` through `OpenBusinessForTool`, and it **requires `--dry-run`**: no MiniMax call and no write on PG. On MySQL the source default (`--dry-run=false`) is unchanged.
- **comida-stock-backfill lock:** `TryNamedLock(ctx, dialect, …)`, the existing pinned-connection helper. On MySQL it uses GET_LOCK; on PG a session advisory lock on one `sql.Conn`. Release discards the connection via ErrBadConn when the unlock is unconfirmed.

## SQL semantics per site

### comida-stock-backfill (6)

- **Tenants:** portable.
- **Catalogue:**
  - PG twins quote `"POSTRES"`, `"NUM"` and `"DESCRIPCION"`.
  - The `NULLIF(titulo,'')` PAD SPACE behaviour becomes `CASE WHEN rtrim(titulo,' ')='' THEN NULL ELSE titulo END` on PG.
  - `stock_item_id IS NOT NULL` scans into bool on both.
  - **VINOS is absent from the source schema:** the query fails on both engines, as in the source.
- **Find by sku:** portable. `uq(restaurant_id, sku)` is the idempotency key, and runs are serialized by the lock.
- **Insert stock_items:** `sqldb.InsertID`. MySQL uses LastInsertId (unchanged); PG uses `RETURNING id`. The enum columns take the string params.
- **Insert unit:** portable.
- **Link update:** PG uses quoted table and identifier names. `stock_item_id` is numeric(20,0) on PG and takes an int64 param.
- The single transaction (item, unit, link) is unchanged.

### group-menu-arroz-cleanup (5)

- Menus re-read, update, sections select and filtered menu list: all portable.
- **Catalogue arroz:** PG17 rejects LIKE on ci_ai, so it uses `LOWER(TRIM(…)) COLLATE "C" LIKE 'arroz%'`, the same rewrite as `backoffice_group_menus`. Accent-insensitive LIKE remains the recorded search gap.
- **isMissingTableErr:** now also matches PG `42P01` (undefined_table only), so the source behaviour of skipping a missing catalogue is preserved.

### backfill-translations (9)

- comida_items, menus, sections, dishes, loadHashes and minimax config are portable.
  - `annotations_json` is text on PG.
  - The `source_hash` char(64) value is 64 hex characters, so it is never padded.
- **backfillSimple:** the PG twin quotes every identifier. The map order is captured once and reused for both the select list and the scan, as before.
- **DIA / FINDE:** PG quoted twins. **VINOS, DIA and FINDE are absent from the source schema:** the query fails and is printed on both engines, as before.
- **Upsert into dish_translations:** PG uses `ON CONFLICT (restaurant_id, entity_type, entity_id, field_name, lang) DO UPDATE`. That is the only collidable unique key (id is identity). `updated_at` is set by the existing on-update trigger.

## Coordinator manual read-only evidence (05:54 CET)

These are **5 ported query texts**, run as a PG prepared statement and compared with MySQL `villacarmen_source`:

| Query | Result |
|---|---|
| Daily override for rid 1, date 2023-09-23 | 0 rows |
| Default limit | 45 |
| Seats used | 55 |
| Booking search `%mar%` | ids 4, 26, 36, 59, 65 |
| First 5 reservation times | 13:30, 14:00, 14:30, 15:00, 21:00 |

- All five matched between engines.
- PG `EXPLAIN` of the day-lock statement: syntax OK. No lock was executed.
- **Scope of this evidence:** it covers the executed shape of those 5 SQL texts only. It is not a full-handler, Go-scan or concurrency proof.
- **55 > 45** is existing source data: manual or front bookings that are not limited by policy. It is not a regression, and no new capacity enforcement was added to the front.

## Gates (not waived)

- **PG run:** report only for the 3 tools until runtime QA.
- **Source anomalies:** VINOS, DIA and FINDE are absent (preserved).
- **Search:** accent-insensitive LIKE.
- **Remaining work:** the 9 utility sites and 5 api mysqlOnly sites.
- **Collation:** PAD SPACE on unverified unique keys.
- **Data and delivery:** NUL in jsonb, notifications at most once.
- **Concurrency:** capacity is code review only.
- **Source data:** 10 guarded IGNORE sites; 1117 pending; 4 NOT VALID FKs with 731 orphans.
- **Environment:** the local source environment does not equal production.
- **General gates.**

## Next

1. The remaining 9 utility sites: chatgpt-plugin-token, migrate-wine-images, seed-legal-pages, update-restaurant-info.
2. Re-verify the 5 api mysqlOnly sites.
3. The full collation unique-key writer audit (121 keys).
4. Then more manual read-only QA.

## Files and backup

- `cmd/comida-stock-backfill/{main.go,dialect.go}`
- `cmd/group-menu-arroz-cleanup/{main.go,dialect.go}`
- `cmd/backfill-translations/{main.go,dialect.go}`

Backup: `/tmp/ut1.bak.tar`.

