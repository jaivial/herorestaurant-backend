# result-native-pos11: POS admin + stock_realtime (38 sites)

Code only. go build -p 2 ./... exit 0. Runtime-verified 0. PG readyfalse; the server refuses PG; MySQL stays the default.

## Counts (inventory r66)
- **Repo:** 2475 sites = native 2127 (incl 5 provisional, no new) + blocked 3 + unwrapped 345; dynamic 70; audit failures 0. Delta vs r65: +38.
- **POS:** 336/564 ported, 228 left (28 dynamic), 29/35 files done.

## Work
- **backoffice_pos_admin (13, 2 dynamic):**
  - Enums read as text.
  - The status filter compares as text, so an unknown status returns no match instead of a PG enum error.
  - card_last4 is read as text.
  - Health intervals are computed against naive UTC statement time.
  - The permissions upsert uses ON CONFLICT on the PK.
  - The anomalies list is composed from fragments and rebound once.
  - resolved_at uses timestamptz statement time.
- **backoffice_pos_stock_realtime (25):**
  - Level ensure becomes ON CONFLICT DO NOTHING, followed by FOR UPDATE.
  - Movement and snapshot ids come back via InsertID RETURNING.
  - applied_at and reversed_at use timestamptz statement time.
  - Ignored anomaly inserts and snapshot lookups run inside savepoints.
  - INSERT IGNORE becomes ON CONFLICT on the snapshot key.
  - The deduction source enum is read as text.

## New gates (unwaived, separate from 1117)
- **INSERT IGNORE into anomalies:** MySQL clamps an out-of-range quantity_after_base and inserts it; PG raises, the savepoint rolls back and the anomaly is skipped (IGNORE gate).
- **Default display unit:** LIMIT 1 without ORDER BY picks an arbitrary row when several defaults exist. Line and ticket restore order is undefined on both.
- **Health counters:** when SUM is NULL, the ignored scan error leaves both counters at 0 (source quirk, kept on both).
- **Float to numeric rounding** for stock quantities (numeric gate).
- **Level-row lock timing:** a no-op ODKU locks the row on MySQL, while PG locks it at the next FOR UPDATE. This is code-reviewed only.
- **Earlier gates stay open:** pos10 (POSTRES sequence, first-invoice gap lock, replay IGNORE), all prior gates, the 1117 import proof, and 4 NOT VALID FKs / 731 orphans.

## Next
- **Next checkpoint:** backoffice_invoices 21 (4 dynamic) + backoffice_pos_packs 23 (3 dynamic) = 44.
- **Then:** premium 39 (16 dynamic), pos 44, checkout 48, rail 53. Each is its own checkpoint, or split ~25.

