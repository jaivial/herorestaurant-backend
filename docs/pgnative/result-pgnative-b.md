# pgnative-B — native PostgreSQL code (source/build only)

Authority: Main 11:56/11:57 (native PG pivot). This is the worktree villacarmen-bot-pgnative-20261113 only. Nothing was run against any DB. No tests were written or run.

## B1: pgnative-gate unique-index check
- The gate now uses `internal/db.CheckNativeKeyIndex`, shared with the server start gate. The index must:
  - exist in current_schema()
  - be on the expected table
  - be unique, valid, ready and non-partial
  - have a key list equal to the expected one after `NormIndexDef`, which strips quotes, parentheses, `::type` casts, whitespace and `public.`, and lowercases.
- Example: `app.norm_ascii((session_id)::text)` and `app.norm_ascii("session_id")` both reduce to `app.norm_asciisession_id`.
- It still fails on a missing, non-unique, invalid or partial index, and on a different key list.

## B2: removed emulation
- **Deleted** (moved to /tmp/pgnative-b-removed/, nothing rm'd):
  - internal/mysqlcoll, internal/third_party/vitess
  - cmd/wk-backfill, cmd/collkey-compare
  - internal/api/wk_keys.go, wk_bounds.go, manual_wkqa.go
  - internal/analytics/wk_keys.go
  - internal/db/wk_gate.go, padkeys.go, padshadow.go
- **Mechanical collapse to the flag-off path** (generated, scripts /tmp/pgb1.py, pgb2.py):

  | construct | sites |
  |---|---|
  | `wkPG(off,on)` → off text | 366 |
  | `wkKeyArgs` | 203 |
  | `wkKeyArg` | 154 |
  | `wkSupArgs` | 6 |
  | `wkPosCatArgs` | 2 |
  | `wkSetItems` lines | 8 |
  | `wkArgs` sites | 5 |
  | `if …wkOn()… {}` blocks dropped (flag-on LIKE post-filter, Go TitleJoin, etc.); else branches kept | 29 |
  | `ErrTooBroad` handlers removed | 2 |
  | gmJoinComidaItems (Go join) removed | 1 |
  | sheetListFrom id-set parameter removed | 1 |

- **ci_ai → native** (script /tmp/pgb8.py): 467 expression rewrites in SQL literals of 81 files.
  - Lookups: `(rtrim(col,' ') COLLATE dbmigrate.ci_ai) = (rtrim(?,' ') COLLATE …)` → `app.normX(col) = app.normX(?)`.
  - ON CONFLICT targets: `((rtrim(col,' ')) COLLATE dbmigrate.ci_ai)` → `app.normX(col)`.
  - The bare forms `col COLLATE dbmigrate.ci_ai` and `CAST(enum AS text) COLLATE …` → `app.normX(...)`.
  - Which function: app.norm vs app.norm_ascii is chosen per table+column with the same rule as the DDL generator (dbmigrate.NormFunc: general_ci or ASCII identifier name → norm_ascii). The parameter side always gets the column side's function.
  - Notes (7) are in docs/pgnative/pgnative-b-norm-choice-notes.tsv: expressions such as `COALESCE(food_type,'')` and the role CASE, plus probe/text cases defaulted to app.norm, and 1 ambiguous case (invoices status, enum → app.norm).
- **ON CONFLICT check** (docs/pgnative/pgnative-b-on-conflict-check.tsv): of 162 targets, 49 match a ux__ inventory expression exactly and 101 match a raw unique key or PK. The 12 listed as no-match were each reviewed and are not real failures:
  - camelCase quoted `"reservationDate"` columns (a check-script artefact)
  - SQLite bot tables
  - a comment
  - pos_products, misattributed by the script
- **8 targets pointed at PK expressions** that have no unique index in the native schema. These are now the raw PK columns: pos_daily_sequences, pos_role_permissions, bo_role_permissions, stock_role_permissions, platform_settings, restaurant_ai_provider_keys, and whatsapp_bot_sessions ×2.
- **ORDER BY** (script /tmp/pgb13.py): 35 user-visible name/title-like ORDER BY items in PG-only literals (2nd argument of nativeSQL/sqlFragment) now carry `COLLATE "es-x-icu"`, across 22 files. One accidental GROUP BY change was reverted (stock_suppliers:377). Other ORDER BY on text now sort by the database's default collation.
- **23505 → 409.**
  - `sqldb.IsUniqueViolationOn` matches `ux__<pg.TableName>__<mysqlKey>` (`sqldb.NativeUniqueName`, same 63-byte hashing as dbmigrate.PGName). The ixpad/ixwk name arguments were removed from 5 callers.
  - backoffice_premium: area/table create/update errors use `boPremiumDBStatus(err)` → 409 on a unique violation (8 sites). `isSQLDuplicateError` also uses `sqldb.IsUniqueViolation`.
- **Start guard.** internal/db/business.go now runs `RequireNativeKeys` (no `__wk`/`__pad`/dbmigrate-collation columns, app.norm* IMMUTABLE, all 117 ux__ indexes OK) instead of the PAD/shadow/`__wk` gates. The import marker and ON UPDATE gates and DB_PG_TOOLS_QA_APPROVED are unchanged. The waiver `shadow-fk` is still accepted but is now a no-op. pad-gate-check runs business.native_keys; its -wk-qa and -ddl modes were removed. qa-seed-scratch no longer writes `__wk`.
- **Kept fixes.**
  - fiscal full_number: key value gone, the plain insert remains.
  - pos_payments/pos_refunds idempotency inserts and ON CONFLICT targets via app.norm_ascii (see the on-conflict check TSV).
  - nextPOSTicketNumber: `regexp_match(ticket_number, …)`, now without COLLATE "C".
  - backoffice_stock_documents.go:680 untouched (documented bug). Site-builder domain checkout untouched.
- **dbmigrate (generator).** `native-convert` now saves, drops and re-adds the STORED generated columns of tables that still have dbmigrate-collated columns around the collation ALTER (the generalised pos_shifts.open_terminal_key fix). On an already converted scratch this step is a no-op. A fresh import needs no such step, because columns are created with the default collation.
- **Counts.**
  - 648 SQL literals changed across 120 files (docs/pgnative/pgnative-b-changed-queries-per-file.tsv).
  - Outside internal/dbmigrate (which must still name ci_ai to convert it), internal/db/nativekeys.go and cmd/pgnative-gate (both detect leftovers), grep finds 0 matches for wk*/`__wk`/ci_ai/mysqlcoll/vitess/ixwk/ixpad in Go.
  - go.mod/go.sum: `go mod tidy` ran offline (GOPROXY=off) and changed nothing; the vendored code used only std + already-required modules.

## Build
- `go build -p 2 ./...` passes, and `go vet` passes for internal/sqldb, internal/db, cmd/pgnative-gate, cmd/pad-gate-check and cmd/qa-seed-scratch. MySQL paths are unchanged: only PG texts, PG args and helpers were touched.
- sha256 (static, CGO_ENABLED=0):
  - vc-server-pgnative b2c132138cbdec55a9a313dfb4bb612918501f1b0c983483644f75e54756c5c8
  - qa-http 73d1a14fc43d31b25bc7f41e9ff4548715a22ef6effb40974f55f475d657a51e
  - qa-seed-scratch 002d48977827240426607a7b50c8ed2c5e46560ff042c0e7b3d2b27f3c8fd557
  - pgnative-gate 37351d9195bd2e7cee1e5e54e26ec6424eec60d0ddd9d55f9656149c014bbf1d
  - dbmigrate-pgnative 3c260a8d69cdc07cd1e8c638d8bda2872b5e409ffaf93b73be882795d2b6e23c
  - pad-gate-check cd8751ccb9620e3256e1709e8f6b2b8ae40a5075b23ca22368836624d2b24cad

## Divergences from MySQL (native by decision)
- Text PKs (bo_roles.slug, platform_settings.setting_key, whatsapp_bot_sessions.user_phone, …) are byte-equal unique. app.norm lookups on them work, but uniqueness is not case/accent-folded.
- Searches that used the flag-on post-filter now use the flag-off PG text `LOWER(col) COLLATE "C" LIKE lower(?)`: case-insensitive but not accent-insensitive. The 'search too broad' 422 is gone. Possible next step: `app.norm(col) LIKE app.norm(?)`.
- Empty-string PAD checks (`rtrim(x,' ') COLLATE "C" = ''`, 13 sites) were kept as value semantics, not keys.
- The group-menu comida join now runs in SQL with the default collation, instead of the Go unicode_ci TitleJoin.
- ORDER BY outside the 35 rewritten items uses the default DB collation.

## Not verified
- No DB execution of any changed query. ON CONFLICT targets were checked only statically against the inventory, and `NormIndexDef` only against the rendering example given (`app.norm_ascii((session_id)::text)`).
- The `COLLATE "es-x-icu"` collation must exist in the PG build; with ICU it is standard.
- The 7 norm-choice notes need review.
- The 12 ON CONFLICT no-match rows were reviewed by eye.
- No HTTP QA; the binaries are built but have not been run.
