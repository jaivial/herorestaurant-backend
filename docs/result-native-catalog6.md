# Native PG port: catalog_stock group 6 (group-menu satellites; compile only, NOT runtime-verified, NOT PG-ready)

## Counts (inventory r23: /tmp/villacarmen-bot-20261111/sql-inventory-r23.json, dbmigrate-r11)
- **Repo:** 2472 call sites, 804 native (+35 ported this phase), 274 dynamic. portable_audit_failures is 0.
  - Excluded: 5 helper-internal calls, plus 3 prepared-stmt calls (new basis, see below).
- **Core:** 406/406. **Bookings:** 161/161.
- **catalog_stock:** 504 sites on the r11 basis (the 2 stmt.ExecContext calls in special_sections are now counted once at their Prepare). 237 native, 267 remaining (76 dynamic). 7/43 files are done:
  - backoffice_stock;
  - stock_documents;
  - stock_recipes;
  - group_menus_v2;
  - **group_menus_v2_special_sections (17/17 on the new basis; 19 before)**;
  - **group_menus_v2_ws (10/10)**;
  - **group_menus_v2_items (8/8)**.
  - Ledger: `docs/dbmigrate/native-ledger-catalog.json`.
- **Build:** `go build -p 2 ./...` exits 0 under the inherited cgroup (CPU 200%, 6G). No SQL was run, and no tests, vet, fixtures or Playwright.

## Extractor/audit review (static, no tests)
1. **Flaw found and fixed: prepared statements.** `stmt.ExecContext(ctx, idx, …)` on a `*sql.Stmt` was counted as a dynamic call site, with the first bind arg taken as the "query".
   - r11 skips Exec/Query on identifiers assigned from `Prepare/PrepareContext` in the same function, and counts them in `prepared_stmt_calls_excluded` (3 repo-wide).
   - The SQL is counted once at the Prepare site, so the canonical total goes 2475 → 2472. Historical r16–r22 files are untouched.
   - **Limitation:** purely syntactic. A stmt passed across functions would still be counted.
2. **Quoted-literal audit hardened:**
   - Comments (`--…`, `/*…*/`) are removed before literal stripping, so an apostrophe in a comment can't open a fake literal and hide identifiers.
   - Literals are stripped only when quotes are balanced and there is no backslash escape (MySQL `\'` vs PG standard strings). Otherwise the raw text is checked, so the audit can only over-report.
   - The MySQL-only function check is unchanged.
   - **Limitation:** lexical, not a full SQL parser. Dollar-quoting and quote characters inside identifiers are not modelled.

## Changed functions
### backoffice_group_menus_v2_special_sections.go
- **Create section:** next position via `COALESCE(MAX)+1`; the INSERT uses `InsertID`.
- **Patch section (dynamic title/price SET):** rebound once. RowsAffected is unused.
- **Delete; image path reads; reorder ×2:** the Prepare text is portable and rebound, with the same tx/stmt loop.
- **Image state UPDATEs ×3:** `CURRENT_TIMESTAMP` becomes `date_trunc('second', statement_timestamp())`.
- **Visibility (dynamic menus SET of fixed columns):** rebound once.
- **Load sections:**
  - `COALESCE(created_at, <statement time>)`.
  - The uploading window `NOW() - INTERVAL 5 MINUTE` becomes `<statement time> - interval '5 minutes'` (timestamptz).

### backoffice_group_menus_v2_ws.go
- **Read states:** portable.
- **AI generate state UPDATEs** (dish `(1,1)`, preview `(1,1,1)`) keep MySQL's changed-rows quirk: a repeat request while the flags are already set returns "not found". PG adds `AND (flags) IS DISTINCT FROM (1,1[,1])`.
- **Other result/clear UPDATEs:** portable. RowsAffected is unused.
- **No ws/AI/provider code was touched or run.**

### backoffice_group_menus_v2_items.go
- **Section lock:** `FOR UPDATE` is portable.
- **Position shift:** portable.
- **Dish INSERT:** `CAST(? AS jsonb)` plus `InsertID`.
- **Dish delete:** RowsAffected gate on matched rows.
- **Section create INSERT…SELECT MAX+1:**
  - Explicit casts (integer/text), so strict-length errors are kept.
  - `InsertID`: the aggregate always yields one row.
  - Concurrent creates can compute the same position on both dialects; the source has no lock.
- **Patch section (dynamic fixed lowercase SET items):** rebound once.

**Source anomalies:** none in these files. The known ones elsewhere stay documented and unfixed.

## Pending risks (none waived)
- **JSON:** text render and comparison.
- **Timestamps:** JSON timestamp QA (UTC ScanLocation from code reading).
- **CI/accent:** ordering.
- **Schema time semantics:** ON UPDATE (136) / DEFAULT (353).
- **Unchanged:**
  - 190 range narrowings.
  - The recent_conversations view.
  - NUL QA.
  - 4 NOT VALID FKs covering 731 orphans.
  - 1117 pending, ready:false.
  - PAD SPACE.
  - FULLTEXT.
  - Source anomalies.

MySQL stays the default and the server still refuses PG. No DB writes, activation, providers, payments, email, WhatsApp, sends, .env/proof edits, commits or deploys.

## Next coherent remaining files (actual r23 counts)
| file | remaining | dynamic |
|---|---|---|
| comida.go | 31 | 12 |
| backoffice_comida_categories.go | 22 | 10 |

These two form one comida pair, 53 sites with 22 dynamic. Splitting is recommended: comida.go first.

**Then:**
| file | remaining | dynamic |
|---|---|---|
| backoffice_stock_analytics.go | 26 | 0 |
| public_menus.go | 18 | 10 |
| backoffice_stock_suppliers.go | 17 | 2 |
| group_menus.go | 14 | 6 |
| menu_section_dessert_source.go | 11 | 1 |
| backoffice_group_menus.go | 10 | 2 |
| others | … | … |

