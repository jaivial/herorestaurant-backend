# result-repair-vitess4

Model: claude-merge/claude-opus-5-5-acc2 (low).
- Local development only. Nothing is wired into runtime or activated; production stays read-only.
- The GPL question on the weight tables is open with Jaime.
- No network, no SQL run, no tests, fixtures or Playwright.
- go.mod and go.sum are byte-identical before and after (cmp OK). Builds ran with GOFLAGS=-mod=readonly, GOPROXY=off and GOTOOLCHAIN=local.

## 1. Vendored minimal subtree: internal/third_party/vitess (6.8 MB, 67 files)
- Source: the local modcache, vitess.io/vitess@v0.23.5, commit 1e30dbc6658b7078eda58dd79969ce1f76cf132d, h1:hdJlWjs1le0mR5aD3okgZAd/8X6UCkfguNlIuOAFGos=.
- Packages:
  - go/hack
  - go/mysql/collations
  - …/colldata
  - …/internal/uca
  - …/charset, plus types, unicode, eightbit, japanese, korean and simplifiedchinese
  - go/vt/vthash, plus highway and metro
- Not copied: tests, `colldata/fuzz.go` (gofuzz build tag) and every other Vitess package.
- **The cut:** the only sqltypes edge was `collations/local.go` `CollationForType`, which I removed. `MySQL8()` is kept. Nothing else was modified beyond import paths. The protobuf, grpc, glog, pflag and sqltypes dependencies are gone.
- External imports that remain: golang.org/x/text (korean and simplifiedchinese charsets) and golang.org/x/sys/cpu (highway). Both were already in go.mod as indirect at v0.41.0 and v0.47.0. go.mod was NOT edited, so the "// indirect" markers are now stale, but the build under -mod=readonly passes.
- Licence obligations:
  - root `LICENSE` (Apache-2.0) copied;
  - `vt/vthash/highway/LICENSE` kept;
  - upstream has no NOTICE;
  - all original headers kept;
  - 29 files whose imports or content changed carry "MODIFIED by villacarmen (repair-vitess4) … import paths rewritten" (local.go also notes the removal);
  - `README.vendored.md` records the provenance and the open GPL note.
- `hack` uses unsafe and linkname runtime tricks (upstream design). Noted for security review; not changed.

## 2. internal/mysqlcoll (mysqlcoll_v1), as designed in vitess3
- `New(name, padSpace)`: utf8mb4_* only; an unknown collation gives ErrUnknown; PAD SPACE requires a non-empty SPACE weight.
- `Key`: naked WeightString, minus trailing whole SPACE-weight units when PAD.
- `Equal` / `Compare`: key bytes. The library Collate is never used.
- `Like(pattern)` returns a Matcher: Wildcard('_', '%', '\\') on the original bytes.
- `SpaceWeight`.
- Every entry point checks utf8.Valid (ErrInvalidUTF8) and recovers library panics as errors.
- There are no runtime callers yet.

## 3. cmd/collkey-compare (collkey_compare_v1)
Built only; NOT run. Binary: /tmp/villacarmen-bot-20261111/collkey-compare, 19 MB.

Inputs:
- Env `CKC_MYSQL_DSN` (required) and `CKC_PG_DSN` (optional). Neither is ever printed; open errors print a generic message only.
- `-columns` file, lines: `table column pk collation PAD|NOPAD [tenant_col]`.
- Identifiers must match `^[A-Za-z_][A-Za-z0-9_]{0,63}$`, otherwise it refuses. Single-column PK only.

Read-only enforcement:
- MySQL: a single connection and `START TRANSACTION READ ONLY` (BeginTx ReadOnly). It requires `@@session.transaction_read_only=1`; if that is 0 it sets SESSION READ ONLY (session-scoped, not a data write), re-checks, and refuses otherwise.
- PG: `OpenPostgresReadOnly`, a single connection, a READ ONLY tx, and refusal unless `SHOW transaction_read_only` = on.
- Both transactions are rolled back.

Output per column, JSON counts only (no values, keys or PKs):
- rows, nulls, invalid_utf8;
- `key_vs_mysql_weight_mismatch`: the Go key vs MySQL WEIGHT_STRING after the same SPACE strip;
- `raw_weight_equal`;
- `distinct_go_key` vs `distinct_mysql_eq` (COUNT(DISTINCT) under the source collation);
- `max_key_len`;
- `max_rows_per_tenant`;
- PG: `pg_rows`, `pg_missing` (MySQL non-NULL rows absent or NULL in PG), `pg_extra_nonnull`, `pg_raw_bytes_mismatch` (sha256 of the raw value per PK; PG `::text`, MySQL raw bytes).

