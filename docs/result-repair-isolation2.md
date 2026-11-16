# result-repair-isolation2: shared-identity tenant isolation (Stripe Connect account hash, UAZAPI instance name, instatic port)

Code only. go build -p 2 ./... exits 0. Runtime-verified 0. PG readyfalse, server refuses PG, MySQL default.

Nothing was run: no SQL, no Stripe, no provider calls. No secrets were read or printed. The fixes are reviewed code only; their gates stay open until QA. The r5 proof is untouched.

## Writers audited (all writers of each shared identity)
| table / shared unique key | writers | notes |
|---|---|---|
| restaurant_stripe_connect: PK restaurant_id, UNIQUE account_hash | saveConnectAccount (callers: refresh, demo enable, live create); DELETE by restaurant_id | only one upsert writer |
| restaurant_uazapi_instances: UNIQUE restaurant_id, UNIQUE instance_name | provision upsert (:637); adopt INSERT (name `legacy-<rid>`, plain INSERT → error on collision, unchanged); UPDATE/DELETE all `WHERE restaurant_id=?` | only the provision upsert could touch a foreign row |
| instatic_instances: UNIQUE restaurant_id, UNIQUE port | start() upsert; UPDATEs by restaurant_id | port comes from an in-process map (collision only across processes or restarts) |

## Bug and fix (both dialects; intentionally not the MySQL source behaviour)
The source MySQL ODKU fired on any unique key, so a collision on the shared key rewrote another restaurant's row:
- **Stripe Connect:** an encrypted account bound to the wrong AAD, so the victim could no longer decrypt it.
- **UAZAPI:** another tenant's server and instance token.
- **instatic:** another tenant's port and paths.

The fix (`shared_identity_isolation_v1`) has four parts:
- **MySQL:** every ODKU assignment is `col = IF(restaurant_id = VALUES(restaurant_id), VALUES(col), col)`. restaurant_id is never assigned, so the condition is stable across the assignment list.
- **PG:** `ON CONFLICT (restaurant_id)` only. A shared-key conflict raises 23505.
- **Owner re-read (Stripe, UAZAPI):** after a successful statement, `SELECT restaurant_id … WHERE <shared key>=?`. A foreign owner returns the new error (MySQL guarded no-op case).
- **Unique errors:** `sqldb.IsUniqueViolationOn` on the shared key, by the MySQL and plan index names, maps to the same error. This covers two cases: PG, and MySQL when the own row would be re-pointed to a foreign key value.

Errors and responses:
- **Stripe Connect:** `errConnectAccountTaken`. Callers keep their existing responses ("No se pudo guardar la cuenta de cobros" / "No se pudo activar el modo demo" / refresh error).
- **UAZAPI:** `errUAZAPIInstanceTaken`. It is returned before the server-count, integration-sync and webhook side effects, which keep their existing error path.
- **instatic:** a foreign port means the row is not written. The error stays ignored as in the source, and is now logged.

Inventory reclassification (code-reviewed fixes, QA pending):
- stripe_connect and uazapi move from provisional to nativeSQL.
- instatic:211 moves from blocked to nativeSQL. The PG statement now exists.

## Atomicity and race argument
- **No tx needed:** each fix is a single statement plus a read-only owner re-read. A foreign row is never modified, and the own row only changes when the own key conflicts, so a failure leaves no partial write in the DB.
- **Concurrent first insert of the same shared key** (A, B new): the unique index makes B wait for A.
  - If A commits: MySQL takes the guarded no-op (the row is still X-locked) and B's autocommit re-read sees owner A; PG gets 23505. Either way B gets the error.
  - If A rolls back: B inserts.
- **Not relied on:** no FOR UPDATE on missing rows.
- **MySQL RR vs locking read:** the re-read is a fresh autocommit statement, not inside a stale RR snapshot, so it reads the latest committed row.
- **External effects:**
  - UAZAPI: the provider instance is created before the DB write. A foreign-name error leaves that provider instance orphaned. It is logged, not deleted (no provider call).
  - Stripe Connect: the Stripe account is created before the save, so a taken-hash error leaves it unlinked.

