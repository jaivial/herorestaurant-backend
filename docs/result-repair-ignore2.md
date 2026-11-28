# result-repair-ignore2: IGNORE on categories, fiscal series, stock anomalies, stock snapshots and tags

Code only. `go build -p 2 ./...` exits 0. I ran no tests or harness, executed no SQL, and wrote to no DB. Runtime-verified is 0, PG remains ready:false, and the server still refuses PG. R5 is untouched.

## Inventory r84

`/tmp/villacarmen-bot-20261111/sql-inventory-r84.json` has 2487 sites in 281 files, all classified:

| Classification | Sites |
|---|---|
| native | 846 |
| portable | 1473 |
| selected | 82 |
| pgOnly | 42 |
| mysqlOnly | 44 |

- 0 blocked, 0 provisional, 13 helper sites excluded.
- The +1 site is the portable category-length precheck.
- 12 `INSERT IGNORE` nativeSQL sites remain, outside this scope.

## Source facts

- **sql_mode** (from `villacarmen-full-mysql-source-settings-20261111.json`): STRICT_TRANS_TABLES, NO_ZERO_IN_DATE, NO_ZERO_DATE, ERROR_FOR_DIVISION_BY_ZERO, NO_ENGINE_SUBSTITUTION. With IGNORE, strict errors become warnings.
- **No warning is ever read** (no SHOW WARNINGS anywhere), so every IGNORE downgrade was invisible.
- **MySQL DSN has no clientFoundRows**, so an unchanged ODKU duplicate reports 0 rows. That makes the "ODKU no-op" form give the same RowsAffected as IGNORE on duplicate.

## Fixes (explicit, both dialects)

### 1. Category import (`handleBOPOSImportConfirm`)

**Columns:** `comida_items.categoria` is varchar(140); `pos_product_categories.name` is varchar(120), unique on (restaurant_id, name).

**Before:**
- MySQL IGNORE stored a 120-character cut as an orphan category. The link UPDATE compared against the full name, so it set the product's `category_id` to NULL. The category was lost silently.
- On PG the 22001 error was swallowed in a savepoint, with the same NULL result.

**Now:**
- A portable `CHAR_LENGTH` precheck inside the tx returns 400 "Carta category name is longer than 120 characters". The whole import tx rolls back.
- Errors from the category insert (COMIDA and VINO) and from the link UPDATE now propagate. The response is 500 and the tx rolls back, where before `_ =` discarded them.
- A duplicate category is still a no-op.
- The wine `tipo` column is varchar(50), so it always fits.
- No identity is ever truncated, so there is no cross-category merge.

### 2. Fiscal series (`ensurePOSFiscalSeries`)

**Schema:** `terminal_key` is varchar(64); `series_type` is ENUM(SIMPLIFICADA, RECTIFICATIVA); unique on (restaurant_id, terminal_key, series_type).

**Before (MySQL IGNORE):**
- An invalid enum value was stored as ''.
- A terminal key over 64 characters was cut, the follow-up SELECT then missed the row, and an orphan series row was left in the tx.

**Now, on both dialects, before any write:**
- An unknown document type returns an error.
- A terminal key with invalid UTF-8, a NUL, or more than 64 runes returns an error.
- Trailing spaces are dropped **for the series scope only**. This matches MySQL PAD SPACE, where "main " was already the same series as "main". Without it, PG ci_ai (no pad) would create a second series with the same prefix, restarting at 1.
- The document's own `terminal_key` and the hash content are unchanged.

**Unchanged:**
- `next_number` (numeric(20,0), CHECK >= 1) is locked FOR UPDATE and scanned into int64. Any value too large fails the scan, so nothing is clamped.
- `uq_pos_fiscal_documents_full` still rejects a duplicate full number.

**Errors** surface through the existing callers: 500 "Error issuing fiscal document", or 409 for rectify. The tx rolls back, so no number is consumed.

### 3. Stock anomalies (realtime deduct and adjust, checkout)

