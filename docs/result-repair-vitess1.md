# result-repair-vitess1

Model: claude-merge/claude-opus-5-5-acc2 (low).
- The application go.mod, go.sum and runtime are unchanged; no app dependency was added.
- The worker ran no SQL, network or providers. No tests, fixtures or assertions.
- R5 untouched, PG disabled, checkout 503.

## Done
- docs/dbmigrate/vitess-review.md holds the grounded URLs, the pinned candidate (v0.23.5), Go compatibility, license, the security caveat, and the semantics to review.
- I wrote the manual diagnostic CLI source in the ISOLATED dir `/tmp/villacarmen-vitess-review-20261112/diag`:
  - `main.go` (`vcolldiag`): reads a samples FILE in JSONL with hex inputs, so NUL and invalid bytes survive. Per line it prints the naked WeightString key as hex plus sha256 for a and b, `keys_equal`, `collate_equal` (Collate oracle), and an optional `a_like_pat` (Wildcard Match, escape \\). A panic is caught and reported. Nothing is decoded for display. No SQL or network.
  - `go.mod`: module vcollreview, go 1.25, require vitess.io/vitess v0.23.5.
  - `samples.jsonl`: 10 literal codepoint samples matching the 10:55 matrix: PAD space, tab, ß=s/ss, æ, combining acute, NUL, LIKE accent, 0900 NO PAD, invalid byte 0xFF. These are not user data.

## Blocked
- The sandbox has no network, so the module download failed (DNS socket not permitted). As a result:
  - The CLI is NOT built.
  - Its API calls are unverified against v0.23.5: `collations.MySQL8().LookupByName`, `colldata.Lookup`, `WeightString(nil,src,0)`, `Collate`, `Wildcard(...).Match`.
  - The deps list, sizes, go.sum hash and vulnerability check are not done.
- Coordinator steps, in the review dir only, CPU 200% / 6G:
  `GOTOOLCHAIN=local GOMODCACHE=/tmp/villacarmen-vitess-review-20261112/modcache GOFLAGS=-mod=mod go mod download vitess.io/vitess@v0.23.5 && go mod tidy && go list -deps . && go build -o vcolldiag . && ./vcolldiag samples.jsonl`
  - Fix the API names if the build fails.
  - Record the go.sum h1: hash, the dependency list and sizes.

## Plan after the build (no runtime change yet)
1. **Canonical key.** Compare naked keys against Collate equality on the PAD samples. Choose between space-weight trailing stripping and fixed-length padding by the observed behaviour, then re-verify against MySQL `WEIGHT_STRING` (raw and `AS CHAR(n)`) and `=` on the same hex literals (coordinator manual SELECT).
2. Run the bulk comparison per column for the key columns (hash counts plus mismatch counts, no values). Each column is blocked until its mismatch count is 0.
3. **LIKE post-filter rule.** A tenant-scoped PG prefilter returns ALL candidate rows (no LIMIT). Go `Wildcard` match, sort by the same ORDER BY (weight key for text), then paginate and count in Go.
   - Above a technical row bound (proposal: the existing page-size max × N, or measured p99 table-per-tenant size), the endpoint fails closed with an explicit error. It never silently truncates.
   - The bound is a technical parameter, to be set from measured per-tenant counts.
4. Only then propose `internal/mysqlcoll` (Key/Like wrapper) plus a vendored minimal subtree for review.

## Gates still open
- Collation and LIKE (library unbuilt).
- 731 orphans (approval).
- 1117 pending; 0 handler QA; 2 read-qa Scans unverified.
- Enum numeric input; replay payload; unsigned generator range.
- PG disabled; checkout 503; R5 untouched.
Order: full PG verified, then the functional bot, then GraphRAG. Production read-only.
