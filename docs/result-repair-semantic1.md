# result-repair-semantic1

Model: claude-merge/claude-opus-5-5-acc2 (low).
- The worker ran no SQL, no DB access, no server, no providers and no sends.
- No tests, vet, fixtures or Playwright.
- R5, the proof data and the 731 orphan rows are untouched.
- PG stays disabled and checkout stays 503.

## 1. Manual read-only Go query QA command (built, not run)
Files:
- New: internal/api/manual_readqa.go (coordination id manual_readqa_v1)
- Edited: cmd/pad-gate-check/main.go (adds the `-read-qa` flag)

Command, for the coordinator on the isolated scratch only (parent GO 08:53):
  PADQA_BUSINESS_DSN=<env, never printed> pad-gate-check -read-qa -rid <operator rid> -date YYYY-MM-DD [-domain <host>]

What it calls: the REAL ported functions, with their actual handler SQL and Go Scan paths. No query text is copied.

| function | source | output |
|---|---|---|
| affluenceDailyRows (bookings, last 30 days) | backoffice_affluence.go:195 | row count, bookings and covers totals |
| botDayCapacityOn (bot capacity) | whatsapp_bot_executor.go:492 | limit and total |
| botLoadDateFloorsFrom | whatsapp_bot_floor.go:65 | floors and active count |
| botLoadBookingExtras | whatsapp_bot_extras_admin.go:85 | count |
| loadPOSModifierGroupsFrom (catalog) | backoffice_pos_modifiers.go:78 | count by enum `kind` |
| loadStockBOM (stock) | backoffice_stock_recipes.go:105 | recipe and component counts |
| restaurantDomainOwner, lock=false (only with -domain) | domain_ownership.go:32 | found / owner_is_rid |

Read-only enforcement, in layers:
1. The session is opened with `default_transaction_read_only=on`. The existing `open` function refuses to continue unless `SHOW transaction_read_only` returns `on`. The pool has one connection.
2. All checks except affluence run inside `BeginTx(ReadOnly:true)`, which is rolled back at the end. Affluence runs on the read-only session before the tx, because its helper takes a `*sql.DB`.
3. A `readOnlyQuerier` guard checks every statement before it reaches the driver:
   - It refuses any statement that does not start with SELECT/WITH.
   - It also refuses statements containing INSERT, UPDATE, DELETE, MERGE, FOR UPDATE/SHARE/NO KEY, pg_advisory, set_config or nextval.
   - It refuses every Exec call.
   - A refusal is reported per function as "refused: non-SELECT statement …" and that function is not run.
   - loadStockBOM takes a `*sql.Tx`, so it gets the READ ONLY tx directly. PG itself rejects writes there.

Output is aggregate JSON only: counts and enum kinds. There are no row values, no PII, no DSN and no secrets. It asserts nothing and has no pass/fail. The human reads it and compares it with the MySQL twin.

The handlers are not called; the command uses a zero `Server{}`. Only `SetSQLDialect(Postgres)` is set in this separate process. Readiness globals are not touched and the server never starts. The package init() functions only register in-memory tools/ops (checked).

**Scope claim:** read query + Go Scan for these 7 functions only. This is NOT HTTP, auth, concurrency, writes or full handler QA. Handler QA stays at 0.

Build: `go build -p 2 ./...` OK. No SQL site text was added (the guard only inspects strings), so the inventory stays r112 (2511).

## 2. Orphan full-preservation plan (731 rows; nothing mutated, no waiver)
Counts come from docs/dbmigrate/staging-orphan-allowlist.json (schema sha 4cbbdea1…). The table name is `invoice_line_items`, not `bo_invoice_line_items`.

| child | FK | parent | MySQL action | orphan rows | child has tenant column? |
|---|---|---|---|---|---|
| bo_invoice_comments.invoice_id | fk_bo_invoice_comments_invoice | invoices.id | ON DELETE CASCADE | 1 | no |
| invoice_line_items.invoice_id | fk_invoice_line_items_invoice | invoices.id | ON DELETE CASCADE | 726 | no |
| site_builder_domain_mappings.site_id | fk_domain_site | site_builder_sites.id | ON DELETE CASCADE | 3 | no |
| site_builder_pages.site_id | fk_page_site | site_builder_sites.id | ON DELETE CASCADE | 1 | no |

What the orphans mean:
- Every FK is CASCADE, so these rows can only exist if the source ran with FKs disabled (bulk load or FOREIGN_KEY_CHECKS=0) or with a non-transactional delete. This is a historical source fact.
- **Wrong tenant cannot be inferred.** None of the four children carries restaurant_id, so tenant ownership is unknowable without the parent. Inventing a parent or tenant is forbidden.

Current visibility, from source:
- **Invoice comments and line items:** the readers join through or filter by invoice_id. These rows are invisible in the app because no invoice exists to open them.
- **Orphan pages:** reached only through a site id that does not exist, so they are invisible.
- **Orphan domain mappings:**
  - The `instatic_proxy.go:151` host fallback runs `SELECT restaurant_id FROM site_builder_domain_mappings`, but that table has no restaurant_id column in the source schema. The query fails on both dialects, and routing falls through unresolved. This is a pre-existing source bug. I left it unchanged because a "fix" would start routing mappings and needs a decision.
  - The mapping domains are still physically in the global PAD-unique domain index. They can therefore block a new claim of the same domain. Query 4 in the probe file counts this exposure.

