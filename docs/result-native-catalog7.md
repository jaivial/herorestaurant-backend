# Native PG port: catalog_stock group 7 (comida.go; compile only, NOT runtime-verified, NOT PG-ready)

## Counts (inventory r24: /tmp/villacarmen-bot-20261111/sql-inventory-r24.json, dbmigrate-r12)
- **Repo:** 2472 call sites, 835 native (+31), 262 dynamic. portable_audit_failures is 0.
  - Excluded: 5 helper-internal calls and 3 prepared-stmt calls.
- **Core:** 406/406. **Bookings:** 161/161.
- **catalog_stock:** 268/504 native, 236 remaining (64 dynamic). 8/43 files are done; **comida.go is 31/31 (12 dynamic reviewed)**.
  - Ledger: `docs/dbmigrate/native-ledger-catalog.json`.
- **Build:** `go build -p 2 ./...` exits 0 under the inherited cgroup (CPU 200%, 6G). No SQL was run, and no tests, vet, fixtures or Playwright.

## Audit flaw found and fixed (dbmigrate-r12)
**The flaw:**
- r11's comment remover was a regex that was **not quote-aware**. A `--` or `/*` inside a SQL string could delete the real remainder, including mixed-case table references, and falsely pass the identifier audit.
- The earlier claim "can only over-report" was therefore not supported for that case.

**The fix:** `identAuditText` is a lexical scan.
- Single-quoted literals have their bodies blanked; the `''` escape is handled.
- `"…"` and backtick identifiers are copied verbatim.
- Comments are dropped **only outside quotes**:
  - `-- ` only when followed by whitespace or end of text, the form both MySQL and PG treat as a comment;
  - `/* */`.
- It **returns the raw text (fail closed)** on:
  - a backslash inside a literal (MySQL `\'` vs PG standard strings);
  - an unterminated literal, identifier or comment;
  - a MySQL `#` comment outside quotes.
- The MySQL-only function check always runs on the raw text.

**Remaining limits:** not a SQL parser. PG dollar quotes and `E''` strings are not modelled; they stay in the checked text, so the audit only over-reports.

## comida.go changes
- **Base category upserts (plato/bebida):**
  - `ON CONFLICT (restaurant_id, slug)`.
  - `source = IF(source='custom', source, VALUES(source))` becomes CASE over the old row value / EXCLUDED.
- **Custom category upserts ×3:** `InsertID` with `ON CONFLICT … DO UPDATE … RETURNING id`.
  - **Pending:** the MySQL insert id on the duplicate path is not proven from source.
  - If it is 0, MySQL falls back to a slug-OR-name lookup (no ORDER BY) that may pick another row; PG returns the slug row.
- **Catalog list (dynamic):**
  - The where fragments are per dialect:
    - LIKE becomes `LOWER(col) COLLATE "C"` with a lowered pattern (CI/accent gap);
    - `JSON_CONTAINS(COALESCE(x, JSON_ARRAY()), JSON_QUOTE(?))` becomes `COALESCE(x,'[]'::jsonb) @> to_jsonb(CAST(? AS text))`, equal for flat arrays and scalars (nested arrays differ).
  - The head/tail are sqlFragment'd:
    - `CAST(alergenos_json AS text)`;
    - has_foto as CASE 0/1;
    - `CAST(production_type AS text)`;
    - `updated_at DESC NULLS LAST`.
  - COUNT and data are each rebound once.
- **Item detail:** the same column conversions.
- **POSTRES** (exists in source, with uppercase table and NUM/DESCRIPCION columns; **not a source anomaly**):
  - Quoted identifiers in list, count, detail, insert (`InsertID` returning `"NUM"`), update and delete.
  - The longtext `alergenos` filter uses `CAST(COALESCE(alergenos,'[]') AS jsonb)`. Invalid JSON text errors on both dialects; error timing and row order may differ.
- **Wines list/detail (dynamic):**
  - New `vinoV2NumSQL()` (CAST AS bigint on PG; range class).
  - has_foto CASE and production_type text via sqlFragment.
  - Reviewed `vinoV2From`/`vinoV2WhereLegacyNum*` portable fragments.
  - LIKE uses the CI port. Each query is rebound once.
- **Dynamic UPDATEs (comida item, postre, wine)** keep MySQL's changed-rows gate (an unchanged save answers "not found", source quirk).
  - New helper `pgChangedRowsPredicate(sets, setArgs, plan types)` builds `ROW(cols) IS DISTINCT FROM ROW(CAST(? AS type)…)`.
  - Text compares with `COLLATE "C"`; `col = NULL` items become typed NULLs.
  - An unknown column or shape **fails the request** rather than silently changing the contract. Type maps come from plan.sql.
- **Inserts:** the comida item uses `InsertID` (`CAST(? AS jsonb)`, `NULLIF(CAST(? AS text),'')`); the postre uses `InsertID`. Genuine errors surface; no id-only failure path exists.
- **Toggles, deletes and category reads:** portable.

**No source anomaly is touched:** comida.go has no DIA/FINDE and no menu_dishes_catalog.name.

## New pending items
- **Wines precio float(10,2):** MySQL FLOAT(M,D) rounds the stored value to 2 decimals. The plan maps the column to `real`, which does no D-rounding on write. This is a schema-level gap and is not hidden.
- **ODKU insert-id path** (above).
- **JSON_CONTAINS** nested-array semantics.
- **jsonb vs binary JSON equality** in changed-row checks.

**Unchanged:**
- CI/accent/PAD SPACE.
- FULLTEXT.
- 190 range narrowings.
- ON UPDATE (136) / DEFAULT (353) time semantics.
- The recent_conversations view.
- NUL QA.
- JSON text and timestamp QA.
- 4 NOT VALID FKs covering 731 orphans.
- 1117 pending, ready:false.
- Source anomalies.

MySQL stays the default and the server still refuses PG. No DB writes, activation, providers, payments, email, WhatsApp, sends, .env/proof edits, commits or deploys.

## Next
| file | remaining | dynamic |
|---|---|---|
| backoffice_comida_categories.go | 22 | 10 |
| backoffice_stock_analytics.go | 26 | 0 |
| public_menus.go | 18 | 10 |
| backoffice_stock_suppliers.go | 17 | 2 |
| group_menus.go | 14 | 6 |
| menu_section_dessert_source.go | 11 | 1 |

