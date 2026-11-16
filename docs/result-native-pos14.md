# result-native-pos14 — backoffice_pos core (44 sites, 3 dynamic)

Code only. go build -p 2 ./... exit 0. Runtime-verified 0. PG readyfalse; the server refuses PG; MySQL stays the default.

## Counts (inventory r69)
- **Repo:** 2475 = native 2254 (5 provisional, none new) + blocked 3 + unwrapped 218; dynamic 44; audit failures 0.
- **Delta vs r68:** +44.
- **POS:** 463/564, 101 left (2 dynamic), 33/35 files done.
- **pos13 audit:** all queryOneAsMap/queryAllAsMaps callers pass raw static SQL (none wrap it in portableSQL), so there is no double rebind. The 49 variant literals sit behind 39 canonical sites; counts are coherent.

## Work
- **Ticket number sequence:**
  - The ODKU becomes ON CONFLICT on the PK with next_value+1. The LAST_INSERT_ID value was never read.
  - The catch-up MAX uses an explicit PG last-segment, leading-digits cast.
  - The LIKE uses lower-case C collation with posLowerLikeArg.
- **Duplicate-tolerant inserts in the transaction:** visit, line, pack parent and component inserts now use TxStatementScope + InsertID (RETURNING). The four ignored audit inserts also get savepoints, and JSON_OBJECT becomes typed jsonb_build_object.
- **Reads:**
  - Enums are read as text; TIME_FORMAT becomes to_char.
  - The tables screen casts EXISTS AS integer and puts display_order NULLS FIRST.
  - Visit status and acceptance_type filters compare as text.
  - Course labels use the PAD SPACE twin.
- **Writes:**
  - The settings upsert uses ON CONFLICT on the single PK.
  - Time columns: consumed_at is naive UTC; line voided_at uses timestamptz statement time.
- **UUID:** no SQL UUID() in this file, so nothing is blocked or provisional here.

## Gates (unwaived)
- **New in this file:**
  - **Ticket-number catch-up cast:** MySQL lenient UNSIGNED vs PG regex prefix cast differ on exotic values (sign, overflow, non-ASCII digits).
  - **Re-read after duplicate:** RR vs RC snapshot (existing crossaudit gate).
  - **Duplicate detection:** it matches on the message substring (source breadth kept).
  - **jsonb audit normalisation.**
  - **pos_pin_hash:** the column is dump-only missing; the source failure path is kept.
- **pos13 domain-register cross-tenant takeover:** a safety gate that must be resolved before activation. It is not accepted.
- **Carried over:** all prior pos10–13 gates, the 1117 import proof readyfalse, 4 NOT VALID FKs / 731 orphans, and the general collation/search/UUID/SEO/multikey/routing/NUL/TIME/numeric/fiscal-hash/136-onupdate gates.
- **Pending technical repairs (dedicated QA):** guest-label/catalog-scope. Parity is preserved for now.

## Next
- backoffice_pos_checkout 48 (one checkpoint).
- Then backoffice_pos_rail 53 (2 dynamic).

