# result-repair-vitess8

Model: claude-merge/claude-opus-5-5-acc2 (low).
- Source and build only. Nothing was measured or run against any database.
- No tests or Playwright, no SQL writes, no activation, no network.
- go.mod and go.sum are unchanged (cmp OK).
- `go build -p 2 ./...` OK.
- To compute the hash I compiled a throwaway hash-printer under cmd/ and `go run` it. Its source is moved out of the worktree to /tmp; it is not a test.

## 1. ON UPDATE helper ignores __wk (internal/dbmigrate/plan.go)
- `dbmigrate.on_update_ts` now also drops every `<col>__wk` attribute from OLD and NEW before the "real change" comparison:
  - the filter is `right(attname::text, 4) = '__wk'`, read from the catalog;
  - no new trigger args are needed;
  - this mirrors the existing generated-column exclusion.
- **Gate hash:** the ON UPDATE gate (internal/db/onupdate_gate.go) hashes `dbmigrate.OnUpdateFunctionSources()` at runtime, so its expectation follows the planner automatically. Values:
  - on_update_ts prosrc sha256: **old** 4e165e675d74d2fe96f06c6630da4e05ce5f387a3f5108d200417930b9830cce, **new** fb5d43068b75333aacee0291c3902e296fc5a4eb19ade7c3a968808108bde3eb;
  - on_update_mark: unchanged, 9c05aefd03115094f9febf12e21825eec47f7841e1a4e8611f532d003e6359e6.
- **Consequence for scratch** (not applied): the scratch copy still has the 10:22 function, so `pad-gate-check` `business.onupdate_triggers` will now FAIL with "differs from planner source". To restore it, apply `docs/dbmigrate/onupdate-wk-replace-vitess8.sql`, a single `CREATE OR REPLACE FUNCTION dbmigrate.on_update_ts`.
  - The 273 triggers reference the function by name, so they need no recreate.
  - The file is wrapped in BEGIN … ROLLBACK and needs your review.
- `docs/dbmigrate/forward-temporal-timeqa1.sql` (historical) still contains the old body. Future plan renders take the new body.
- `cmd/wk-backfill` still refuses tables with `onupd%` triggers. That check can be relaxed only after the replace is applied and the gate passes; not changed yet.

## 2. Writer triage: docs/dbmigrate/wk-writer-triage-vitess8.tsv (644 rows)
- Columns: file, line, table, verb, dialect, touches_key, key_cols.
- The classifier is lexical (a 10-line window, cut at the closing backtick) and heuristic:
  - **pg:** ON CONFLICT, COLLATE, ::, statement_timestamp, $n, quoted identifiers;
  - **mysql:** ON DUPLICATE KEY, NOW(), IFNULL, INSERT IGNORE, REPLACE, VALUES(col);
  - **portable:** the preceding `portableSQL(`;
  - **unknown:** otherwise.
- Of the vitess7 list, 7 of 651 lines did not parse as a registered table.

| dialect | touches key | no key |
|---|---|---|
| pg | 72 | 45 |
| mysql | 86 | 66 |
| portable | 95 | 147 |
| unknown | 45 | 88 |

- **portable/yes (95)** must be split into nativeSQL so a PG-only `__wk` variant can exist.
- **unknown (133)** needs manual reading.
- Lines classed "mysql" only need their PG twin, which is usually the next or adjacent pg row.

**Wired this batch** (flag VC_PG_WK_KEYS, off = identical SQL and args):
- `backoffice_uazapi_provisioning.go` provisioned insert (~l.650):
  - adds `instance_name__wk` to the column list and VALUES;
  - adds `instance_name__wk = EXCLUDED.instance_name__wk` to the ON CONFLICT (restaurant_id) update;
  - adds `ixwk__restaurant_uazapi_instances__uniq_restaurant_ua_96a63182` to the IsUniqueViolationOn names, which is harmless when off.
- `backoffice_uazapi_provisioning.go` legacy adopt insert (~l.800): `instance_name__wk` for `legacy-<rid>`.

Total writers wired: restaurant_domains.domain (4 statements) and restaurant_uazapi_instances.instance_name (2 statements). That is **2 of 149 columns**, so no column set is complete across all writers (UPDATEs of those columns: none found in the triage for these two).

Next clear PG batch (pg/yes INSERTs, listed in the TSV):
- site_builder_domain_mappings (instatic_manager.go:948, site_builder_registrar.go:303; needs domain__wk and id__wk);
- campaign_suppressions (6 sites; channel and target);
- restaurant_areas and restaurant_tables (premium.go:2161-2486);
- comida slug categories;
- the analytics keys.

## 3. LIKE sites (same flag)
New helper `wkLikeMatchIDs`:
- runs the tenant candidate query (id plus N text columns, no LIKE, no LIMIT);
- applies the MySQL-collation LIKE on each column (OR);
- returns an id set used as `AND id = ANY(?)`, so COUNT, ORDER BY and LIMIT/OFFSET stay in SQL;
- above the bound returns ErrTooBroad, mapped to 422 "Search too broad, please refine the filters".

Wired:
- **stock:** `backoffice_stock.go` item list q (name, sku, barcode; utf8mb4_unicode_ci PAD).
- **comida:** `comida.go` listPostres search (POSTRES.DESCRIPCION), with the caller mapping ErrTooBroad to 422.

Not wired:
- **admin menus** (`admin_menus.go:450`): the DIA and FINDE tables do not exist in the source schema; this is the "SOURCE ANOMALY" already noted in the code. There is no collation metadata to apply, and the source query always errors. Left unchanged.
- **assistant catalog** (`assistant_catalog_tools.go:575`): dynamic multi-table clause builder (`assistantLikeClause`). It needs a per-table candidate query; pending.

Known gaps:
- PG ORDER BY `i.name` still uses the ci_ai collation, not the MySQL weights. Sort parity is a separate gate (SortByKey or `__wk` ordering).
- `= ANY(int8[])` relies on pgx encoding `[]int64`. stock_items.id is unsigned bigint, NUMERIC in the -unsigned-full-domain plan, and the implicit int8→numeric cast applies there. Unverified at runtime.
- Bound `wkLikeMinBound` = 5000 is provisional (not measured).

Remaining LIKE sites:
- assistant catalog
- invoices 228-237, 782, 786
- POS 689; POS customers 105, 112; POS reservations 31
- checkcancel 126/139
- dish allergens 56
- AI usage 281
- technical sheet 378
- bookings search 152-162; bookings 368-375
- stock options 613
- group menus v2 2487
- comida 818 and 1111
- group_menus.go:940 (TitleJoin ready)

## Inventory r115
- 2532 sites = 2511 native (unchanged) + 21 non-native. The new non-native site is the wkLikeMatchIDs candidate query.

## Gates
- Scratch onupdate gate now FAILS until the reviewed replace is applied. This is expected.
- `__wk`: DDL unapplied; writers 2 of 149; LIKE 3 sites.
- Sort parity; provisional bounds.
- GPL/provenance still open.
- PG disabled, checkout 503, 0 handler QA, 1117 pending, 731 orphans, R5 untouched.
