# Native PostgreSQL port: site_analytics checkpoint 2

**Status:** this code compiles. No SQL was executed, and no generator, publish, registrar or Cloudflare/domain provider, legal publish or upload path was run. The app is **not** PG-ready: `ready:false`, MySQL stays the default and the server refuses to start on PG.

## Counts (canonical inventory r39, dbmigrate-r12 basis)

| Scope | Value |
|---|---|
| Call sites (repo) | **2474** (was 2472; see the note below) |
| Native (repo) | 1293 (+26) |
| Dynamic (repo) | 184 |
| Portable-audit failures | 0 |
| Excluded | 9 helper-internal calls, 3 prepared-statement executions (unchanged) |
| site_analytics | 74/203 native, 129 remaining (14 dynamic), 6/17 files done |
| Repo remaining | 1181 (= 2474 − 1293) |

- Inventory: `/tmp/villacarmen-bot-20261111/sql-inventory-r39.json`
- Ledger: `docs/dbmigrate/native-ledger-site.json`
- Semantic notes: `/tmp/villacarmen-bot-20261111/site-semantics2.json`

**Why the canonical total rose by 2:** the seo_item_routes insert upsert is now an explicit dialect branch with 3 call sites: one MySQL-only statement and two PG-only statements. All three are native. The 24 sites ported here plus these 2 account for the +26 native.

## Build
`go build -p 2 ./...` exited 0 under the inherited cgroup (200% CPU, 6G memory), with pipefail. No tests, vet, fixtures or Playwright were run.

## Source-anomaly provenance (correction to checkpoint 1)
- **The evidence is the immutable restored-dump schema**, not live production:
  - `/home/jaime/orq/villacarmen-bot-20261111/full-export/schema.json`, sha256 `4cbbdea14083fd075802cd6676fb6a542500440c41974803ec50ccc416578830`. Its `database` is `villacarmen_source` and its `server_version` is `8.0.45`, which is the restored container. The separately captured live-source settings file reports `8.0.46-0ubuntu0.22.04.4`.
  - The imported r4 plan, `/tmp/villacarmen-bot-20261111/full-pg-plan-r4/plan.sql`, sha256 `8b9514f226e3ed84abad8d4ae1fcc074ca96eead5e9ecb3e0bafcf8839ff644b`.
- **Not rechecked against current live production:** `production-readonly-metadata-20261111.tsv` contains no `website_*` rows. Checkpoint 1's wording "restored/live schema lacks them" overstated this. Only the dump is confirmed.
- **Preserved failures:** the missing `website_pages` meta, homepage, status and position columns, and the missing `website_assets` and `website_publish_history` tables, remain coded failures on both engines. Changing that would be a new semantics decision, not a fix.

## Files completed (24 sites)

### website_builder_generator.go (4)
- Uses the same anomaly tables and columns, so it fails on both engines.
- The PG `ORDER BY` has explicit `NULLS LAST`.
- JSON `COALESCE(.., JSON_OBJECT())` becomes `CAST(COALESCE(.., '{}'::jsonb) AS text)`.
- The assets read targets the absent table and fails on both.

### website_builder_public.go (4)
- **Menus:** `COALESCE(price, '')` would error on a numeric in PG, so it becomes `CAST(price AS text)`; a `numeric(10,2)` keeps its scale like MySQL's decimal text. `modified_at DESC NULLS LAST`.
- **Opening hours:** the date is scanned into a string as RFC3339 `time.Time` text on both drivers (code reading only). Its parameter is a Go-formatted local date string, which I left unchanged.

### legal_pages.go (3)
- The upsert becomes `ON CONFLICT (restaurant_id, slug)`, the only unique key besides the generated id.
- The naive `updated_at` ON UPDATE is handled by the generic trigger (part of the pending ON UPDATE gap).