## Known effect (fail-closed)
- **Demo Stripe Connect:** every demo restaurant uses the constant `connectDemoAccountID`, so they share one lookup hash.
  - Before: on MySQL, the second restaurant to enable demo silently corrupted the first one's row. PG already raised an error.
  - Now: it fails closed with the existing message.
  - Repair candidate: per-restaurant demo identity (hash includes restaurant_id). Not implemented.

## Research only (not implemented)
- **Custom domain mappings (site_builder handleCreateDomain / verify):**
  - The principal must be able to access the site (`canAccessSiteBySiteID`).
  - The domain is globally unique in mappings, with no check against `restaurant_domains` ownership.
  - `handleVerifyDomain` marks the mapping verified without any DNS check (source TODO).
  - The host router `serveRestaurantSiteIfHost` queries `SELECT restaurant_id FROM site_builder_domain_mappings`. That table has no restaurant_id column (plan schema), so the lookup errors and the fallback is dead code today. This is a source anomaly, kept.
  - Safe invariant to implement:
    1. Mapping create only if the domain has no restaurant_domains row or it is owned by the site's restaurant.
    2. Verification must prove DNS control (TXT token) before status='verified'.
    3. Routing via mappings only for verified rows, joined through site_builder_sites.restaurant_id.
- **Registrar purchase TOCTOU:**
  - The owner pre-check runs before CheckDomain/RegisterDomain. A concurrent insert by another restaurant between check and DB write means A pays Cloudflare and then gets 409; Cloudflare itself rejects a second registration of the same name.
  - Fix candidate: reserve before purchase. Use an owner-guarded upsert of a `pending` row plus an owner re-read, then call the registrar. On provider failure, mark the own pending row failed (own row only).
- **Stripe domain checkout:**
  - Checkout creation does not check ownership before payment, so a buyer can pay for a domain another restaurant owns. The webhook now skips provisioning and logs `[domain_owner_isolation_v1]` as a manual refund candidate. No refund is made.
  - Fix candidate: an owner pre-check at checkout creation, keeping the webhook guard for the race.
- **Domain fixes from isolation1:** these remain code-only fixes and are not runtime-validated. The gate stays open until QA.

## Counts (inventory r76)
| class | count |
|---|---|
| call sites | 2478 (2475 canonical + 1 owner helper r75 + 2 owner re-reads r76) |
| syntax-classified | 2476: nativeSQL 844, portableSQL 1462, selectedSQL 82, pgOnlySQL 39, mysqlOnlySQL 46 (port/retire) |
| provisional | 3: seo_item_routes 707/715 (two-step), site_builder_registrar:189 (mapping UUID v4) |
| blocked | 2: assistant_pos_tools:68 and instatic_manager:915, both UUID() v1 |
| unwired PG twins | ops-check, cdn-sweep |
| runtime-verified | 0 |

## Gates (unwaived)
- **Import:** 1117 import proof readyfalse; 4 NOT VALID FKs / 731 orphans.
- **Inventory gates:** IGNORE truncation, gap locks, NUL/JSON, search, TIME, UUID, SEO, multikey, collation/numeric, fiscal hash, 136 onupdate.
- **Tooling:** unwired tools; botstore Rebind quote awareness.
- **Pending repairs:** guest-label/catalog-scope repairs pending dedicated QA; PIN columns dump-only.
- **New in this repair:**
  - Demo Stripe Connect shared hash (fail-closed).
  - Orphaned provider resources on conflict.
  - Domain mapping ownership / verification / routing anomaly.
  - Registrar and checkout TOCTOU.

## Next
1. **SEO 707/715:**
   - One tx: INSERT … ON CONFLICT DO NOTHING (PG) inside a savepoint, then the route-key-first UPDATE in the same tx. MySQL ODKU stays (both keys are tenant-scoped).
   - Keep it provisional until reviewed.
2. **The 2 UUID v1 blocked sites:**
   - Generate google/uuid v1.6.0 `NewUUID()` (v1, already in go.mod) in Go and bind it on both dialects, after a consumer grep for ordering/time reliance.
   - registrar:189 v4 → the same v1 helper.
3. **Domain follow-ups** from the research above: reserve-before-purchase, checkout pre-check, mapping ownership invariant.

