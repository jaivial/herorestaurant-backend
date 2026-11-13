# Vitess MySQL-collation library review (repair-vitess1)

The worker could not fetch the module. The sandbox has no network: `go mod download vitess.io/vitess@v0.23.5` failed with "dial udp 127.0.0.53:53: socket: operation not permitted"; GOTOOLCHAIN=local, Go 1.26.0. So I have not inspected any of the following myself: go.sum hash, transitive dependency list, package sizes, govulncheck results. Every fact below comes from the coordinator's public research (11:08) unless it is marked as the worker's own.

## Grounded facts (coordinator, URLs)
- **API, v0.24.2** (https://github.com/vitessio/vitess/blob/v0.24.2/go/mysql/collations/colldata/collation.go):
  - `Collation.WeightString(dst, src []byte, numCodepoints int)`
  - `Collate(left, right []byte, isPrefix bool)`
  - `Wildcard(pat []byte, matchOne, matchMany, escape rune)`
- **Collations** (https://github.com/vitessio/vitess/blob/v0.24.2/go/mysql/collations/colldata/mysqldata.go): utf8mb4_general_ci, utf8mb4_unicode_ci, utf8mb4_0900_ai_ci and utf8mb4_bin.
- **License:** Apache-2.0 (https://github.com/vitessio/vitess/blob/master/LICENSE).
- **History:** clean-room algorithms plus weight data (https://github.com/vitessio/vitess/pull/8991). This does not prove that every input today is equivalent to MySQL 8.0.45.
- **Go versions:**
  - v0.24.2 needs Go 1.26.4, but the local toolchain is 1.26.0. Rejected, because local Go must not auto-upgrade.
  - v0.23.5 (https://github.com/vitessio/vitess/blob/v0.23.5/go.mod) needs go 1.25.11, so it is compatible. **Pinned candidate: v0.23.5.** Its API and tables must be re-checked at that tag, since the URLs above are v0.24.2.
- **Security:** GO-2024-2826 (https://osv.dev/vulnerability/GO-2024-2826) was an OOM on malformed UTF-16/32/UCS-2 input, fixed in 0.17.7, 0.18.5 and 0.19.4. This is not a full current audit, and govulncheck is not installed.

## Dependency strategy (to verify once downloaded)
- Do not depend on the vitess root module wholesale: it is huge, with gRPC, protobuf, etc. Needed packages: `go/mysql/collations`, `go/mysql/collations/colldata`, plus their internal charset/vterrors imports.
- In the review dir, run: `go list -deps vitess.io/vitess/go/mysql/collations/colldata`. Record the package list, `du` sizes, the go.sum h1: hash for vitess.io/vitess v0.23.5, and every module that list pulls in.
- If the dependency closure is large, vendor a minimal Apache-2.0 subtree with its headers and NOTICE, pinned to the exact commit.
- Do not run third-party scripts or go generate.

## Semantics to review before any key design
- **PAD SPACE equality key.** WeightString with numCodepoints=0 returns a *naked* variable-length key. For PAD SPACE collations, 'main' and 'main ' are Collate-equal, but their naked keys may differ. Two options:
  - (a) strip trailing *weight units* equal to the space weight (the collation's own rule, not ASCII rtrim);
  - (b) pad to a fixed column length (numCodepoints = column char length) for a fixed-length canonical key.
- **The rule:** a unique key must be canonical, meaning Collate(a,b)==0 if and only if key(a)==key(b). Verify with Collate as the oracle, not hashes alone.
- **Ignorables.** Do NUL and other ignorable characters produce zero weight units? MySQL treats NUL as equal to the plain string in unicode_ci and 0900, but not in general_ci (per the 10:55 matrix).
- **MySQL `WEIGHT_STRING(col)` without AS CHAR(n)** may itself be non-canonical for PAD. Compare raw weights AND equality; do not compare hashes only.
- **Avoid WeightStringLen/size helpers** on arbitrary input unless their contract (panic or limit) has been read.
- **Invalid UTF-8 and binary bytes:** record how WeightString treats invalid sequences (replacement weights?), and make the key builder fail closed rather than silently map them.
- **LIKE** is not done via weight keys: LIKE has different expansion and trailing-pad behaviour (the matrix shows LIKE trailing space = 0 even under PAD SPACE). Use `Wildcard(...).Match` only.
