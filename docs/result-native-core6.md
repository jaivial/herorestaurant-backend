# Native PG port: core checkpoint 6

## Scope done (compile only, nothing SQL-runtime-verified)
- **Build:** `go build -p 2 ./...` exits 0, run under the inherited cgroup (CPU 200%, 6G). No tests, vet, fixtures or Playwright.
- **Inventory r11** (`/tmp/villacarmen-bot-20261111/sql-inventory-r11.json`):
  - Repo: 2477 call sites (one new PG-only DDL site), 376 native, 305 dynamic, portable_audit_failures 0.
- **Core:** 406 sites, 376 native, 30 remaining (2 dynamic). 57 of 63 files are done.
  - This phase converted 24 sites: modifications_api 12, events 6, modification_history 4, widget_routes 2. The previous checkpoint converted 11.
- **Ledgers refreshed from actual inventory:** `docs/dbmigrate/native-ledger-core.json` (per file, with remaining lines) and `native-semantic-notes-core.json` (new behaviour deltas appended).

### Per file
- **modifications_api:**
  - `"dailyLimit"`, `"reservationDate"` and `"hourData"` are quoted.
  - SUM/COUNT queries are portable.
  - The modification_history INSERT uses `sqldb.InsertID` (RETURNING id), with `now()` into a timestamptz column.
  - The dynamic `UPDATE bookings SET <col>` takes its column from a fixed lowercase whitelist, so it is portable at both call sites.
  - Line 597 selects `bookings.created_at`, a column that does **not exist** in the exported schema. It fails on both dialects. This is preserved as a source bug.
- **events:**
  - The message_deliveries INSERT uses `CAST(? AS jsonb)` plus RETURNING id.
  - `sent_at` is a naive datetime, so NOW() becomes `now() AT TIME ZONE 'UTC'`.
  - The status UPDATEs are portable.
- **modification_history:** gets an explicit PG twin of the runtime DDL. The MySQL DDL is untouched.
  - `CREATE TABLE IF NOT EXISTS` copies plan.sql exactly: identity id, types, `timestamptz(0) DEFAULT CURRENT_TIMESTAMP`, the octet_length checks, and the `pk__`/`fk__…ibfk_1` names with ON DELETE CASCADE.
  - Then the three `ix__modification_history__*` indexes via `CREATE INDEX IF NOT EXISTS`.
  - Then `ADD COLUMN IF NOT EXISTS restaurant_id integer NOT NULL DEFAULT 1`, `ix__modification_history__idx_restaurant_booking`, and `COMMENT ON TABLE`.
  - **Finding:** the exported source schema lacks `restaurant_id` and idx_restaurant_booking, although the MySQL ALTER would add them. The PG variant adds them on first call, exactly as MySQL does. Why the source lacks them is unexplained.
  - Empty-DB/new-tenant startup is not runtime-verified. A multi-process first-call DDL race exists, the same as on MySQL.
- **widget_routes:**
  - The SELECT is portable.
  - The dynamic upsert becomes `ON CONFLICT (restaurant_id) DO UPDATE SET col = EXCLUDED.col` on PG. restaurant_id is the PRIMARY KEY, and the columns come from a fixed list.

## Open semantic gaps (none waived)
- **checkcancel search:** a LIKE with `COLLATE "C"` is accent-sensitive, unlike MySQL general_ci, because PG17 doesn't allow LIKE on nondeterministic collations. This is a pending CI search gap.
- **rice_types:** FORMAT rendering needs manual QA.
- **ON UPDATE gap:** affects the upserts (salon_condesa, openinghours, mobility_day_override, widget_settings).
- **LastInsertId:** on PG, a RETURNING error now surfaces where MySQL used to ignore the LastInsertId error.
- **Unchanged:**
  - CI/accent/PAD SPACE.
  - 190 range narrowings.
  - ON UPDATE gap on 136 tables.
  - recent_conversations view.
  - Storage adapter QA.
  - 4 NOT VALID FKs covering 731 orphans.
  - 1117 pending, ready:false.
  - Stripe CAST AS UNSIGNED and amount render.
  - div_precision_increment production value.
  - FOR KEY SHARE race.

No DB writes, activation, providers, sends, .env/proof edits, commits or deploys. MySQL stays the default and the server still refuses PG.

## Next: the remaining 30 core sites
| file | lines |
|---|---|
| ad_tools.go (8) | 167, 443, 448, 572, 599, 608, 730, 733 |
| backoffice_accounting_exports.go (5) | 144, 191, 225, 265 (dyn), 335 (INSERT IGNORE) |
| beverage_options.go (6) | 35, 117, 134, 144, 155, 271 |
| n8n_reminder.go (5) | 232, 438, 457, 459, 469 |
| public_unsubscribe.go (5) | 135, 168, 193, 306, 315 (INSERT IGNORE) |
| server.go (1) | 1696 (dyn wine fragments) |

After that comes bookings (160 sites in 24 files).

