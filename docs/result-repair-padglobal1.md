# Result: repair-padglobal1 (PG global key PAD SPACE parity; code-only, nothing applied)

## Metadata (local only)
- docs/dbmigrate/collation-metadata-local-20261112.json is a sidecar of the coordinator's read-only check of the LOCAL restored source (villacarmen_source, 8.0.45, 07:16 CET):
  - utf8mb4_0900_ai_ci: NO PAD
  - utf8mb4_bin: PAD SPACE
  - utf8mb4_general_ci: PAD SPACE
  - utf8mb4_unicode_ci: PAD SPACE
  This is not production proof and not a new export.
- The DERIVED schema is /tmp/villacarmen-bot-20261111/derived-localmeta-20261112/schema.json: the archived schema plus that metadata, labelled with the archived sha256 4cbbdea1…8830. The archived file is unchanged (sha re-checked). R5 and full-export are untouched.
- padplan on the derived schema: 134 proposed_gated and 7 gated_no_pad_expression (/tmp/…/padplan.json). The critical subset is in docs/dbmigrate/padplan-global-keys-localmeta-20261112.json (applied:false).

## Required future DDL (NOT executed; additive; keep the raw R5 unique indexes until the callers have passed QA, then retire only by a reviewed proposal)
```
CREATE UNIQUE INDEX "ixpad__restaurant_domains__uniq_restaurant_domains_domain" ON "restaurant_domains" ((rtrim("domain", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__site_builder_domain_mappings__uk_domain" ON "site_builder_domain_mappings" ((rtrim("domain", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__site_builder_sites__uk_subdomain" ON "site_builder_sites" ((rtrim("subdomain", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__site_builder_sites__uk_custom_domain" ON "site_builder_sites" ((rtrim("custom_domain", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__restaurant_uazapi_instances__uniq_restaurant_ua_96a63182" ON "restaurant_uazapi_instances" ((rtrim("instance_name", ' ')) COLLATE "dbmigrate"."ci_ai");
```
- These indexes stay global (no tenant column), as in the source.
- NULL custom_domain stays distinct.
- An empty custom_domain stays a single unique value, as in MySQL.
- Before creating them, check existing data for rows that collide after rtrim. The local count is 0 (coordinator 06:44); that is current data only.

## Code changes
- **PG text only; every MySQL text is unchanged.** Equality is (rtrim(col,' ') COLLATE dbmigrate.ci_ai) = (rtrim(?,' ') COLLATE dbmigrate.ci_ai), and the conflict target is ON CONFLICT ((rtrim(domain, ' ')) COLLATE dbmigrate.ci_ai).
- **Rebinding:** portableSQL→nativeSQL, so each statement is rebound exactly once. queryOneAsMap gets sqlFragment and rebinds internally once.
- **Behaviour kept:** no change to arg order, RowsAffected checks, FOR UPDATE, or owner-guarded DO UPDATE WHERE.
- **Raw values kept:** stored and bound values are never trimmed.
- **Patched files** (32 rows in docs/dbmigrate/padding-global-key-callers.tsv):
  - domain_ownership.go
  - domain_reservation.go
  - backoffice_premium.go (owner lock, 2 upserts, verify lookup, lookupTakenDomains IN list)
  - instatic_proxy.go (routing)
  - tenant.go (routing)
  - site_builder_billing.go (7)
  - site_builder_registrar.go (6)
  - instatic_manager.go (subdomain select + 2 upserts)
  - backoffice_uazapi_provisioning.go (owner lookup)
- **internal/sqldb/sqldb.go:** IsUniqueViolationOn now takes several PG constraint names (variadic; existing callers are unaffected). The UAZAPI call adds the ixpad name. The site_builder uk_subdomain/uk_domain substring checks already match the ixpad names, so they are unchanged.
- **internal/db/padkeys.go (new) – the readiness gate:**
  - RequiredPadKeyIndexes plus RequirePadKeyIndexes, a read-only pg_catalog check: unique, valid, one key, no predicate, an expression key, collation dbmigrate.ci_ai, and an rtrim(col,' ') rendering.
  - The rendering is matched with a regex that is unverified until QA.
- **internal/db/business.go:** OpenBusinessForTool now refuses a PG business DB that lacks these indexes, in addition to the existing marker and QA env gates. It never falls back and never partially uses the DB.
- **cmd/server/main.go:** a comment says the server must call the gate if the PG refusal is ever lifted. The refusal itself is unchanged.
- **internal/dbmigrate/padmeta.go:** proposals now use the index_elem form, identical to the runtime ON CONFLICT target.

## Build / inventory
- go build -p 2 ./... OK.
- Inventory r96: 2500 sites (+1: the pgOnlySQL gate query in padkeys.go), 285 files, all native, 0 portable audit failures. This is syntax only.

## Remaining / gates (not claimed)
- These callers carry no new QA; runtime verification is 0. Compatibility of the PG expression and ON CONFLICT inference is unverified until manual QA against a PG copy that has the ixpad indexes.
- lookupTakenDomains: PG compares the rtrim'd stored key against args that Go already lowercased and TrimSpace'd. Its result map still keys on the Go-lowered scan, which does not fold accents. That is source behaviour on both engines.
- restaurant_websites.domain/subdomain is not one of the 121 unique keys and is out of scope.
- ci_ai only approximates unicode_ci (expansions; general_ci vs unicode_ci vs ICU unresolved). The other ~116 PAD keys are not patched; general collation parity is NOT complete.
- Unchanged: R95→r96 syntax / 17 legit MySQL-only / 0 handler QA / 1117 pending / 731 orphans / R5 untouched / checkout 503 / server PG disabled / tools report-only and now also pad-index-gated. No SQL, DB writes, schema changes, providers or sends.

## Next
1. The coordinator reviews the DDL.
2. Read-only collision probe on the PG copy.
3. Manual QA plan for these callers.
4. Then the next bounded key group, using the padplan proposals.
