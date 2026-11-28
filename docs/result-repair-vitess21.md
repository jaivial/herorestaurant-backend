# result-repair-vitess21

Model: claude-merge/claude-opus-5-5-acc2 (low).
- Source and build only. Nothing was run.
- No tests or Playwright, no SQL writes, no activation, no network.
- go.mod and go.sum are unchanged.
- `go build -p 2 ./...` OK with /home/linuxbrew/.linuxbrew/bin/go.
- Backup: /tmp/v21-pre.bak.tar.
- backoffice_stock_documents.go:680 is untouched.

Input (16:34): the QA server ran inside scratch, and healthz, getActiveMenusForDisplay, getMenuForDisplay 1-3, searchPostres and getAllMenus returned 200 from PG. But the log said `wa_bot_store_pg_v1 driver=sqlite postgres=false`.

## 1. Bot context store selection: fixed
**Cause.** `newBotConversationStoreFor` (internal/api/whatsapp_bot_context_sqlite.go) used `cfg.BotContextDriver`, i.e. `BOT_CONTEXT_DRIVER`, which **defaults to "sqlite"** (internal/config/config.go:210), independently of `DB_DRIVER`.
- Under `DB_DRIVER=postgres` with `env -i`, the store therefore opened SQLite at the default `./data/whatsapp-bot-context.sqlite`. It probably created that file and directory inside the container's working directory, so delete it there if present.
- This violated the "all PostgreSQL" requirement.

**Fix.** With `DB_DRIVER=postgres`, the bot context store is now **always PostgreSQL**:
- DSN = `BOT_CONTEXT_POSTGRES_DSN`, or `DATABASE_URL` when unset (the unified DB);
- schema = `BOT_CONTEXT_POSTGRES_SCHEMA`, or `bot_context` (`sqldb.SchemaBotContext`) when unset;
- an explicitly set non-postgres `BOT_CONTEXT_DRIVER` (e.g. `sqlite`) is **refused**, so `NewServer` panics and startup fails closed;
- no SQLite fallback.

MySQL runs are unchanged (the BOT_CONTEXT_DRIVER behaviour as before). The panic text now says "bot context store" instead of "SQLite".

## 2. bot_context on scratch and startup behaviour (from code and docs; no SQL run)
**What scratch holds** (coordinator 09:05 and the wiring2 docs): the `bot_context` schema holds exactly the 7 SQLite-origin tables: bot_knowledge_fts, bot_store_meta, chat_images, conversation_messages, handoff_state, management_requests, pipeline_decisions. The MySQL-origin bot tables are in `public`, and `botStateDB()` uses the business pool for them.

**What startup does in PG unified mode** (botstore.OpenSchema, Init, RequireMigrated):
1. `CREATE SCHEMA IF NOT EXISTS bot_context`, then `Init` with `postgresSchema[:postgresContextStmts]`: `CREATE TABLE/INDEX IF NOT EXISTS` for the context tables only. These are no-op DDL on scratch, but they are DDL statements executed on scratch.
2. `RequireMigrated` requires `bot_store_meta` key `migration_verified` = 1 row in bot_context. The R5 context round-trip wrote it (`ctx-verify-unified.json`); if it is absent, startup refuses. `BOT_CONTEXT_POSTGRES_ALLOW_EMPTY=1` would bypass this and **must not be set** in QA.

**Expected log** after the fix: `[bot] checkpoint wa_bot_store_pg_v1 driver=postgres postgres=true`.

## 3. Backoffice search endpoints: auth and read-only wget lines
**Auth layers on `/api/admin/*`** (server.go:234+):
1. `requireVaultKey`: a shared secret (VAULT_KEY), a no-op when VAULT_KEY is empty.
2. `requireBearerToken`: the same pattern.
3. Then per route: `requireBOSession` (a `bo_session` cookie looked up in bo_sessions, plus section gates), or `requireAdmin` (`X-Admin-Token`/Bearer = ADMIN_TOKEN) on the legacy endpoints.

**The startup WARNING** `... empty - admin API authentication is DISABLED` (config.go:277) means only layers 1 and 2 are off.
- `requireBOSession` is still enforced. Without a valid `bo_session` cookie these endpoints return **401**.
- So the empty VAULT_KEY does **not** expose the backoffice searches.

