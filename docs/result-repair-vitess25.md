# result-repair-vitess25

Model: claude-merge/claude-opus-5-5-acc2 (low).
- Source and build only. No tests, no SQL, no network, no production.
- go.mod unchanged. Backups: /tmp/v25-pre.bak.tar and /tmp/v25-w.bak.tar.
- backoffice_stock_documents.go:680 is untouched.

## 1. Empty key stored as NULL: cause (confirmed in code)
- `mysqlcoll.(*Coll).Key` returns `WeightString(nil, "", 0)`, which is **nil** for '' (and for any text whose weights are all stripped SPACE units under PAD).
- That nil flowed through:
  - `WriterKey` → `ColumnKey` → `KeyArgs` (api `wkKeyArgs`, analytics `wkKeyArgs`, `wkSetItems`, `wkSupArgs`, `wkKeyArg` lookups);
  - `wkArgs`;
  - `cmd/wk-backfill`;
  - `cmd/qa-seed-scratch`.
- database/sql/pgx binds a nil `[]byte` as **SQL NULL**. So '' got a NULL `__wk`, and `CheckStoredKeys` (text non-NULL, key NULL) counted null_mismatch.
- The same nil made a `col__wk = ?` lookup for '' compare with NULL, so it never matched.

## 2. Fix (one place, covers every producer)
- `Key` now returns `[]byte{}` (non-nil) when the computed key is empty.
- Every producer goes through `Key`, so a non-NULL text always binds a non-NULL (possibly empty) bytea: KeyArgs, wkKeyArg, wkSetItems, wkSupArgs, wkArgs, analytics, wk-backfill, qa-seed-scratch, and the LIKE/TitleJoin key users.
- NULL text still yields a NULL key: `WriterKey(nil)` and KeyArgs' explicit nil branch are unchanged.
- **CheckStoredKeys:** stored NULL vs text '' is a null_mismatch (correct, since that is the old bad state). A stored empty bytea scans as `[]byte{}` (non-nil), so it is compared as `"" == ""`, which is OK.
- **Existing bad rows** (e.g. the comida_categories.food_type row from QA) stay NULL until rewritten. `wk-backfill -apply` on scratch fixes them: it selects `__wk IS NULL AND col IS NOT NULL`, and with this build it binds the empty key.

**Other empty-key places checked:**
- `wkScopeKeyText` never returns '' ('global' or a date).
- `SortByKey` already mapped nil to empty.
- `Equal` and `Compare` are unaffected (bytes equality).
- PAD SPACE values like '   ' also get an empty key, now non-NULL; this matches MySQL '' = '   '.

## 3. Supplier aliases 500: fixed
- `backoffice_stock_suppliers.go` (PUT aliases) and `backoffice_stock_documents.go:698` (invoice confirm alias upsert) were unwired. The flag-on variants (wkPG) now:
  - add `supplier_name__wk, supplier_code__wk, normalized_description__wk` via `wkKeyArgs(6, …)`;
  - use `ON CONFLICT (restaurant_id, supplier_name__wk, supplier_code__wk, normalized_description__wk)`.
- Flag off: identical SQL and args.
- The follow-up `SELECT id` in suppliers now compares all three `__wk` columns. Before, only normalized_description used the key.
- The stock movement INSERT at :680 is untouched.

## 4. Re-scan: why the earlier claim was wrong, and what it found
**Why vitess16's "all PG writers wired" was wrong:** that triage accepted a row if `__wk` appeared anywhere in a ±25-line window, so a neighbouring wired SELECT hid an unwired INSERT.

**New scan:** each SQL literal (backtick and double-quoted) on a registered table that writes a registered column. A literal counts as wired only if **it** contains `__wk`, or it is the OFF text of a `wkPG` pair, or it is the MySQL text of a native pair.
- It found **14 more unwired app writers**, now wired:
  - pos_daily_sequences.sequence_type (backoffice_pos.go nextPOSTicketNumber);
  - pos_stock_exceptions.replay_idempotency_key (pos_reports resolve);
  - stock_role_permissions upsert;
  - platform_settings.setting_key (stripe fees);
  - restaurant_ai_provider_keys.provider;
  - restaurant_booking_extras.slug (bot catalog extras);
  - stock_categories import upsert;
  - message_deliveries.delivery_key (pre-shift);
  - pos_fiscal_series.terminal_key (INSERT … ON CONFLICT);
  - pos_role_permissions upsert;
  - pos_tickets.checkout_idempotency_key (checkout UPDATE);
  - pos_customers anonymise (`phone__wk=NULL, email__wk=NULL`);
  - pos_product_categories ×2 (INSERT … SELECT from comida_items.categoria and wines.tipo). With the flag on, the source value is read first with the same predicate, `wkPosCatArgs` computes its key, and the key binds as `CAST(? AS bytea)` with `ON CONFLICT (restaurant_id, name__wk)`.
- **Remaining after the scan:**
  - backoffice_stock_documents.go:680, the stock movement (user decision);
  - cmd tools outside app runtime: backfill-translations:386 (dish_translations) and comida-stock-backfill:229/240 (stock_items, stock_item_units). They are not wired; do not run them on a flag-on DB without wiring;
  - one false positive: accounting_exports:349, a quote inside a literal; it is wired.
- internal/bot has no SQL writers for registered tables.
- ON CONFLICT targets: no PG `ON CONFLICT (…rtrim(…)…)` text remains outside the OFF side of a wkPG pair.

**Still a lexical scan:** builder-composed SQL (string concatenation across functions) can still escape it. The startup guard plus CheckStoredKeys after QA remains the real detector.

## 5. Binaries (/tmp/villacarmen-bot-20261111/, static CGO_ENABLED=0, -trimpath, go build -p 2)
systemd-run --user is not permitted in this sandbox, so the builds ran under the inherited cgroup.

| binary | sha256 |
|---|---|
| vc-server-qa | 5ec8cfcc9caf20726224b39e7a74f66bfbbf4262d9616129861097cbc6748638 |
| wk-backfill | 32d87a0ba90182c26a1501c846ac6f7b6c04fe10fd276e4c90a54ef4121a27a2 |
| pad-gate-check | 0cf1eb8bcdfc149f8081a21e841413703b762bc426e3f3706578caf45ba24c79 |
| qa-http | dac985b00d04b0f6a0babde05f7fd45c6da593be0a052b85cde440bf974b6714 (unchanged source) |
| qa-seed-scratch | 8233f4edde9c342a12e0d84615781168f11af7513ff8c29aec97ad2c12dfa991 |

## Not verified
- No run. The next scratch steps are:
  1. `wk-backfill` dry-run, then `-apply` for comida_categories.food_type;
  2. `PADQA_WK=1 pad-gate-check`;
  3. a re-run of the PUT aliases call;
  4. a POS ticket number and checkout flow (they touch fiscal/money tables; scratch only, under the GO).
- The 14 newly wired sites were generated mechanically and spot-checked; they have not been executed.
- The wkPosCatArgs pre-read is not in the same statement, so under a concurrent change of comida_items.categoria the key could lag. That is acceptable on scratch, but noted.
