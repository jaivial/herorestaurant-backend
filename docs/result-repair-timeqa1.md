# Result: repair-timeqa1 (manual scratch QA SQL, forward temporal DDL, mark-scheme safety, first 10 enum writers; code-only; nothing executed)

## Mark-scheme safety (read from the actual helper code; internal/dbmigrate/plan.go)
- **Triggers in play:** the source has 0 triggers, routines and events (schema.json other_objects). The plan creates only onupc__<t>__<col> (BEFORE UPDATE OF col → on_update_mark) and onupd__<t> (BEFORE UPDATE → on_update_ts). RI constraint triggers are AFTER triggers, so they never run between them.
- **Order:** for each row, PG fires same-event BEFORE ROW triggers in name order. "onupc…" < "onupd…", and PGName's hash suffix keeps the prefix. So mark(row) → refresh(row), then the next row.
- **Refresh reads then clears** `dbmigrate.onupd_explicit` (set_config(…, true): transaction-local; reverted by ROLLBACK and ROLLBACK TO SAVEPOINT, per GUC semantics).
- **Leak cases (exact):**
  - **(a) A foreign BEFORE UPDATE row trigger** on the same table, sorting between onupc and onupd, that returns NULL (row skipped) or runs nested DML. The mark of the skipped row is not consumed and could suppress one refresh of the next row of the SAME table. A mark of ANOTHER table is now detected and RAISEd (new fail-closed check). Source has none; any future trigger must be reviewed. Manual QA step 0 lists the triggers.
  - **(b) An error after the mark:** the statement aborts. With a savepoint, the GUC reverts; without one, the transaction is aborted. No leak.
  - **(c) Nested UPDATE from inside our triggers:** none; the functions run no DML.
  - **(d) UPDATE … FROM with duplicate join rows:** PG updates each target row once, so the triggers fire once per row.
- **Generated columns** (including future __pad shadows) are excluded via pg_attribute.attgenerated. to_jsonb(NEW) is built before that removal, and the removal happens before comparison. No false change.
- **Not proven:** a per-row key is not used (ctid/PK are not robustly available to the mark). Safety relies on (a) staying true. That is a readiness requirement, not a proof.

## Forward temporal migration (docs/dbmigrate/forward-temporal-timeqa1.sql; NOT executed)
- **One transaction** on an R5-derived PG copy (business schema):
  - CREATE OR REPLACE dbmigrate.on_update_ts plus dbmigrate.on_update_mark (from the derived render future-plan-timeqa1-r5compat: schema.json + reviewed storage-overrides.json sha b58da14e…);
  - DROP the 136 R4 triggers (`onupd__<t>`, old 2-part args, which the new function would RAISE on);
  - CREATE 273 (136 refresh + 137 mark);
  - 353 `ALTER COLUMN … SET DEFAULT` with statement_timestamp truncated to fsp (287 timestamptz + 66 naive `AT TIME ZONE 'UTC'`).
- Existing stored values are unchanged.
- **Optional numeric storage** (unsigned full domain) is NOT included. It needs identity drop, sequence creation, FK-pair ALTER TYPE and a re-validation proposal, kept separate (design in repair-time1).
- Original R5 schema, proofs and archived plans are untouched.

## Manual scratch QA (docs/dbmigrate/manual-qa-timeqa1.sql; coordinator only; plain SQL, human-read; no assertions, DO blocks or fixtures; ends in ROLLBACK)
- **Setup inside one transaction:** both functions and the trigger swap for restaurant_areas and pos_visits only.
- **Steps:**
  - 0: trigger list and order;
  - 1: no-op (expect unchanged);
  - 2: real change of another column (expect the statement second);
  - 3: explicit same timestamp plus another change (expect unchanged, the MySQL contract);
  - 4: explicit different timestamp (expect the exact value);
  - 5: multi-row real change;
  - 6: multi-row explicit (per-row marks);
  - 7: UPDATE … FROM with duplicate join rows;
  - 8: a table with a generated column (pos_visits.open_table_id), no-op vs real change;
  - 9: savepoint rollback of a mark.
