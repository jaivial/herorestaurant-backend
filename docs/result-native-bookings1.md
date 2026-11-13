# Native PG port: bookings group 1 (compile only, NOT runtime-verified, NOT PG-ready)

## Counts (inventory r13: /tmp/villacarmen-bot-20261111/sql-inventory-r13.json)
- **Repo:** 2477 call sites, 444 native, 2033 remaining, 297 dynamic. portable_audit_failures is 0.
- **Core:** still 406/406 (syntax complete, semantics not verified). The core ledger is unchanged.
- **Bookings:** 38/160 native. 5/24 files are done, 122 sites remain (6 dynamic).
  - Ledger: `docs/dbmigrate/native-ledger-bookings.json` (per file, with remaining lines and pending deltas).
- **Build:** `go build -p 2 ./...` exits 0 under the inherited cgroup (CPU 200%, 6G). No SQL was run, and no tests, vet, fixtures or Playwright.

## Converted (38 sites, including 6 dynamic)
- **backoffice_booking_documents_ws (1):** portable tenant guard.
- **backoffice_bookings_search (2 dynamic):**
  - The composed COUNT and data query are rebound once at the call site.
  - The PG SELECT head is `bookingListSelectPG`, with the same column order:
    - to_char date/time;
    - `to_char(added_date AT TIME ZONE 'UTC')`;
    - quoted `"babyStrollers"` and `"highChairs"`.
  - LIKE clauses get PG fragments.
- **backoffice_bookings (11, including 2 dynamic):**
  - The date list reuses the same head.
  - Prefix search and FULLTEXT have PG fragments; see the gaps below.
  - Cancel tx: SELECT, `INSERT cancelled_bookings` with `now() AT TIME ZONE 'UTC'` (datetime), and a DELETE whose RowsAffected check is preserved.
  - The cancelled and modified tabs use to_char.
  - Reactivate tx casts the varchar `reservation_time` to time.
- **backoffice_booking_mutations (11):**
  - The rice list's FORMAT becomes `to_char(ROUND(..))`.
  - `boInsertBooking` uses `sqldb.InsertID` (RETURNING id) inside its tx.
  - The update, export and byID queries use quoted columns, to_char and the UTC added_date.
  - The booking_modifications INSERT uses `now() AT TIME ZONE 'UTC'`.
  - The flag UPDATEs and SELECTs are portable.
- **backoffice_special_dates (13, including 2 dynamic):**
  - The special_dates upsert becomes `ON CONFLICT (restaurant_id, date)` (the real unique key):
    - `COALESCE(?, special_dates.whatsapp_only)`;
    - `EXCLUDED.*`;
    - `CAST(? AS jsonb)` for adelanto_payment_methods.
  - mobility_day_override becomes `ON CONFLICT (restaurant_id, "reservationDate")`.
  - The reservation_manager latest-row join quotes the camelCase columns.
  - The special_date_menus insert uses InsertID.
  - Dynamic IN-lists are composed and rebound once. The label CONCAT is the only fragment that differs.
- **New helper `sqlFragment(mysql, pg)`** in sql_native.go: picks a per-dialect fragment **without** rebinding, so a composed statement is rebound exactly once by portableSQL at the call site. nativeSQL must not be used on fragments that contain "?".
- **Booking policy guards are untouched.** That covers the 35-day horizon, event stickiness and the management phone. Only SQL text changed. No writes were executed.

## Semantic gaps (pending, none waived)
- **FULLTEXT:**
  - MySQL: InnoDB BOOLEAN MODE `+tok*`.
  - PG: `to_tsquery('simple', 'tok:* & ...')` over the plan GIN index expression.
  - Not equivalent in tokenisation (emails, punctuation), stopwords or accent folding. A tsquery error falls back to the prefix path, as MySQL's errors do.
  - **Needs a decision and QA before cutover.**
- **Name/email LIKE:** case-insensitive through LOWER plus `COLLATE "C"`, but **accent-sensitive**, unlike general_ci (CI search ledger).
- **cancelled_bookings.reservation_time** is varchar: invalid stored strings give NULL on MySQL and an error on PG.
- **special_date_menus insert:** a LastInsertId error is now surfaced. A RETURNING error surfaces on PG.
- **ON UPDATE gap:** affects the special_dates and special_date_menus `updated_at`.
- **adelanto_payment_methods:** jsonb text rendering class.
- **Rice FORMAT:** needs manual QA.
- **Unchanged:**
  - CI/accent/PAD SPACE.
  - 190 range narrowings.
  - ON UPDATE gap on 136 tables.
  - The recent_conversations view.
  - NUL storage QA.
  - 4 NOT VALID FKs covering 731 orphans.
  - 1117 pending, ready:false.
  - Stripe.
  - div_precision_increment.
  - The FOR KEY SHARE race.
  - The bookings.created_at and modification_history.restaurant_id source anomalies (research only, not fixed).

MySQL stays the default and the server still refuses PG. No DB writes, activation, providers, sends, .env/proof edits, commits or deploys.

## Next bounded bookings group (suggested ~40 sites)
| file | remaining | dynamic |
|---|---|---|
| reservations_availability.go | 17 | 1 |
| booking_checkout.go | 15 | 0 |
| booking_insert.go | 3 | 0 |
| booking_availability_context.go | 3 | 0 |
| reservation_day_context.go | 3 | 1 |

These are the availability, insert and checkout paths, and they carry the horizon and capacity guards, so review must keep the guards intact.

**Later:**
| file | remaining | dynamic |
|---|---|---|
| bookings_admin | 14 | 1 |
| booking_documents | 11 | 0 |
| special_booking_snapshot | 8 | 2 |
| booking_reminders | 6 | 0 |
| floors_date | 6 | 0 |
| public_reservation_pages | 6 | 0 |
| location_booking | 5 | 0 |
| public_booking_api | 5 | 0 |
| reservation_self_service | 5 | 1 |
| special_booking_qr | 5 | 0 |
| booking_extras | 4 | 0 |
| reservation_special_dates_public | 3 | 0 |
| booking_notification_settings | 2 | 0 |
| special_date_stats | 1 | 0 |

