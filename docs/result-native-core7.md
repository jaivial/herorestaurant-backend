# Native PG port: core checkpoint 7 (core SQL syntax complete, NOT PG-ready)

## Counts (inventory r12: /tmp/villacarmen-bot-20261111/sql-inventory-r12.json)
- **Core:** core_backoffice SQL is 406/406 native (0 remaining, 0 dynamic remaining) across 63/63 files.
  - Ledger: `docs/dbmigrate/native-ledger-core.json` (`syntax_complete:true, runtime_verified:false, pg_ready:false`).
- **Repo:** 2477 call sites, 406 native, 2071 not yet ported, 303 dynamic. portable_audit_failures is 0.
  - The audit only sees static portableSQL texts. The dynamic sites wrapped this phase were reviewed manually; see below.
- **Build:** `go build -p 2 ./...` exits 0 under the inherited cgroup (CPU 200%, 6G). Nothing was run: no SQL, tests, vet, fixtures or Playwright.

## This phase: 30 sites
- **ad_tools (8):**
  - Plain SELECT/DELETE/UPDATE are portable, including the NOT IN (SELECT … LIMIT ?) derived table.
  - The revision INSERT uses `CAST(? AS jsonb)`; the revision SELECT reads `CAST(design_json AS text)`.
- **backoffice_accounting_exports (5, including dynamic 265 and INSERT IGNORE 335):**
  - Sales and refunds:
    - `to_char(service_date,'YYYY-MM-DD')`;
    - `CAST(ROUND(qty*cents)-discount AS bigint)`, with half-away rounding on both dialects;
    - enum cast to text;
    - refunds `ORDER BY r.id, rl.id NULLS FIRST`, matching MySQL's NULL-first ascending order.
  - Payments: enum cast to text.
  - Stock:
    - `CAST(occurred_at AS date)`;
    - numerics cast to text;
    - `COALESCE(unit_cost, 0::numeric(18,6))`, which keeps MySQL's "0.000000" scale.
  - `accountingSimpleCSV` takes only caller-selected fixed statements (selectedSQL).
  - INSERT IGNORE becomes a VALUES CTE plus `EXISTS restaurants FOR KEY SHARE` plus `ON CONFLICT (restaurant_id,export_type,period_from,period_to,payload_hash) DO NOTHING`.
    - The enum is cast to `enum__accounting_exports__export_type`. `kind` is whitelisted and the hash is 64 hex chars, so there is no truncation or enum-coercion IGNORE path.
- **beverage_options (6):**
  - `ON CONFLICT (menu_id, beverage_option_id)`, keeping the explicit `updated_at = CURRENT_TIMESTAMP`.
  - `ON CONFLICT (restaurant_id, slug)`.
  - The rest are portable.
- **n8n_reminder (5):**
  - DATE_FORMAT/TIME_FORMAT become to_char; the camelCase columns are quoted; date and time params are cast explicitly.
  - conversation_states `expires_at` gets `CAST(? AS timestamp) AT TIME ZONE 'UTC'`, because MySQL reads the zoneless string as session UTC. That existing local-vs-UTC behaviour is preserved, not fixed.
  - `updated_at = now()`.
- **public_unsubscribe (5, including INSERT IGNORE):**
  - The table has no FK. `ON CONFLICT (restaurant_id,channel,target) DO NOTHING`.
  - `reason` is `CAST AS varchar(255)`, matching IGNORE's character truncation. `target` is prechecked at ≤190 bytes.
  - CONCAT becomes `||` (NULL propagation is the same).
  - `LIKE '%@%'` gets `COLLATE "C"`. The pattern has no letters, so CI and accent handling don't matter.
- **server.go:1696 (dynamic wine query):**
  - The fragments were reviewed: `vinoV2From` and `vinoV2WhereLegacyNumJoined` are valid on both dialects.
  - `vinoV2NumExpr` (CAST AS SIGNED) gets a PG `CAST(... AS bigint)` at this site.
  - `has_foto` becomes CASE 0/1, because it is scanned into an int.
  - The shared fragments are unchanged, and other domains still use them.

