# result-repair-collation2

Model: claude-merge/claude-opus-5-5-acc2 (low).
- Docs only: no code, no SQL, no DB access, no installs, no network.
- No tests, vet or fixtures.
- R5 untouched, PG disabled, checkout 503.
- The build was not re-run because no code changed.

## 1. Coordinator evidence, 10:55 (authoritative)

**read-qa** on PG scratch, rid 1, date 2023-09-23. All six functions completed their query and Scan:
- affluenceDailyRows: 2 rows (bookings 9, covers 58)
- botDayCapacityOn: limit 45, total 55
- floors: 2 (active 1)
- booking extras: 6
- POS modifiers: 0
- stockBOM: 0

What this proves:
- The Scan path is verified only for the four functions that returned rows: affluence, capacity, floors and extras.
- The modifiers and stockBOM row Scans are UNVERIFIED, because they returned 0 rows.
- Not covered: HTTP, auth, writes, concurrency.
- Full handler QA stays at 0.

**Collation matrix.** MySQL 8.0.45 (local restore) vs PG17 ICU ci_ai. Columns are unicode_ci / general_ci / 0900_ai_ci / PG ci_ai:

| sample | unicode_ci | general_ci | 0900 | PG ci_ai |
|---|---|---|---|---|
| ASCII case, accent, ñ | 1 | 1 | 1 | true |
| ß=ss | 1 | 0 | 1 | true |
| ß=s | 0 | 1 | 0 | false |
| 'main '='main' | 1 | 1 | 0 | false raw (rtrim fixes) |
| tab trailing | 0 | 0 | 0 | false |
| NUL embedded = plain | 1 | 0 | 1 | n/a (text cannot hold NUL) |
| a+combining acute = á | 1 | 0 | 1 | true |
| æ=ae | 0 | 0 | 1 | TRUE |
| LIKE accent | 1 | 1 | 1 | ERROR (nondeterministic LIKE unsupported in PG17); "C" ILIKE false |
| LIKE trailing space | 0 | 0 | 0 | n/a |

**Conclusion:** ICU und primary strength approximates 0900_ai_ci. It does not match unicode_ci (æ differs) and it is furthest from general_ci (ß and combining marks differ). Source preservation therefore cannot accept ci_ai as final. My earlier "approximation" label was too weak: these are concrete mismatches.

## 2. Technical path: exact MySQL collation keys

Principle: equality, uniqueness and ordering must use MySQL's own weights for each column's source collation. LIKE must use MySQL's wildcard rules over those weights. Neither unaccent nor hand-written per-character replacement tables are acceptable; both are rejected as inexact.

### Option A (recommended): weight-key columns plus a Go collation library
1. **Stored data, at import.** Compute the key on the SOURCE engine with `WEIGHT_STRING(col)`, using each column's own collation. Import it into an added `<col>__wk bytea` column, so stored values get their keys from MySQL itself.
   - The raw text is kept byte-exact.
   - Unique indexes become `(scope…, <col>__wk)` with C/byte ordering. They replace the ci_ai rtrim expression indexes only after review.
   - Weight strings differ by collation, so per-column metadata (already exported: collation and PAD_ATTRIBUTE) selects the collation.
2. **New writes and lookups.** Compute the same key in Go with a library that implements MySQL's collation algorithms. The candidate is Vitess `go/mysql/collations` (vitess.io/vitess, Apache-2.0). From my knowledge — not verified in this worktree, because the module is not in the offline cache — it includes:
   - utf8mb4_general_ci, utf8mb4_unicode_ci (UCA 4.0.0) and utf8mb4_0900_ai_ci (UCA 9.0.0), generated from MySQL's own tables;
   - a `WeightString` API;
   - a `Wildcard` (LIKE) matcher that follows MySQL semantics, including PAD SPACE.

   Integration options:
   - Vendor only that package and its small internal dependencies, not all of Vitess, after a license, size and security review. The generated tables are several MB.
   - Pin an exact version. golang.org/x/text/collate is present in the cache but implements CLDR/DUCET, not MySQL's tables, so it is rejected for exactness.
3. **Readers.** Equality lookups bind the Go-computed key: `WHERE <col>__wk = $1`. ORDER BY on a collated column uses `__wk` (byte order equals MySQL weight order).
4. **LIKE.** PG cannot run MySQL wildcard-on-weights.
   - Bounded approach: a tenant-scoped PG prefilter (restaurant_id and other non-text predicates), then Go `Wildcard` matching over the candidate rows, then pagination in Go.
   - This works for the backoffice search tables, which are tenant-bounded (bookings search etc.).
   - Each LIKE site needs a size bound or it fails closed. A catalog of LIKE sites is required: from the earlier inventory, every `LIKE ?` with a text column.
5. **Proof (manual, read-only).** For every stored value, compare the import-time MySQL `WEIGHT_STRING` bytes with the Go library output (hash per column, counts only). Run the literal matrix through the Go library as well. Any mismatch blocks that column.

Known caveats:
- Changing a weight-key column is a schema change. The proposal is future-only and unapplied.
- Every writer must also maintain `__wk`: a Go writer helper, plus a PG trigger is impossible because PG has no MySQL weights. External SQL writers would therefore bypass it, and that stays gated.
- 0900_ai_ci is NO PAD while the others are PAD SPACE. The library handles this per collation; verify with the matrix (rows 'main ' and tab).

