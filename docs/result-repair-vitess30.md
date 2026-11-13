# result-repair-vitess30 — nondeterministic collation vs regex/substring functions (source/build only)

## Cause
PostgreSQL rejects regex and substring-search functions on nondeterministic collations: regexp_*, `~`, substring-from-regex, split_part, strpos/position, and LIKE on PG 17. `pos_tickets.ticket_number` is `dbmigrate.ci_ai`, so nextPOSTicketNumber's `regexp_match(ticket_number, ...)` fails as soon as there is a row to evaluate. It did not fail on an empty table because there was nothing to evaluate. The port added `COLLATE "C"` to the LIKE but not to the regex input.

## Fixes
- internal/api/backoffice_pos.go:689 (PG text only): `regexp_match(ticket_number COLLATE "C", '([^-]*)$')`. The suffix extraction is byte-wise, which equals MySQL SUBSTRING_INDEX(...,'-',-1) and CAST semantics for the digit suffix. The MySQL text is byte-identical. The always-run structure is unchanged: the Go comment says the scan runs only on a collision, but the code runs it every time; this is left as-is, not changed.
- cmd/ops-check/main.go:166/173/169/176/182 (same bug class, PG text): split_part, regexp_match and substring on `stock_margin_scopes.scope_key` now read `scope_key COLLATE "C"`. The split_part result is re-collated to `dbmigrate.ci_ai` before `='platos'/'bebidas'`, so the equality keeps the MySQL ci semantics.

## Audit
docs/dbmigrate/nondeterministic-collation-audit-vitess30.tsv (file, line, function, column, verdict).
- Grep across internal/api, internal/analytics and cmd/ (internal/bot does not exist) for regexp_*, ~, similar to, position/strpos/overlay/split_part/translate/initcap, substring..from, replace/starts_with on columns:
  - 7 real sites, all fixed (1 api, 6 ops-check).
  - 2 already OK: campaign_email_guard:332-333 uses LOWER() COLLATE "C" ~, and bookings:959 uses COLLATE "C" ~.
  - Other hits are Go identifiers or prose.
- LIKE/ILIKE in backtick SQL literals:
  - 57 already use COLLATE "C", directly or as `LOWER(x) COLLATE "C"`.
  - 23 are MySQL-only texts.
  - The remaining 14 were classified by hand. Each is one of: a MySQL fragment whose PG twin uses COLLATE "C", a prose comment, the campaign_auto_retake MySQL constant (its PG twin is campaignBounceFrozenClausePG), or LIKE on information_schema column names, which use a deterministic collation.
  - No unfixed LIKE on a ci_ai column was found.

## Build
- `go build -p 2 ./...` passes under the inherited cgroup. go.mod is unchanged.
- sha256:
  - vc-server-qa 13a24b85855ff5f6a481c4af556ca5722743061d97f51c8f61e0e3438aad6e1a
  - wk-backfill 32d87a0ba90182c26a1501c846ac6f7b6c04fe10fd276e4c90a54ef4121a27a2
  - pad-gate-check 13bf931aacbd0e7cd682876bdc97593b0a915f744d64da4bb13a9f3bf105ca79
  - qa-http dac985b00d04b0f6a0babde05f7fd45c6da593be0a052b85cde440bf974b6714
  - qa-seed-scratch 8233f4edde9c342a12e0d84615781168f11af7513ff8c29aec97ad2c12dfa991

## Not verified
- Nothing has been run against a DB. Re-test POST /api/admin/pos/visits with existing pos_tickets rows on the scratch.
- The LIKE scan is regex-based over backtick literals. LIKE inside SQL built from Go string concatenation outside backticks, and LHS forms the regex missed (e.g. `) LIKE`), may be missing.
- The per-column collation check assumed every business text column is ci_ai; no column-by-column cross-check against the schema metadata was done.
- backoffice_stock_documents.go:680 is untouched.