Design options. Both change active visibility or constraints, so **both need user approval**:
- **A. Archival quarantine (recommended technical default):**
  - Create `dbmigrate_quarantine.<child>` tables with the exact original column types and bytes, plus provenance (source table, PK, source dump sha, R5 hash, FK name, reason, import run id).
  - Move the 731 rows there during a FUTURE import, in the same transaction as the FK VALIDATE.
  - Live tables then get VALID FKs.
  - Admin-only read views keep the history visible.
  - Restoring is a plain INSERT…SELECT back, if a parent ever reappears.
  - Cost: the domain claims held by the 3 mappings are released, so those domains become claimable. Approval must cover that.
- **B. Keep the rows in place:**
  - The live FK stays NOT VALID (which is currently forbidden: no waiver), or the parent relationship is replaced by surrogate "historical parent" placeholder rows. That second route invents data and is rejected.
  - A compatibility view would hide the orphans from readers.
  - B keeps FK integrity unproven, so it is not a cutover-ready design.
- Not acceptable: dropping rows, inventing parents, or an automatic waiver.

Coordinator classification probes: docs/dbmigrate/orphan-classify-semantic1.sql. These are read-only, aggregate COUNTs only, and the same text runs on MySQL and PG:
1. totals per group;
2. distinct missing parent ids;
3. missing ids above the current max id (never existed) vs inside the range (deleted);
4. domain collision exposure, active/verified mapping status and orphan is_home pages;
5. a manual comparison against older backup inventories, for restorable parents only.

## 3. Numeric ENUM input adapter (research)
MySQL (documented in the ENUM manual):
- A number stored into an ENUM column is treated as a 1-based index; 0 is the empty error value.
- A string that matches no label but looks like a number is also used as an index. In strict mode an invalid index or label is an error.
- Comparisons use the column collation and PAD SPACE.

PostgreSQL:
- Native enum input is exact, case-sensitive label matching, and an unknown label raises 22P02.
- There is no hook to make a type's input function accept ordinals without a custom C extension. Implicit casts from int to enum cannot reproduce "string that looks like a number".
- Changing columns to text is ruled out.

Go caller catalog:
- The 113-column audit shows no in-repo writer binds a number to an enum: all are canonical labels, fixed labels or validated input.
- So an adapter is needed only for EXTERNAL or raw-SQL writers. The gate stays.

Proposal (not applied, no DDL file emitted, needs review). One explicit function per enum type, called in writer bindings only:
`dbmigrate.enum_in_<type>(v text) RETURNS <type>`. It would:
1. rtrim ASCII space from v;
2. match a label using ci_ai and return the stored canonical label;
3. otherwise, if v is all digits n with 1 ≤ n ≤ label count, return the nth label in declared order (`enum_range`);
4. otherwise RAISE 22P02.

What it does not cover:
- It cannot intercept external writers that do not call it.
- It does not reproduce MySQL non-strict empty-string storage (source mode is STRICT_TRANS_TABLES, so that is an error there too).
- Production's saved sql_mode differs from the local restore, which is another reason the gate stays.

## 4. LIKE / ci_ai accent folding (largest remaining gate), technical plan
Facts:
- Source columns use unicode_ci, general_ci (PAD SPACE, UCA 4.0 / simple folding) and 0900_ai_ci (NO PAD).
- PG uses ICU und-u-ks-level1, nondeterministic.
- Already observed: ß=ss is TRUE in PG ci_ai, while general_ci treats ß=s. Trailing space is FALSE in PG.

Options:
1. Per-column custom ICU collations tailored to each source collation. They still cannot reproduce UCA 4.0 contractions or ignorables exactly.
2. LIKE via `unaccent` + lower. unaccent is available but not installed. It is NOT exact: it handles no ß/æ expansions consistently with MySQL and no ignorables.
3. Keep LIKE on ci_ai where PG17 supports nondeterministic LIKE, and report the differences per column.

Next step: run docs/dbmigrate/collation-matrix-semantic1.sql. It holds human SELECTs on literal codepoints only: ASCII case, á, ñ, ß→ss/s, trailing space and tab, embedded NUL (MySQL only; PG text cannot hold NUL), combining acute, æ, LIKE with accent and trailing space. It has no table data, no fixtures and no assertions. Then pick a per-column choice from the observed matrix. Nothing here claims unaccent or ICU is exact.

## Gates still open
- R5 untouched.
- 1117 original pending; 731 orphans need approval of A/B.
- PG server disabled; checkout 503.
- 0 handler QA; read-qa has not been run yet.
- ci_ai is an approximation, and LIKE is open.
- Numeric/external enum input is gated.
- Shadow FKs blocked by the orphans.
- Replay payload comparison pending.
- Unsigned generator range.
- instatic mapping restaurant_id source bug (decision).

## Next
1. Coordinator runs `-read-qa` for one rid/date on PG scratch and compares it with MySQL.
2. Run the orphan probes and the collation matrix.
3. User decides between orphan options A and B.
Order after that: full PG verified, then the functional bot, then GraphRAG. Production read-only.