### Option B: native PG extension (C/Rust) implementing the MySQL collations
- It would port the same UCA tables into a PG type/collation provider or opclass. LIKE would be a custom operator.
- It is exact only if it is ported from the MySQL sources. MySQL is GPLv2, so license review is required. A Vitess-derived port (Apache-2.0) is the alternative source.
- It is large: a security surface inside the DB, plus build and deploy on every PG. It should be considered only if Option A's Go-side LIKE bounds fail.

### Option C: PostgreSQL 18
- PG18 adds LIKE for nondeterministic collations (release notes; not verified locally).
- It fixes the LIKE ERROR but still uses ICU weights, so the æ, ß and combining-mark mismatches remain.
- It is not a fix on its own. No upgrade; R5 is PG17.

### Not acceptable
- unaccent or lower as "exact".
- Invented character maps.
- Keeping MySQL as the runtime query engine (the target is all-PG runtime).

### Bounded prototype
Not implemented this batch. No reviewed MySQL-collation library is available offline (GOPROXY=off; vitess is absent from GOMODCACHE). Installing an unreviewed dependency or writing weights by hand would be ungrounded.

To unblock, the main agent or parent should:
- fetch and review a pinned vitess collations package (license, size, transitive deps, CVEs);
- then a bounded prototype: `internal/mysqlcoll` wrapper with Key(collation, s) and Like(collation, s, pattern), plus one column end to end (bookings search) with a manual read-only hash comparison.

## 3. Orphans: corrected evidence and preservation design (no execution)

Corrected MySQL counts (10:55):
- comments: 1 row, 1 missing invoice
- invoice_line_items: 726 rows referencing 245 missing invoices
- mappings 3 and page 1, all pointing to the same 1 missing site
- mapping status active/verified: 3
- mapping domain also in restaurant_domains: 1; overlap with live mappings: 0
- the orphan page is_home: 1
- all missing ids are below the current max

**Retraction.** My semantic1 claims about the cause ("FKs disabled / bulk load", "deleted vs never existed") are unsupported and withdrawn. Ids below the max prove nothing about deletion or authorship. Owner: UNKNOWN.

**Withdrawn recommendation.** Quarantine is not recommended as-is: moving the 3 mappings would release domain reservations (3 active/verified, 1 also in restaurant_domains). Required design before any move:
1. **Keep all rows and reservations in place** until approved.
2. **Domain reservation tombstones.** A `dbmigrate_domain_reservations` denylist holds the 3 orphan mapping domains, using their PAD/collation key. It records provenance (source table and PK, row sha256 of the original bytes, R5 hash) and owner=UNKNOWN. Every domain claim path (the 5 global keys / 32 callers patched earlier) must check it and fail closed. The denylist must exist BEFORE any archival move.
3. **Archival visibility.**
   - The archive tables use exact original column types and bytes, plus provenance and a per-row sha256 over the original bytes, verified against R5 and the MySQL source.
   - They are visible to admins only, read-only, and never assigned to any restaurant: there is no tenant column and the owner is unknown.
   - Restore is a byte-exact INSERT…SELECT only if a provably matching parent is recovered.
4. Live FKs become VALID only after 1 to 3, and only with user approval.
5. **No invented parents, no waiver, no deletion.**

Blocker: this needs user approval. The parent has not asked the user yet. Other work is not blocked.

**Older backups that could restore parents.** The worker listed only filenames and table-DDL presence; no row data was read:
- `~/Documents/mariacv/alqueriavillacarmen_backup_20260214_224040/villacarmen_20260214_224040.sql` (6.0 MB, 2026-02-14): none of the 6 tables (legacy schema).
- `~/projects/newvillacarmen/villacarmen_backup/villacarmen_2026021{5,6}_*.sql.gz` (7 files, about 5.0 MB, Feb 2026): sampled the 2026-02-16 18:00 file; none of the 6 tables.
- `~/newvillacarmen/.dev-dump.sql.gz` (1.07 MB, 2026-10-21): contains all 6 tables. Provenance is unknown (a "dev" dump could be non-production data). It is the only candidate.

Read-only recovery check, for the coordinator only:
1. Restore .dev-dump into a NEW isolated scratch MySQL, network none, never into the source.
2. Run COUNT-only and hash-only queries; no values are printed. Pass the missing ids from the source as a temp list produced by the coordinator inside scratch:
   - `SELECT COUNT(*) FROM invoices WHERE id IN (<245 ids>)` and, for site_builder_sites, `id IN (<1 id>)`.
   - Children identity: `SELECT COUNT(*) FROM invoice_line_items WHERE SHA2(CONCAT_WS(0x1f,id,invoice_id,description,quantity,unit_price,iva_rate,iva_amount,line_total,sort_order,created_at),256) IN (<source child hashes>)`, with the same expression computed on the source.
   - The 3 mappings and 1 page: the same hash comparison over all columns.
   - A recovered parent is acceptable only if its children hash-match exactly and the parent row includes restaurant_id. Even then, restoring is a user-approved write, not automatic.

## Gates still open
- Collation: needs Option A review. ci_ai is wrong for unicode/general columns.
- LIKE on PG errors.
- 731 orphans: blocked on approval of the reservation-tombstone + archive design.
- 1117 pending; 0 full handler QA; 2 read-qa functions with unverified Scans.
- Numeric/external enum input; replay payload; unsigned generator range.
- PG disabled, checkout 503, R5 untouched.

## Next
1. Review and fetch the pinned Vitess collations package.
2. `internal/mysqlcoll` prototype with one column and a manual hash comparison.
3. A LIKE-site catalog.
4. Approval of the orphan design, then the dev-dump recovery check on scratch.
Order: full PG verified, then the functional bot, then GraphRAG. Production read-only.
