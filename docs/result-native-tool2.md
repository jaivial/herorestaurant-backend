# result-native-tool2: internal/dbmigrate 18 + cmd/ops-check 15 (33 sites)

Code only. go build -p 2 ./... exits 0. Runtime-verified 0. Nothing was run: no ops-check, export, import, SQL, writes or providers. PG readyfalse; server refuses PG; MySQL is the default.

## Counts (inventory r73)
- **Repo:** 2475 = native 2425 (incl 5 provisional) + blocked 3 + unwrapped 47; dynamic 10; audit failures 0.
- **Delta vs r72:** +33.
- **Syntax-complete:** POS 564, WhatsApp 316, botstore 37, dbmigrate 18, ops-check 15.
- **Remaining 47:**
  - backfill-translations 9
  - cdn-sweep 8
  - comida-stock-backfill 8
  - group-menu-arroz-cleanup 5
  - update-restaurant-info 4
  - db/migrations 4
  - migrate-wine-images 3
  - botstore-migrate 2
  - chatgpt-plugin-token 1
  - cmd/dbmigrate 1
  - seed-legal-pages 1
  - internal/db 1

## internal/dbmigrate (18, markers only)
- **Engine roles kept as they are:** export.go and schema.go read the MySQL source; pgimport.go writes and verifies the PG target.
- **New file:** internal/dbmigrate/dialect_markers.go defines identity mysqlOnlySQL and pgOnlySQL.
- **Marker counts:** mysqlOnlySQL 4 (session setup, ROLLBACK, table read, information_schema scan); pgOnlySQL 14.
- **Unchanged:** no SQL text changed. The r5/import proof data and the 1117 pending classification are untouched. The prebuilt inventory binary is unaffected.

## cmd/ops-check (15, explicit twins)
- **Local helpers:** a package dialect variable plus nativeSQL, portableSQL and sqlFragment (rebind once).
- **The dialect stays MySQL:** ops-check still opens MySQL and runs the MySQL migration runner unconditionally, exactly as the source does.
- **Translations:**
  - GET_LOCK/RELEASE_LOCK → pg_try_advisory_lock/pg_advisory_unlock(hashtextextended('villacarmen:ops-check',0)).
  - Run and delivery ids via InsertID; summary/payload as jsonb.
  - NOW() → naive UTC for started_at, finished_at and sent_at; timestamptz for original_deleted_at. CURDATE → UTC date.
  - DATE_SUB intervals → naive UTC.
- **Cover check:**
  - HAVING on aliases → the expression is repeated.
  - Three distinct service_type enums are compared as text.
  - covers_mode is read as text.
- **Margin-scope check:**
  - SUBSTRING_INDEX → split_part / last-segment regexp.
  - Lenient CAST AS UNSIGNED → leading-digits numeric.

## New gates (unwaived)
- **ops-check PG wiring (cutover):** open the PG pool with the sqldb UTC pin, skip migrations.Apply on PG, set the dialect. Nothing is activated.
- **Advisory lock vs GET_LOCK:** both are session locks on arbitrary pooled connections. The release can land on a different connection — the source has the same bug class.
- **Scope-key cast:** exotic inputs (sign, overflow, non-ASCII digits) need QA, as in pos14. The ci_ai collation on the 'platos'/'bebidas' match is a gate.

## Still open
- **Botstore:** the Rebind is not quote-aware. The rule that SQL text never contains a literal '?' needs a source caller audit; it is not assumed.
- **Legacy schemaless PG:** the UTC gap remains. The unified schema is UTC-confirmed.
- **Earlier gates:** 1117 import proof readyfalse; 4 NOT VALID FKs / 731 orphans; IGNORE, routing, gap locks, NUL/JSON, search, TIME, UUID, SEO, multikey, collation/numeric, fiscal hash, 136 onupdate.
- **pos13:** the domain-register takeover remains unaccepted.
- **Pending repairs:** guest-label/catalog-scope technical repairs need dedicated QA. The PIN columns are dump-only.

## Next
The remaining 47 small tool sites, in one checkpoint.

