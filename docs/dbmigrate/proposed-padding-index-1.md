# proposed-padding-index-1 (PROPOSAL ONLY, applied: false)

This is not part of internal/db/migrations, plan r4, or the r5 proof. Nothing has been executed.

## Goal

Make PG uniqueness for selected keys treat values that differ only by trailing ASCII spaces as equal, as MySQL `utf8mb4_unicode_ci` (PAD SPACE) does, **without changing stored values**.

## Generic form (per key)

```sql
-- 1. read-only pre-check (must return 0 rows before step 2; else manual merge decision):
--    SELECT <scope cols>, rtrim(col,' ') COLLATE dbmigrate.ci_ai, count(*)
--      FROM t GROUP BY 1.. HAVING count(*) > 1;
-- 2. CREATE UNIQUE INDEX CONCURRENTLY ix__t__<key>__pad
--      ON t (<scope cols>, (rtrim(col, ' ') COLLATE dbmigrate.ci_ai));
-- 3. move every ON CONFLICT target to the same expression:
--      ON CONFLICT (<scope cols>, (rtrim(col, ' ') COLLATE dbmigrate.ci_ai)) DO ...
--    (the target must match the index expression exactly, including COLLATE)
-- 4. move every equality lookup on user input to:
--      WHERE <scope> AND rtrim(col, ' ') = rtrim(CAST(? AS text), ' ')
--    (the column's ci_ai is the implicit collation; keep it, do not use "C")
-- 5. only then DROP the plain index ix__t__<key>
--    (it would otherwise still allow PAD-equal pairs).
```

- **Explicit COLLATE:** `rtrim` returns text with the column's implicit collation. The COLLATE is still written explicitly so the index, the ON CONFLICT target and `pg_index` agree.
- **rtrim(x, ' ') removes only ASCII spaces,** which matches PAD SPACE. TAB and NBSP are not padding in MySQL either.
- **NULL:** `rtrim(NULL)` is NULL, and NULLs stay distinct exactly as in both engines' plain unique indexes (multiple NULLs allowed). This applies to `restaurant_tables.name` and `site_builder_sites.custom_domain`.
- **Tenant scope:** the scope columns stay in front, unchanged (restaurant_id, site_id, group_id, ...). The global keys (domain, subdomain, custom_domain, instance_name) stay global.

## Per gap key

| Key | Scope + expression | ON CONFLICT users to move | Lookups to move |
|---|---|---|---|
| restaurant_areas name | (restaurant_id, rtrim(name)) | none (plain INSERT, `backoffice_premium.go:2148–2166`) | none found by name |
| restaurant_tables name | (restaurant_id, rtrim(name)) | none (INSERT `:2401–2435`) | none found by name |
| site_builder_pages slug | (site_id, rtrim(slug)) | none (INSERT `site_builder.go:554`) | public slug resolution (to audit next) |
| stock_supplier_aliases | (restaurant_id, rtrim(supplier_name), rtrim(supplier_code), rtrim(normalized_description)) | `backoffice_stock_suppliers.go:300` and `backoffice_stock_documents.go:686` ON CONFLICT targets | `backoffice_stock_documents.go:161` supplier_name lookup |
| restaurant_domains domain / domain_mappings domain / sites custom_domain / sites subdomain / uazapi instance_name | (rtrim(col)), global | `domain_reservation.go:58`; `backoffice_premium.go:1146/1154`; `site_builder_registrar.go:305`; `instatic_manager.go:950`; the uazapi INSERTs | owner reads in `domain_ownership.go`; `backoffice_uazapi_provisioning.go:679` |

- **The five global identity keys are now code-canonical** (repair-padding1). The index is belt-and-braces for legacy or imported rows and for any future writer.
- **pos_product_categories** (residual): (restaurant_id, rtrim(name)). Its ON CONFLICT targets are `backoffice_pos_catalog.go:213/226` (PG text).

## What this does NOT make equivalent (honest limits)

- **Other comparisons stay non-PAD:** equality in other queries, ORDER BY / GROUP BY / DISTINCT, LIKE, `<`/`>` and joins on the raw column. MySQL applies PAD SPACE everywhere on these columns; this proposal covers only uniqueness, the moved conflict targets, and the listed lookups.
- **Other collation deltas remain:** expansions, ignorable characters, and LIKE accents (collation1, types2).
- **Generic fix needs a separate design:** a generic all-text PAD SPACE solution would need systematic equality and ordering handling, for example a normalized shadow column or a custom collation, which is not available in ICU. That is not covered here.
- **The 93 remaining keys are not audited.**

