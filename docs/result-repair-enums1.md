# Result: repair-enums1 (ENUM/generated compatibility audit, ON UPDATE explicit-write fix, storage-override provenance; code-only; nothing applied; NOT ready)

## Storage override provenance (found, not new)
- **File:** docs/dbmigrate/storage-overrides.json, sha256 b58da14e49d8…, identical to the sha recorded in orq result-stage1-nul-fix.md, and to R4 plan.json storage_overrides {assistant_messages.content: bytea}.
- **Reason:** 1 source row holds a NUL byte (full-source-nul-inventory.json). The byte preservation was already verified.
- **Correction:** the time1 renders omitted this override, so their counts (775 like / 772 pad and no storage_app_adapter) were wrong. The earlier proof is not discarded. The renders are regenerated below with the reviewed override; no archive or override was edited.

## Future plan renders (local only; derived local metadata + reviewed view and storage overrides; R5 untouched)
Paths: /tmp/villacarmen-bot-20261111/future-plan-enums1-{r5compat,fulldomain}

| render | pending keys | categories | triggers |
|---|---|---|---|
| r5compat | 1116 | like_nondet 774, pad_space 771, range_narrowing 190, on_update_explicit_marked_qa 136, storage_app_adapter 1, view_review 1 | 273 |
| fulldomain | 1017 | like 774, pad 771, unsigned_generator_range 91, on_update_explicit_marked_qa 136, storage 1, view 1 | 273 |

- 273 triggers = 136 refresh triggers + 137 per-column mark triggers.
- The counts now match the original R4 categories (774/771/190/136/1/1).

## ON UPDATE (internal/dbmigrate/plan.go; source fix, QA pending)
- **Generated columns:** helperOnUpdate excluded only the generated columns passed as args. Per the PG manual (Generated Columns), stored generated columns are computed after BEFORE triggers and cannot be read reliably there. A future shadow `__pad` column, not listed in the args, would therefore look changed on every UPDATE, a false refresh.
  - **Fix:** the function now excludes every attgenerated column of TG_RELID by catalog lookup. Comparisons are deterministic over base columns only. The "-col" args are still accepted.
- **Explicit same-value assignment** (the MySQL contract: an explicit assignment, even to the same value, keeps the value):
  - **Mechanism:** PG `BEFORE UPDATE OF col` fires whenever col is a SET target, even with an unchanged value (CREATE TRIGGER docs). The plan now emits a per-column mark trigger `onupc__<t>__<col>` → dbmigrate.on_update_mark('col'). It appends "relid:col" to the transaction-local setting `dbmigrate.onupd_explicit` (set_config(…, true)).
  - **Ordering:** PG fires same-event row triggers in name order for each row, and "onupc…" sorts before "onupd…".
  - **Refresh side:** the refresh trigger reads and immediately clears the setting, then skips refreshing any marked column. Nothing carries over to the next row or statement, and the setting is per transaction (pool-safe; reset at commit/rollback).
  - **Effect:** external writers get the MySQL behaviour too.
  - **Status:** the pending label is now `on_update_explicit_marked_qa` (136). It is not closed: manual scratch QA is needed for trigger ordering, multi-row UPDATEs, UPDATE … FROM and the fsp truncation.
- **Forward migration onto an imported DB (not written):** replace on_update_ts, add on_update_mark, and drop and recreate all 136 + 137 triggers in one transaction. The old 2-part args fail closed.

## ENUM audit (source schema; counts exact)
- **113 ENUM columns, 103 distinct label sets** (the plan creates 113 PG enum types, one per column; quoted names enum__<t>__<c>).
  - Collations: 112 utf8mb4_unicode_ci and 1 utf8mb4_0900_ai_ci.
  - 74 have mixed/upper-case labels.
  - No labels have edge spaces, are numeric-like, or are empty strings.
  - 41 are part of an index, 74 have defaults, 13 are nullable.

| MySQL (strict mode, source) | PG native enum |
|---|---|
| input matched case-insensitively under the column collation; stored as the canonical label | exact, case-sensitive label, else error 22P02 |
| trailing spaces in the input are ignored (PAD SPACE) | error |
| numeric input k → the k-th label | error |
| '' / an invalid label: error in strict mode | error |
| ORDER BY is by declaration index | same (declaration order) |
| comparison with a string literal under the collation (ci) | text comparison needs `CAST(col AS text)`; equality with a non-canonical literal fails or is false |

- **Production SQL mode** (metadata) lacks ONLY_FULL_GROUP_BY; the local restored source has it. That difference does not affect enum mechanics.
- **App side:** every ported PG read already uses CAST(col AS text) (304 cast sites in internal/api). Writers in the ported handlers validate against Go allowlists (validPOSPaymentMethod, validMarginScopeKind, ToUpper normalizers, …), which emit canonical labels.
- **Not proven by source:** that every writer, including external and legacy writers, sends canonical labels. Non-canonical input that MySQL accepts would error on PG.
- **No caller fix this batch.** I found no ported writer that sends a client value to an enum unnormalized and that is "already required" to validate. A per-writer audit of all enum writers is still open, and no Go EqualFold was added.
- **Global adapter design (not implemented):** an optional plan mode making each enum column `text COLLATE dbmigrate.ci_ai` plus a CHECK over the label set, with a BEFORE INSERT/UPDATE trigger that canonicalizes input (rtrim + ci_ai match → canonical label, or an error mirroring strict mode) and ORDER BY via an `array_position` expression.
  - It preserves all legal source inputs.
  - It costs native enum ordering and type safety and needs ORDER BY rewrites, so it needs review. Gated.

## Generated columns
- **5 stored generated columns** (plan generated=5). Two are scope_key on restaurant_floors/restaurant_salons and one is pos_shifts.open_terminal_key; all are translated by TranslateExpr.
- They are excluded from the on-update comparison by catalog lookup (above).
- Their expression parity (DATE_FORMAT / IFNULL / CASE) is still manual-QA gated.

## Build / inventory
- go build -p 2 ./... OK.
- Inventory r108: 2507 (no app SQL change), all native.

## Gates (not claimed)
- 136 on-update (now marked-QA), 190 narrowing / 91 generator, 774 like and 771 pad collation, the enum input adapter, generated-expression QA, the storage adapter, view review.
- 731 orphans; checkout replay payload (awaiting approval).
- R5 untouched; PG server disabled; checkout 503; 0 handler QA; no worker SQL.