### site_builder_registrar.go (3, all errors ignored outside any transaction)
- **restaurant_domains:** `ON CONFLICT (domain)`. The domain key is global, so the upsert is cross-tenant, as in the source.
- **site mappings:** `UUID()` (MySQL v1) becomes `gen_random_uuid()` (v4). The text form is the same 36 characters, but the version and ordering differ.
- **Several sites per restaurant:** MySQL inserts the first row and then ODKU-updates the same-domain row for each remaining one. PG cannot touch a row twice in one ON CONFLICT statement, so the PG version uses `LIMIT 1`, an arbitrary row, like MySQL's unordered SELECT.

### seo_item_routes.go (10, 4 dynamic; full composed strings wrapped once)
- **Insert upsert:** MySQL ODKU fires on **any** unique key, either `uniq_seo_item_route` or `uniq_seo_item_slug`. The PG branch:
  1. inserts with a target-less `ON CONFLICT DO NOTHING`, which covers any unique key;
  2. if no row was inserted, sets `active = 1` on the conflicting row, preferring the route-key row.
  - **Assumption:** that MySQL checks the route key before the slug key is taken from the schema's index order, not proven at runtime. The two steps are not atomic against a concurrent delete.
- **Dates:** `COALESCE(datetime, '')` becomes `to_char(..., 'YYYY-MM-DD HH24:MI:SS')`; the dish `updated_at` is a TIMESTAMP, so it is rendered `AT TIME ZONE 'UTC'`.
- **Recent-inactive flag:** `CASE 1/0`; NULL `inactive_since` gives 0.
- **Time:** `NOW()` and `inactive_since = NOW()` use the UTC naive statement second.
- **Other:** `CONCAT(entity_type,'/',entity_id) IN (...)` is portable over NOT NULL columns. Allergen JSON is read as text.
- **Not changed:** an empty `usedKeys` produces `IN ()`, a syntax error on both engines, as in the source.

### Cross-cutting checks (crossaudit rules)
- The AST pass over these 5 files found no transaction-bound calls and no `time.Time` arguments.
- A lexical pass over the PG halves found no MySQL functions, no `?` inside literals and matching arity.
- The fallback order and PG undefined-table/column recognition from checkpoint 1 are unchanged.

## Open (not accepted)
- **SEO upsert:** the two-step PG emulation of "any unique key" ODKU, and the assumed index check order.
- **UUID:** the v1 vs v4 difference.
- **Registrar:** the multi-site LIMIT 1 is an arbitrary pick.
- **Collation:** slug, domain and entity uniqueness are under the CI/accent gap.
- **JSON:** text rendering and key-order QA.
- **Source anomalies:** they need a separate decision, and live-production confirmation is pending.
- **Unchanged from earlier checkpoints:**
  - crossaudit limits: interface and unported callers, `any` helpers, zero time, and the isolation default (RR vs RC);
  - the bounce invalid-UTF-8 suppression gap is **cutover-blocking**;
  - 1117 pending items with `ready:false` (the 1117 is the import-proof count and does **not** include all semantic gaps);
  - 4 NOT VALID FKs with 731 orphans, CI/accent/PAD SPACE, FULLTEXT, 190 ranges, 136 ON UPDATE / 353 defaults, the view, NUL QA, the numeric, JSON, ID and lock gaps, and source anomalies.

## Next: site_builder.go (47 sites), split into two coherent checkpoints
- **A (24 sites):** sites, pages, assets and versions, lines 205–1062: handleListSites through handleGetVersion. 3 are dynamic: UpdateSite, UpdatePage and ListAssets.
- **B (23 sites):** components, bindings, publish, domains and access helpers, lines 1117–1737. 2 are dynamic: ListComponents and UpdateBinding.
- After that: instatic_manager 24 and instatic_proxy 3, then analytics/ads/AI usage, 55 sites.

## Not done
No commit, merge, deploy, DB writes, providers, payments, email, WhatsApp or sends, and no `.env`, source or proof-data edits.

