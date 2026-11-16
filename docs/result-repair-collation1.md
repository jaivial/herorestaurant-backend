# result-repair-collation1: bounded collation repair, POS clock fix, first manual read-only app-SQL QA

All changes are code only. `go build -p 2 ./...` exits 0. I ran no tests and no SQL, wrote to no DB, and started no server. Runtime-verified app SQL is 0, PG is still ready:false, and R5 is untouched. The forward proposals are **not applied**.

## Inventory r93

`/tmp/villacarmen-bot-20261111/sql-inventory-r93.json` has 2498 sites, all classified. Only the PG text changed: 4 statements in `backoffice_pos_catalog`.

## Evidence used (coordinator manual read-only checks, 05:43 CET)

- **Collation comparisons.** PG `dbmigrate.ci_ai` gives `'main' = 'main '` **FALSE**, `'jose' = 'josé'` TRUE, and `'ß' = 'ss'` TRUE. MySQL `utf8mb4_unicode_ci` gives TRUE for all three. So the only measured delta is **PAD SPACE**.
- **Extensions.** `unaccent` and `uuid-ossp` are available but not installed. I added no dependency.
- **Trailing-space keys** (`COLLATE "C"` with `rtrim`, rows with a trailing space / total rows):

  | Table | Result |
  |---|---|
  | bo_users | 0/38 |
  | campaign_suppressions | 0/1007 |
  | message_deliveries | 0/15 |
  | menu_visibility | 0/2 |
  | pos_customers | 0/0 (empty) |
  | pos_product_categories | 0/0 (empty) |

  The two empty tables give no future-proof evidence.
- **bo_sessions.token_sha256:** all 219 values are lowercase. As instructed, this alone is **not** a reason to switch to "C".

## Code changes (bounded, grounded)

1. **POS category import, PAD SPACE (PG only).**
   - The source category writer is `INSERT … SELECT categoria` / `tipo` straight from `comida_items` / `group_menu_wines_v2`, with no trim. On MySQL (PAD SPACE), "Postres " and "Postres" are one category under `uq(restaurant_id,name)`, and the link join `pc.name = c.categoria` matches either one.
   - On PG, ci_ai would have created a second category and linked to that.
   - The PG text now uses `rtrim(x, ' ')` (**ASCII space only**, not Go TrimSpace or Unicode) on the inserted name and on both link joins. This mirrors PAD SPACE equality. The MySQL text is unchanged.
   - Every other POS name writer (UI create and update for categories, modifiers, packs, kitchen stations, service periods, tags) already calls `strings.TrimSpace` before writing, so they never store a trailing space. No change needed.
2. **POS clock suffix.** `posClockMinutes` used to parse only the first 5 characters, so "23:59:99" passed and then failed at write time on both engines. It now requires either exactly HH:MM or a strict HH:MM:SS (the shared `validClockHHMMSS`). Invalid input gets the existing 400. This applies to both dialects: service periods and `business_day_cutoff`.

## Audited, no change (already canonical)

- **Suppressions:** writers and readers go through `campaignCanonicalTarget`: email = lower(TrimSpace), phone = digits. No trailing space is ever stored, and the probes are canonical. The PAD gap cannot apply. The DB collation lookup from suppression2 stays as it is. No new identity merging.
- **bo_users login:** the identifier is `ToLower(TrimSpace)`.
- **pos_customers:** email is lower(TrimSpace); phone is normalized.
- **message_deliveries:** keys are generated in code.
- **menu_visibility:** keys are constants.

## All 121 text unique keys

`docs/dbmigrate/collation-unique-keys-r93.tsv` lists every key with its text columns. The class assigned is a **heuristic from the column name, not verified per writer**:

| Class | Keys |
|---|---|
| opaque | 53 |
| code | 32 |
| human | 15 |
| contact | 8 |
| **unknown** | **13** |

Only the keys named above are verified.

## Forward proposal (applied: false; plan r4 and proof are not edited)

- **PAD SPACE on PG unique keys.** For each key whose writers are *not* verified canonical, add a forward unique expression index on `(…, rtrim(col, ' '))` with ci_ai, or add a normalized generated column. This must be done together with every writer and reader of that key. Do the 13 unknown and 15 human keys first, after per-writer verification.
- **Opaque identifiers.** Keep ci_ai. Do not switch to "C" until the generators and callers prove the source never relied on case-insensitive equality.
- **Accent-insensitive LIKE.** The source is accent-insensitive; the PG port is `LOWER(col) COLLATE "C" LIKE`, which is case-insensitive only. That gap stays and **PG stays off for search parity**. Installing `unaccent` would be a separate decision: it matches `utf8mb4_unicode_ci` only approximately, so it would not be claimed equivalent.

## Manual READ-ONLY app-SQL QA readiness (for the coordinator; not run by code)

These are the PG texts exactly as the ported handlers emit them, after rebind. Run each in a read-only session (`SET default_transaction_read_only = on`). Take `<rid>` from `SELECT id FROM restaurants ORDER BY id LIMIT 3;`. Use metadata only: no auth, secrets or provider data.

1. **Bot capacity** (`botDayCapacityOn`):
   - `SELECT "dailyLimit" FROM reservation_manager WHERE restaurant_id = $1 AND "reservationDate" = $2 ORDER BY id DESC LIMIT 1;` and `SELECT daily_limit FROM restaurant_reservation_defaults WHERE restaurant_id = $1 LIMIT 1;`
   - `SELECT COALESCE(SUM(party_size),0) FROM bookings WHERE restaurant_id = $1 AND reservation_date = $2;` with `<rid>, '2023-09-23'`.
   - Compare against MySQL `villacarmen_source`.
2. **Suppression filter:** the `campaignSuppressionQueryPG` text (the seven-branch union, `COLLATE "C"`) with `<rid>` three times. Compare the row count with the MySQL variant.
3. **Suppression collation probe:** `SELECT p.i FROM (SELECT 0 AS i, CAST($1 AS text) AS probe) p WHERE EXISTS (SELECT 1 FROM campaign_suppressions s WHERE s.restaurant_id = $2 AND s.channel = $3 AND s.target = p.probe COLLATE "dbmigrate"."ci_ai");`, using one existing target and an accent variant of it.
4. **Booking search:** `SELECT id FROM bookings WHERE restaurant_id=$1 AND (LOWER(customer_name) COLLATE "C" LIKE $2) LIMIT 5;` with `'%mar%'`. This shows the case-insensitive behaviour and the accent gap.
5. **Time scan:** `SELECT to_char(reservation_time,'HH24:MI:SS') FROM bookings WHERE restaurant_id=$1 ORDER BY id LIMIT 5;` should match the MySQL `TIME_FORMAT`.
6. **Defaults:** the `loadReservationDefaults` SELECT, with `<rid>`.
7. **Day-lock syntax only:** `EXPLAIN SELECT pg_advisory_xact_lock(hashtextextended(CAST('booking_day:1:2023-09-23' AS text),0));`. EXPLAIN does not take the lock; do not run it without EXPLAIN in a shared session.

## Gates (not waived)

- PAD SPACE on unverified keys.
- Accent-insensitive LIKE and search.
- The local MySQL source differs from production (sql_mode, server collation).
- NUL in jsonb for other writers.
- Notifications at-most-once.
- Capacity: code review only.
- 10 guarded IGNORE sites.
- 1117 pending; 4 NOT VALID FKs with 731 orphans.
- General gates.

## Files

- `internal/api/backoffice_pos_catalog.go`
- `internal/api/backoffice_pos.go`
- `docs/dbmigrate/collation-unique-keys-r93.tsv`

