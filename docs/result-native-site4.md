# Native PostgreSQL port: site_analytics checkpoint 4 (site_builder.go part B; file complete)

**Status:** all SQL in this file now has an explicit PG variant, but that is **not** runtime equivalence. No SQL was executed, and no publish, domain-verify, network or provider path was run. The app is not PG-ready: `ready:false`, MySQL stays the default and the server refuses to start on PG.

## Counts (canonical inventory r41, dbmigrate-r12 basis)

| Scope | Value |
|---|---|
| Call sites (repo) | 2474 |
| Native (repo) | 1340 (+23) |
| Dynamic (repo) | 179 |
| Portable-audit failures | 0 |
| Excluded | 9 helper-internal calls, 3 prepared-statement executions |
| site_builder.go | 47/47 |
| site_analytics | 121/203 native, 82 remaining (9 dynamic), 7/17 files done |
| Repo remaining | 1134 |

- Inventory: `/tmp/villacarmen-bot-20261111/sql-inventory-r41.json`
- Ledger: `docs/dbmigrate/native-ledger-site.json`
- Semantic notes: `/tmp/villacarmen-bot-20261111/site-semantics4.json`

## Build
`go build -p 2 ./...` exited 0 under the inherited cgroup (200% CPU, 6G memory), with pipefail. No tests, vet, fixtures or Playwright were run.

## Part B (23 sites, 2 dynamic): lines 1172–1792 of the current file
- **Components:** the list (dynamic category filter, one wrap of the full string) and get-by-type read the jsonb schemas as text. `category` and `type` are varchar, so no enum cast issue arises.
- **Bindings:** list, create, update (dynamic `query_config` jsonb fragment, one wrap of the full string) and delete.
  - `refresh_mode` is read as text.
  - Its **assignment is left plain**, so an invalid label fails on both engines (the error classes differ). Assignments are not blanket-cast.
- **Publish:** snapshot reads with jsonb and enum columns as text, the version INSERT (jsonb), and the site status UPDATE.
  - There is no transaction. The source ignores the Scan and Query errors of the assets snapshot outside any transaction, and that is kept.
  - The `MAX(version)+1` race on `uk_site_version` exists on both engines.
- **Publish status:** the queue's enum columns are read as text; `status IN ('pending','processing')` uses enum literals.
- **Domains:** list, create, delete and verify. Enum columns are read as text.
  - **Bool audit:** `is_primary` is a Go `bool`, written with `CAST(CAST(? AS boolean) AS integer)`. This is the only bool going into a smallint in part B.
  - **Duplicate domain:** detected by the err text containing `uk_domain`. The PG name `ix__site_builder_domain_mappings__uk_domain` contains it.
- **Access helpers:** `getSiteByID` (jsonb and enum as text) and `canAccessSiteBySiteID`.
- **UUIDs:** this file has **no SQL UUID generation**; every id comes from Go `uuid.New()`, which is identical on both engines. No new UUID gate was needed.
- **Time:** every `time.Time` argument goes into a `timestamptz` column, so the instant is preserved (AST pass). `NaiveUTCArg` is not needed.
- **Transactions and changed rows:** there are no transactions, so no savepoints apply, and there are no `RowsAffected` gates.
- **Unique keys:** no upserts exist in this file, so the multi-key ODKU question does not arise here.

## Cutover gates (unchanged, explicitly not equivalent)
- **SEO two-step upsert:** not atomic, and the key priority is assumed.
- **Registrar mapping insert:** MySQL's first-scanned row is unproven, while PG `LIMIT 1` has no ORDER BY.
- **UUID v1 → v4:** not accepted. **The next file, instatic_manager.go:883, also uses `INSERT .. SELECT UUID()`.** It will be detected and kept as a gate. I will not port it as a silent v4 equivalent, and I will use an accurately labelled provisional or blocked variant rather than a guessed one.
- **Bounce invalid-UTF-8 suppression:** still cutover-blocking.

## Other open items (none waived)
- **Website anomalies:** the evidence is the restored dump only (schema sha256 `4cbbdea14083fd075802cd6676fb6a542500440c41974803ec50ccc416578830`), not live production.
- **Import proof:** 1117 pending items with `ready:false`. That count does not cover all the semantic gaps.
- **Still open:**
  - 4 NOT VALID FKs with 731 orphans;
  - CI/accent/PAD SPACE and FULLTEXT;
  - 190 ranges and 136 ON UPDATE / 353 defaults;
  - the view and NUL QA;
  - the numeric, JSON, ID and lock gaps;
  - the timestamp(0) fractional-second tie;
  - enum error-class differences;
  - crossaudit limits: interface and unported callers, `any` helpers, zero time, and the isolation default.

## Next checkpoint (27 sites, 0 dynamic)
- **instatic_manager.go:** 24 sites, including the `UUID()` mapping insert (a gate) and the domain-mapping upsert.
- **instatic_proxy.go:** 3 sites, including the mapping lookup by domain.
- These are instance and proxy management paths; they will be code only, with no provider or network execution.

## Not done
No commit, merge, deploy, DB writes, providers, payments, email, WhatsApp or sends, and no `.env`, source or proof-data edits.

