# result-audit-padding1

This is a docs-only audit. I did no build (none was needed), executed no SQL, changed no code, and did no provider calls. Nothing was dumped: no values, no secrets.

- **Audit:** `docs/dbmigrate/collation-padding-audit-1.md` covers all **28** human/unknown keys. The request asked for 30, and only 28 exist.
- **Evidence:** the schema and plan r4 (column type, collation, nullability, index columns, generated columns), plus every writer and reader as `file:line` with its trim behaviour.
- **Results:**
  - 13 keys need no change; their writers already trim, or the values are constants, opaque or enums.
  - 1 key was already handled in collation1.
  - **7 GAP keys** have writers that store untrimmed user values: area and table name updates, billing domain, domain mapping insert, page slug, site subdomain, and supplier alias name.
  - 1 key is an opaque provider instance name; the recommendation is fail-closed.
  - 6 items are open or unverified.
- **Critical cross-tenant or routing gaps on PG** (MySQL PAD SPACE would reject these; ci_ai does not):
  - billing checkout `claimOwnedDomain(req.Domain raw)` (checkout is currently 503-gated);
  - `site_builder_domain_mappings`, which checks a normalized value but inserts the raw one;
  - `site_builder_sites.subdomain`;
  - uazapi `instance_name`, a value returned by the provider.
- **Remedies:** IDX-RTRIM (a forward expression index plus matching lookups) is the only remedy that preserves stored values. TRIM-CHANGE is flagged wherever it would alter identity (leading spaces, Unicode spaces, stored raw values). Nothing was applied; no schema or writer code changed.
- **Remaining:** 93 opaque/code/contact keys are not audited.
- **Unchanged:** r95, with 2499 sites classified (syntax coverage only). There are still 17 legitimate MySQL-only sites, 0 full handler runtime QA, the PG server is disabled, tools are report-only, 1117 pending and 731 orphans unwaived, and R5 is unchanged.
- **Next:**
  1. A bounded code fix for the 4 critical keys: normalize at entry where a source-consistent rule exists (domain/subdomain), and fail closed on a provider name with trailing spaces.
  2. A forward IDX-RTRIM proposal per GAP key, not applied.
  3. Audit chunk 2 (30 of the 93).

