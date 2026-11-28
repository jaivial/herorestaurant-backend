# result-native-wa5 — WhatsApp proposal-admin files (37 sites)

**Nothing here is runtime-verified (0 sites); it is a syntax port only. PG is not ready (ready:false).** MySQL stays the default, and the server still refuses `DB_DRIVER=postgres`. No proposal approval, notify, provider, configuration change or WhatsApp path was run, and nothing was sent. Business permissions and the 35-day and event guards are unchanged.

## Scope: 37 sites, all 5 files complete
| File | Sites |
|---|---|
| `whatsapp_bot_extras_admin.go` | 6 |
| `whatsapp_bot_catalog_extras.go` | 10 |
| `whatsapp_bot_config_admin.go` | 13 (6 dynamic) |
| `whatsapp_bot_notif_settings.go` | 5 |
| `whatsapp_bot_special_create.go` | 3 |

## Build and inventory
- `go build -p 2 ./...` gave EXIT=0, under the inherited CPU 200% / 6G limits. No tests, vet, fixtures or Playwright.
- Inventory: `/tmp/villacarmen-bot-20261111/sql-inventory-r51.json`.
  - Totals: canonical 2475 = native 1637 (includes 3 provisional) + blocked 3 + unwrapped 835. Runtime-verified: 0.
  - Other figures: dynamic 117; portable audit failures 0.
- Count change from r50: native +37, unwrapped −37.
- Lexical check of the dialect pairs: clean.
- Ledger: `docs/dbmigrate/native-ledger-whatsapp.json`.
  - WhatsApp domain: 162/316 ported, 154 remaining (22 dynamic), 14 files complete.

## Semantics (compile-only)
### Shared special-row select
- `extras_admin` was the last caller of the MySQL `botSpecialRowSelect`. It now uses `botSpecialRowSelectSQL()`.
- The const is kept as the MySQL half of that function.

### Upserts
Each ON CONFLICT target below is the only collidable unique key in the plan.
- **`restaurant_booking_extras`:** `ON CONFLICT (restaurant_id, slug)`.
- **`restaurant_days`:** multi-row `ON CONFLICT (restaurant_id, date)`. This is safe only because `botConfigValidateDates` removes duplicate dates first; PG errors if one statement touches the same row twice.
- **`mesas_de_dos` / `mesas_de_tres`:** `ON CONFLICT (restaurant_id, "reservationDate")`. The table name is fixed by `botConfigMesasTable`.
- **`salon_condesa`:** `ON CONFLICT (restaurant_id, date)`.
- **`booking_notification_settings`:** `ON CONFLICT (restaurant_id)`, its primary key.
- **`reservation_manager`:** DELETE then INSERT, as in the source, with the camelCase columns quoted.

### Config queriers with `forUpdate` (reviewed manually)
- Callers of `botConfigDayBefore`, `DailyLimitBefore`, `MesasBefore` and `SalonCondesaBefore`:
  - propose passes `s.db` with `forUpdate=false`;
  - execute passes the claim transaction with `true` for the before re-read;
  - the re-read after the write uses the transaction with `false`.
- Errors propagate, so all of them fail closed.
- **Locking gap:** on MySQL, `FOR UPDATE` under REPEATABLE READ also gap-locks absent dates, so a concurrent panel insert for the same (restaurant, date) waits. PG locks only the rows that exist.
  - The unique keys still prevent duplicate rows.
  - The before-key snapshot can race, though. Open.

### Types
- **Notification settings:** Go bools into smallint use `CAST(CAST(? AS boolean) AS integer)` on PG; the MySQL driver still sends 1/0.
- **Booking ids:** `InsertID` (RETURNING). go-sql-driver v1.9.3's `LastInsertId` never errors, so the MySQL path is identical.
- **Before-keys built from DATE columns scanned into strings:** both engines produce `time.Time`, rendered as an RFC3339 string. Source review only.

### Collation
- The slug unique index is `ci_ai`. It keeps case-insensitive collisions; the accent and PAD SPACE differences are still open.

## Gates (all unwaived)
- **Proposal hashes and backups across the cutover:** need cross-engine byte QA plus a drain-or-rehash plan.
- **TIME range:** MySQL ±838 h vs PG 24 h.
- **Locking gaps:**
  - Duplicate-create race on a slot: the candidate advisory lock would have to be taken by every writer.
  - Document insert vs cancel: relies on the booking row lock; unverified.
  - Reference-row gap locks.
  - New: config before-key gap locks.
- **General semantics:** collation and PAD SPACE; NUL bytea QA; jsonb `\u0000` silently dropping audit rows; numeric, JSON, ID and locking QA.
- **Email:** bounce invalid UTF-8.
- **UUID:** POS `UUID()` v1; registrar UUID.
- **Site domain:** SEO two-step upsert; instatic multi-key upsert.
- **Wiring:** analytics dialect.
- **Import proof:** 1117 pending, ready:false; 4 NOT VALID FKs with 731 orphans.

**Source facts:**
- `modification_history` has no `restaurant_id` — confirmed in production at 16:38.
- Still dump-only, not confirmed in production: the missing website columns, `restaurants.phone`, `group_menus` and `wines`.

## Next coherent checkpoint, 35 sites: WhatsApp provider and connection configuration (SQL only, no provider calls)
| File | Sites |
|---|---|
| `backoffice_uazapi_provisioning.go` | 17 |
| `backoffice_uazapi_servers_admin.go` | 4 (1 dynamic) |
| `whatsapp_bot_ai_config.go` | 4 |
| `whatsapp_bot_admin.go` | 4 |
| `whatsapp_connection_watchdog.go` | 3 |
| `whatsapp_bot_webhook.go` | 3 |

After that: `whatsapp_bot_context_sqlite` 16 (bot_context schema), `whatsapp_outbox` 9, `conversation_history` 8, `whatsapp_bot_rag` 8 (has BeginTx), and so on.

## No actions taken
No SQL execution, DB writes, activation, providers, payments, email, WhatsApp, sends, `.env`/proof edits, commit, merge or deploy.

