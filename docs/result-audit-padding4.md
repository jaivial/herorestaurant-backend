# Result: audit-padding4 (docs-only, source-only)

- Wrote docs/dbmigrate/collation-padding-audit-4.md: the final 33 keys, with disjoint statuses: GEN-HEX 5, GEN-FMT 1, GEN-COL 2, CLIENT-TRIM 7, CLIENT-RAW 9, CLIENT-MIXED 3, PROVIDER 1, ALLOWLIST 3, NO-WRITER 1, UNTRACED 1.
- Coverage: all 121 r93 keys have exactly one audit row. A script compared the sets: 121 unique, no duplicates, none missing. This is SOURCE-ONLY coverage, not full parity. Imported/legacy rows, external writers, PAD SPACE lookup semantics and ci folding of opaque keys are still unsettled.
- Closed all 5 audit-3 open items: margin validator, POSTRES and group-menu ui_data_id (no writer anywhere in the repo), comida ui_data_id, and the analytics identity builder.
- Corrected the planner plan:
  - no PAD inference from the collation suffix;
  - _bin treated as text, C kept separate from ci_ai;
  - general_ci/unicode_ci/ICU equivalence unresolved;
  - ENUM trailing-space behaviour unaudited.
- docs/dbmigrate/padding-index-callers.tsv: a lexical catalogue of ON CONFLICT, IsUniqueViolationOn and MySQL ODKU/IGNORE sites, 350 rows. 25 are marked blocked_unresolved and need a manual read.
- New source gaps:
  - the mcp token endpoint looks up client_id raw while the authorize endpoint trims it;
  - stock_item_units.code is trimmed on update but stored raw on create;
  - 9 raw client idempotency keys.
- No code, schema or SQL changes. Build not run (docs-only). All PG gates are still held: R95 2499 syntax / 17 legit MySQL-only / 0 handler QA / 5 shapes / 1117 pending / 731 orphans / R5 unchanged / checkout 503.
- Next, in bounded chunks for coordinator review:
  1. a source-only importer plan for rtrim expression indexes with per-column collation;
  2. caller patches for lookups and conflict targets;
  3. resolve the 25 blocked caller rows.
