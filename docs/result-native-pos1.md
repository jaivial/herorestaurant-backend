# result-native-pos1 — POS billing checkpoint 1: `backoffice_pos_modifiers.go` (30 sites)

**Runtime-verified: 0 sites. Syntax port only; ready:false.** MySQL stays the default and the server still refuses PG. No SQL, DB writes, workers, providers or sends.

## Build and inventory
- `go build -p 2 ./...` gave EXIT=0 (inherited CPU 200% / 6G limits). No tests, vet, fixtures or Playwright.
- Inventory r56 (`/tmp/villacarmen-bot-20261111/sql-inventory-r56.json`):
  - Canonical 2475 = native 1821 (includes 4 provisional) + blocked 3 + unwrapped 651.
  - Dynamic 94; audit failures 0.
- New ledger: `docs/dbmigrate/native-ledger-pos.json`.
  - POS: 30/564 ported, 534 remaining (52 dynamic), 1/35 files done.

## Semantics (compile-only)
- **`SELECT DISTINCT` ordered by a non-selected column** (`pmg.sort_order`, product-filtered catalog):
  - PG rejects this; MySQL accepts it because `ONLY_FULL_GROUP_BY` is off in the source sql_mode.
  - With a product filter, the `pos_product_modifier_groups` PK (restaurant_id, pos_product_id, group_id) yields at most one row per group. The PG twin therefore drops `DISTINCT`; rows and order are identical.
  - The unfiltered path orders by selected columns only, so it keeps `DISTINCT`.
- **`kind` enum:** read as text. Writes bind a text parameter that PG infers as the enum; an invalid label errors on both engines (strict mode).
- **Create (group and option):** `sqldb.InsertID` (RETURNING). The MySQL path is identical, because v1.9.3's `LastInsertId` never errors. Unique-name violations take the same 400 path.
- **Transactions:** the ticket-line modifier insert inside the transaction propagates its errors, so no savepoint is needed.
- **`updated_at`:** a naive timestamp maintained by the plan's ON UPDATE trigger. This falls under the general 136-table ON UPDATE gate. RowsAffected is not used in this file.

## New open gates
- **Collation:** name uniqueness and `ORDER BY name` on `ci_ai` keep case-insensitive behaviour, but accent, PAD SPACE and ordering differ.
- **Rounding:** quantity float64 into `numeric(12,3)` needs rounding QA.

## Botstore pool timezone (investigated in code)
- `botstore.OpenSchema` calls `sqldb.OpenPostgres` when a schema is set. That is the unified `bot_context` configuration, and it pins the session to UTC.
- With an empty schema it uses a plain `sql.Open("pgx")` with the server's default TimeZone.
- The gate is therefore narrowed to the no-schema configuration only. It is not a new blocker.

## All gates remain open
- Import proof: 1117 pending, ready:false; 4 NOT VALID FKs with 731 orphans.
- Gap locks; NUL / JSON audit drops; UUIDs; SEO and multi-key upserts.
- Routing; `IGNORE` truncation in suppressions and the outbox; claim batch size.
- Collation; numeric QA; search (FTS); TIME range; proposal hashes and backups across the cutover.
- Crossaudit unique-site count: 301 (current).
- Production-confirmed source anomalies: `bookings.created_at`, `menu_dishes_catalog.name`, `DIA`/`FINDE`, `modification_history.restaurant_id`. Other anomalies come from the restored dump only.

## Next POS checkpoint: `backoffice_pos_operations.go` (27 sites, 0 dynamic)
- A coherent single file, chosen before the money-heavy ones.
- After that, split into parts of about 30 sites:
  - `backoffice_pos_rail.go` 53
  - `backoffice_pos_checkout.go` 48
  - `backoffice_pos.go` 44 (includes the ticket-number `UUID()` gate)
  - `backoffice_premium.go` 39 (16 dynamic)

