# result-repair-types2: remaining TIME writers, collation plan, QA plan corrections

This work is code only. `go build -p 2 ./...` exits 0. In this batch I ran no tests and no SQL and wrote nothing to any database. The coordinator's 05:32 CET manual read-only preflight is quoted below as evidence; it is **not** app-SQL QA, so runtime-verified app SQL is still 0. PG is still ready:false, and the r5 proof is untouched.

## Inventory r92

r92 is identical to r91: 2498 sites, all classified. No SQL text changed; only Go validation did.

## Coordinator preflight evidence (manual, read-only, local)

**PG (newvillacarmen_business, read-only, PG 17, UTC):**

| Item | Value |
|---|---|
| Marker | ok=true, ready=false |
| Tables / rows | 253 / 23878 |
| FKs | 289 valid, 4 NOT VALID, 731 orphans |
| Pending | 1117 |
| bookings | 2355 rows, min 13:00:00, max 23:00:16 |
| cancelled_bookings | 228 |
| bytea NUL | exactly 1 |
| restaurant_domains | 5, all pending |
| pos_fiscal_documents | 0 |

- `ci_ai` is ICU `und-u-ks-level1`, nondeterministic. PG 17 exposes it as `colllocale`, not `colliculocale`.
- For the first 5 rows, `bookings.added_date` in PG (UTC) matches the naive MySQL value.

**MySQL source:** the local container's database is `villacarmen_source`, not newvillacarmen.

| Setting | Local container | Production read-only metadata |
|---|---|---|
| `@@sql_mode` | ONLY_FULL_GROUP_BY, STRICT_TRANS_TABLES, NO_ZERO_IN_DATE, NO_ZERO_DATE, ERROR_FOR_DIVISION_BY_ZERO, NO_ENGINE_SUBSTITUTION | the same list **without** ONLY_FULL_GROUP_BY |
| `time_zone` | SYSTEM | – |
| `collation_server` | utf8mb4_0900_ai_ci | – |

**Environment caveat:** the local source is not identical to production. Server defaults differ, and the DSN-level collation (unicode_ci) was not checked locally. Do not reset any settings, and do not treat the local proof as production-equivalent. For stored data, only the schema and column collations (`full-export/schema.json`) matter, not the server default. ONLY_FULL_GROUP_BY can make local MySQL reject a query that production accepts.

**NUL query fix:** `LOCATE(CHAR(0), content)` under a CI collation treats NUL as ignorable and gave 928 false matches. The binary form `LOCATE(BINARY UNHEX('00'), content) > 0` gives the real count of 1; the coordinator's result is pending.

## TIME: complete writer coverage

| Writer | Validation |
|---|---|
| Public front booking, Stripe checkout replay (`prepareFrontBooking`) | `ensureHHMMSS` (strict since types1) |
| Backoffice create/edit; bot special create, CRUD, reschedule overrides; WhatsApp special create (`boNormalizeAndValidateBookingInput`) | `ensureHHMMSS` (line 760) |
| Assistant `create_booking` | goes through `handleBOBookingCreate`, then the normaliser |
| Bot `create_booking` / `modify_booking` | `ensureHHMMSS` |
| Self-service modify | `ensureHHMMSS` |
| Legacy admin edit (`handleEditBooking`) | `ensureHHMMSS` |
| Reactivate (both) | the stored source value (clock); `ensureHHMMSS` normalises only |
| Public confirm/rice pages | display only, `ensureHHMMSS` normalise |
| **`modifications_api` field `reservation_time`** | **was stored raw. Now `ensureHHMMSS`; invalid returns the existing "Invalid value format".** |
| Fichaje entries/schedules, assistant horarios | `parseStrictHHMM` with `^(?:[01]\d|2[0-3]):[0-5]\d$` |
| POS service periods, POS `business_day_cutoff` | `posClockMinutes`, which applies `time.Parse("15:04")` to the first 5 characters. A longer bad suffix (e.g. "23:59:99") is stored raw and fails on **both** engines (strict, PG), so the behaviour is the same in both. Recorded; not changed. |
| `checkcancel` | read-only lookup, regex plus `time.Parse("15:04:05")` |

Every writer of a TIME column is now validated as a clock value. There are no duration columns and no plan revision.

## Collation plan (no code change: nothing beyond the existing ports was grounded enough to implement)

**Source column collations** (schema.json):

| Collation | Columns | Tables |
|---|---|---|
| utf8mb4_unicode_ci | 846 | 242 |
| utf8mb4_general_ci | 24 | bookings contact/name/status, admin_sessions, no_marketing, hours_percentage, mesas_de_dos |
| utf8mb4_0900_ai_ci | 18 | bot_inbound_queue, group_menu_wines_v2, invalid_phones, legacy_dish_map |
| utf8mb4_bin | 14 | JSON-like text: menus, arroz, metadata, context |

There are 121 text unique keys: 118 unicode_ci, 2 general_ci, 1 0900_ai_ci.