Known limits:
- PK matching uses `CAST(pk AS CHAR)` vs `pk::text`. That is fine for int and varchar PKs; other types are unverified.
- Hashes are held in memory per column (about 24k rows max in the current data).
- Composite PKs are not supported; it refuses by design.

SQL inventory r113: 2521 sites = the 2511 native sites unchanged, plus 10 new non-native sites (4 static, 6 dynamic). I attribute them to this manual diagnostic, which has separate MySQL and PG connections by design and is never part of the app runtime. The inventory summary has no per-file breakdown, so this attribution is not confirmed; it is not marked native.

Build: `go build -p 2 ./...` OK; gofmt clean.

## 4. LIKE design, continued
**group_menus.go:940 (join predicate).** The source is a LEFT JOIN comida_items ci ON tenant AND source_type='platos' AND (`ci.nombre = d.title_snapshot` OR `LOWER(d.title_snapshot)=LOWER(ci.nombre)` OR `d.title_snapshot LIKE CONCAT(ci.nombre,' (+%')`).
- Source quirk to preserve: `%` and `_` inside ci.nombre act as wildcards.

Proposed PG design (not implemented):
1. Query 1: the dishes, exactly as now, without the ci join.
2. Query 2: all `platos` comida_items for the tenant.
3. A Go join that, for each dish, evaluates the three predicates using mysqlcoll on the source column collations:
   - `=` → Equal;
   - LOWER=LOWER → Equal on lowered bytes. For ci collations this is the same as Equal; the LOWER is kept for exactness;
   - LIKE → `Like(nombre + " (+%")` with nombre unescaped, which preserves the wildcard quirk.
4. LEFT JOIN multiplicity: a dish with N matches yields N rows, and a dish with none yields one row with NULL ci fields.
5. Order: the source ORDER BY on d keys. Ties between ci matches are engine-arbitrary in the source; keep ci id order and document it.
6. Bound: the platos count per tenant must be ≤ the measured bound, otherwise explicit failure.

**Size-limit method (technical, no policy):**
- Bound per site = ceil(4 × max_rows_per_tenant measured by collkey-compare on the source), clamped to the per-request memory budget of the table's row width.
- Above the bound the endpoint returns an explicit error ("search too broad, refine filters") and never truncates. Count and pagination are computed in Go on the full filtered set.
- Re-measure on each import.

**REGEXP / LOCATE / INSTR audit (lexical, non-test Go):**
- `campaign_email_guard.go:329-333`: MySQL REGEXP (ICU, case-insensitive under the column collation) on `diagnostic`, with ASCII patterns. The PG side is `LOWER(diagnostic) COLLATE "C" ~`.
  - Gap: non-ASCII case or accent folding in the diagnostic text, for example an accented letter in provider text, and ICU vs POSIX regex dialect (`{0,1}` was already ported).
  - Gated as "approximate, ASCII patterns".
  - Candidate fix: Go `regexp` over mysqlcoll-folded text is still not ICU, so it stays an approximation.
- `backoffice_bookings.go:943`: a PG-only format regex on reservation_time, ASCII digits only, with no collation semantics. OK.
- LOCATE, INSTR, POSITION, FIND_IN_SET, strpos and SIMILAR TO: none in non-test Go, apart from the inventory scanner's own patterns.

## Gates still open
- GPL/provenance decision (owner).
- collkey-compare runs (coordinator) and per-column results.
- `__wk` future DDL plus writer helper.
- LIKE post-filter implementation (group A sites and the 940 join).
- REGEXP approximation.
- 731 orphans (approval).
- 1117 pending; 0 handler QA; 2 read-qa Scans unverified.
- Enum numeric input; replay payload; unsigned generator range.
- PG disabled; checkout 503; R5 untouched.
Order: full PG verified, then the functional bot, then GraphRAG. Production read-only.

## Manual command (coordinator; scratch and local source only)
`CKC_MYSQL_DSN=<env> CKC_PG_DSN=<env> /tmp/villacarmen-bot-20261111/collkey-compare -columns cols.txt`

Example lines; take collation and PAD from the source metadata sidecar, do not copy these blindly:
```
bookings customer_name id utf8mb4_general_ci PAD restaurant_id
restaurant_domains domain id utf8mb4_unicode_ci PAD
```
