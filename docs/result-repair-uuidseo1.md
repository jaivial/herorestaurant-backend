# result-repair-uuidseo1: UUID v1 (2 blocked + registrar), SEO two-step, instatic port keeper

Code only. go build -p 2 ./... exits 0. Runtime-verified: 0. Nothing ran; no UUIDs were generated now. PG readyfalse, the server refuses PG, MySQL default. The r5 proof is untouched.

## UUID v1
**Consumer audit:**
- **ticket_number `F-<UUID()>` (assistant pos_ticket_create):**
  - Uniqueness via uq (restaurant_id, ticket_number).
  - No ordering or time extraction: the ticket catch-up only parses `<prefix>-YYYYMMDD-NNNN` via LIKE, so `F-…` never matches.
  - The invoice "F-" prefix is unrelated.
- **site_builder_domain_mappings.id:** PK, used opaquely (delete and verify by id; listing ordered by created_at).
- **Findings:** no consumer reads the v1 timestamp, MAC or ordering.

**Implementation:**
- New `internal/api/uuid_v1.go` `sqlUUIDv1()` (`sql_uuid_v1_pg_v1`) uses google/uuid v1.6.0 (already in go.mod) `NewUUID()` to produce RFC 4122 v1 as 36-char lowercase text.
- The PG twins bind it per actual runtime call. MySQL branches keep `UUID()` in SQL, unchanged.
- **Generation failure:** returns an error (ticket create, instatic provisioning) or logs and stops (registrar provision). Never an empty id.
- **Not byte-identical:** the node is a host MAC or random, not the MySQL server's.
- **Sites:**
  - **assistant_pos_tools pos_ticket_create:** blocked → nativeSQL. PG binds `'F-'+v1`. The id comes from InsertID/RETURNING; MySQL Exec+LastInsertId is unchanged.
  - **instatic_manager mapping insert:** blocked → nativeSQL. PG binds v1, with the owner-guarded `ON CONFLICT (domain) … WHERE site_id = EXCLUDED.site_id`. subdomain is unique, so there is at most one site row and one id.
  - **site_builder_registrar provision mapping:** gen_random_uuid (v4) → bound v1. It **stays provisional**: site selection is arbitrary on both engines (MySQL takes the first scanned site and the rest hit the guarded no-op; PG uses LIMIT 1 without ORDER BY). Unresolved; no policy guess.

## SEO insertSeoItemRoute
**Source:**
- One MySQL ODKU `active = 1`.
- InnoDB checks unique keys in index order: PRIMARY, `uniq_seo_item_route` (restaurant_id, entity_type, entity_id), then `uniq_seo_item_slug` (restaurant_id, entity_type, slug) (migration 201, schema.json order).
- The first conflicting row is updated. MySQL is unchanged.

**PG (`seo_route_upsert_tx_v1`), one tx:**
1. In a savepoint: `INSERT … ON CONFLICT (route key) DO UPDATE SET active = 1`.
   - A route conflict is resolved atomically on the locked route row. slug isn't changed, so the slug key isn't re-checked. This matches MySQL's first-key choice when both keys conflict.
2. A slug-key 23505 with no route row → rollback to savepoint → `UPDATE … SET active = 1 WHERE restaurant_id AND entity_type AND slug`.
   - 0 rows (the slug row was deleted concurrently) → retry once from step 1, where MySQL would have inserted.
   - Still racing → fail-closed error.
   - Other errors propagate.

**Concurrency:**
- Same route key: serialised by the unique index and row lock.
- Same slug, different route: the loser takes the slug path. This doesn't rely on a missing-row lock.
- Both keys include restaurant_id, so no other tenant's row is reachable.
- 2 pgProvisionalSQL → pgOnlySQL. Reviewed; QA pending.

## instatic keeper (follow-up on isolation2)
**Finding:** routing to an instance uses the in-process `portByRestaurant` map, built once at load. It has no cross-process coordination.
- If another process (or a restart race) already recorded port P for restaurant B, this process could start A on P.
- The DB write then failed or did nothing, and the error was ignored, so B's host traffic proxied by any process using P would be served by A's site.
- Data and paths are per-restaurant directories, not shared.

**Fix, both dialects, fail-closed:**
- **Pre-start:** `SELECT restaurant_id FROM instatic_instances WHERE port = ?`. A foreign owner forgets the port and returns an error. A DB error also returns an error.
- **Post-record re-check:** for the race between check and record, a foreign owner kills the just-started process, removes it from `proc`, and forgets the port.

## Counts (inventory r77)
| class | count |
|---|---|
| call sites | 2479 (2475 canonical + 4 added owner checks across isolation1/2 and this batch) |
| syntax-classified | 2479: nativeSQL 846, portableSQL 1463, selectedSQL 82, pgOnlySQL 41, mysqlOnlySQL 46 (port/retire) |
| provisional | 1: site_builder_registrar.go:203 (mapping site selection) |
| blocked | 0 |
| unwired PG twins | ops-check, cdn-sweep |
| runtime-verified / semantically QA'd | 0 / 0 |

Every r76→r77 reclassification is a code-reviewed source fix with QA pending; none is a validated equivalence.

## Gates (unwaived)
- **Import and data:** 1117 import proof readyfalse; 4 NOT VALID FKs / 731 orphans.
- **SQL semantics:** IGNORE truncation, gap locks, NUL/JSON, search, TIME, collation/numeric, fiscal hash, 136 onupdate.
- **Inventory gates still open:** UUID node non-identity, SEO, multikey.
- **New:** the registrar mapping site-selection gate.
- **External resources:** provider, Stripe and UAZAPI resources created before a shared-key conflict stay orphaned (logged, no external cleanup).
- **Stripe Connect demo:** the shared demo hash fails closed.
- **Domain follow-ups:** reserve-before-purchase TOCTOU, checkout ownership pre-check, mapping ownership/verification/routing anomaly.
- **Tooling:** unwired tools; botstore Rebind quote awareness.
- **Pending repairs:** guest-label/catalog-scope repairs pending dedicated QA; PIN columns dump-only.

## Next
1. **Domain reserve-before-purchase** (registrar): owner-guarded `pending` row + owner re-read before RegisterDomain; mark only the own row failed on provider error.
2. **Stripe domain checkout:** owner pre-check before CreateCheckoutSession; keep the webhook guard.
3. **Mapping invariant:**
   - Create only for an unowned or same-owner domain.
   - DNS TXT verification before `verified`.
   - Route only verified mappings via site_builder_sites.restaurant_id. The current router queries a non-existent column; this is a source anomaly.
4. **Shared helpers:** factor the owner-guarded upsert plus re-read pattern (domains, shared identities, ports) into one reviewed helper, then wire ops-check and cdn-sweep for PG.

