# result-audit-padding3

- **Scope:** docs only. No code changed, no build, no SQL, nothing dumped.
- **`docs/dbmigrate/collation-padding-audit-3.md`:** 30 keys (10 code, then 20 opaque), each with exactly one status. Recount by row:

  | Status | Rows |
  |---|---|
  | Generated | 14 |
  | Code-canonical | 3 |
  | Enum or enum + validated | 2 |
  | No runtime Go writer | 4 |
  | Client opaque key, trimmed by the source code itself | 3 |
  | NO PAD source collation, no gap | 1 |
  | Opaque, value source not traced | 3 |

  No key is given full parity: legacy and imported rows are not settled.
- **The 5 audit-2 open paths, all closed by source:**
  - The bo_users dynamic update never writes email or username.
  - The member insert uses a normalized email.
  - comida_categories slugs come from `slugifyCategoryName`.
  - The menu SEO slug goes through `normalizeMenuSEOSlug` (pattern forbids spaces).
  - Beverage slugs come from `beverageOptionSlug`.
- **Plan (source-only, not implemented):** the import planner would emit an rtrim expression unique index with explicit `COLLATE dbmigrate.ci_ai` for every PAD SPACE text unique key, keep raw values, pre-check collisions read-only, and come with a catalogue of ON CONFLICT targets, `IsUniqueViolationOn` names and input lookups that must change. It lists what stays non-equivalent: ordering and grouping, LIKE, joins, general_ci vs ICU equivalences, and ignorable characters.
- **Still gated:** the global owner lookups for padded rows (including the custom_domain guard) and the four human GAP keys are unapplied. All PG activation gates stay.
- **Remaining:** 33 keys unaudited, mainly POS client idempotency keys, plus 5 open items from this batch.
- **Unchanged:**
  - R95: 2499 sites, syntax coverage only, of which 17 are legitimate MySQL-only.
  - 0 full-handler runtime QA; 5 query shapes match.
  - 1117 pending and 731 orphans, unwaived; R5 untouched.
  - Checkout still at 503; PG server disabled; tools report-only.

