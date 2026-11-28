# Native PostgreSQL port: site_analytics checkpoint 1 (website_builder_handlers.go)

**Status:** this code compiles. No SQL was executed, and no external site, domain registrar, publish, Bunny upload or provider action was run. The app is **not** PG-ready: `ready:false`, MySQL stays the default and the server refuses to start on PG.

## Counts (canonical inventory r38, dbmigrate-r12 basis)

| Scope | Value |
|---|---|
| Call sites (repo) | 2472 |
| Native (repo) | 1267 (+48 since r37) |
| Dynamic (repo) | 188 |
| Portable-audit failures | 0 |
| Excluded | 9 helper-internal calls (was 8; +1 for the RELEASE added below), 3 prepared-statement executions |
| site_analytics | 48/201 native, 153 remaining (18 dynamic), 1/17 files done |
| Repo remaining | 1205 |

- Inventory: `/tmp/villacarmen-bot-20261111/sql-inventory-r38.json`
- Ledger: `docs/dbmigrate/native-ledger-site.json` (new)
- Semantic notes: `/tmp/villacarmen-bot-20261111/site-semantics1.json`

## Build
`go build -p 2 ./...` exited 0 under the inherited cgroup (200% CPU, 6G memory), with pipefail. No tests, vet, fixtures or Playwright were run.

## Correction to crossaudit 1 counts
The report said "297 transaction-bound", but its categories sum to 251+9+14+27 = **301**. The artifact (`crossaudit-raw-final.json`) contains **301 unique non-`*sql.DB` call sites**: 286 `*sql.Tx`, 13 interface receivers and 2 `*sql.Stmt`. Each site has exactly one class, so the categories do not overlap. **297 was an arithmetic error in the report text**; the correct total is 301. The per-file coverage JSON was already computed from the 301 sites.

## TxStatementScope cleanup fix
- **Code evidence:** the previous helper ran `ROLLBACK TO SAVEPOINT sqldb_stmt` and returned. In PG, `ROLLBACK TO` keeps the savepoint defined, so repeated failing scopes in one transaction would leave one active savepoint each, because every one used the same name.
- **Fixed:** after a successful `ROLLBACK TO`, the helper now also runs `RELEASE SAVEPOINT`. The savepoint is therefore released on both success and failure, and same-name nesting resolves to the innermost savepoint, which is legitimate.
- **Error handling:** the original `fn` error is always returned first, joined with any ROLLBACK TO or RELEASE cleanup error via `errors.Join`. The MySQL path is unchanged (`fn()` only).
- No SQL was executed to verify this; it is from code review only.

## website_builder_handlers.go (48 sites, 4 dynamic)
- **SOURCE ANOMALY, preserved (not fixed):** in the source schema, `website_pages` has **no** `meta_description`, `meta_keywords`, `is_homepage`, `status` or `position` columns, and the tables **`website_assets` and `website_publish_history` do not exist**. Migration 026a defines them, but the restored and live schema lacks them.
  - These fail on MySQL and PG alike: ListPages (both variants), CreatePage, the homepage resets, UpdatePage when those fields are sent, ensureHomepage, the asset list/upload/delete, and the publish-history INSERT.
  - Upload still runs the Bunny upload first, as in the source; it was not run here.
  - PublishHistory and the version lookup degrade gracefully through `isSQLSchemaError`, which recognises PG 42P01/42703 via `sqldb.IsUndefinedObject`, so both engines take the same path.
  - The PG `ORDER BY` clauses carry explicit `NULLS LAST`/`NULLS FIRST`, so a later meaning fix keeps MySQL's order.
- **DeleteWebsite transaction:** on MySQL, the DELETEs against the missing tables fail per statement, the errors are ignored, and the transaction continues to delete the site. PG would abort the transaction, so all three ignored DELETEs are wrapped in `sqldb.TxStatementScope` (crossaudit rule).
- **JSON:**
  - Reads use `COALESCE(col, JSON_OBJECT())` → `CAST(COALESCE(col, CAST('{}' AS jsonb)) AS text)`, and `template_data` is read as text.
  - Writes use `CAST(? AS jsonb)`, including the dynamic `settings`/`dynamic_params` fragments (`sqlFragment`, with each final string wrapped once) and the `restaurant_websites` settings variants.
- **execVariants:** the 14 variant query literals are now `nativeSQL` at construction (`NOW()` on timestamptz → `date_trunc('second', statement_timestamp())`, settings → jsonb), and the executor uses `selectedSQL(variant.query)`. The fallback-on-schema-error semantics are unchanged.
- **Insert IDs:** 5 `LastInsertId` sites (CreatePage, CreateSection, CreateSectionComponent, UploadAsset, seedTemplateSections) now use `sqldb.InsertID`. The source ignored the LastInsertId error, which the MySQL driver never returns.
- **Templates:** `category` is a native PG enum, read with `CAST AS text` and sorted `NULLS FIRST` (it is nullable, and that is MySQL's ASC order). The enum's declared order is preserved by the import plan.
- **Changed-row and locking review:** no `RowsAffected` gates exist in this file. The reorder transactions return on error.
- **Time arguments:** there are none; only `time.Now().UTC()` used for path and JSON strings.
- **Checks run:** the AST pass on this file found no other ignored transaction errors. A lexical pass over all 27 `nativeSQL` PG halves found no MySQL functions, no `?` inside literals and matching `?` arity.

## Open (none accepted)
- **JSON text rendering and key order** of settings, template data and component defaults (existing JSON QA gap).
- **Enum writes:** `status` is written as a PG enum, so an invalid value errors. MySQL strict mode errors on an invalid enum too, but the exact error class and message differ.
- **Collation:** names and slugs are under the CI/accent gap, including uniqueness of (website_id, slug) and `component_type`.
- **Source anomalies:** the missing columns and tables above need a separate meaning decision.
- **Unchanged from earlier checkpoints:**
  - crossaudit limits: interface and unported callers, `any` helpers, zero time, and the isolation default (RR vs RC) are unresolved;
  - the bounce invalid-UTF-8 suppression gap is **cutover-blocking**;
  - 1117 pending items with `ready:false`, 4 NOT VALID FKs with 731 orphans, CI/accent/PAD SPACE, FULLTEXT, 190 ranges, 136 ON UPDATE / 353 defaults, the view, NUL QA, source anomalies, and the numeric, JSON, ID and lock gaps.

## Next checkpoint (24 sites)
| File | Sites | Dynamic |
|---|---|---|
| website_builder_generator.go | 4 | |
| website_builder_public.go | 4 | |
| site_builder_registrar.go | 3 | |
| legal_pages.go | 3 | |
| seo_item_routes.go | 10 | 4 |

The registrar and publish paths are code only; nothing will be run.

## Not done
No commit, merge, deploy, DB writes, providers, payments, email, WhatsApp or sends, and no `.env`, source or proof-data edits.

