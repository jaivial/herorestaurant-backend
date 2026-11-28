# result-native-tool3: remaining 47 tool sites. Inventory coverage complete (not functional completion)

Code only. go build -p 2 ./... exits 0. Runtime-verified: 0. No tool, ops, export, import or sweep was run. PG is readyfalse, the server refuses PG, and MySQL is the default.

## Counts (inventory r74)
- **Repo:** 2475 = native/marked 2472 + blocked 3 + unwrapped 0. Dynamic unclassified: 0. Audit failures: 0.
- **Delta vs r73:** +47.
- **Marker kinds:** nativeSQL 841, portableSQL 1460, selectedSQL 81, pgOnlySQL 39, mysqlOnlySQL 46, provisional 5 (provisionalSQL 3 + pgProvisionalSQL 2), blocked 3.
- **Coverage is not functional completion:**
  - 46 mysqlOnlySQL sites belong to MySQL-bound tools and have no PG path.
  - The PG twins in ops-check and cdn-sweep are unwired.

## This checkpoint
| scope | sites | marking | basis |
|---|---|---|---|
| cmd/cdn-sweep | 8 | nativeSQL 7 + portableSQL 1 | Recurring utility, explicit twins behind a `dialect` var (stays MySQL). Advisory lock; InsertID; naive UTC NOW + `NaiveUTCArg(LastModified)`; PAD SPACE NULLIF; information_schema `lower(column_name) LIKE` (MySQL *_ci); quoted mixed-case registry identifiers |
| backfill-translations 9, comida-stock-backfill 8, group-menu-arroz-cleanup 5, update-restaurant-info 4, migrate-wine-images 3, chatgpt-plugin-token 1, cmd/dbmigrate 1, seed-legal-pages 1 | 32 | mysqlOnlySQL | Each opens the MySQL DB only: one-shot backfills, cleanups, seeding, token tool, and the MySQL source DATABASE() check |
| internal/db + internal/db/migrations | 5 | mysqlOnlySQL | MySQL bootstrap: admin CREATE DATABASE and the MySQL migration runner. The PG schema comes from the plan, not from these migrations |
| cmd/botstore-migrate | 2 | selectedSQL | SQLite context source reads |

Package-local marker files (`sqlmarkers.go`, `cmd/cdn-sweep/dialect.go`) are identity functions. No SQL text changed in the MySQL-only tools. The r5 proof snapshot is untouched.

## New and open gates (unwaived)
- **cdn-sweep PG wiring:**
  - Not wired yet: the PG pool open (UTC pin) and passing the PG schema name.
  - Safety: a wrong registry/schema match on PG would mark live images as orphans. Dry-run stays the default; QA is required before any --apply on PG.
- **Session advisory locks** (ops-check, cdn-sweep, comida-stock-backfill): the lock is taken and released on arbitrary pooled connections. This source bug class exists on both engines.
- **MySQL-only tools:** port or retire before any post-cutover run.
- **Carried over:**
  - ops-check PG twins unwired (tool2).
  - Botstore Rebind is not quote-aware; the no-literal-'?' rule needs a caller audit.
  - The legacy schemaless PG UTC gap.
  - All prior gates: 1117 import proof readyfalse; 4 NOT VALID FKs / 731 orphans; IGNORE truncation; routing; gap locks; NUL/JSON; search; TIME; UUID; SEO; multikey; collation/numeric; fiscal hash; 136 onupdate.
  - pos13 domain-register cross-tenant takeover (unaccepted).
  - Guest-label/catalog-scope technical repairs (dedicated QA).
  - PIN columns dump-only.

## Proposed next repair batch (technical, resolvable locally without user decisions; no SQL execution)
1. **UUID v1, 3 blocked sites:** assistant_pos_tools:68 ticket_number, instatic_manager:211/903 domain mappings; registrar provisional v4.
   - Check the plan for uuid-ossp: no reference found in plan.sql.
   - Decide by source usage. If only uniqueness and opacity are needed, ask whether ordering or time-extraction is ever relied on (grep consumers). If v1 semantics are required, generate v1 in Go with the vetted `github.com/google/uuid` v1.6.0 NewUUID (v1) — already a direct dependency in go.mod — and bind it explicitly on both engines (MySQL path unchanged).
   - Otherwise propose a plan revision adding `CREATE EXTENSION uuid-ossp` with uuid_generate_v1(). This needs a capability check on the target PG, done read-only by the coordinator.
2. **Multi-unique / tenant upserts, 5 provisional:**
   - stripe_connect (restaurant_id vs account_hash).
   - uazapi:637 (restaurant vs instance name).
   - SEO two-step (707/715 key priority and atomicity).
   - registrar first-site choice.
   - Implement the source arbiter order explicitly: lock or probe each unique key in MySQL index order inside one tx (SELECT .. FOR UPDATE on each key, then UPDATE the matched row or INSERT), so the PG outcome equals MySQL ODKU (first conflicting index wins). SEO: wrap the two steps in one tx with a savepoint.
3. **pos13 domain-register takeover:** guard ON CONFLICT DO UPDATE with `WHERE restaurant_domains.restaurant_id = EXCLUDED.restaurant_id`, and treat 0 rows as DOMAIN_UNAVAILABLE. This is safer than both engines' current race. It is a technical safety fix to apply after review, not silently.
4. **Rebind safety:** make sqldb.Rebind and botstore Rebind quote-aware with the existing lexical scanner (fail closed on comments, dollar-quotes and ?-jsonb operators). Then audit callers for literal '?'.
5. **Advisory locks:** pin a `*sql.Conn` for lock and release (both engines), or use `pg_try_advisory_xact_lock` in a tx.
6. **Cutover wiring:**
   - Single dialect source from `DB_DRIVER`.
   - ops-check and cdn-sweep PG pool and schema.
   - Analytics wiring.
   - Schemaless botstore UTC pin.
   - Keep the server PG refusal until runtime QA passes.
7. **Source-bug repairs, dedicated QA batch:** guest-label NULLIF backslashes, catalog EXISTS scope, ticket catch-up exotic casts, refund-sum fan-out (reported only).

**Recommended order:** 3, 2, 1, 4, 5, 6, then runtime QA of the full-PG business path on the local PG copy, then the verified functional bot, then tenant-isolated GraphRAG. Production stays read-only.

