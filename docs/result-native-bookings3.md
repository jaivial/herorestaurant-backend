# Native PG port: bookings group 3 + NOW() semantics fix (compile only, NOT runtime-verified, NOT PG-ready)

## Counts (inventory r15: /tmp/villacarmen-bot-20261111/sql-inventory-r15.json)
- **Repo:** 2478 call sites, 528 native (+43), 1950 remaining, 292 dynamic. portable_audit_failures is 0.
  - The +1 site is the new PG-only booking_documents branch.
- **Core:** 406/406, unchanged.
- **Bookings:** 122/161 native. 15/24 files are done, 39 remain (1 dynamic).
  - Ledger: `docs/dbmigrate/native-ledger-bookings.json`.
- **Build:** `go build -p 2 ./...` exits 0 under the inherited cgroup (CPU 200%, 6G). No SQL was run, and no tests, vet, fixtures or Playwright.

## NOW() semantics fix (all prior ported sites)
- **Source behaviour:** MySQL `NOW()`/`CURRENT_TIMESTAMP` (fsp 0) is the **statement** start time, truncated to the second.
- **What was wrong:** PG `now()` is the *transaction* start time and rounds into (0) columns.
- **New rule in every PG DML variant:**
  - `date_trunc('second', statement_timestamp())`;
  - `(date_trunc('second', statement_timestamp()) AT TIME ZONE 'UTC')` for naive datetime columns and comparisons.
- **Applied to 36 occurrences:**
  - 35 `now()` in 20 files;
  - plus the beverage_options `updated_at = CURRENT_TIMESTAMP` upsert.
- **Audit:** 0 bare SQL `now()` remain in internal/api; only Go `h.now()` and JS `Date.now()` are left.
- **Not affected:** `NOW(3)`/`UTC_TIMESTAMP`/`CURDATE` occur only in still-unported domains (whatsapp_bot, premium, campaigns…), and will be handled there.
- **Not changed, inventoried pending** in `docs/dbmigrate/now-semantics-ledger.json`:
  - **plan.sql DEFAULTs:**
    - 287× `timestamptz(0) DEFAULT CURRENT_TIMESTAMP`;
    - 64× `timestamp(0) DEFAULT (CURRENT_TIMESTAMP AT TIME ZONE 'UTC')`;
    - 2× `timestamp(3)` defaults.
    - That totals 353, matching the source's 351 `CURRENT_TIMESTAMP` + 2 `CURRENT_TIMESTAMP(3)`.
  - The ON UPDATE trigger function on 136 tables (`now()`).
  - The modification_history runtime DDL default.
  - All of these are transaction-time and rounding. The fix candidate is a reviewed schema revision. **The imported proof schema is not modified.**

## Group 3 converted (43 sites)
- **bookings_admin (14, including dynamic list):**
  - Quoted camelCase columns; TIME_FORMAT becomes to_char.
  - Columns scanned into Go strings now reproduce MySQL's parseTime→RFC3339 text exactly: `YYYY-MM-DDT00:00:00Z` for date, and UTC `…T HH:MM:SSZ` for added_date/cancellation_date.
  - Cancel: `DELETE` keeps its RowsAffected check.
  - Reactivate INSERT uses `InsertID`. The ignored LastInsertId error now surfaces with the insert error.
  - The dynamic list is `sqlFragment` head + validated orderBy + LIMIT, rebound once.
- **booking_documents (11):**
  - The attached insert becomes an explicit PG branch: `INSERT … SELECT … FOR SHARE OF b RETURNING id`. That is the InnoDB shared-lock wait, and no row gives the same "la reserva ya no existe" error.
  - The unattached insert uses `InsertID`.
  - `DATE_FORMAT(created_at)` on the datetime column becomes to_char.
  - Soft-delete uses the statement-time UTC naive form.
- **special_booking_snapshot (8, including 2 dynamic):**
  - JSON is cast to text.
  - The DIA/FINDE/NUM lookup is quoted on PG.
  - The IN-list is rebound once.
- **booking_reminders (6):**
  - The claim INSERT IGNORE becomes a VALUES CTE with `CAST varchar(128)` plus `ON CONFLICT (delivery_key) DO NOTHING`. RowsAffected is 1 or 0, as before.
  - `sent_at` uses statement time.
- **reservation_special_dates_public (3):** to_char and JSON text.
- **Policy, capacity and payment logic is unchanged:** the 35-day horizon, event stickiness, capacity, payment checks and tx boundaries. Only SQL text changed.

## Source anomalies (blockers, preserved, not fixed)
- **special_booking_snapshot.go:672:** `SELECT name FROM menu_dishes_catalog`. The table has no `name` column (it has `title`), so it fails on both dialects. Candidate fix `title` needs review.
- **lookup("DIA"/"FINDE"):** these tables are absent from the exported schema, so it fails on both.
- **Earlier anomalies (research only):** bookings.created_at and modification_history.restaurant_id.

## Production read-only metadata commands
`docs/dbmigrate/production-readonly-metadata-queries.sql` uses only INFORMATION_SCHEMA and @@ variables, plus `SHOW CREATE TABLE bookings` (no secrets). It covers:
- div_precision_increment;
- the innodb_ft_* variables, ft_boolean_syntax and ngram;
- the bookings FULLTEXT definition;
- the stopword source (default list size and custom table name);
- presence of bookings.created_at/added_date, the modification_history columns and indexes, menu_dishes_catalog name/title, and the DIA/FINDE tables.

The coordinator runs these through the approved read-only source; the agent has no network. Restored defaults are not evidence.

## Open gaps (none waived)
- **FULLTEXT:** the PG approximation is not accepted.
- **Invalid cancelled time forms:** MySQL TIME allows up to 838h; PG time is under 24h, and the bounded regex returns NULL.
- **RFC3339 reinsertion:** the reactivate path's date text reinsertion.
- **Unchanged:**
  - JSON render class.
  - CI/accent/PAD SPACE.
  - 190 unsigned range narrowings.
  - ON UPDATE gap on 136 tables, plus its time semantics.
  - Schema default time semantics.
  - The recent_conversations view.
  - NUL storage QA.
  - 4 NOT VALID FKs covering 731 orphans.
  - 1117 pending, ready:false.
  - Stripe.
  - div_precision_increment.
  - The FOR KEY SHARE race.

MySQL stays the default and the server still refuses PG. No DB writes, activation, providers, sends, .env/proof edits, commits or deploys.

## Remaining bookings (39 sites)
| file | remaining | dynamic |
|---|---|---|
| public_reservation_pages | 6 | 0 |
| floors_date | 6 | 0 |
| location_booking | 5 | 0 |
| public_booking_api | 5 | 0 |
| reservation_self_service | 5 | 1 |
| special_booking_qr | 5 | 0 |
| booking_extras | 4 | 0 |
| booking_notification_settings | 2 | 0 |
| special_date_stats | 1 | 0 |

These 39 sites are one bounded group that finishes the bookings domain.

