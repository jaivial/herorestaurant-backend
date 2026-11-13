# Result: repair-padmeta1 (tooling code + source-only proposal; no DB/SQL)

## Code (dbmigrate tooling only; no runtime API changes)
- internal/dbmigrate/padmeta.go (new):
  - CollationMeta: name, charset, pad_attribute.
  - extractCollations reads information_schema.COLLATIONS for every collation used by a column or table, and records the source version in collation_metadata_source.
  - On error it records collation_metadata_error and guesses nothing.
  - PadAttribute(), shared pgCollationFor(), padPending(), PadPlan(), and PadIndexProposal with parts and gates.
- internal/dbmigrate/schema.go:
  - optional fields collations / collation_metadata_source / collation_metadata_error (omitempty, backwards-compatible);
  - ExtractSchema calls extractCollations.
- internal/dbmigrate/types.go: the PAD inference from the collation name (the old _0900_ check) is removed. The collation mapping is unchanged: _ci -> dbmigrate.ci_ai, _bin (a text collation) -> "C".
- internal/dbmigrate/plan.go: PAD pending is decided from metadata.
  - PAD SPACE -> pad_space (with the source recorded).
  - NO PAD -> none.
  - No metadata -> pad_attribute_unknown, an explicit decision. It never defaults to NO PAD.
  - plan.sql DDL is unchanged; only the pending labels change.
- cmd/dbmigrate/main.go:
  - -mode schema exits 3 with a blocker when collation metadata is unavailable;
  - new -mode padplan (reads schema.json and writes a proposal with applied:false; never merged into plan.sql).
- PadPlan contract:
  - covers every unique index, PRIMARY included;
  - keeps source column order and DESC;
  - NULLs are distinct in both engines;
  - tenant columns are kept raw;
  - a PAD SPACE text part becomes (rtrim(col,' ') COLLATE <the column's own PG collation>) and raw values are retained;
  - ENUM parts are not wrapped and carry an enum_input_trailing_space_parity_gated gate;
  - prefix or functional parts, unknown PAD, or an unmapped collation -> blocked;
  - generated columns, char/bpchar and ci_ai approximation each get a gate;
  - every proposal carries caller_compat_pending (ON CONFLICT, unique-error names and readers must match the expression) and a not-merged-into-R5 gate.
- Build: go build -p 2 ./... OK.

## Proposal output
- The archived R5 schema.json has no collation metadata. The proposal is in docs/dbmigrate/padplan-proposal-r5-nometa.json: 136 blocked (pad_attribute_unknown) and 7 gated_no_pad_expression (enum-only). This run read the file only, with no SQL.
- To produce real proposals, a new read-only metadata export from the source is needed. It was not run, and the archived exports and R5 are untouched.

## Audit corrections
- audit-4: the COLLATE "C" remedy was an error. It now keeps the source equality (ci_ai, gated as an approximation), with no ID trimming and no type change.
- Checkout key: S-CLIENT-RAW. The Go replay check uses exact ==, which is stricter than MySQL ci/PAD.
- MCP client_id: there is no configured source. Unregistered ids chosen by the client are accepted.
- Shift terminal key: one writer, which uses TrimSpace.
- padding-index-callers.tsv: all 25 blocked rows were resolved by reading the source:
  - 19 were comments (their real statements are already catalogued);
  - mesas_de_dos/tres is a fixed dynamic table with a non-text key;
  - chat_images is a botstore table outside the MySQL schema;
  - campaign_email_retries and widget_settings have int keys;
  - pos_fiscal_series matches key_in_121.
- Caveat: the other rows are still lexical. Some not_in_121 rows may also be comments.

## Gates (unchanged)
R95 2499 syntax / 17 legit MySQL-only / 0 handler QA / 5 shapes / 1117 pending / 731 orphans / R5 untouched / checkout 503 / server PG disabled / tools report-only. No SQL, DB writes, import, provider calls or sends.

## Next
- Critical: patch the global domain callers (restaurant_domains.domain, site_builder_domain_mappings.domain) so ON CONFLICT targets and owner lookups use the identical padplan expression.
- This needs a future plan generated from a fresh metadata export. The current R5 raw unique index must not be assumed. It stays gated until the coordinator reviews it.
