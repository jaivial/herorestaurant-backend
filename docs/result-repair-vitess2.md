# result-repair-vitess2

Model: claude-merge/claude-opus-5-5-acc2 (low).
- Isolated review directory only. The app go.mod, go.sum and runtime are unchanged. No app dependency.
- The worker did not run the CLI. No SQL, no network (GOPROXY=off, GOSUMDB=off, local cache). No tests, fixtures or assertions.
- R5 untouched, PG disabled, checkout 503, 0 handler QA. 1117 pending, 731 orphans.

## Pinned module (coordinator 11:19)
- vitess.io/vitess v0.23.5, commit 1e30dbc6658b7078eda58dd79969ce1f76cf132d.
- Hashes: h1:hdJlWjs1le0mR5aD3okgZAd/8X6UCkfguNlIuOAFGos=; go.mod h1:GHrEVM3nwyBNiCLq+um8MmB7g10Znbv0FO0eD5+MwIM=.
- go.sum sha f5054be8….
- Footprint: 209 deps, 273 MB cache, 16 MB binary.

## Closure correction (worker, go list -deps, offline)
My vitess-review "small closure" claim was FALSE.
- `colldata` imports `collations`, which imports `sqltypes` → `vt/proto/{query,topodata,binlogdata,vtrpc,…}` → google.golang.org/protobuf (29 pkgs), grpc (8), genproto, plus vt/log (glog), vterrors, flagutil (spf13/pflag), mysql/decimal, datetime, format, and vthash (highway, metro).
- In total, colldata pulls 32 vitess packages.
- The algorithm-only packages are colldata, internal/uca, charset/*, vthash and hack. A minimal vendored subtree would have to cut `collations` (env/ID/sqltypes coupling) away from colldata. That is a source edit of Apache-2.0 code: allowed with attribution, but not done.

## Actual API (read in modcache v0.23.5: colldata/collation.go:60-150, unicode.go:52-120, uca.go:318-372)
**WeightString(dst, src, numCodepoints):**
- PAD SPACE: n>0 pads to CHAR(n) with SPACE weights. n=0 gives a naked key (VARCHAR sort).
- PadToMax needs a preallocated dst (otherwise it returns empty).
- NO PAD (0900): ignores n except PadToMax.

**Collate:** compares naked weight sequences.
- general_ci: `return len(left)-len(right)`.
- uca_legacy: `int(l)-int(r)` once one iterator ends.
- So **Collate is not PAD-aware.** This matches the 11:20 observation `Collate('main ','main')≠0`. In Vitess, PAD handling for VARCHAR equality lives elsewhere (evalengine), not in Collate.

**Invalid UTF-8:**
- general_ci WeightString and Collate stop or fall back on RuneError: `break` in WeightString, `bytes.Compare` in Collate.
- That explains the 0xFF sample producing an empty key with Collate equal TRUE.
- A wrapper MUST check utf8.Valid and fail closed.

**Other notes:**
- I did not use `WeightStringLen`; it is an upper bound and needs no arbitrary-input contract.
- `Hash` collides by contract, so it is not a key.

## PAD design (proposal, unverified until the manual comparison)
- **PAD SPACE collations,** with pad_space taken from actual source metadata (PAD_ATTRIBUTE), never guessed:
  - canonical key = naked weight string with trailing whole weight units equal to the collation's own SPACE weight (`WeightString(" ")`) removed;
  - unique and equality use this key.
- Why weights rather than byte rtrim: this models strnncollsp, which pads the shorter side with SPACE weights. Two consequences:
  - Characters whose weight equals SPACE (to check: U+00A0 in UCA 4.0) would equal plain trailing space. ASCII rtrim would miss that.
  - Ignorables (combining marks have no primary weight in the legacy UCA) inside or after a trailing space are handled by the weights. Example: 'a␠◌́' collapses to 'a' if the mark is ignorable.
  - Both must be confirmed against MySQL, not assumed.
- **NO PAD (0900):** naked key, unchanged.
- **utf8mb4_bin:** it is a PAD SPACE collation in local metadata. Its weight is the codepoint, and the same stripping rule applies (sample included).
- **Key size:** about 2 bytes per weight unit for legacy collations; expansions (ß→ss) add units. For VARCHAR(190) the worst case is a few hundred bytes, within PG's btree limit of about 2.7 KB. Per-column maximum lengths must be computed before any index.
- **LIKE:** `Wildcard` on ORIGINAL bytes only. No trimming, no keys. The matrix shows LIKE trailing space = 0 even under PAD.
- **Raw `WEIGHT_STRING(col)` from MySQL is NOT canonical for PAD.** Neither the raw naked key nor bare MySQL WEIGHT_STRING can be used directly as a unique key. Compare raw weights AND equality, not hashes only.

## Diagnostic CLI (built offline, not run by worker)
Location: `/tmp/villacarmen-vitess-review-20261112/diag`. The previous main.go is backed up as `main.go.vitess1.bak`.
- Build: `go build -p 2 -buildvcs=false` with GOTOOLCHAIN=local, GOPROXY=off, GOSUMDB=off and the isolated modcache/gocache. Build OK, 16 MB binary.
- Input is JSONL with fields `label`, `coll`, `pad_space` (required; refused if absent), `a_hex`, `b_hex` and optional `pat_hex`.
- Invalid UTF-8 is refused with "fail closed".
- Output fields: `a/b_raw_hex`, `a/b_pad_hex`, `raw_key_equal`, `lib_collate_equal` (reported as not PAD-aware), `pad_key_equal` (the proposed canonical equality) and `a_like_pat` (Wildcard on original bytes).
- `samples.jsonl` has 21 literal codepoint samples:
  - main+space for unicode, general and 0900, plus main+tab;
  - NBSP trailing (unicode and general);
  - a+space+combining and a+combining+space;
  - æ/ae for unicode, general and 0900 (unicode æ added as requested);
  - ß/ss and ß/s;
  - NUL embedded and trailing;
  - LIKE accent and trailing;
  - bin + space;
  - invalid 0xFF.
- Run: `./vcolldiag samples.jsonl`.

MySQL twin: `docs/dbmigrate/vitess2-weightstring-mysql.sql`. It has manual literal SELECTs of HEX(WEIGHT_STRING) raw and AS CHAR(10), plus `=` equality, for the same hex samples.

## Manual evidence so far (coordinator 11:20, earlier CLI)
| sample | result | verdict |
|---|---|---|
| Collate('main ','main') | FALSE | library not PAD-aware |
| Wildcard 'main ' vs 'main' | FALSE | correct per source LIKE |
| general_ci ß=s | TRUE | matches source |
| unicode_ci ß=ss | TRUE | matches source |
| unicode_ci combining acute | TRUE | matches source |
| general_ci NUL embedded ≠ ab | ≠ | matches source |
| 0xFF | empty key, equal TRUE | unsafe without the wrapper |

## Open (no full-compat claim)
1. Run the new CLI and the MySQL twin. pad_key_equal must equal MySQL `=` for every sample, especially NBSP, a␠◌́, NUL trailing and æ. Any mismatch kills the design for that collation.
2. Per-column bulk comparison (counts only), then the vendoring decision: the full module (209 deps) or a minimal Apache subtree cut from `collations`/sqltypes.
3. Security: GO-2024-2826 covers UTF-16/32 only. utf8mb4 still needs a current govulncheck, which is not installed and was not installed blindly.
4. LIKE post-filter bound (fail closed); enum, orphan and other gates unchanged.
Order: full PG verified, then the functional bot, then GraphRAG. Production read-only.
