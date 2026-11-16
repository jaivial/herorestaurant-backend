# result-native-wa4 — WhatsApp special-admin satellites (36 sites)

**Nothing here is runtime-verified (0 sites); it is a syntax port only. PG is not ready (ready:false).** MySQL stays the default, and the server still refuses `DB_DRIVER=postgres`. No propose, approve, execute, rice change, provider, notify or WhatsApp path was run, and nothing was sent.

## Scope: 36 sites, all 6 files complete
| File | Sites |
|---|---|
| `whatsapp_bot_floor.go` | 7 |
| `whatsapp_bot_groupmenu_admin.go` | 8 |
| `whatsapp_bot_groupmenu_remove.go` | 6 |
| `whatsapp_bot_rice_admin.go` | 6 |
| `whatsapp_bot_mobility_admin.go` | 5 |
| `whatsapp_bot_flags.go` | 4 |

## Build and inventory
- `go build -p 2 ./...` gave EXIT=0, under the inherited CPU 200% / 6G limits. No tests, vet, fixtures or Playwright.
- `gofmt -l` lists 8 other files that were already unformatted before this checkpoint; I did not touch them.
- Inventory: `/tmp/villacarmen-bot-20261111/sql-inventory-r50.json`.
  - Totals: canonical 2475 = native 1600 (includes 3 provisional) + blocked 3 + unwrapped 872. Runtime-verified: 0.
  - Other figures: dynamic 125; portable audit failures 0.
- Count change from r49: native +36, unwrapped −36.
- Lexical check of the dialect pairs: clean.
- Ledger: `docs/dbmigrate/native-ledger-whatsapp.json`.
  - WhatsApp domain: 125/316 ported, 191 remaining (30 dynamic), 9 files complete.

## Semantics (compile-only)
### Shared select
- The ported execute paths now use `botSpecialRowSelectSQL()` plus `FOR UPDATE`.
- The MySQL const `botSpecialRowSelect` stays, because `whatsapp_bot_extras_admin.go` (not yet ported) still uses it.

### Statements
- **Static UPDATEs written with backtick identifiers:** paired `nativeSQL` variants with double quotes.
- **Dynamic SETs:** whitelisted column names, quoted per dialect, rebound once.
- **`result_json` UPDATEs:** portable.
- **`booking_modifications` inserts:** `NOW()` becomes the naive UTC statement time.
- **`*CurrentSelect` consts:** contain no mixed-case columns, so they are wrapped with `portableSQL` at each call.
- **Group-menu price:** `menus.price` is numeric(10,2) NOT NULL. On PG, `COALESCE(price, '')` would coerce `''` to numeric and fail, so the PG twin uses `COALESCE(CAST(price AS text), '')`. That text equals the MySQL DECIMAL driver bytes.

### Interface callers (reviewed manually)
- **`botGMenuQuerier`:** propose passes `s.db`; execute passes the claim transaction.
  - The old-title read falls back to a placeholder on any error in the source, and execution continues. When `q` is a `*sql.Tx`, that read now runs in `sqldb.TxStatementScope` (`ErrNoRows` excluded), so a failure cannot abort the PG transaction.
  - `botGMenuFetchMenu` errors still propagate, so it fails closed.
- **Floor `readRows` (`botRowQuerier`, plain connection or transaction):** the callers build the query from portable literals, which are wrapped once at the call; errors propagate.
  - The source has no `ORDER BY` there, so row order, and therefore merge order, is arbitrary on both engines. Not proven equivalent.

## Corrected fact
- **`modification_history` has no `restaurant_id`.** This is production-confirmed, not dump-only: `production-readonly-metadata-20261111.tsv`, lines 71–77, from the coordinator's read-only check at 16:38. The production columns are `id`, `booking_id`, `customer_phone`, `field_modified`, `old_value`, `new_value` and `modification_date`.
- **Still dump-only, not confirmed in production:** the missing website columns, `restaurants.phone`, `group_menus` and `wines`. These are not in that artifact.

## Gates (all unwaived)
- **Cutover handling of pending proposals and backups:** cross-engine byte QA and a drain-or-rehash plan are needed; neither is accepted yet.
- **TIME range:** MySQL ±838 h vs PG 24 h, unresolved.
- **Locking gaps:**
  - Duplicate-create race on a slot: MySQL gap locks have no PG equivalent. The candidate advisory lock only works if every writer takes it.
  - Document insert vs bot cancel: relies on the booking row lock, because there is no FK; unverified.
  - Reference-row gap locks in `botSpecialLockRefs`.
- **Import proof:** 1117 pending, ready:false; 4 NOT VALID FKs with 731 orphans.
- **General semantics:** collation and PAD SPACE; NUL bytea QA; jsonb `\u0000` silently dropping audit rows; numeric, JSON and locking QA.
- **Email:** bounce invalid UTF-8.
- **UUID:** POS `UUID()` v1 (candidate `uuid-ossp`, technical work); registrar UUID.
- **Site domain:** SEO two-step upsert; instatic multi-key upsert.
- **Wiring:** analytics dialect.

## Next coherent checkpoint, 37 sites: the remaining proposal/execute admin files
| File | Sites | Notes |
|---|---|---|
| `whatsapp_bot_extras_admin.go` | 6 (2 dynamic) | last user of the MySQL `botSpecialRowSelect` const |
| `whatsapp_bot_catalog_extras.go` | 10 | |
| `whatsapp_bot_config_admin.go` | 13 (6 dynamic) | `botConfigMesasBefore` / `SalonCondesaBefore` queriers with `forUpdate` |
| `whatsapp_bot_notif_settings.go` | 5 | |
| `whatsapp_bot_special_create.go` | 3 | |

After that: `backoffice_uazapi_provisioning` 17, `whatsapp_bot_context_sqlite` 16, `whatsapp_outbox` 9, `conversation_history` 8, `whatsapp_bot_rag` 8 (has BeginTx), and so on.

## No actions taken
No SQL execution, DB writes, activation, providers, payments, email, WhatsApp, sends, `.env`/proof edits, commit, merge or deploy.

