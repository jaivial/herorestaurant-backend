# Native PostgreSQL port: site_analytics checkpoint 3 (site_builder.go part A)

**Status:** this code compiles. No SQL was executed, and no publish, provider, registrar, upload or application path was run. The app is **not** PG-ready: `ready:false`, MySQL stays the default and the server refuses to start on PG.

## Counts (canonical inventory r40, dbmigrate-r12 basis)

| Scope | Value |
|---|---|
| Call sites (repo) | 2474 |
| Native (repo) | 1317 (+24) |
| Dynamic (repo) | 181 |
| Portable-audit failures | 0 |
| Excluded | 9 helper-internal calls, 3 prepared-statement executions |
| site_analytics | 98/203 native, 105 remaining (11 dynamic), 6/17 files done |
| Repo remaining | 1157 |

- Inventory: `/tmp/villacarmen-bot-20261111/sql-inventory-r40.json`
- Ledger: `docs/dbmigrate/native-ledger-site.json`, which now has a `cutover_gates` field
- Semantic notes: `/tmp/villacarmen-bot-20261111/site-semantics3.json`

## Build
`go build -p 2 ./...` exited 0 under the inherited cgroup (200% CPU, 6G memory), with pipefail. No tests, vet, fixtures or Playwright were run.

## site_builder.go part A (24 sites, 3 dynamic): sites, pages, assets, versions
- **JSON:** jsonb columns (`theme_config`, `settings`, `tree`, `seo_config`, `collection_binding`, `metadata`, the version snapshots) are read with `CAST(.. AS text)` and written with `CAST(? AS jsonb)`. This includes the dynamic UpdateSite and UpdatePage fragments (`sqlFragment`), and each full composed string is wrapped once.
- **Enums:** `status`, `page_type` and asset `type` are read as text. Writes of an invalid label error on both engines (strict-mode enum error vs 22P02; the classes differ).
- **is_home:** the Go `bool` is written with `CAST(CAST(? AS boolean) AS integer)`. The MySQL driver sends 1/0; pgx cannot encode a `bool` into an int2 column. Reads scan a smallint into `bool` on both.
- **Asset type filter:** in MySQL, ENUM = string compares under the column collation and an unknown value simply matches nothing. A PG enum cast would raise 22P02 instead, so the PG version uses `CAST(type AS text) COLLATE dbmigrate.ci_ai = ?`. This depends on the imported collation object.
- **Time:** every `time.Now()` argument goes into a TIMESTAMP column, which is `timestamptz` in PG. Both drivers preserve the instant, so `NaiveUTCArg` is not needed (checked with the AST pass). The `timestamp(0)` fractional-second tie gap is unchanged.
- **Unique violations:** detection uses `err.Error()` containing `uk_subdomain` / `uk_site_slug`. The PG constraint names `ix__site_builder_sites__uk_subdomain` / `ix__site_builder_pages__uk_site_slug` contain the same substrings, so the same branch is taken. That is text-coupled and kept as in the source.
- **Other checks:**
  - No transactions, no `RowsAffected` gates and no `LastInsertId` (ids are Go `uuid.New()`).
  - `MAX(version_number)+1` races on `uk_site_version` on both engines.
  - All ORDER BY columns are NOT NULL.

## Review of the prior registrar claim (REVISED; not equivalent)
The source statement is `INSERT .. SELECT UUID(), id, ?, 1, 'active','active' FROM site_builder_sites WHERE restaurant_id=? ON DUPLICATE KEY UPDATE status='active', ssl_status='active'`. The unique keys are PRIMARY `id` and `uk_domain`.

- **(a) A mapping already exists for the domain:** every selected row conflicts. Only `status`/`ssl_status` are set, and `updated_at` moves only if they actually changed. `site_id`, `is_primary` and `id` are untouched, and all generated UUIDs are discarded. **The PG LIMIT 1 version gives the same result.**
- **(b) No mapping exists yet:** MySQL inserts the **first row in its scan order**, with that row's `site_id` and a fresh UUID. Every later row conflicts on `uk_domain` and only re-sets the same status values.
  - The scan order depends on the access path. With `idx_restaurant_id` it would be `(restaurant_id, id)`, i.e. the smallest `id`, but that is not runtime-proven.
  - PG `LIMIT 1` without ORDER BY may choose a **different site_id**.
- **Counts:** one mapping row on both engines; affected counts are unused because errors are ignored.
- **Verdict:** this is **not equivalent**, and my previous "like MySQL's unordered select" claim is withdrawn.
- **Options, not applied (need a decision or runtime evidence):**
  - `ORDER BY id` with a MySQL-equivalent ordering;
  - insert first, then update.

## UUID v1 → v4 (not accepted)
- **Where SQL generates ids:** only two `INSERT .. SELECT UUID()` sites: the registrar (ported) and instatic_manager (not ported yet). Everything else in site_builder already uses Go `uuid.New()`, which is v4.
- **Readers of `site_builder_domain_mappings.id`:** lookups by id (get, delete, verify) and ListDomains `ORDER BY is_primary DESC, created_at ASC`. No ordering or time extraction by id was found, and instatic_proxy reads by domain.
- **What is lost:** MySQL v1 embeds time and node and sorts roughly chronologically. No current consumer was found that relies on it, but it is **recorded NOT-EQUIVALENT**.
- **Options:**
  - keep v4 with explicit owner acceptance;
  - generate the id in Go (this would change the MySQL path too, so it is not done silently);
  - `uuid-ossp` `uuid_generate_v1` (needs an extension that is absent from the import plan).
- No change was made, and no custom UUID algorithm was written.

## SEO two-step upsert (strict cutover gate)
- The PG emulation (ON CONFLICT DO NOTHING plus an UPDATE that prefers the route-key row) is **not atomic**. The route-before-slug key order is **assumed** from the schema index order.
- It is native syntax only, **not runtime-equivalent**.
- **Options:**
  - a transaction with `SELECT .. FOR UPDATE` on both keys (atomic, but it changes lock behaviour);
  - otherwise there is no single-statement multi-key PG form.
- It is listed under `cutover_gates` in the ledger.

## Other open items (unchanged, none waived)
- **Website anomalies:** the evidence is the restored dump only (schema sha256 `4cbbdea14083fd075802cd6676fb6a542500440c41974803ec50ccc416578830`), not live production.
- **Bounce invalid-UTF-8 suppression:** still **cutover-blocking**.
- **Import proof:** 1117 pending items with `ready:false`. That is the import-proof count, not all semantic gaps.
- **Still open:**
  - 4 NOT VALID FKs with 731 orphans;
  - CI/accent/PAD SPACE and FULLTEXT;
  - 190 ranges and 136 ON UPDATE / 353 defaults;
  - the view and NUL QA;
  - the numeric, JSON, ID and lock gaps;
  - crossaudit limits: interface and unported callers, `any` helpers, zero time, and the isolation default.

## Next: site_builder.go part B (23 sites, 2 dynamic)
Components, bindings, publish and status, domains (list, create, delete, verify) and the access helpers. Publish and the domain verify/registrar paths are code only.

## Not done
No commit, merge, deploy, DB writes, providers, payments, email, WhatsApp or sends, and no `.env`, source or proof-data edits.