- `pg_sleep(1.2)` between steps; every step prints updated_at, statement_timestamp() and mark_left.
- Only existing rows are touched (bg_color, covers) and everything is rolled back.

## ENUM writers: first 10 highest client-input-risk columns (exact source; others NOT audited)
| enum column (labels) | writer(s) | entry normalization | PG-native risk | action |
|---|---|---|---|---|
| pos_payments.method (CASH,CARD,BIZUM,BANK,OTHER) | checkout.go:362; assistant_pos_tools.go:83 | validPOSPaymentMethod (ToUpper+TrimSpace) but the RAW value was bound; the assistant had no validator | 'cash' / 'CASH ' stored by MySQL, error on PG | FIXED: bind mysqlUpperEnumArg (ToUpper + TrimRight " ", the label MySQL stores; leading spaces still fail on both) |
| pos_refunds.payment_method (same) | checkout.go:706; assistant:92 | ToUpper only (checkout); raw (assistant) | trailing-space input | FIXED: mysqlUpperEnumArg |
| stock_movements.waste_reason (8 upper labels) | stock.go:1460 | validStockWasteReason(ToUpper+TrimSpace); bound stockNullableString(raw) | lower-case input | FIXED: stockNullableString(ToUpper(...)) (trim already done) |
| stock_movements.type | stock.go movement paths | validStockMovementType exact canonical | none (non-canonical is rejected in Go on both) | none |
| pos_visits.channel (DINE_IN…) | pos.go:781 | ToUpper(TrimSpace), default DINE_IN | none (canonical) | none |
| pos_cash_movements.movement_type | pos_cash.go | validPOSMode exact IN/OUT | none | none |
| pos_cash_closures.closure_type | pos_cash.go:472 | validPOSMode exact X/Y/Z | none | none |
| stock_margin_scopes.scope_kind | margin_scopes.go:158 | ToUpper(TrimSpace) + allowlist | none | none |
| restaurant_tables.shape (round,square) | premium.go:2362/2585 | normalizePremiumTableShape (lower, default round) | none | none |
| pos_visits.service_type (LUNCH,DINNER,OTHER) | pos.go (moment.ServiceType), assistant 'OTHER' literal | server-derived | unverified path (moment derivation) | gate |

- 103 of 113 enum columns are not audited this batch.
- **Not handled by any caller:** MySQL numeric ordinal input (k → k-th label) and external writers.
- **Native adapter plan (keeps native types and ordering):** a per-enum-type canonicalization function `dbmigrate.enum_in_<type>(text)` (rtrim + ci_ai match to the label, or numeric ordinal → enum_range[k], else raise like strict mode), used by app binds where input is not canonical, plus an optional BEFORE INSERT/UPDATE trigger for external writers on reviewed columns. Design only.

## Code changes
- internal/dbmigrate/plan.go: the helper's cross-table stale-mark RAISE and the safety comment.
- internal/api/sql_native.go: mysqlUpperEnumArg.
- Enum binds: backoffice_pos_checkout.go (:362, :706), assistant_pos_tools.go (:83, :92), backoffice_stock.go (:1460). MySQL results are identical to the source's stored canonical labels.

## Build / inventory
- go build -p 2 ./... OK.
- Inventory r109: 2507, all native (bind args only).

## Gates (not claimed)
- **Manual QA not run:** the mark scheme and the forward DDL.
- **Enum:** 103 columns unaudited, plus the ordinal/external gap.
- **Pending:** 136 marked-QA; 190 narrowing / 91 generator; 774 like / 771 pad; storage 1; view 1.
- 731 orphans; checkout replay payload (awaiting approval).
- R5 untouched; PG server disabled; checkout 503; 0 handler QA; no worker SQL.
