# result-repair-vitess6

Model: claude-merge/claude-opus-5-5-acc2 (low).
- Build only. No app wiring, no activation, no SQL writes or SQL run, no network, no Playwright.
- go.mod and go.sum are unchanged (cmp OK).
- **Rule slip:** I invoked `go vet ./cmd/collkey-compare` once by mistake. It aborted on a syntax error before doing any analysis, and it was not repeated.

## 0. Your 12:35 run, re-read from the jsonl (aggregates only)
- 140 columns, 15,722 rows, 14,925 non-NULL.
- 0 invalid UTF-8, 0 key_vs_mysql_weight mismatches, distinct_go_key = distinct_mysql_eq everywhere.
- raw_set_digest is equal for 140 of 140; 0 errors. The maximum key length is 210 bytes.

Caveats:
- **68 of 140 columns have 0 rows**, so stored data proves nothing for them.
- raw_weight_equal = 14,925 = every non-NULL value. No stored value carries a trailing-space weight, so the PAD strip path is **not exercised by the stored data**. Only the literal samples (vitess2 and 11:50) exercise it.

## 1. collkey-compare extensions (build OK, static binary /tmp/villacarmen-bot-20261111/collkey-compare)
**Composite PKs.** The pk field accepts `a,b,c`:
- MySQL: `CONCAT_WS(CHAR(31), CAST(a AS CHAR), …)`;
- PG: `concat_ws(chr(31), "a"::text, …)`;
- each part is identifier-validated.

Equal text form is expected for int, varchar and ISO date (pos_daily_sequences.business_date relies on PG DateStyle ISO); other types are unverified.

**`-check-metadata`** (MySQL session only, the same read-only tx):
- For each cols.txt line it reads `information_schema.COLUMNS` joined with `COLLATIONS` (collation, PAD_ATTRIBUTE) and `STATISTICS` (PRIMARY column order).
- Output: one JSON object with columns, match, and lists of column NAMES only (metadata, not row data) for missing, collation_diff, pad_diff and pk_diff.

**New column files:**
- `/tmp/villacarmen-bot-20261111/collkey-cols-vitess6.txt`: all 149 columns, single and composite PKs.
- `…-vitess6-composite.txt`: the 9 previously unresolved columns:
  - whatsapp_bot_sessions.user_phone
  - bo_role_permissions.role_slug and .section_key
  - pos_daily_sequences.sequence_type
  - pos_role_permissions.role_slug and .permission_key
  - restaurant_ai_provider_keys.provider
  - stock_role_permissions.role_slug and .permission_key
- All 9 are unicode_ci PAD per the recorded metadata. To verify live, run `-check-metadata` first.

**Coordinator commands** (same docker cp/exec pattern as vitess5; DSN from env):
- MySQL: `ckc -check-metadata -columns cols-vitess6.txt`, then `ckc -columns cols-vitess6-composite.txt`.
- PG: `ckc -columns cols-vitess6-composite.txt`.
- Then compare the digests.

## 2. __wk key helper (internal/mysqlcoll/wk.go, mysqlcoll_wk_v1; not wired)
- `WKColumn(col)` returns `col__wk`.
- `(*Coll).WriterKey(*string)` returns the key bytes. NULL text gives a NULL key; invalid UTF-8 is an error, so a writer must fail and never store an empty key. Every writer must set `col` and `col__wk` in the same statement.
- `CheckStoredKeys(ctx, q, coll, table, col)`:
  - PG only, read-only; the caller passes a READ ONLY tx;
  - identifier-validated;
  - streams `col::text` and `col__wk` and returns counts: rows, null_both, null_mismatch, invalid_utf8, key_mismatch;
  - `Ready()` is true only if every mismatch count is 0;
  - this is the readiness check that catches external or raw-SQL writers bypassing the helper.
- **DDL proposal:** `docs/dbmigrate/wk-ddl-proposal-vitess6.sql`. NOT applied, and wrapped in BEGIN … ROLLBACK.
  - 149 `ADD COLUMN <col>__wk bytea` statements.
  - 134 `CREATE UNIQUE INDEX ixwk__…` statements, one per ixpad entry in padkeys.go. PAD parts become `__wk` and plain parts are unchanged. Names are unique and at most 62 characters.
  - Primary keys stay raw as FK targets.
  - Order for any future scratch apply: add the columns, backfill with a reviewed Go tool using WriterKey, check readiness (0 mismatches), create the indexes, switch callers and ON CONFLICT to `__wk`, then retire ixpad.
- Not done yet:
  - the backfill tool;
  - the caller switch;
  - the startup gate (RequirePadKeyIndexes would become an ixwk gate plus CheckStoredKeys);
  - the composite-key collision semantics for entries whose PAD parts include site_id (site_builder_pages, collections, versions). These are kept as `__wk` exactly as the ixpad Pad flag says.

## 3. LIKE post-filter (internal/mysqlcoll/like.go, mysqlcoll_like_v1; not wired)
- `Bound(maxPerTenant, min)` = max(4×measured, min).
- `LikeAny(rows, bound, matchers, fields)`:
  - the source shape `a LIKE ? OR b LIKE ? …`;
  - a NULL field never matches;
  - returns ErrTooBroad when there are more rows than the bound, and never truncates.
- `SortByKey(rows, coll, col, tie)`: MySQL ORDER BY under the collation key, NULLs first (ASC), with a deterministic tie-break for pagination.
- `(*Coll).TitleMatches(title, nombre)`: the group_menus.go:940 predicate.
  - `=` via Equal;
  - LOWER=LOWER via the collation's own case map (`colldata.CaseAwareCollation.ToLower`), then Equal;
  - `LIKE nombre+' (+%'` with nombre NOT escaped, preserving the source wildcard quirk.
- `(*Coll).TitleJoin(titles, nombres, bound)`: the two-query LEFT JOIN.
  - It returns (dish, item) index pairs in dish order, then item order, with -1 for no match (NULL ci fields).
  - The caller runs query 1 (dishes without the ci join, the existing ORDER BY) and query 2 (the tenant's platos comida_items ordered by id), then maps the pairs to the existing row assembly.
  - Tie order between matches follows item id: a documented choice, because it is engine-arbitrary in the source.
- Wiring per site (group A list, vitess4) is NOT done. Each site needs:
  - the PG prefilter without the LIKE clause and without LIMIT;
  - the bound, from measured `max_rows_per_tenant`;
  - Go count and pagination;
  - an explicit error mapping for ErrTooBroad.

## Gates
- Live metadata check and composite run: pending, coordinator.
- PAD strip path: literal-only proof.
- 68 empty columns are unproven on data.
- GPL/provenance: open.
- `__wk`: DDL, backfill and gate unapplied; LIKE sites not wired.
- REGEXP is an approximation.
- PG disabled, checkout 503, 0 handler QA, 1117 pending, 731 orphans, R5 untouched.
