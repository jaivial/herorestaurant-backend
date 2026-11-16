# result-repair-vitess11

Model: claude-merge/claude-opus-5-5-acc2 (low).
- Source and build only. No tests or Playwright, no SQL writes, no activation, no network.
- go.mod and go.sum are unchanged.
- `go build -p 2 ./...` OK with /home/linuxbrew/.linuxbrew/bin/go.

## 1. Writers
**Wired this batch** (VC_PG_WK_KEYS; off = identical SQL and args):
- **site_builder_registrar.go:303:** the domain mapping insert.
  - PG args are `[id, site_id, domain]` (the existing PG-only UUID prepend).
  - After that prepend, `mapArgs = wkKeyArgs(len(mapArgs), …, {"domain","id"}, {mapArgs[2], mapArgs[0]}, mapArgs...)` runs when the flag is on.
  - The PG text adds `domain__wk, id__wk`, VALUES `?, ?`, and `ON CONFLICT (domain__wk)`.
- **instatic_manager.go:948:** the INSERT … SELECT mapping.
  - PG args are `[id, sub, restaurant_id, label]`. The keys go before `FROM` at arg position 2.
  - id is the first arg and domain is `sub`.
- **comida.go:2404:** bebida category create (`InsertID` with the RETURNING expression kept as the id column parameter); key = slug.
- **backoffice_premium.go:**
  - 3 restaurant_areas UPDATE variants and 9 restaurant_tables UPDATE variants. A transformer added `name__wk = COALESCE(?, name__wk)` after `name = COALESCE(?, name)`, with the key argument nested right after the name argument. A NULL name gives a NULL key, so COALESCE keeps the old key, matching the text column.
  - The dedicated `UPDATE restaurant_tables SET numero_mesa = ?` (~l.3020) now also sets `numero_mesa__wk`.

**Re-triage after these edits:**
- `docs/dbmigrate/wk-writer-triage-vitess11-raw.tsv` (688 candidate lines);
- `docs/dbmigrate/wk-writer-unwired-vitess11.tsv` (rows touching a registered column).

| status | rows |
|---|---|
| wired | 123 |
| UNWIRED-PG-or-portable | 151 |
| MySQL text (its PG twin is a separate row) | 67 |
| mysql-only | 1 |

- The status is lexical. "wired" means `__wk` appears within the statement window, or the site is a wkSetItems builder.
- The UNWIRED rows are mostly the idempotency, POS, stock, fiscal, bo_* and site_builder keys from padidem1 to padbatch4:
  - stock_movements 11, stock_item_units 7, pos_ticket_lines 6, bo_users 6, stock_items 4, and others.
  - Many are `portableSQL` statements, which must first be split into native MySQL/PG pairs.
- So **writer coverage is NOT complete.** This file is the explicit work list.

## 2. LIKE sites
**Wired this batch:**
- **checkcancel.go:**
  - The candidate query runs over the same predicates (restaurant, date, time, party_size) with `TRIM(customer_name)`; PG TRIM removes spaces only, like MySQL.
  - Matching uses **utf8mb4_general_ci**, the source collation of bookings.customer_name. The `LOWER` is redundant under a case-insensitive collation.
  - The PG variant becomes `AND id = ANY(?)`. Results: 422 on too broad, 500 on any other error.
- **whatsapp_bot_dish_allergens_tools.go:**
  - stock_recipes.name, unicode_ci.
  - The PG variant is `id = ANY(?)` and keeps `ORDER BY name LIMIT 5`.
  - Errors return the existing bot error JSON.

LIKE sites wired in total: 7 (postres, stock list, comida postres, invoices list, POS customers, checkcancel, dish allergens).

**Not done this batch:**
- assistant catalog
- the bookings search built at runtime (backoffice_bookings*.go, invoices 782/786)
- POS reservations
- AI usage
- technical sheet
- group menus v2
- comida 818/1111
- stock options
- group_menus.go:940 TitleJoin

## 3. Startup guard (implemented, not run): internal/db/wk_gate.go
- `RequireWKKeysIfFlag` is a no-op with the flag off.
- With the flag on, `RequireWKKeys` refuses unless all of these hold:
  1. Every `mysqlcoll.WKColumns` entry (149) has `<col>__wk` as `bytea` in current_schema().
  2. Every one of the 134 `ixwk__` indexes exists. Each is derived from RequiredPadKeyIndexes, with names truncated at 63 the same way as the DDL proposal, and must be unique, valid, non-partial and expression-free, with exactly the expected column order (PAD parts become `__wk`).
  3. `mysqlcoll.CheckStoredKeys` reports `Ready()` (0 null, utf8 and key mismatches) for every column, inside a READ ONLY tx.
- Wired into `OpenBusinessForTool` after RequireOnUpdateTriggers, so it is fail-closed with no fallback.
- `pad-gate-check` runs it as `business.wk_keys` when `PADQA_WK=1`. It is optional, because scratch has no `__wk` DDL yet.

## Inventory r118
2534 sites. Native rose to 2513: the 2 wk_gate catalog queries, which are pgOnlySQL.

## Gates
- `__wk` DDL unapplied, so with the flag on the guard refuses by design.
- 151 unwired writer rows.
- LIKE: 7 of about 20 sites.
- Sort parity; provisional bounds.
- GPL/provenance still open.
- Shadow FK.
- PG disabled, checkout 503, 0 handler QA, 1117 pending, 731 orphans, R5 untouched.
