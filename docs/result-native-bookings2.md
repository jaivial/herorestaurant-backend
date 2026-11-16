# Native PG port: bookings group 2 (compile only; equivalence NOT runtime-verified, NOT PG-ready)

## Counts (inventory r14: /tmp/villacarmen-bot-20261111/sql-inventory-r14.json)
- **Repo:** 2477 call sites, 485 native (+41), 1992 remaining, 295 dynamic. portable_audit_failures is 0.
- **Core:** 406/406, unchanged; syntax only.
- **Bookings:** 79/160 native. 10/24 files are done, 81 sites remain (4 dynamic).
  - Ledger: `docs/dbmigrate/native-ledger-bookings.json` (per file, with remaining lines and the cumulative pending deltas).
- **Build:** `go build -p 2 ./...` exits 0 under the inherited cgroup (CPU 200%, 6G). No SQL was run, and no tests, vet, fixtures or Playwright.

## Converted (41 sites)
- **reservations_availability (17, including dynamic loadClosedDayOverrides):**
  - Quoted `"reservationDate"`/`"dailyLimit"`. `is_open = FALSE` becomes `is_open = 0` (smallint).
  - mesas_de_dos and restaurant_days upserts become `ON CONFLICT` on their real unique keys.
  - The reservation_manager DELETE+INSERT replace is kept, and so is the latest-row join.
  - special_dates DATE_FORMAT becomes to_char.
  - The FROM-less COALESCE subquery SELECT is valid PG.
- **booking_checkout (15):**
  - The status-transition UPDATEs are kept, including the `WHERE status = ?` claim with its RowsAffected check. status always changes there, so changed rows equal matched rows. A concurrent claimer re-checks the row after the lock and gets 0 rows.
  - `paid_at`/`completed_at` use `now()` (timestamptz).
  - stripe_events INSERT IGNORE becomes a VALUES CTE:
    - `CAST varchar(128)` for event_id and type, matching IGNORE truncation;
    - jsonb payload (a constant `{}`);
    - `ON CONFLICT (event_id) DO NOTHING`, so a duplicate gives RowsAffected 0 and the same "duplicate" response.
  - The table has no FK.
- **booking_insert (3):** the public booking INSERT uses `sqldb.InsertID` (RETURNING id) in the same tx, with quoted camelCase columns. The salon and menu lookups are portable.
- **booking_availability_context (3)** and **reservation_day_context (3, including dynamic menus IN-list):** `menu_choose_main` is cast to text (JSON render class).
- **Policy, capacity and payment logic is unchanged:**
  - the 35-day horizon, event stickiness and management phone;
  - capacity sums and limits;
  - tx boundaries;
  - the Stripe signature, amount and metadata checks before the paid state.
  - Only the SQL text changed.
- **Cancelled-booking time guard** (reactivate, from group 1):
  - PG now returns NULL instead of a cast error for invalid stored text, via a `COLLATE "C"` regex for `H:MM`, `HH:MM` and `HH:MM:SS` with H ≤ 23.
  - **Bounded subset only.** MySQL's lenient string→TIME also accepts compact HHMMSS, `D HH:MM`, fractions and hours up to 838 (TIME range). PG `time` is under 24h. Those forms now yield NULL on PG. **Not claimed equal.**

## New known deltas
- **NOW() precision (new, global):**
  - MySQL NOW() has second precision and is stored truncated.
  - PG `now()` stored into a (0)-precision column **rounds** to the nearest second, so it can be +1s.
  - This affects every prior `now()` conversion into timestamp(0)/datetime(0) columns.
  - The exact fix is `date_trunc('second', now())`. It was not applied in this checkpoint, so all sites stay consistent. **Decision pending.**
- **JSON:** the mandatory_menus JSON text rendering class.
- **Unchanged:**
  - FULLTEXT (below).
  - CI/accent LIKE.
  - PAD SPACE.
  - 190 unsigned range narrowings.
  - ON UPDATE gap on 136 tables.
  - The recent_conversations view.
  - NUL storage QA.
  - 4 NOT VALID FKs covering 731 orphans.
  - 1117 pending, ready:false.
  - Stripe CAST AS UNSIGNED.
  - div_precision_increment.
  - The FOR KEY SHARE race.
  - The created_at and modification_history anomalies.

## FULLTEXT read-only investigation: `docs/dbmigrate/fulltext-evidence-bookings.json`
- **Sites:** there is one MATCH site, on index `ft_bookings_customer_contact_commentary` (migration 025). It uses the default parser and general_ci columns.
- **Query builder:**
  - Lowercases the input.
  - Keeps only a-z, 0-9 and runes ≥128.
  - Drops tokens under 3 runes.
  - Turns each token into `+tok*`, so the user cannot inject operators.
  - Punctuation is deleted inside a token, so `ana@x.com` becomes `anaxcom`. That can't match the InnoDB-split tokens on MySQL either; the prefix LIKE branch is what finds emails.
- **Differences from the current PG variant:**
  - accent folding;
  - stopwords;
  - tokenisation (emails, URLs, hyphens);
  - stored token min length.
- **Production settings are needed (read-only):**
  - the innodb_ft_* variables, ft_boolean_syntax and ngram_token_size;
  - SHOW CREATE TABLE bookings;
  - the stopword list.
  - These are not captured. Restored-container defaults are not evidence.
- **Candidate equivalent, not implemented:** a custom PG text search configuration, built from those settings, with:
  - an InnoDB-like delimiter parser;
  - unaccent+lower folding;
  - the production stopword list;
  - a min-length filter;
  - a matching GIN expression.
  - It needs the unaccent extension and manual QA on the real data. **The current PG fragment is not final.**

MySQL stays the default and the server still refuses PG. No DB writes, activation, providers, sends, .env/proof edits, commits or deploys.

## Next bounded bookings group (~41 sites)
| file | remaining | dynamic |
|---|---|---|
| bookings_admin.go | 14 | 1 |
| booking_documents.go | 11 | 0 |
| special_booking_snapshot.go | 8 | 2 |
| booking_reminders.go | 6 | 0 |
| reservation_special_dates_public.go | 3 | 0 |

**Then:**
| file | remaining | dynamic |
|---|---|---|
| floors_date | 6 | 0 |
| public_reservation_pages | 6 | 0 |
| location_booking | 5 | 0 |
| public_booking_api | 5 | 0 |
| reservation_self_service | 5 | 1 |
| special_booking_qr | 5 | 0 |
| booking_extras | 4 | 0 |
| booking_notification_settings | 2 | 0 |
| special_date_stats | 1 | 0 |

