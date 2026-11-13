# result-native-assistant2 — assistant checkpoint B (assistant domain finished)

**Nothing here is runtime-verified (0 sites); it is a syntax port only. PG is not ready (ready:false).** MySQL stays the default, and the server still refuses `DB_DRIVER=postgres`.

## Scope: 24 sites
- `assistant_ws.go` 10, `assistant_pos_tools.go` 5, `assistant_confirmation.go` 4, `assistant_memory.go` 4, `assistant_llm.go` 1.
- 23 sites are syntax-ported. 1 is blocked: `assistant_pos_tools.go:68` (pos_ticket_create uses SQL `UUID()` v1).
- **Assistant domain total:** 56 sites in 8 files: 55 ported + 1 blocked. Everything is done except the blocked site.

## Build and inventory
- `go build -p 2 ./...` gave EXIT=0, under the inherited CPU 200% / 6G limits. No tests, vet, fixtures or Playwright.
- Inventory: `/tmp/villacarmen-bot-20261111/sql-inventory-r46.json` (dbmigrate-r14).
  - Totals: canonical 2475 = native 1475 (includes 3 provisional) + blocked 3 + unwrapped 997. Runtime-verified: 0.
  - Other figures: dynamic 147; portable audit failures 0; helper-internal 9 and prepared-statement executions 3 excluded.
- Count change from r45: native +23, blocked +1, unwrapped −24.
- Ledger: `docs/dbmigrate/native-ledger-assistant.json` (updated).

## Changes
### Confirmation tokens
- `NOW()` becomes `date_trunc('second', statement_timestamp() AT TIME ZONE 'UTC')`, because `expires_at` is a naive UTC column.
- The bound expiry `time.Now().Add(ttl)` goes through `sqldb.NaiveUTCArg`.
- **Single use under concurrency (code review only):** the consume DELETE is unchanged. On PG, a second concurrent DELETE blocks on the row lock, re-checks after the first commits and deletes 0 rows. That is the same outcome as InnoDB. It is a single statement, so it does not depend on the isolation level.

### Memory
- The two MySQL upserts (`ON DUPLICATE KEY`) become `ON CONFLICT` on the only collidable unique key in each table:
  - nodes: `uq_memory_scope_label` (restaurant_id, user_id, label);
  - edges: `uq_edge` (from_node, to_node, rel).
- The `weight` increments are qualified with the table name.
- `updated_at DESC` gets an explicit `NULLS LAST`, matching MySQL's NULL ordering.

### POS tools
- **Visit create:** `CURDATE()` becomes the UTC statement date, and the id comes from `sqldb.InsertID`.
- **Payment and refund create:** `INSERT…SELECT` with `InsertSelectID`. If the ticket does not exist, the id is 0, as on MySQL. The PG select-list parameters are cast explicitly, because PG has no assignment cast from text to an enum or bigint.
- **Ticket create: blocked on PG.** `UUID()` v1 has no PG core equivalent. On PG the tool returns an explicit error.

### WS and sessions
- Session creation uses `InsertID`.
- `role` is read with `CAST AS text`.
- The user and assistant message inserts and the history read keep `content` as `[]byte`, so PG bytea preserves exact bytes. These are the only application callers of `assistant_messages` in `internal/` and `cmd/`.

### LLM prompt
- The `name, phone` read stays portable and keeps the source anomaly. `restaurants.phone` is absent in the restored dump, so the query fails and the prompt falls back to the base text.

## Questions from the last report: answered from code, no secrets read
- **Wine price text: resolved.**
  - go-sql-driver v1.9.3 (`packets.go`) returns `float32` for FLOAT columns with **both** the text protocol (`ParseFloat`, 32-bit) and the binary protocol (`Float32frombits`).
  - `database/sql` then formats that value with `FormatFloat('g', -1, 32)`.
  - pgx's Float4Codec returns `float64(float32)`.
  - Both dialects now scan into `any`, and `float32SQLText` reproduces the source text exactly. The `interpolateParams` setting turned out not to matter. The float(10,2) storage-rounding issue is still a proposal only.
