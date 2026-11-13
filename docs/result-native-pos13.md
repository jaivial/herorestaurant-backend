# result-native-pos13: backoffice_premium (39 sites, 16 dynamic)

Code only. go build -p 2 ./... exit 0. Runtime-verified 0. PG readyfalse, server refuses PG, MySQL default.

## Counts (inventory r68)
- **Repo:** 2475 = native 2210 (5 provisional, none new) + blocked 3 + unwrapped 262; dynamic 47; audit failures 0.
- **Delta vs r67:** +39.
- **POS:** 419/564 ported, 145 left (5 dynamic), 32/35 files done.

## Work
- **49 schema-fallback variant literals:** each is an explicit sqlFragment(mysql, pg) twin, rebound once at its exec site.
  - Time functions: NOW() → timestamptz statement time; the naive next_run_at → UTC statement time + interval '1 month'; CURDATE → UTC date.
  - Upsert and boolean: ODKU → ON CONFLICT (domain); `is_active = true` → `= 1`.
  - Fallback detection: missing-column fallbacks keep the source order via PG 42703.
- **Domain-register tx loops** (4 sites): each attempt is savepoint-scoped on PG, since the source continues the tx after a schema or duplicate error.
- **Activate recurring:** changed-rows predicate, because affected==0 makes MySQL insert a new row.
- **Static sites:**
  - Upserts: table layouts and templates use ON CONFLICT on their unique keys with jsonb casts; menu templates become a plain INSERT (no unique key, so ODKU never fires).
  - Deliveries: casts payload to jsonb, writes sent_at as naive UTC.
  - Table delete: FK detection now also accepts PG 23503 via new sqldb.IsForeignKeyViolation.
  - Remaining static sites are statement-time updates or portable selects.
- **Map helpers (SELECT *):** queryOneAsMap/queryAllAsMaps rebind once internally (this also covers the website_builder callers). normalizeSQLValue treats a pgx string the same as a go-mysql []byte.

## New gates (unwaived, separate from 1117)
- **Concurrent domain register:** MySQL's RR gap lock on the absent domain makes a second restaurant fail; on PG the second restaurant's ON CONFLICT DO UPDATE takes over the domain (cross-tenant). Not equivalent.
- **SELECT * map JSON shape:** jsonb text normalisation (e.g. restaurant_websites.settings), numeric text and enum/time values need QA.
- **Activate predicate:** float→numeric(10,2) rounding and jsonb vs MySQL JSON equality affect it (numeric/JSON gate).
- **Fallback variants:** they depend on missing website/area/recurring columns, which are dump-only source anomalies (not confirmed live).
- **pos12:** the invalid-filter PG 500 vs MySQL warning still needs QA.
- **pos10/11:** POSTRES sequence, first-invoice gap lock, IGNORE losses, default display unit, row-lock timing.
- **General:** 1117 import proof readyfalse; 4 NOT VALID FKs / 731 orphans; collation/search/UUID/SEO/multikey/routing/NUL/TIME/numeric/fiscal hash/136 onupdate.
- **Repairs and dump-only:** guest-label/catalog-scope repairs pending dedicated QA; PIN columns dump-only.

## Next
Run each as its own checkpoint:
- backoffice_pos 44 (3 dynamic)
- backoffice_pos_checkout 48
- backoffice_pos_rail 53 (2 dynamic)

