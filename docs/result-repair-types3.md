# Result: repair-types3 (numeric precision / unsigned ranges; source-audit plus future plan; no code or SQL changes)

## Manual proof recorded (coordinator, scratch only; parent GO 08:53 covered scratch only)
- **09:27:** pad-gate-check (CGO=0, cgroup 2 CPU/6 GB, a single read-only command in the isolated scratch) returned:
  - business.pad_key_indexes = true: all 134 real indexes match the validator, which confirms the strict regex against the actual catalog;
  - business.mysql_origin_bot_tables = true;
  - bot.no_mysql_origin_tables = true;
  - business.pad_shadow_fks = false, as expected (missing bo_roles.slug__pad).
- **That verifies only:** the wiring2 pool mapping and the index gate, via a read-only diagnostic. It is not handler or functional QA.
- **09:16:** the scratch shadow DDL was syntax-accepted, but FK validation failed: site_builder_domain_mappings has historical orphan site_id values. The whole transaction rolled back and no shadows remain. The orphan blocker is preserved, with no NOT VALID waiver.
- Originals, R5, the app, providers, payments and sends were untouched. padfk1 is superseded by wiring2 (namespace).

## Pending decisions in the actual plan (R4 coverage.json, unchanged)
1116 keyed entries, 1873 items (the 1117 count includes the view entry):

| category | items | status |
|---|---|---|
| like_on_nondeterministic_collation | 774 | open (collation batch) |
| pad_space | 771 | source callers covered for 134 keys; data/QA open |
| range_narrowing | 190 | analysed here; open (decision below) |
| on_update_explicit_same_value_gap | 136 | open (next: time batch) |
| storage_app_adapter | 1 | open |
| view_activation_review | 1 | open |

Nothing is closed or marked ready.

## Unsigned integers: exact source domain vs PG target
- **Source:** 230 unsigned columns (224 bigint, 3 tinyint, 2 smallint, 1 int).
- **Preserved now, by the plan mapping (types.go unsignedRanges):**
  - tinyint unsigned → smallint CHECK 0..255;
  - smallint unsigned → integer CHECK 0..65535;
  - int unsigned → bigint CHECK 0..4294967295;
  - 34 non-identity-linked bigint unsigned → numeric(20,0) CHECK 0..18446744073709551615. The full source range is kept.
- **Narrowed (190, listed in docs/dbmigrate/unsigned-narrowing-190.tsv):** 91 AUTO_INCREMENT ids plus 99 identity/FK-linked columns, mapped to bigint CHECK >= 0, max 2^63-1. The legal source range is 0..2^64-1.
- **Inserts are never silently clamped:** a value > 2^63-1 is a PG error (22003/23514), not a clamp.
- **App limit (source code):**
  - every id path in Go scans and binds int64/int (1136 Scan calls; no uint64 scan target in internal/api; uint64 appears only in generic formatters such as backoffice_premium.go:4310 and group_menus_v2_ws.go:1535, which already clamp or format);
  - on MySQL, scanning a stored uint64 > 2^63-1 into int64 fails in database/sql (range error), so the application already cannot read such ids on the source either;
  - JSON numbers above 2^53 are also lossy in the JS clients.
- **The narrowing is still a loss of legal DATA domain** for any row written by external or legacy tools. It is not closed by app evidence.

**Future plan (separate; does NOT change R5's plan, export or proof):**
- **Option A, bigint (current):** the gate stays open until a read-only probe proves `max(col) <= 9223372036854775807` for all 190 on source and copy. That covers current data only, so writers above 2^63 stay unguarded.
- **Option B, full-domain preserving (recommended technical default):**
  - all 224 bigint unsigned columns become numeric(20,0) CHECK 0..2^64-1;
  - identities are replaced by `DEFAULT nextval(seq)` on a bigint sequence (PG identities require integer types). The generator ceiling stays 2^63-1, a documented generator-only limit, not a data limit;
  - FK pairs stay numeric(20,0) on both sides, so equality is exact.
- **Option B caller constraints:**
  - pgx returns numeric as text, and database/sql converts it into int64/NullInt64 (values ≤ 2^63-1 OK, above that an error like MySQL);
  - Scans into `any` (rowsToMaps/normalizeSQLValue, premium tables only) would yield a string where MySQL gives an int64. None of the 224 columns are read through those helpers (checked: they serve the premium/site tables);
  - InsertID RETURNING id → int64 is OK;
  - arithmetic such as next_value + 1 / series counters stays numeric and is scanned to int64;
  - planner work needed: a new opt-in BuildPlan option (`-unsigned-full-domain`), sequence defaults instead of identity, import setval, and FK type pairing. Not implemented in this batch.

## Numeric precision / division (app SQL audit)
- **Source:** 100+ decimal columns (32×decimal(10,2), 30×(18,4), 15×(5,2), …). They map 1:1 to numeric(p,s); float(10,2)/double map to real/double precision (2 doubles, 1 float). The float precision gate is unchanged.
- **Integer division:** MySQL `/` always yields a decimal; PG int/int truncates. A literal scan of all SQL strings with `/` found 15 PG sites:
  - fichaje (402/604/767/945, executor 1587): trunc(EXTRACT(EPOCH…)/60) matches TIMESTAMPDIFF(MINUTE);
  - fichaje_realtime 130/149 and labour_cost 298-300: EXTRACT is numeric, ROUND(minutes::numeric/60,4) and ROUND(gross/hours,6);
  - platform 1170-1171: ROUND(amount/12|3,6) on decimal;
  - stock 1490/1656/1748: decimal operands, ROUND(…,14);
  - stock_analytics 267: ROUND on decimal;
  - uazapi 742: numeric(18,4) casts, ROUND 8.

  All PG twins use numeric operands or an explicit trunc/ROUND, so **no unintended integer-division truncation was found**. The ROUND scales were chosen in earlier batches to match MySQL div_precision_increment (4) display. Exact equality with MySQL decimal results is still not runtime-verified (manual QA).
- **SUM/AVG:** PG SUM(bigint) → numeric and SUM(int) → bigint, scanned into int64/float64 (pgx text → convertAssign). MySQL SUM yields a decimal. Equivalent for values < 2^63; overflow is impossible here (cents columns are int).

## Changes
- No Go or SQL changes; the inventory is unchanged at r107 (2507).
- New: docs/dbmigrate/unsigned-narrowing-190.tsv.
- Build not rerun: no code changed since r107, whose build was OK.

## Remaining (not ready)
- range_narrowing 190: needs Option B to be implemented, or Option A proven by a probe.
- 136 ON UPDATE and 353 defaults: next, in the time batch.
- Collation (774 LIKE, 771 PAD data), enums, triggers, storage adapter and view review are open.
- 731 orphans (now concretely blocking the shadow FKs too).
- The checkout replay payload gate is still awaiting approval.
- Checkout 503; PG server disabled; 0 handler QA.