**Schema:** `quantity_after_base` is numeric(18,4). The unique key is (restaurant_id, ticket_line_stock_id), and there are 4 FKs with RESTRICT.

**Before:** MySQL IGNORE clamped an overflow to the column maximum, a wrong figure, and also ignored FK errors. On PG the realtime path swallowed errors in a savepoint.

**Now:**
- The MySQL statement is `INSERT … ON DUPLICATE KEY UPDATE ticket_line_stock_id=ticket_line_stock_id` (a no-op on duplicate). PG still uses ON CONFLICT DO NOTHING.
- Range and FK errors propagate in the realtime path, as checkout already did.
- Callers (line add, pack, line move, quantity change) already return 500 and roll back.

### 4. Stock replay snapshot (`backoffice_pos_reports`)

- The MySQL statement now uses `ON DUPLICATE KEY UPDATE id=id`, which reports 0 rows on duplicate, the same as before.
- Range and FK errors now reach the existing 500 "Error saving replay snapshot".

### 5. Tags (rail attach for ticket and line; visit merge)

**Before:** MySQL IGNORE also ignored FK failures. For example, if a tag or line was deleted after the validity check, the API answered "attached" when nothing was attached.

**Now:**
- MySQL uses an ODKU no-op: `tag_id=tag_id` for VALUES inserts, and `VALUES(tag_id)` in the INSERT…SELECT, to avoid ambiguity with the source table. VALUES() is deprecated but valid in 8.0.
- An FK error now returns the existing 500 (attach) or rolls back the merge.
- Duplicates stay idempotent.

### 6. Suppression diagnostic, NUL case made lossless

- No NUL: `[raw-latin1]` followed by a bijective Latin-1 reading of the first 288 bytes.
- NUL present: `[raw-b64]` followed by base64 of the first 216 bytes.
- The result is always valid UTF-8 and at most 300 characters.
- This is still not MySQL-equivalent (unproven).
- No logging was added.

## Status labels

Code fix only. These fixes, ignore1 and suppression2 all stay **QA-pending until runtime**. Nothing is runtime-closed.

## Gates (not waived)

- **Collation:** ci_ai vs unicode_ci, PAD SPACE and expansions. Category names that differ only by trailing spaces become two rows on PG.
- **Remaining IGNORE sites (12):** accounting_exports, menu_visibility, provision ×6 and bo_user_restaurants ×3, booking_reminder_deliveries, stripe_events, campaign_email_retries/recipients, pre_shift (LEFT cut on event/key), bot_inbound_queue.
- **Realtime snapshot lookup:** the error is still swallowed, and a missing snapshot skips the anomaly.
- **Multi-terminal numbering:** the full number has no terminal in it, so two terminals produce colliding numbers. This is a source design issue; the unique key rejects the collision.
- 1117 import-proof ready:false, 4 NOT VALID FKs with 731 orphans, gap locks, NUL/JSON, search, TIME, UUID, multikey, numeric, fiscal hash, 136 on-update, bare jsonb `?`, Stripe retention/processed, domain DNS, MySQL-only utilities.

## Manual read-only PG QA plan (later, local only)

1. A 121-character categoria, which must give 400 with no rows written.
2. A fiscal terminal "main " that reuses the "main" series, and a 65-rune terminal that is rejected.
3. Anomaly overflow and FK error, which must roll back the sale.
4. Attaching a tag deleted mid-flight, which must give 500.
5. A diagnostic containing a NUL, which must be stored as b64.

## Next

1. Stripe processed flag and reconciliation.
2. The remaining 12 IGNORE sites.
3. Gap-lock advisory locks.
4. TIME, NUL and collation adapters.
5. Read-only local PG QA run plan.

## Files and backup

Files changed (all in `internal/api`):
- `backoffice_pos_catalog.go`
- `backoffice_pos_fiscal.go`
- `backoffice_pos_stock_realtime.go`
- `backoffice_pos_checkout.go`
- `backoffice_pos_reports.go`
- `backoffice_pos_rail.go`
- `campaign_suppression_target.go`

Backup: `/tmp/ign3.bak.tar`.

