# Native PostgreSQL port: site_analytics checkpoint 5 (instatic_manager.go and instatic_proxy.go)

**Status:** this code compiles. No SQL was executed, and no instance process, network, Cloudflare, proxy or publish code was run. The app is not PG-ready: `ready:false`, MySQL stays the default and the server refuses to start on PG.

## Counts (canonical inventory r42, dbmigrate-r12 basis)

| Scope | Value |
|---|---|
| Call sites (repo) | 2474 |
| Inventory "native" (wrapped) | 1367 (+27 since r41) |
| Dynamic (repo) | 179 |
| Portable-audit failures | 0 |
| Excluded | 9 helper-internal calls, 3 prepared-statement executions |
| Repo remaining | 1107 |

**Exact split of the 27 sites in this checkpoint:**
- **25 ported:** PG variants exist (22 portable, 3 explicit `nativeSQL` pairs).
- **2 BLOCKED_ON_PG:** wrapped in `mysqlOnlySQL` with an explicit PG skip. The inventory counts them as wrapped, but **they are not validated PG ports**.

**site_analytics:** 148/203 wrapped = 146 ported + 2 blocked; 55 remaining (9 dynamic); 9/17 files done.

- Inventory: `/tmp/villacarmen-bot-20261111/sql-inventory-r42.json`
- Ledger: `docs/dbmigrate/native-ledger-site.json`, which now has `blocked_on_pg`, `native_ported_pg`, `native_blocked_on_pg` and `cutover_gates` fields
- Semantic notes: `/tmp/villacarmen-bot-20261111/site-semantics5.json`

## Build
`go build -p 2 ./...` exited 0 under the inherited cgroup (200% CPU, 6G memory), with pipefail. No tests, vet, fixtures or Playwright were run.

## Blocked sites (cutover gates; MySQL path unchanged)

### 1. `instatic_manager.go:211`, the instatic_instances upsert
- **Source behaviour:** the ODKU fires on **any** unique key, either `uniq_instatic_instances_restaurant` (restaurant_id) or `uniq_instatic_instances_port` (port). A port collision would update *another* restaurant's row.
- **Why it is blocked:** PG `ON CONFLICT` has a single target, so no source-faithful single statement exists.
- **What PG does instead:** the source ignores the error, so the PG branch writes nothing and logs `BLOCKED_ON_PG`. Downstream, `portFor` and the session-token reads will not find a row on PG.

### 2. `instatic_manager.go:903`, the domain mapping `INSERT .. SELECT UUID()`
- **Why it is blocked:** MySQL `UUID()` is v1. PG 17 core has only `gen_random_uuid()` (v4).
- **No extension available:** `grep` of `full-pg-plan-r4/plan.sql` finds **no `CREATE EXTENSION`** (no uuid-ossp, no pgcrypto), so `uuid_generate_v1` is unavailable without a new extension decision. No custom generator was written.
- **Not ambiguous otherwise:** `uk_subdomain` guarantees at most one selected site row, so the first-row question does not arise at this site.
- **What PG does instead:** the source ignores errors, so on PG nothing is written and the skip is logged. instatic_proxy's mapping lookup therefore has no rows from this path on PG.

## Ported sites
- **restaurant_domains upsert:** `ON CONFLICT (domain)`, the only key that can collide; the `cf_zone_id = ?` parameter is kept. It is cross-tenant on the global domain key, as in the source.
- **Time:** `last_health_at = NOW()` on a timestamptz column becomes `date_trunc('second', statement_timestamp())`.
- **comida_items:** `alergenos_json` (jsonb) is read as text, and `ORDER BY category_id`, which is nullable, is `NULLS FIRST`.
- **The other 20 manager sites and 3 proxy sites are portable:**
  - plain selects and updates, plus the menus JSON text columns read as text;
  - error handling unchanged;
  - no transactions, no `time.Time` arguments (AST pass) and no bool arguments.
- **Collation:** domain and slug lookups are under the CI/accent gap.

## Cutover gates (none waived)
- **SEO two-step upsert:** not atomic, and the key priority is assumed.
- **Registrar:** the LIMIT 1 first-site choice is unproven, and its UUID v1 → v4 is unaccepted.
- **New in this checkpoint:**
  - the instatic_instances multi-key upsert, **BLOCKED_ON_PG**;
  - the instatic domain mapping `UUID()` insert, **BLOCKED_ON_PG**.
- **Bounce invalid-UTF-8 suppression:** still cutover-blocking.

## Other open items (none waived)
- **Website anomalies:** the evidence is the restored-dump schema only (sha256 `4cbbdea14083fd075802cd6676fb6a542500440c41974803ec50ccc416578830`), not live production.
- **Import proof:** 1117 pending items with `ready:false`. That count does not cover all the semantic gaps.
- **Still open:**
  - 4 NOT VALID FKs with 731 orphans;
  - CI/accent/PAD SPACE and FULLTEXT;
  - 190 ranges and 136 ON UPDATE / 353 defaults;
  - the view and NUL QA;
  - the numeric, JSON, ID and lock QA;
  - crossaudit limits.

## Next: analytics, ads and AI usage (55 sites, 9 dynamic)

| File | Sites | Dynamic |
|---|---|---|
| internal/analytics/service.go | 27 | |
| internal/analytics/breakdowns.go | 6 | |
| backoffice_ads.go | 9 | 2 |
| backoffice_ai_usage.go | 7 | 4 |
| ai_usage_logging.go | 2 | 1 |
| cafes_page_seo.go | 2 | 2 |
| public_ads.go | 1 | |
| public_ads_embed.go | 1 | |

Suggested split:
- **Checkpoint A:** analytics/service.go and breakdowns.go (33 sites). Expect date bucketing, timezones and GROUP BY.
- **Checkpoint B:** the remaining 22.

## Not done
No commit, merge, deploy, DB writes, providers, payments, email, WhatsApp or sends, and no `.env`, source or proof-data edits.

