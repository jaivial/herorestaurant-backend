# result-native-wa6 — WhatsApp connection and config files (35 sites)

**Nothing here is runtime-verified (0 sites); it is a syntax port only. PG is not ready (ready:false).** MySQL stays the default, and the server still refuses `DB_DRIVER=postgres`. SQL code only: no provisioning, server-admin, webhook, watchdog, provider or network path was run. No tokens, keys or `.env` values were read or printed.

## Scope: 35 sites, all 6 files complete
| File | Sites |
|---|---|
| `backoffice_uazapi_provisioning.go` | 17 |
| `backoffice_uazapi_servers_admin.go` | 4 |
| `whatsapp_bot_ai_config.go` | 4 |
| `whatsapp_bot_admin.go` | 4 |
| `whatsapp_connection_watchdog.go` | 3 |
| `whatsapp_bot_webhook.go` | 3 |

## Build and inventory
- `go build -p 2 ./...` gave EXIT=0, under the inherited CPU 200% / 6G limits. No tests, vet, fixtures or Playwright.
- Inventory: `/tmp/villacarmen-bot-20261111/sql-inventory-r52.json`.
  - Totals: canonical 2475 = native 1672 (**includes 4 provisional**) + blocked 3 + unwrapped 800. Runtime-verified: 0.
  - Other figures: dynamic 116; portable audit failures 0.
- Count change from r51: native +35 (one of them provisional), unwrapped −35.
- Lexical check: clean apart from one intentional arity flag. The `provisioning.go:322` PG half binds the merge patch twice, and `recordWebhookArgs` supplies the extra argument.
- Ledger: `docs/dbmigrate/native-ledger-whatsapp.json`.
  - WhatsApp domain: 197/316 ported, 119 remaining (21 dynamic), 20 files complete.

## New provisional gate (not source-equivalent)
**`backoffice_uazapi_provisioning.go:637`** is an upsert (`ON DUPLICATE KEY`) into `restaurant_uazapi_instances`.
- The table has **two** unique keys: `uniq(restaurant_id)` and `uniq(instance_name)`.
- **MySQL:** updates whichever row collides first. On an `instance_name` collision that can be another restaurant's row, i.e. a cross-tenant overwrite.
- **PG variant:** `ON CONFLICT (restaurant_id)`, which raises an error on an `instance_name` collision instead.
- The name is normally `nv-<rid>-<unixnano>`. It differs only when the provider returns its own name.
- It is listed in `provisional_gate_list` and is not claimed equivalent.

## Semantics (compile-only)
### JSON
- **`JSON_MERGE_PATCH`:** the PG twin is `CASE WHEN jsonb_typeof(m)='object' THEN m || patch ELSE patch END`.
  - This matches RFC 7396 only for the patch actually sent here: a flat object of non-null scalars (`webhook_url`, `webhook_applied`, `webhook_synced_at`).
  - jsonb text normalisation needs QA.
- **JSON columns:** written with `CAST(? AS jsonb)` and read with `CAST AS text`.

### Upserts
Each `ON CONFLICT` target is the table's primary key, which is its only unique key:
- `restaurant_integrations`
- `whatsapp_bot_config`
- `restaurant_ai_provider_keys` (restaurant_id, provider)
- `restaurant_bot_ai_config`

### Time
- `updated_at` and `created_at` (timestamptz) use the truncated `statement_timestamp()`.
- The naive `last_seen_at` and `connected_at` use the UTC wall clock.
- Explicit `updated_at` assignments are kept.

### Changed rows
- **Reactivate** (`found = affected > 0`): MySQL counts only changed rows, so an already-active row touched within the same second reports 0. The PG twin adds an `IS DISTINCT FROM` predicate on (`is_active`, `status` with collation C, `updated_at`) to keep that.

### Other statements
- **Server choice:** the DECIMAL ratio gets `ROUND(…, 8)`, which is the MySQL result scale (4 plus `div_precision_increment` 4).
- **Runtime status update:** the `CASE` placeholders get explicit text/integer casts on PG.
- **`uazapi_servers` insert:** `InsertID`. The duplicate-`base_url` detection (`isSQLDuplicateError`) also matches the PG message.
- **`uazapi_servers` dynamic UPDATE:** the jsonb cast and `updated_at` are per-dialect fragments, rebound once.

### Open gaps
- **Webhook resolution:** the `instance_token` / `connected_phone` lookups use `LIMIT 1` on non-unique columns, so the row is arbitrary on both engines. CI/PAD SPACE comparison on the token is open.

## Gates (all unwaived)
- **Locking gaps:**
  - Config before-state race on absent rows (gap locks).
  - Reference-row gap locks.
  - Duplicate-create race: the advisory-lock candidate would have to be taken by every writer.
  - Document insert vs cancel: relies on the booking row lock; unverified.
- **Proposal hashes and backups across the cutover:** need byte QA plus a drain-or-rehash plan.
- **TIME range:** MySQL ±838 h vs PG 24 h.
- **NUL / JSON:** NUL bytea QA; jsonb `\u0000` silently dropping audit rows.
- **Email:** bounce invalid UTF-8.
- **UUID:** POS `UUID()` v1; registrar UUID.
- **Multi-key upserts:** SEO; instatic; and now the uazapi instances upsert.
- **Wiring:** analytics dialect.
- **General QA:** numeric, JSON and IDs.
- **Import proof:** 1117 pending, ready:false; 4 NOT VALID FKs with 731 orphans.

**Source facts:**
- `modification_history` has no `restaurant_id` — confirmed in production.
- Still dump-only: the missing website columns, `restaurants.phone`, `group_menus`, `wines`.

## Next coherent checkpoint, 33 sites: conversation storage and outbox
| File | Sites |
|---|---|
| `whatsapp_bot_context_sqlite.go` | 16 (2 dynamic, bot_context schema / NUL adapter path) |
| `whatsapp_outbox.go` | 9 (claim, locking and retry semantics) |
| `conversation_history.go` | 8 |

`whatsapp_bot_rag.go` (8 sites, with its own BeginTx transaction) follows with `whatsapp_bot_media_ops` 7 and `menu_tools` 7.

## No actions taken
No SQL execution, DB writes, activation, providers, payments, email, WhatsApp, sends, `.env`/proof edits, commit, merge or deploy.