**Reads cause writes:** `requireBOSession` **writes** on every validated request (`UPDATE bo_sessions SET last_seen_at …, expires_at …`, backoffice_middleware.go:61), and `POST /api/admin/login` inserts a session row. Backoffice QA therefore mutates scratch bo_sessions even for GETs. That is acceptable only on the disposable scratch and under the GO; it is never valid for production data.

**Session:** an existing scratch `bo_users` account whose password the operator already knows. Do not create users or credentials.
```
wget -q -O /dev/null --save-cookies /tmp/c.txt --keep-session-cookies --post-data='{"identifier":"<known scratch login>","password":"<known>"}' --header='Content-Type: application/json' http://127.0.0.1:18080/api/admin/login
```
- Busybox wget may lack `--save-cookies`. In that case read `Set-Cookie: bo_session=…` with `wget -S` and pass it back with `--header='Cookie: bo_session=<value>'`.
- Check the exact login JSON field names in handleBOLogin before use.
- The tenant is the session's active restaurant.

Set `C="--header=Cookie: bo_session=<value>"`, then the read-only GETs:
```
wget -q -O - "$C" 'http://127.0.0.1:18080/api/admin/bookings/search?name=<n>'          # wkBookingSearchIDs (general_ci)
wget -q -O - "$C" 'http://127.0.0.1:18080/api/admin/bookings/search?phone=<digits>'
wget -q -O - "$C" 'http://127.0.0.1:18080/api/admin/bookings?date=<YYYY-MM-DD>&q=<prefix>'   # list prefix search
wget -q -O - "$C" 'http://127.0.0.1:18080/api/admin/stock/items?q=<term>'              # stock list (name__wk sort)
wget -q -O - "$C" 'http://127.0.0.1:18080/api/admin/stock/item-options?q=<term>'       # stock options LIMIT 500
wget -q -O - "$C" 'http://127.0.0.1:18080/api/admin/invoices?search=<x>&search_by=name'
wget -q -O - "$C" 'http://127.0.0.1:18080/api/admin/pos/customers?q=<term>'            # needs POS feature + sell permission
wget -q -O - "$C" 'http://127.0.0.1:18080/api/admin/comida/technical-sheets?q=<term>'
wget -q -O - "$C" 'http://127.0.0.1:18080/api/admin/comida/platos?search=<term>'
wget -q -O - "$C" 'http://127.0.0.1:18080/api/admin/group-menus-v2'
```
- Gates per route: reservasGate (bookings), stockViewGate (stock), facturasGate (invoices), posSellGate plus the POS feature, sheetsViewGate, menusGate. A 403 means the scratch account lacks that section; it is not a PG error.

**Admin-token endpoints** (`requireAdmin`, no session writes):
```
T="--header=X-Admin-Token: <the process-local ADMIN_TOKEN>"; R="--header=X-Restaurant-Id: <rid>"
wget -q -O - "$T" "$R" 'http://127.0.0.1:18080/api/searchPostres.php?searchTerm=<term>'
wget -q -O - "$T" "$R" 'http://127.0.0.1:18080/api/menuDeGruposBackend/getAllMenus.php'
```
`X-Restaurant-Id` is honoured only together with a valid admin token.

## 4. Rebuilt binary
- `/tmp/villacarmen-bot-20261111/vc-server-qa`, static (CGO_ENABLED=0, -trimpath).
- sha256 ba0075bd102beee2e51bcf8bccdeaaf49db815a84478549f4f82b4ea6924a5b9.
- Use the vitess20 run recipe. Do NOT set BOT_CONTEXT_DRIVER (or set it to `postgres`), and do not set BOT_CONTEXT_POSTGRES_ALLOW_EMPTY.

## Inventory r127
2540 sites, 2512 native, unchanged.

## Gates
- Flag and QA mode are scratch-only.
- Backoffice QA writes bo_sessions (scratch only).
- About 29 approximate lookups.
- Stock document insert bug.
- GPL/provenance still open.
- 1117 pending and 731 orphans untouched.
- R5 untouched.
