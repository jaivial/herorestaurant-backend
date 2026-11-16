# result-repair-vitess20

Model: claude-merge/claude-opus-5-5-acc2 (low).
- Source and build only. Nothing was run.
- No tests or Playwright, no SQL writes, no activation, no network.
- go.mod and go.sum are unchanged.
- `go build -p 2 ./...` OK with /home/linuxbrew/.linuxbrew/bin/go.
- Backup: /tmp/v20-pre.bak.tar.
- backoffice_stock_documents.go:680 is untouched.

Input:
- Parent GO 16:01 and 16:17: the QA server on the disposable scratch, with the import-marker and shadow-FK gates skippable ONLY there and documented. The 731 orphans are never deleted or invented.
- -wk-qa (16:17): flag-on LIKE counts equal MySQL (stock 288/6/445, bookings 2151/1/2334); the old path gave 11 and 137.

## 1. Scratch waiver (code)
`VC_PG_LOCAL_HTTP_QA_SCRATCH_WAIVE`, e.g. `import-marker,shadow-fk`, in cmd/server/pg_local_qa.go.
- **Where it is read:** only inside `runPGLocalQA`, so only when `VC_PG_LOCAL_HTTP_QA=1` and after every local QA guard.
- **Refused** unless `DATABASE_URL` is a unix socket (`?host=/…`) or loopback. Never on TCP non-loopback.
- **Default** (empty): no waiver.
- **Banner:** each waiver logs `*** WAIVED on disposable scratch: <name> *** (QA only; orphans untouched; not valid for any other database)`.
- **New `appdb.OpenBusinessForLocalQA(ctx, cfg, waivers)`:**
  - it accepts only `import-marker` (skips the not-ready check of the import report; the marker row must still exist) and `shadow-fk` (skips RequirePadShadowFKs);
  - unknown names are refused;
  - all other gates still apply: PG tools approval, ping, PAD key indexes, ON UPDATE triggers, and `__wk` keys when VC_PG_WK_KEYS=1.
- `OpenBusinessForTool` is unchanged in behaviour; it calls the same code with no waivers.
- **No data is changed.**

## 2. Static binary
- `/tmp/villacarmen-bot-20261111/vc-server-qa`: CGO_ENABLED=0, -trimpath, statically linked ELF x86-64.
- Size 78,970,484 B; sha256 4e88bae0f1bd79200a6f725fee842d811bf0a1c8ab66dc31aa02d2048836c075.

## 3. Run recipe (coordinator, inside the scratch container; network none)
```
docker cp /tmp/villacarmen-bot-20261111/vc-server-qa instinct-villacarmen-padqa-20261112:/tmp/vc-server-qa
docker exec -it instinct-villacarmen-padqa-20261112 sh -c '
  env -i PATH=/usr/bin:/bin HOME=/tmp     DB_DRIVER=postgres     DATABASE_URL="postgres://<qa_user>@/<scratch_db>?host=/var/run/postgresql&sslmode=disable"     DB_PG_TOOLS_QA_APPROVED=1     VC_PG_LOCAL_HTTP_QA=1     VC_PG_LOCAL_HTTP_QA_SCRATCH_WAIVE=import-marker,shadow-fk     VC_PG_WK_KEYS=1     BO_DISABLE_BACKGROUND_WORKERS=1     PORT=18080     ADMIN_TOKEN="$(head -c 24 /dev/urandom | od -An -tx1 | tr -d " 
")"     /tmp/vc-server-qa > /tmp/vc-server-qa.log 2>&1 &'
```
- Fill in `<qa_user>` and `<scratch_db>` from the container's existing local setup. Use peer or trust auth on the socket, so no password goes in the URL.
- The server binds **127.0.0.1:18080** (QA mode rewrites `:PORT`).
- Startup log, expected:
  - two WAIVED banners;
  - `[workers] background loops disabled`;
  - `LOCAL QA ONLY: API on 127.0.0.1:18080`;
  - no MySQL migration run, because the PG path never calls migrations.
- Any guard failure is fatal and names the variable, never its value.
- **ADMIN_TOKEN** is a throwaway random value generated inside the container for this process only. It is not stored and not a real credential. Read it from the process env if needed: `cat /proc/<pid>/environ` inside the container. Do not copy it anywhere.
- With `VC_PG_WK_KEYS=1` the startup guard requires all 151 `__wk` columns, the 134 ixwk indexes, and CheckStoredKeys = 0. These passed on scratch at 15:33.

## 4. Read-only HTTP endpoints worth hitting (busybox wget, inside the container)
`W='wget -q -O - --header=X-Admin-Token:$TOK --header=X-Restaurant-Id:<rid>'`

**Public / no session:**
- `wget -q -O - http://127.0.0.1:18080/healthz`: liveness.
- Tenant by domain: `wget -q -O - --header='Host: <a restaurant_domains host>' http://127.0.0.1:18080/api/menuDeGruposBackend/getActiveMenusForDisplay`. This exercises the converted `restaurant_domains.domain__wk` lookup (tenant.go) and the **group_menus TitleJoin** path (`getActiveMenusForDisplay`, `getMenuForDisplay?id=<menuId>`).

**Admin token** (`requireAdmin`; tenant through `X-Restaurant-Id`, which is honoured only with a valid admin token):
- `$W 'http://127.0.0.1:18080/api/searchPostres.php?searchTerm=<term>'`: admin_postres LIKE (flag on).
- `$W http://127.0.0.1:18080/api/menuDeGruposBackend/getAllMenus.php`.

**Backoffice** (`/api/admin/...`, `requireBOSession`, bo_session cookie):
- `GET /api/admin/bookings/search?name=<n>&phone=<p>`
- `GET /api/admin/bookings?…q=<prefix>`
- `GET /api/admin/stock/items?q=<term>` and `/api/admin/stock/item-options?q=<term>`
- `GET /api/admin/invoices?search=<x>&search_by=name`
- `GET /api/admin/pos/customers?q=<term>`
- `GET /api/admin/comida/technical-sheets?q=<term>`
- `GET /api/admin/comida/platos?search=<term>`, `/api/admin/comida/postres?search=<term>`, `/api/admin/comida/vinos?search=<term>`
- `GET /api/admin/group-menus-v2`

How to get a BO session for QA without secrets:
- **What exists:** `POST /api/admin/login` (handleBOLogin) issues the `bo_session` cookie for an existing `bo_users` row.
- That needs a password the operator already knows for a **scratch** account.
- **Do not** create users, reset passwords or put credentials in docs. Creating a QA user would be a scratch write, and it needs its own GO.
- If no known scratch credential exists, limit QA to the public and admin-token endpoints above.

Compare the counts and id sets against the MySQL source for the same terms; -wk-qa already did this for stock and bookings. Look in the log for `ErrTooBroad`/422, SQL errors (unknown column, i.e. a missing `__wk`), and `outbound HTTP disabled` lines (an attempted provider call).

## 5. Not covered
- Writes: they would mutate scratch, so out of scope unless the GO extends to it.
- Provider flows: blocked.
- WhatsApp webhooks: not called.
- Concurrency: not tested.
- Production: never.

## Gates
- Flag and QA mode exist for scratch only.
- PG server refusal unchanged for normal runs.
- About 29 approximate lookups.
- Stock document insert bug.
- GPL/provenance still open.
- 1117 pending and 731 orphans unchanged and unwaived outside scratch QA.
- R5 untouched.
