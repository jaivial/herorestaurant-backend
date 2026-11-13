# result-native-pos15: backoffice_pos_checkout (48 sites)

Code only. go build -p 2 ./... exit 0. Runtime-verified 0. PG readyfalse, server refuses PG, MySQL default. No checkout, provider or payment path was invoked.

## Counts (inventory r70)
- **Repo:** 2475 = native 2302 (incl 5 provisional, no new) + blocked 3 + unwrapped 170; dynamic 44; audit failures 0.
- **Delta vs r69:** +48.
- **POS:** 511/564 ported, 53 left (rail, 2 dynamic), 34/35 files done.
- **Unwrapped 170:**
  - backoffice_pos_rail: 53
  - Tooling: 117, of which internal/botstore 37, internal/dbmigrate 18, cmd/ops-check 15, backfill-translations 9, cdn-sweep 8, comida-stock-backfill 8, group-menu-arroz-cleanup 5, update-restaurant-info 4, db/migrations 4, migrate-wine-images 3, botstore-migrate 2, chatgpt-plugin-token 1, cmd/dbmigrate 1, seed-legal-pages 1, internal/db 1.

## Work
- **Checkout tx** (shared with bulk close):
  - Ticket stays FOR UPDATE; idempotency short-circuit unchanged.
  - paid_at and closed_at use naive UTC statement time.
  - Enums read as text.
  - The ignored CHECKOUT audit runs in a savepoint, because the bulk loop reuses the same tx.
- **Stock:**
  - Snapshot and movement ids come back via InsertID RETURNING.
  - Level ensure becomes ON CONFLICT DO NOTHING, followed by FOR UPDATE.
  - The ignored sale_movement_id lookup runs in a savepoint.
  - INSERT IGNORE anomalies becomes ON CONFLICT on the snapshot key.
  - applied_at and reversed_at use timestamptz statement time.
- **Refunds:**
  - Refund id via InsertID.
  - Ignored audit, restock-sum and REVERSED update run in savepoints.
  - The CASE parameter is typed as text.
- **Covers:**
  - Single unique key, so the upsert uses ON CONFLICT.
  - Reconciliation compares the three distinct service_type enums as text, and the GROUP BY now includes the constant restaurant_id.
- **Typed JSON:** audit JSON_OBJECT → jsonb_build_object with bigint/text arguments.

## Tool incident
A lit() prefix anchor double-wrapped one pos_visits CLOSED statement. The inventory caught it and I fixed it by hand. A repo-wide grep finds 0 nested wrappers.

## Gates (unwaived)
- **New in this file:**
  - IGNORE range-clamp loss on anomalies.
  - Float→numeric rounding of the restock fraction.
  - Enum ORDER BY declaration order (QA).
  - Bulk-close audit JSON normalisation.
  - Row-lock timing on level ensure.
- **Carried from earlier checkpoints:**
  - pos13 concurrent domain-register cross-tenant takeover (unaccepted).
  - pos14 ticket catch-up lenient UNSIGNED cast (sign/overflow/non-ASCII digits).
  - Duplicate re-read under default isolation (RR vs RC).
  - All pos10–13 gates.
- **General:** 1117 import proof readyfalse; 4 NOT VALID FKs / 731 orphans; collation/search/UUID/SEO/multikey/routing/NUL/TIME/numeric/fiscal hash/136 onupdate.
- **Repairs and dump-only:** guest-label/catalog-scope technical repairs pending dedicated QA; PIN columns dump-only.

## Next
1. backoffice_pos_rail: 53 sites, 2 dynamic. This is the last POS file.
2. The 117 tooling sites as their own checkpoint(s): botstore 37 + dbmigrate 18 first, then the cmd/* tools.