- **Space-only input to `NULLIF`: grounded.**
  - `internal/db/mysql.go` builds the DSN in code: `parseTime=true&charset=utf8mb4&collation=utf8mb4_unicode_ci&multiStatements=true`. It has no `interpolateParams` and no `loc`, so the defaults (false, UTC) apply.
  - The connection collation is therefore utf8mb4_unicode_ci, which is PAD SPACE. It is not the driver default utf8mb4_general_ci.
  - On PG, the catalog update (generic and wine) and `audio_url` bind `''` for space-only input via `padSpaceNullIfArg`. The MySQL path is unchanged.
  - Remaining gap: characters with zero weight in UCA 4.0 compare equal to `''` on MySQL only.
- **LastInsertId errors:** v1.9.3 `mysqlResult.LastInsertId` always returns `nil` as the error. So `sqldb.InsertID` behaves identically on the MySQL path, where the source previously ignored that error.
- **No production metadata needed** for either question; the facts above come from the code and the driver source.

## Open semantics (unwaived; separate from the 1117 import-proof count)
- **Search:** LIKE is case-insensitive only, without accent folding or PAD SPACE.
- **Rounding:** float to decimal/bigint at half-way values needs QA.
- **Invalid date text:** PG raises an error where MySQL does not.
- **Case/accent/trailing-space comparisons:** results differ for the status filters and the varchar ORDER BY.
- **jsonb in audit rows:** jsonb rejects `\u0000`, and those audit writes ignore errors, so such a row is lost on PG.
- **JSON text normalisation** needs QA.
- **Products top-20:** tie ordering.
- **Memory unique keys:** `label`/`rel` uniqueness on `ci_ai` has accent/PAD deltas.
- **Source anomalies (restored dump only, not checked against production):** `group_menus`, `wines`, `restaurants.phone`.
- **NUL bytea adapter:** runtime QA still pending.

## Gates (all unwaived)
- **New:** pos_ticket_create `UUID()` is blocked on PG.
- **Site domain:** SEO two-step upsert (not atomic, assumed key priority); registrar first-site choice and UUID v1→v4; 2 instatic sites blocked; analytics dialect wiring.
- **Email:** bounce invalid-UTF-8 suppression.
- **Import proof:** 1117 pending, ready:false; 4 NOT VALID FKs with 731 orphans.
- **General semantics:** CI, accent and PAD SPACE; FULLTEXT; 190 range narrowings; 136 ON UPDATE tables and 353 defaults; the broken view; NUL adapter QA; numeric, JSON, ID and locking QA.
- **Isolation:** MySQL RR vs PG RC default.

## Transaction and interface audit
- txaudit on all 8 assistant files found no `BeginTx`, no transaction parameters and no querier interfaces.
- The transaction-interface callers that the earlier crossaudit did not cover are all in the WhatsApp domain:
  - `whatsapp_bot_config_admin.go`: `botConfigMesasBefore` / `botConfigSalonCondesaBefore`, which take a `forUpdate` flag;
  - `whatsapp_bot_groupmenu_admin.go`: `botGMenuQuerier`;
  - `whatsapp_bot_special_admin_tools.go`: `botRestoreSource*`, which takes a `lock` flag;
  - `BeginTx` in `whatsapp_bot_rag.go`, `whatsapp_bot_special_admin_tools.go` and `whatsapp_bot_special_date_adelanto.go`.

## Domain coverage (r46)
| Domain | Sites | Status |
|---|---|---|
| core | 406 | complete |
| bookings | 161 | complete |
| catalog | 504 | complete |
| campaigns | 64 | complete |
| members | 84 | complete |
| site_analytics | 203 | 2 blocked |
| assistant | 56 | 1 blocked |
| whatsapp_bot | 316 | 0 done |
| pos_billing | 564 | 0 done |
| tools_cmd | 57 | 0 done |
| migration_tooling | 55 | 0 done |
| db_runner | 5 | 0 done |

## Next: WhatsApp checkpoint A, about 37 sites
- `whatsapp_bot_executor.go`: 22 sites, 1 dynamic.
- `whatsapp_bot_admin_reads.go`: 15 sites, 1 dynamic.

Later WhatsApp groups:
- `whatsapp_bot_special_admin_tools.go` (52 sites, 11 dynamic, has transactions and lock interfaces) needs its own two checkpoints.
- Other files: `backoffice_uazapi_provisioning` 17, `whatsapp_bot_context_sqlite` 16, `whatsapp_bot_config_admin` 13 (6 dynamic, interface querier), and so on.

## No actions taken
No SQL execution, DB writes, activation, providers, payments, email, WhatsApp, sends, `.env` reads or edits, proof edits, commit, merge or deploy.