**PG plan r4:**
- 774 columns use `COLLATE dbmigrate.ci_ai` (ICU level1: case- and accent-insensitive, nondeterministic).
- The 14 bin columns use `"C"`.

**Known deltas** (not equivalent; this is the honest list):

| Aspect | MySQL | PG ci_ai | Impact |
|---|---|---|---|
| Trailing spaces | unicode_ci / general_ci are **PAD SPACE** ("a " = "a"); 0900_ai_ci is NO PAD | not padded | equality, unique keys and joins differ for trailing-space values |
| Expansions / contractions | unicode_ci UCA 4.0 (ß=ss); general_ci per-character (ß≠ss); 0900 UCA 9.0 | modern CLDR | rare characters compare differently |
| Ignorables | unicode_ci treats NUL and some controls as ignorable (see the 928 false NUL matches) | ICU level1 has its own ignorables | equality on control characters |
| LIKE | CI and accent-insensitive LIKE | PG 17 **rejects** LIKE on nondeterministic collations | already ported to `LOWER(col) COLLATE "C" LIKE` (case-insensitive only; the accent-insensitive gap is recorded per site) |
| ORDER BY | UCA weights | ICU weights | order may differ for accents and punctuation |
| GROUP BY / DISTINCT | collation-equal values grouped | ICU-equal values grouped | follows the same equality deltas |

**Existing data:** the r5 import built all unique indexes on ci_ai successfully, so the existing rows have no PG-side duplicate keys. MySQL-distinct pairs that might collide under ICU would have failed the import; none did. The residual risk is in new writes and in reads that compare.

**Per-column design proposal** (applied: false; it needs a read-only data check first):

1. **Opaque identifiers** (tokens, sha256, session ids, idempotency or delivery keys, coord ids, provider references, message ids): every writer produces exact machine strings. Propose the `"C"` (deterministic) collation in a forward migration, after confirming that no row has a case or trailing-space variant.
2. **Human text** (names, descriptions, titles): keep ci_ai. Document the PAD SPACE and expansion deltas, and do **not** emulate them in Go.
3. **Contact keys** (email, phone, suppression target, POS customers): the app already canonicalises (lower+trim, digits) before writing and comparing. Keep ci_ai for parity with the source unique semantics. Suppression lookups use the DB collation (suppression2).
4. **PAD SPACE parity on unique keys:** propose an application-side `TrimRight(" ")` at write time per key, only where the data check finds no trailing-space rows. That makes PAD SPACE behaviour moot. This is not done yet.
5. **Accent-insensitive LIKE:** an optional `unaccent`-based expression is a later decision. The extension availability must be checked read-only first.

## Manual read-only QA queries (corrected; run later by the coordinator)

**PG:**
- `SELECT id FROM assistant_messages WHERE position('\x00'::bytea IN content) > 0;` (bytea adapter; this replaces the earlier `convert_to` form) → 1 row.
- `SELECT id, added_date FROM bookings ORDER BY id LIMIT 5;` (corrected: `added_date`, not `created_at`).
- Trailing spaces per unique text key, for example: `SELECT count(*) FROM pos_customers WHERE email <> rtrim(email) OR phone <> rtrim(phone);`. Run the same for `bo_users.email`/`username`, `campaign_suppressions.target`, `message_deliveries.delivery_key`, `pos_product_categories.name`, and `menu_visibility.menu_key`.
- Case variants of opaque keys, for example: `SELECT count(*) FROM bo_sessions WHERE token_sha256 <> lower(token_sha256);`.
- `SELECT extname FROM pg_extension WHERE extname='unaccent';`.

**MySQL source:**
- `SELECT COUNT(*) FROM assistant_messages WHERE LOCATE(BINARY UNHEX('00'), content) > 0;` → 1.
- `SELECT @@session.collation_connection, @@sql_mode;` from a session started with the app's DSN parameters, to record the effective connection collation.
- `SELECT COUNT(*) FROM pos_customers WHERE email <> TRIM(TRAILING ' ' FROM email);`. This runs under PAD SPACE, so use `BINARY` on one side: `WHERE BINARY email <> BINARY TRIM(TRAILING ' ' FROM email)`.

## Gates (not waived)

- Collation deltas (above).
- Local source vs production environment mismatch.
- POS clock suffix (same behaviour on both engines).
- jsonb NUL on other writers.
- Notifications at-most-once.
- Capacity reviewed in code only.
- 10 guarded IGNORE sites.
- 1117 pending; 4 NOT VALID FKs with 731 orphans.
- General gates.

## Next

1. The coordinator runs the read-only checks above.
2. Then decide items 1 and 4 of the collation proposal per column, based on the data.
3. Then the first app-SQL read-only QA against the PG copy, with explicit permission.

## Files

`internal/api/modifications_api.go`. Backup: the prior ty1 backup covers the other files; this edit is a single guarded block.