## New known behaviour deltas (in native-semantic-notes-core.json)
- **Stock CSV:** decimal/enum rendering needs a manual CSV byte compare.
- **design_json:** jsonb text normalisation differs from MySQL's. Go only checks it for null or empty.
- **Wine num:** CAST AS bigint errors above 2^63, where MySQL SIGNED wraps (range class). `tipo` equality depends on the CI collation decision.
- **beverage options:** the restaurant_beverage_options upsert depends on the ON UPDATE gap.
- **unsubscribe:** `target` uniqueness depends on the ICU CI collation.
- **FOR KEY SHARE:** the race review still applies.
- **conversation_states:** the `last_message_at` ON UPDATE gap.

## Read-only trace (no fix applied, for later review)
**bookings.created_at:**
- `handleValidateBookingModifiable` (POST /validate_booking_modifiable.php, modifications_api.go:597) selects `created_at`.
- The exported schema and all 220 migrations define no bookings.created_at. The booking creation timestamp column is `added_date` (timestamp, used by migration 025 indexes).
- `createdAt` is only declared and scanned, never used afterwards. On both dialects the handler currently returns `success:false` with the DB error message.
- Minimal fix candidates:
  - (a) drop `created_at` from the SELECT and the Scan;
  - (b) select `added_date`.
- (a) is smallest, with no behaviour reliance. Uncertainty: production may intend this error path. Not changed here.

**modification_history.restaurant_id:**
- No migration file creates or alters modification_history. It exists only through the runtime `ensureModificationHistoryTable` (sync.Once), which is called from the validate and save handlers.
- The exported source has no `restaurant_id` and no idx_restaurant_booking, so MySQL's ADD COLUMN, whose errors are ignored, evidently never succeeded or never ran in the source process.
- Yet the 678/748 queries use restaurant_id and would fail without it.
- The PG startup DDL adds the column exactly as MySQL would. This does **not** mean the source had it.
- Fix candidates:
  - a reviewed migration adding `restaurant_id` plus its index (MySQL and PG);
  - or confirming the production column state through the read-only source.
- Cause unknown.

## Unchanged blockers
- CI/accent/PAD SPACE.
- 190 unsigned range narrowings.
- ON UPDATE gap on 136 tables.
- The recent_conversations view.
- NUL storage-adapter QA.
- 4 NOT VALID FKs covering 731 orphans.
- 1117 pending, ready:false.
- Stripe CAST AS UNSIGNED / amount render.
- Production div_precision_increment.
- The source anomalies above.

MySQL stays the default and the server still refuses PG. No DB writes, activation, providers, sends, .env/proof edits, commits or deploys.

## Next domain: bookings (160 sites, 24 files, 0 native)
| file | sites | dynamic |
|---|---|---|
| backoffice_booking_documents_ws.go | 1 | 0 |
| backoffice_booking_mutations.go | 11 | 0 |
| backoffice_bookings.go | 11 | 2 |
| backoffice_bookings_search.go | 2 | 2 |
| backoffice_special_dates.go | 13 | 2 |
| booking_availability_context.go | 3 | 0 |
| booking_checkout.go | 15 | 0 |
| booking_documents.go | 11 | 0 |
| booking_extras.go | 4 | 0 |
| booking_insert.go | 3 | 0 |
| booking_notification_settings.go | 2 | 0 |
| booking_reminders.go | 6 | 0 |
| bookings_admin.go | 14 | 1 |
| floors_date.go | 6 | 0 |
| location_booking.go | 5 | 0 |
| public_booking_api.go | 5 | 0 |
| public_reservation_pages.go | 6 | 0 |
| reservation_day_context.go | 3 | 1 |
| reservation_self_service.go | 5 | 1 |
| reservation_special_dates_public.go | 3 | 0 |
| reservations_availability.go | 17 | 1 |
| special_booking_qr.go | 5 | 0 |
| special_booking_snapshot.go | 8 | 2 |
| special_date_stats.go | 1 | 0 |

