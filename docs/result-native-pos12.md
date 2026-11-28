# result-native-pos12: invoices + pos_packs (44 sites)

Code only. go build -p 2 ./... exit 0. Runtime-verified 0. PG readyfalse, server refuses PG, MySQL default.

## Counts (inventory r67)
- **Repo:** 2475 = native 2171 (incl 5 provisional, none new) + blocked 3 + unwrapped 301; dynamic 63; audit failures 0.
- **Delta vs r66:** +44.
- **POS:** 380/564 ported, 184 left (21 dynamic), 31/35 files done.

## Work
- **backoffice_invoices (21, 4 dynamic):**
  - List and count are assembled from sqlFragment pieces and rebound once.
  - Enums/currency read as text; status filter compared as text.
  - LIKE uses lower-case C collation.
  - Create and update cast the is_reservation bool to smallint; create returns its id via InsertID inside the tx.
  - Reservation search reads reservation_time via to_char.
  - WA-send rows: insert via InsertID, COALESCE, PAD SPACE NULLIF; the claim UPDATE was already portable.
- **backoffice_pos_packs (23, 3 dynamic):** portable CRUD wrapped; pack/component ids via InsertID.

## New gates (unwaived, separate from 1117)
- Invalid date/time filter strings in the invoice list and reservation search: MySQL compares with a warning, while PG raises an error and the request returns 500. Filter-validation gate; source behaviour not changed.
- Invoice and booking LIKE search is case-insensitive only on PG; accent and PAD SPACE folding stay under the search gate.
- Pagination ties stay nondeterministic on both.
- Pack name order follows collation (collation gate); pos_packs.updated_at relies on the ON UPDATE trigger (136 gate).
- Prior gates remain open:
  - pos10/11: POSTRES sequence, first-invoice gap lock, replay/anomaly IGNORE, default display unit, row-lock timing.
  - 1117 import proof readyfalse; 4 NOT VALID FKs / 731 orphans.
  - Routing, UUID, SEO, multi-key, NUL/JSON, TIME, numeric, fiscal hash chain.
  - Guest-label/catalog-scope repairs pending dedicated QA; PIN columns dump-only.

## Next (4 large files, 184 sites)
- backoffice_premium 39 (16 dynamic)
- backoffice_pos 44 (3 dynamic)
- backoffice_pos_checkout 48
- backoffice_pos_rail 53 (2 dynamic)
- Each is its own checkpoint. Recommended next: premium 39.

