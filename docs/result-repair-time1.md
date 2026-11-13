# Result: repair-time1 (temporal defaults / ON UPDATE / unsigned full-domain planner; code-only; nothing applied; NOT ready)

## Source facts (full-export schema.json)
- **1006 column defaults:**
  - **353 are clock defaults** (DEFAULT_GENERATED):
    - 168 timestamp CURRENT_TIMESTAMP;
    - 119 timestamp CURRENT_TIMESTAMP ON UPDATE;
    - 47 datetime CURRENT_TIMESTAMP;
    - 17 datetime CURRENT_TIMESTAMP ON UPDATE;
    - 2 datetime(3) CURRENT_TIMESTAMP(3).
  - **653 are literals:** 197 tinyint, 134 varchar, 134 int, 74 enum, 60 bigint, 46 decimal, 3 char, 2 double, 2 smallint, 1 time. These are unchanged (TranslateDefault literal path).
- **ON UPDATE:** 137 columns on 136 tables. One is a timestamp ON UPDATE whose default is NULL.
- **Type mapping (unchanged):** timestamp → timestamptz(fsp), datetime → timestamp(fsp) naive UTC wall clock. Imported values are byte-identical (R5 untouched, no time-zone reinterpretation).

## Source fixes (internal/dbmigrate; future plans only)
1. **Clock defaults (types.go TranslateDefault + new pgClockExpr):**
   - MySQL CURRENT_TIMESTAMP(fsp) is the statement start truncated to fsp.
   - The old plan used PG CURRENT_TIMESTAMP, which is the transaction start; a timestamp(0) column ROUNDS it, so a default could land up to 0.5 s in the future.
   - The plan now emits `date_trunc('second'|'milliseconds', statement_timestamp())` (fsp 0/3; fsp 6 untruncated), with `AT TIME ZONE 'UTC'` for naive datetime. Any other fsp is refused (none exist in the source).
2. **ON UPDATE trigger (plan.go helperOnUpdate):**
   - The value is now statement time truncated to the column fsp; it was transaction now(), then rounded.
   - Args are "col:kind:fsp"; the planner refuses an unsupported fsp.
   - The function RAISEs on old 2-part args, so a forward migration must replace the function AND recreate all 136 triggers together. It fails closed if applied partially.
   - The existing semantics are kept: refresh only when another non-generated column really changed and the UPDATE left the column itself unchanged.
3. **Unsigned full domain (opt-in, PlanOptions{UnsignedFullDomain}, flag `-unsigned-full-domain`; zero value = R5-compatible plan):**
   - All 224 bigint unsigned columns become numeric(20,0) CHECK 0..2^64-1. FK pairs are numeric on both sides; there are no signed/unsigned FK mismatches in the source.
   - The 91 AUTO_INCREMENT unsigned ids get `CREATE SEQUENCE seq__t__c AS bigint MINVALUE 1` plus `DEFAULT nextval(…)` plus `ALTER SEQUENCE … OWNED BY`, so the import's setval(pg_get_serial_sequence) keeps working.
   - Pending becomes `unsigned_generator_range`, 91 entries: the generator stops at 2^63-1 and the app reads ids as int64.
   - A numeric-counter generator table for the full 2^64 range is NOT coded (design only, pending review).

## Future plan outputs (local files; no DB; R5 plan/export/proof untouched)
- **Locations:** /tmp/villacarmen-bot-20261111/future-plan-time1-r5compat and future-plan-time1-fulldomain, rendered from the DERIVED local-metadata schema.
- **Storage override:** the R4 storage-override JSON (assistant_messages bytea) was not found in the work dirs, so these renders lack it. Regenerate with the reviewed override before use.

| render | pending keys | categories |
|---|---|---|
| r5compat | 1116 | like_nondet 775, pad_space 772, range_narrowing 190, on_update_same_value_gap 136, view_review 1 |
| fulldomain | 1017 | like_nondet 775, pad_space 772, unsigned_generator_range 91, on_update_same_value_gap 136, view_review 1 |

- **Storage narrowing is closed in the fulldomain render** (190 → 0). The 91 generator limits stay pending; the 99 FK-linked columns are full-domain storage with app int64 reads.
- 354 statement_timestamp clock expressions are in r5compat (353 defaults + the helper); fulldomain has 91 nextval defaults.

## ON UPDATE same-value gap: caller catalogue (all app UPDATEs on the 136 tables; literal scan)
| pattern | count | PG trigger result vs MySQL |
|---|---|---|
| column omitted from SET | 373 | refreshed iff another column changed (same as MySQL) |
| column set to a clock expression (NOW()/date_trunc(statement_timestamp)) | 102 | NEW ≠ OLD → kept (same) |
| column set to a bound Go time (site_builder.go:1560/1562/1840/1844, time.Now) | 4 | kept; equal to OLD only if the same truncated second, where the refresh gives the same second (same) |
| column set to its own current value (`col = col` / bound old value) | 0 | (the gap case) |

- No app writer hits the gap.
- External and legacy writers are not catalogued: the PG trigger cannot tell an explicit same-value assignment from an omitted one. That would need a session flag (e.g. `SET LOCAL dbmigrate.keep_ts`) honoured by the trigger; this is design only.
- The 136 pending entries stay open. Not marked closed.

## Concurrency / clock notes
- Session TimeZone is pinned to UTC (sqldb). App SQL already uses date_trunc('second', statement_timestamp()) on PG (earlier batches).
- DST does not apply: naive values are UTC wall clock.
- FOR UPDATE flows are unchanged.

## Build / inventory
- go build -p 2 ./... OK.
- Inventory r108: 2507 (planner text is not app SQL), all native.

## Gates
- **Still pending:** 136 ON UPDATE gap (external writers); 91 generator range; like/pad collation; view review; storage override regeneration; 731 orphans (blocking the shadow FKs); triggers, enums and the remaining time QA; the checkout replay payload (awaiting approval).
- **Not applied:** the forward migration of the defaults and triggers onto an already-imported DB is designed here, not written or applied.
- **Unchanged:** R5 untouched; PG server disabled; checkout 503; 0 handler QA.
