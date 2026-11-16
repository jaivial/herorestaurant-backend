# result-repair-utils2: last 9 utility sites + audit of the 5 API mysqlOnly sites

This is a code-only change. `go build -p 2 ./...` exits 0. I ran no tool. No token was issued, revoked or printed, and there were no CDN or network calls and no SQL from code. Runtime-verified app SQL is still 0. The earlier evidence is 5 SQL shapes checked by hand (read-only), not full handlers. PG is still disabled (ready:false), and R5 is untouched.

## Inventory r95

`/tmp/villacarmen-bot-20261111/sql-inventory-r95.json` has 2499 sites, all classified:

| Kind | Sites |
|---|---|
| native | 863 |
| portable | 1494 |
| selected | 82 |
| pgOnly | 43 |
| **mysqlOnly** | **17** (down from 24) |

The +1 site is the PG report count in seed-legal-pages.

## Ported utilities (each has a `dialect.go` with the cdn-sweep contract; every statement is rebound once)

### chatgpt-plugin-token

- **Opening the DB:** `OpenBusinessForTool`. MySQL keeps `OpenMySQL` exactly as before. The MySQL migrations are skipped on PG.
- **What PG allows:** only `--list`, which returns metadata (id, user, restaurant, label, timestamps) and never a secret. Issue and revoke are refused before any credential code runs. `NewPluginTokenService` is now built only inside the issue and revoke branches.
- **The list query** has a PG twin that adds `CAST(? AS integer)` for the `? = 0` guard. The timestamps are cast to text in the UTC session and are display only.

### migrate-wine-images (3)

- **Opening the DB:** `OpenBusinessForTool`.
- **What PG allows:** only `--dry-run` without `--upload`. There are no Bunny/CDN calls, no `api.Server` is built, and no migrations run.
- **The select** has a PG twin with `"VINOS"` and `octet_length`. MySQL `LENGTH` counts bytes; the `foto_path = 0` test is equivalent.
- **VINOS is absent from the source schema.** `isLegacyVinosMissing` now also recognises PG 42P01, so both engines report "nothing to do".
- **The update** has a PG twin (`UPDATE .. FROM legacy_wine_map`, same join and filter). It is not reachable on PG until QA, because dry-run is forced there.

### seed-legal-pages (1, plus 1 report)

- **Opening the DB:** MySQL keeps its own DSN exactly as before. PG goes through `bizdb.OpenBusinessForTool` (`DB_DRIVER=postgres`).
- **PG is report only:** it counts the placeholder rows it would seed. The UPDATE text is portable (content_json is text on PG) and runs only on MySQL for now.

### update-restaurant-info (4)

- **Opening the DB:** MySQL keeps its own DSN. PG is gated and report only, running just the verify SELECT (PG twin with `CAST(? AS integer)`; the restaurant id comes from env as a string).
- **The upsert** has a PG twin with `ON CONFLICT (restaurant_id)`, the only key (PK). It is not executed on PG yet.
- **The 2 `ALTER TABLE ADD COLUMN`** statements stay **mysqlOnly** and run only in the MySQL branch. They are a legitimate MySQL bootstrap schema change. On PG the schema comes only from the import plan, where the website and menu_url columns already exist.

## Remaining mysqlOnly (17), with justification

| Site | Why it stays MySQL-only |
|---|---|
| `internal/db/migrations` Apply / ensureMigrationsTable / isApplied / markApplied (4) | MySQL migration bootstrap. PG never runs them (server, ops and tools skip them on PG). |
| `internal/db/mysql.go` ensureDatabaseExists (1) | MySQL bootstrap. |
| `internal/dbmigrate` export (3) and schema scanAll (1), `cmd/dbmigrate` openMySQL (1) | MySQL source reads for the import. |
| `cmd/update-restaurant-info` ALTER ×2 | MySQL bootstrap schema change inside the MySQL-only branch. |
| **API (5)**, see the audit below | MySQL arms of explicit dialect branches, each with a PG twin. |

## API audit: each mysqlOnly site is the MySQL arm of a proven `sqlDialect` branch with a native PG twin

| Site | Branch | PG twin |
|---|---|---|
| `assistant_catalog_tools` ×2 (lines 199/201) | `if sqlDialect == sqldb.Postgres { … return pgOnlySQL(…) }` | Quoted identifiers, `NULLIF(CAST(? AS text),'')` with `padSpaceNullIfArg`, and `ROW(..) IS DISTINCT FROM`, so it reproduces MySQL's changed-rows count. |
| `backoffice_technical_sheet_steps` (143) | `if sqlDialect == sqldb.Postgres` | Park/unpark two-step renumber (PG has no `UPDATE ... ORDER BY`; per-row unique key check). |
| `booking_documents` (413) | `if sqlDialect == sqldb.Postgres` | `INSERT .. SELECT .. FOR SHARE OF b RETURNING id`, which reproduces the InnoDB shared lock on the booking. |
| `seo_item_routes` (700) | `if sqlDialect != sqldb.Postgres` | `seo_route_upsert_tx_v1`: one tx, route-key ON CONFLICT, then a savepoint for the slug-key 23505. |

None of these MySQL texts is reachable on PG. Each PG path is native, so nothing unported is hidden behind a marker. The PG twins are still QA-pending.

## Gates (not waived)

- All 7 ported utilities are report or dry-run only on PG until runtime QA.
- VINOS, DIA and FINDE are absent from the source schema (preserved).
- Accent-insensitive LIKE.
- PAD SPACE on unverified unique keys.
- NUL in jsonb.
- Notifications are at-most-once.
- Capacity is code review only.
- 10 guarded IGNORE sites.
- 1117 items pending; 4 NOT VALID FKs and 731 orphans.
- The local source environment differs from production.
- General gates.

## Next

1. **Unique-key writer audit of the 121 keys, in bounded chunks of about 30.** Start with the 13 "unknown" and 15 "human" keys from `collation-unique-keys-r93.tsv`: per key, list writers and readers, show canonicalisation evidence, and decide on a PAD remedy.
2. **Manual read-only app-SQL QA on the next set of ported handler SELECTs.**

## Files

- `cmd/{chatgpt-plugin-token,migrate-wine-images,seed-legal-pages,update-restaurant-info}/{main.go,dialect.go}`

Backup: `/tmp/ut2.bak.tar`.

