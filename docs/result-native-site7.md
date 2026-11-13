# Native PostgreSQL port: site_analytics checkpoint 7 (domain syntax-complete apart from 2 blocked sites)

**Status:** all 203 site_analytics sites are now syntax-ported (201) or explicitly blocked on PG (2). That is **not** runtime equivalence. No ad, campaign, AI, tracking, ingestion, API, provider or app path was run. The analytics dialect wiring was **not** activated. The app is not PG-ready: `ready:false`, MySQL stays the default and the server refuses to start on PG.

## Counts (canonical inventory r44, built with dbmigrate-r14)

| Class | Sites |
|---|---|
| Native (syntax-ported, includes 3 provisional) | **1420** (+22) |
| Blocked on PG (not native) | **2** |
| Unwrapped / not ported | **1052** |
| **Total** | **2474** (= 1420 + 2 + 1052) |
| Runtime-verified | **0** |
| Dynamic | 169 |
| Portable-audit failures | 0 |
| Excluded | 9 helper-internal calls, 3 prepared-statement executions |

- **site_analytics:** 203 sites = **201 syntax-ported + 2 blocked** (`instatic_manager.go:211`, `:903`); 17/17 files.
- Inventory: `/tmp/villacarmen-bot-20261111/sql-inventory-r44.json`
- Ledger: `docs/dbmigrate/native-ledger-site.json`. It now says `syntax_complete_excluding_blocked`; the cutover gates also include the analytics wiring.
- Semantic notes: `/tmp/villacarmen-bot-20261111/site-semantics7.json`

### Classifier fix (dbmigrate-r14)
- **Binary:** sha256 `a72f6a6b753a5afdbfbbcae8878d1e495aac8c724fd69c25f982d531464f75b0`. r43, r42 and earlier inventories are preserved.
- **Fix:** `InsertID` on a receiver other than `sqldb` (`botstore.DB.InsertID(ctx, q, ...)`) now reads the query at argument 1; r13 read argument 3.
- **Exact effect** (r13 and r14 run on the same tree):
  - `ai_usage_logging.go:109` goes from unwrapped to selectedSQL;
  - `whatsapp_bot_context_sqlite.go:245` goes from dynamic to static and is still unported.
  - The totals are unchanged.

## Build
`go build -p 2 ./...` exited 0 under the inherited cgroup (200% CPU, 6G memory), with pipefail. No tests, vet, fixtures or Playwright were run.

## Sites ported (22, 9 dynamic)

### backoffice_ads.go (9)
- **JSON:** the five jsonb columns are read as text and written with `CAST(? AS jsonb)`. Both dynamic UPDATE variants are dialect-selected once and executed with `selectedSQL`.
- **Other columns:** the status enum is read as text, the insert id uses `sqldb.InsertID`, and `NOW()` on timestamptz is the statement second. Nullable `updated_at DESC` gets `NULLS LAST`.
- **Dates:** start and end dates are Go-formatted strings, unchanged.
- **Transactions:** the image-URL transaction uses FOR UPDATE and checks every error.
- **Delta (diagnostic only):** when the status update affects 0 rows it only logs. Under MySQL's changed-rows count, a same-status update logs a warning; under PG's matched-rows count it does not.

### backoffice_ai_usage.go (7)
- **ai_usage_logs (business DB):**
  - The bucket comes from `sqlFragment` (MySQL `DATE_FORMAT` vs the existing PG `to_char`/`date_trunc('week')`), with one wrap of the full string.
  - `from`/`to` are UTC-midnight values against a naive `timestamp(3)`, so both drivers bind the same UTC wall clock.
- **whatsapp_message_logs (bot store `st`, its own dialect and Rebind):**
  - Its 3 queries were already dialect-selected by the bot-store port, so they now carry the `selectedSQL` marker.
  - PG `ILIKE` on the bot store's deterministic TEXT is case-insensitive only, while MySQL `LIKE` is case- and accent-insensitive (accent gap).

### ai_usage_logging.go (2)
- **AI usage insert:** `created_at` (naive `timestamp(3)`) is bound with `NaiveUTCArg`, because the MySQL driver stores the UTC wall clock and pgx would store the local one. The insert id uses `sqldb.InsertID`. The `cost` float64 → `numeric(14,8)` conversion needs rounding QA.
- **WhatsApp log insert:** it goes to the bot store, which is already PG-aware, so it is marked `selectedSQL`.

### cafes_page_seo.go (2)
The dynamic ODKU becomes `ON CONFLICT (restaurant_id) DO UPDATE SET col = EXCLUDED.col`. `uniq_rest_page_vis_restaurant` is the single key that can collide, and the full string is wrapped once.

### public_ads.go and public_ads_embed.go (1 + 1)
`updated_at DESC NULLS LAST`.

## End-of-domain static audit (site_analytics, 17 files)
- **AST pass over this checkpoint's files:** no ignored transaction errors. Every `time.Time` argument is UTC-derived or wrapped with `NaiveUTCArg`.
- **Bool and smallint:** the bool-into-smallint arguments in this domain are converted explicitly (site_builder is_home and is_primary). The ads use `boolToTinyint`, which passes 0/1.
- **Classifier:** it separates blocked (2) from provisional (3) from native. **No equivalence is claimed**, and runtime-verified is 0.

## Cutover gates (none waived)
- **SEO two-step upsert:** atomicity and key priority.
- **Registrar:** the site choice and UUID v1 → v4.
- **instatic:** 2 sites blocked.
- **Bounce invalid-UTF-8 suppression.**
- **Analytics dialect wiring:** the cutover must call `analytics.SetSQLDialect` together with the api dialect. It was not activated and no runtime config was added.

## Other open items (none waived)
- **Website anomalies:** the evidence is the restored dump only, not live production.
- **Import proof:** 1117 pending items with `ready:false`, separate from the semantic gaps.
- **Still open:**
  - 4 NOT VALID FKs with 731 orphans;
  - CI/accent/PAD SPACE and FULLTEXT;
  - 190 ranges and 136 ON UPDATE / 353 defaults;
  - the view and NUL QA;
  - the numeric, JSON, ID and locking QA;
  - crossaudit limits.

## Remaining domains (r44)

| Domain | Files | Unported | Dynamic |
|---|---|---|---|
| pos_billing | 35 | 564 | 53 |
| whatsapp_bot | 53 | 316 | 52 |
| tools_cmd | 12 | 57 | 9 |
| assistant | 8 | 55 | 22 |
| migration_tooling_native | 7 | 55 | 31 |
| db_runner | 2 | 5 | 2 |
| **Total** | | **1052** | |

## Next: assistant (55 sites, 8 files), split into two checkpoints
- **A (31 sites, all 22 dynamic):**
  - assistant_catalog_tools.go: 15 (14 dynamic)
  - assistant_tools.go: 12 (5 dynamic)
  - assistant_analytics_tools.go: 4 (3 dynamic)
- **B (24 sites):**
  - assistant_ws.go: 10
  - assistant_pos_tools.go: 5
  - assistant_confirmation.go: 4
  - assistant_memory.go: 4
  - assistant_llm.go: 1
- The assistant tools mutate business data through confirmation paths. Code only; no LLM, provider or tool execution.

## Not done
No commit, merge, deploy, DB writes, providers, payments, email, WhatsApp or sends, and no `.env`, source or proof-data edits.

