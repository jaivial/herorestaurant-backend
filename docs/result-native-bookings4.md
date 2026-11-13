# Native PG port: bookings group 4 (bookings SQL syntax complete; compile only, NOT runtime-verified, NOT PG-ready)

## Counts (inventory r16: /tmp/villacarmen-bot-20261111/sql-inventory-r16.json)
- **Repo:** 2478 call sites, 567 native (+39), 1911 remaining, 291 dynamic. portable_audit_failures is 0.
- **Core:** 406/406. **Bookings:** 161/161 native across 24/24 files.
  - Ledger `docs/dbmigrate/native-ledger-bookings.json`: `syntax_complete:true, runtime_verified:false, pg_ready:false`.
- **Build:** `go build -p 2 ./...` exits 0 under the inherited cgroup (CPU 200%, 6G). No SQL was run, and no tests, vet, fixtures or Playwright.

## Converted (39 sites)
- **booking_extras (4)** and **booking_notification_settings (2):** upserts become `ON CONFLICT` on the real keys, `(restaurant_id, slug)` and `(restaurant_id)`.
- **floors_date (6):**
  - The restaurant_floors insert uses `InsertID`. The ignored LastInsertId error now surfaces. `scope_key` stays a generated column from the plan.
  - The floor_overrides upsert becomes `ON CONFLICT (restaurant_id, date, floor_id)`.
- **location_booking (5):** `"reservationDate"` quoted. The read → update/insert/delete flow is unchanged.
- **public_booking_api (5):**
  - The rice label FORMAT port.
  - Cancel: cancelled_bookings gets statement-time UTC, and the DELETE is portable.
  - **The customer rice update preserves MySQL's changed-rows contract**, because RowsAffected>0 records a modification. The PG UPDATE adds `(arroz_type COLLATE "C", arroz_servings COLLATE "C") IS DISTINCT FROM ($1,$2)`, reusing the positional params. The source columns are utf8mb4_bin, so the compare is byte-wise.
- **public_reservation_pages (6):** to_char, quoted columns, cancel statement time, rice label.
- **reservation_self_service (5, including dynamic SET list):**
  - The shared projection is now `selfServiceBookingHead()`, which uses sqlFragment to pick MySQL or PG with no early rebind.
  - Composed lookups and the UPDATE are rebound once.
  - camelCase SET items are quoted on PG.
- **special_booking_qr (5)** and **special_date_stats (1):** to_char and portable statements.
- **Guards unchanged:** the 35-day horizon, event stickiness, capacity, payment and tx guards. Only SQL text changed.

## Production read-only metadata (coordinator, 16:38, metadata only)
- **div_precision_increment = 4** (global and session) from production directly. **This closes the earlier "restored default only" gap** for the ROUND-scale emulation.
- **FULLTEXT settings:**
  - innodb_ft_min_token_size 3, max 84;
  - stopwords enabled;
  - no server or user custom stopword table, so the default list is used (count 36);
  - ft_boolean_syntax is the default;
  - ngram 2, but the bookings FT index uses the ordinary parser;
  - the columns are utf8mb4_general_ci.
  - Recorded in `fulltext-evidence-bookings.json`.
- **Still needed:** the actual default stopword **words**. A count of 36 does not define the list.
- **FULLTEXT equivalence is NOT solved by the settings.** The tokenizer, accent folding and stopword handling still need an implementation plus manual QA. **The current PG approximation is not accepted.**
- **Live confirmation of the source anomalies** (they are baseline, not import bugs; meaning not fixed):
  - no bookings.created_at, only added_date;
  - modification_history has no restaurant_id and no index;
  - menu_dishes_catalog has no `name`, only `title`;
  - DIA/FINDE do not exist in any case.

## Open gaps (unchanged, not waived)
- **Time semantics:**
  - schema DEFAULTs (353) and the ON UPDATE trigger (136 tables) still use transaction time and rounding; this needs a reviewed plan revision, and the proof schema is not mutated.
  - Invalid cancelled-time forms (MySQL TIME up to 838h, PG time under 24h) and RFC3339 date reinsertion.
- **Rendering and FT:**
  - JSON text render.
  - The FULLTEXT tokenizer and accents.
  - The CI/accent LIKE search gap.
  - PAD SPACE.
- **Integrity and storage:**
  - 190 unsigned range narrowings.
  - The recent_conversations view.
  - NUL storage QA.
  - 4 NOT VALID FKs covering 731 orphans.
  - 1117 pending, ready:false.
- **Other:**
  - Stripe CAST AS UNSIGNED.
  - The FOR KEY SHARE race.
  - Source anomalies.

MySQL stays the default and the server still refuses PG. No DB writes, activation, providers, payments, email, WhatsApp, sends, .env/proof edits, commits or deploys.

## Next domain: catalog_stock (506 sites, 43 files, 90 dynamic, 0 native)
Suggested bounded groups:
1. **stock core:** backoffice_stock (69, 5 dynamic), backoffice_stock_documents (34), backoffice_stock_recipes (29). About 132 sites; split stock first, about 69.
2. **group menus v2:** backoffice_group_menus_v2 (70, 4 dynamic), plus ws (10), items (8) and special_sections (19).
3. **comida/public menus:** comida (31, 12 dynamic), backoffice_comida_categories (22, 10), public_menus (18, 10), group_menus (14, 6), and the vinos files (about 18, all dynamic).
4. **Rest:** stock_analytics (26), suppliers (17), margins/import/reconciliation, menu visibility/seo/cta and small files.

Per-file counts are in the inventory r16 query used for this report.

