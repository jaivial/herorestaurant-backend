# pgnative-C — SQL check tool + fixes (source/build only)

Authority: Main 11:56/11:57. This is worktree villacarmen-bot-pgnative-20261113 only. The tool was NOT run against any database; only its static lint mode ran locally (no DSN, no network).

## C1/C2: cmd/pgnative-sqlcheck
- **Collection.** go/ast walk of internal/ and cmd/, excluding _test.go, internal/dbmigrate and the tool itself. It collects:
  - the 2nd argument of nativeSQL/sqlFragment
  - the argument of portableSQL/pgOnlySQL/selectedSQL; portableSQL(nativeSQL(...)) is collected once, via the inner call
  - every other string literal that mentions app.norm or ON CONFLICT (lint only)

  Literals and +-concatenations of literals are folded. Anything else is SKIPPED "dynamic".
- **-prepare** (DSN only from PG_DSN, never printed): one pinned connection with a single READ ONLY transaction, always rolled back. Per site:
  1. SAVEPOINT
  2. `PREPARE pgnc_N AS <stmt with ? → $n>`
  3. DEALLOCATE / ROLLBACK TO SAVEPOINT

  Nothing else is sent. Only complete SELECT/INSERT/UPDATE/DELETE/WITH statements are prepared. SKIPPED: format verbs (%s etc.), sqlFragment pieces, DDL/utility, multiple statements, temp objects.
- **Lint** (always):
  - (a) `app.normX(x) = rhs` where rhs is a literal with uppercase or trailing space, a raw ?/column, or the other norm function. Also `app.normX(x) IN (...)` with an element not wrapped in the same function.
  - (b) ON CONFLICT targets: for INSERT INTO t, the target set must equal a ux__ inventory key (docs/pgnative/native-ddl-inventory.tsv) or, with -schema, a raw PK/unique column set.
  - (c) `app.normX(col)` on a column of a referenced table whose ux__ index uses the other function.
- **Output:** TSV with pos, helper, ok/error/SKIPPED/lint kind, detail. A JSON summary goes to stderr.
- **Coordinator command:** `PG_DSN=… pgnative-sqlcheck -schema <schema.json> -prepare -out /tmp/report.tsv`, run from the repo root.

## Counts (static run, the same source as the binaries)
- 3246 sites collected.
  - 2152 complete statements, to be PREPAREd by the coordinator.
  - 1094 skipped: 391 dynamic, 248 fragments or DDL/utility (linted), 454 literal pieces (linted), 1 format fragment.
- Lint hits before fixes: 7. After fixes: 3, all by design:
  - backoffice_premium.go:3982 `app.norm_ascii(domain) IN (%s)`: %s is now filled with `app.norm_ascii(?)` elements (pgPH); the tool cannot see it.
  - whatsapp_bot_context_sqlite.go:351 handoff_state and botstore/migrate.go:271 bot_store_meta: bot_context (SQLite-origin) tables. They are not in the business schema or inventory; the targets are those tables' own PKs.
- The PREPARE results are pending the coordinator run.

## C3 fixes (PG variants)
1. backoffice_member_access_flows.go `bo_roles` join: `app.norm_ascii(br.slug) = app.norm(ur.role)` → norm_ascii on both sides. Before, it gave different folding on the two sides.
2. backoffice_middleware.go (session load): the same join with the superadmin CASE → norm_ascii on both sides.
3. backoffice_comida_categories.go comidaCategorySlugTaken: `app.norm(COALESCE(food_type,'')) IN (?,…)` → each element is `app.norm(?)`.
4. stripe_connect_fees.go: `app.norm_ascii(setting_key) IN (?,?,?)` → `app.norm_ascii(?)` elements.
5. backoffice_premium.go (owned domains): `app.norm_ascii(domain) IN (%s)` → the PG list uses `app.norm_ascii(?)`; the MySQL list is unchanged.
6. backoffice_fichaje.go:928 time-entry UPDATE: the PG text had an extra ")" in `CAST(trunc(...) / 60) AS integer)`, a syntax error. Now `CAST(trunc(...) / 60 AS integer)`. This came from the earlier port, before pgnative-B; it was found by a paren-balance scan of nativeSQL pairs.
7. **MySQL-side exception (stated explicitly):** backoffice_comida_categories.go comidaCategorySlugTaken. Its MySQL text was truncated at `… IN (`, missing `+placeholders+")"`, which is an SQL syntax error on MySQL. This is a port bug, present already before pgnative-B. I restored it to `IN (`+placeholders+`)`. It is the only MySQL text touched.

The paren-balance scan reported 4 more asymmetric pairs (backoffice_pos_catalog.go:143, backoffice_pos.go:538). They are intended per-dialect CAST( … ) wrappers split across fragments, not bugs.

## Build
- `go build -p 2 ./...` passes. `go vet ./cmd/pgnative-sqlcheck` passes.
- Static binaries (CGO_ENABLED=0):
  - vc-server-pgnative-static 9740831f3cb185ade641e7176a8b1edf398c399a1afca5090df14c4559e14f6a
  - pgnative-sqlcheck 350fa0881ef8281c16e11f96986475c1e8831ad13edeb2cad8f4360572152a6f
- Static lint report: docs/pgnative/pgnative-c-lint-static.tsv.

## Not verified
- PREPARE has not been run. Expect "could not determine data type of parameter" errors for untyped `$n` in some SELECT lists; these are parse artefacts, not app bugs (the app binds typed Go values).
- The 391 dynamic sites are neither prepared nor linted.
- Lint rule (a) covers `fn(x) = rhs` and `IN` with fn on the left only.
