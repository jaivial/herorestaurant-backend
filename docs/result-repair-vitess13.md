# result-repair-vitess13

Model: claude-merge/claude-opus-5-5-acc2 (low).
- Source and build only. No tests or Playwright, no SQL writes, no activation, no network.
- go.mod and go.sum are unchanged.
- `go build -p 2 ./...` OK with /home/linuxbrew/.linuxbrew/bin/go.
- Pre-batch backup: /tmp/v13-pre.bak.tar.

Input: scratch has the `__wk` DDL (149 columns, 134 ixwk indexes) and a backfill. `pad-gate-check` with `PADQA_WK=1` passes `business.wk_keys`; the shadow FK still fails on the orphans.

## 1. Hand-wired writers (flag VC_PG_WK_KEYS; off = identical SQL and args)
- **pos_fiscal_documents.full_number, the duplicate/copy insert** (backoffice_pos_fiscal.go ~l.500):
  - This is INSERT … SELECT from the original document row, so the PG variant copies `full_number__wk` from the source row in SQL.
  - No Go recomputation: it is the same text, so the same key is exact.
  - The other fiscal inserts were auto-wired in vitess12.
- **accounting_exports.payload_hash:** the CTE form gets `CAST(? AS bytea)` after the CTE placeholders and `ON CONFLICT (…, payload_hash__wk)`; key = hashText.
- **booking_reminder_deliveries.delivery_key:** the same CTE pattern; key = key.
- **restaurant_members.email** UPDATE (numbered `$n` text): adds `email__wk = $11`, with the key appended as the 11th argument. The changed-rows ROW predicate is unchanged.
- **whatsapp_bot_sessions.user_phone**, both inserts (bot history and human takeover):
  - inside the existing `st.Postgres` branch the PG text is chosen through `wkPG`;
  - `ON CONFLICT (restaurant_id, user_phone__wk)`;
  - the provider phone value is keyed raw, never trimmed.
- **restaurant_floors.scope_key**, both `loadDefaultFloors` inserts:
  - `scope_key` is a **STORED GENERATED** column, `IFNULL(DATE_FORMAT(specific_date,'%Y-%m-%d'),'global')`;
  - these inserts never set specific_date, so the key is `ColumnKey("global")`.
  - **New gap:** every writer that sets or changes `specific_date` on restaurant_floors AND restaurant_salons must also set `scope_key__wk` (key of the date text or 'global'). Those writers are not covered by the scope_key triage because scope_key never appears in their SQL.
- **menus.seo_slug:** `seo_slug__wk = ?` with `values[0]` (a NULL slug gives a NULL key).

**Not wired, source bug found:** backoffice_stock_documents.go:680 (stock_movements):
- The `portableSQL` INSERT lists **14 columns but 15 VALUES tokens and 13 placeholders**, with only 12 args passed.
- It fails on BOTH engines, and the error is mapped to 409 "Invoice was already applied".
- This is an existing source/MySQL bug. I left it unchanged because fixing it changes behaviour (invoice application would start writing). It needs a decision.

**The 11 "unverified" UPDATEs, reviewed: NOT wired.** Their rows earlier counted as wired only because `__wk` appeared in nearby statements, so this is an explicit correction.
- pos_service_periods, pos_product_categories, pos_kitchen_stations, stock_vat_rates, stock_suppliers, stock_items (sku), stock_categories and stock_warehouses (code) use `name=?`/`$n` with duplicated changed-rows argument slices.
- pos_tickets (assistant_pos_tools.go:72) uses slice args.
- Each needs a manual edit.
- **Consequence:** with the flag on, an update through these paths leaves `<col>__wk` stale. CheckStoredKeys would then report key_mismatch and the startup guard refuses. **The flag must stay off until they are wired.**

## 2. Sort parity
- **stock list:** `i.name` becomes `i.name__wk` when the flag is on (default, stock_asc and stock_desc orders).
- **stock options:** `ORDER BY i.name__wk LIMIT 500` in the flag variant. The 500-row selection now follows MySQL weight order.
- **dish allergens, decision:** stock_recipes.name is **not** in the 149 registry, so there is no `name__wk`. It keeps PG `ORDER BY name LIMIT 5` under ci_ai, which is a known parity gap. The proposed remedy is to add `stock_recipes.name` to the registry and the DDL; a Go sort is not possible because LIMIT 5 is applied in SQL.

## 3. LIKE sites
None added this batch.

Still remaining:
- assistant catalog
- bookings search at runtime (including invoices 782/786)
- POS reservations
- AI usage
- technical sheet
- group menus v2
- comida 818/1111
- group_menus.go:940 TitleJoin

Wired total: 8.

## 4. Lookups that should use __wk: docs/dbmigrate/wk-lookups-vitess13.tsv
- 186 PG `(rtrim(col,' ') COLLATE dbmigrate.ci_ai) = (rtrim(?,' ') …)` comparisons on registered column names.
- Top columns: slug 25, id 25, domain 20, idempotency_key 11, menu_key 8, supplier_name 7, role_slug 6.
- They still use the ci_ai approximation, so their equality can differ from MySQL (ß, æ, combining marks, general_ci).
- To switch: `col__wk = $key` (key from `mysqlcoll.ColumnKey`), behind the flag.
- The table is matched by column name only; each row must be confirmed for the right table.
- Not switched yet.

## Inventory r120
2534 sites, 2513 native, unchanged.

## Gates
- **Flag must stay off:** 11 unwired UPDATE paths, the specific_date writers (scope_key), and 186 lookups not switched.
- Stock document insert source bug (decision).
- LIKE: 8 of about 16 sites.
- stock_recipes.name registry gap.
- GPL/provenance still open.
- Shadow FK.
- PG disabled, checkout 503, 0 handler QA, 1117 pending, 731 orphans, R5 untouched.
