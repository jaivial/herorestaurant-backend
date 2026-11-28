# result-repair-vitess19

Model: claude-merge/claude-opus-5-5-acc2 (low).
- Source and build only. Nothing was run.
- No tests or Playwright, no SQL writes, no activation, no network.
- go.mod and go.sum are unchanged.
- `go build -p 2 ./...` OK with /home/linuxbrew/.linuxbrew/bin/go.
- Backups: /tmp/v19-pre.bak.tar and /tmp/v19-lk.bak.tar.
- backoffice_stock_documents.go:680 is untouched.

## 1. pad-gate-check -wk-qa (manual diagnostic, built, not run)
`PADQA_BUSINESS_DSN=<env> pad-gate-check -wk-qa -rid N -pattern '%cafe%'`
- **Session:** the same read-only session check as `-read-qa` (refuses unless `transaction_read_only=on`). SELECTs only.
- **Flag:** `mysqlcoll.SetWKFlag(true)` is set **in this diagnostic process only**.
- **What it runs:** the real `wkLikeMatchIDs` for 5 wired candidate shapes (stock_items list, bookings name/email, comida platos, wines, stock_recipes) with `wkBound(table)`.
  - Each prints the new id-set size next to the old PG approximation count (`LOWER(col) COLLATE "C" LIKE lower(pattern)`), plus bound and too_broad.
- **group_menus.go:940 join:** `TitleJoin` pairs (dishes, matched pairs) against the old PG SQL LEFT JOIN row and match counts.
- **Converted lookup sample:** for each domain of the restaurant, `domain__wk = key` count vs the rtrim/ci_ai count. It prints how many agree.
- **Output:** JSON counts only, no row values. Scope: helpers and SQL, not HTTP, writes or concurrency.
- Code: `internal/api/manual_wkqa.go` and `cmd/pad-gate-check` (`-wk-qa`, `-pattern`).

## 2. Lookups
**Inside already-flagged writer texts (15):**
- **8 converted:** the ON variant only, with argument positions mapped through the nested key helpers to the inner OFF argument list:
  - stock_supplier_aliases (supplier_code, normalized_description), backoffice_stock_documents:170
  - campaign_suppressions.target, public_unsubscribe:207
  - bo_role_permissions.section_key, rbac:236
  - ai_image_models.slug, config_ai_image:359
  - dish_translations.lang, translations:542
  - seo_item_routes.entity_type, :954
  - stock_role_permissions.role_slug, stock:96
  - pos_customers.phone, pos_customers:86
- **4 were already converted** in earlier batches (no pattern left in the ON text): middleware:159, stock_suppliers:314, member_access_flows:1240, seo_item_routes:745.
- **3 left:**
  - mcp_oauth_store:147 (table unresolved);
  - instatic_manager:952/956 (slice args).

**Column-to-column (16), converted only where both columns are registered with the same collation:**
- **Converted:** `sa.supplier_name__wk = sup.name__wk` at stock_suppliers:36 and :37. stock_supplier_aliases.supplier_name and stock_suppliers.name are both unicode_ci PAD.
- **Also converted:** menu_visibility:163 (a numbered `$3` lookup that the converter had skipped).
- **Left on ci_ai:**
  - pos_catalog:262/267 (`c.categoria` / `v.tipo` are not registered);
  - site_builder ×7 and mcp_oauth_store:311, member_access_flows ×3: other comparison forms or an unregistered side.

Listed in `docs/dbmigrate/wk-lookups-converted-vitess19.tsv`. About 29 remain, all on the documented ci_ai approximation.

## 3. Local HTTP QA mode (code; default refuses)
`cmd/server/main.go`: the `DB_DRIVER=postgres` refusal is **unchanged for normal runs**. Only when `VC_PG_LOCAL_HTTP_QA=1` does it call `runPGLocalQA` (`cmd/server/pg_local_qa.go`, coordination id pg_local_http_qa_v1).

**Fatal unless all hold:**
- the listen address is loopback: `config.Addr` is always `:PORT` (all interfaces), so QA mode **rewrites it to 127.0.0.1:PORT** before the guard, which then requires a loopback host;
- `BO_DISABLE_BACKGROUND_WORKERS` is set. This uses the existing switch, so no fichaje, campaign-bounce, pre-shift, stock-digest, booking-reminder, WA outbox, WA watchdog, bot-demo or inbound-recover loops start;
- `DATABASE_URL` is postgres:// to a loopback host or a unix socket (`host=/…`);
- no non-empty environment variable whose NAME matches a provider pattern: Stripe, UAZAPI/WhatsApp/Evolution, SMTP/Resend/Mailgun/SendGrid/Postmark/Titan, Twilio, OpenAI/Anthropic/DeepSeek/MiniMax/Gemini/Mistral/OpenRouter, Cloudflare/Bunny/Namecheap/Instatic, Telegram/Slack/webhooks, S3/AWS/R2. Only the names are reported, never values.

**Then:**
- `http.DefaultTransport` and `http.DefaultClient` are replaced by a refusing RoundTripper. 1 custom `&http.Transport{}` exists in internal/ and is NOT covered; **the network namespace is the primary isolation**.
- The pool is opened through `appdb.OpenBusinessForTool`, which also needs `DB_PG_TOOLS_QA_APPROVED=1` and every readiness gate: import marker `ready_for_cutover`, PAD keys, shadow FKs, ON UPDATE triggers, and `__wk` when `VC_PG_WK_KEYS=1`. **No waiver.**
- It serves `api.NewServer` routes on loopback.

**Expected today:** it REFUSES on scratch, because the import marker is not ready (1117 pending) and the shadow-FK gate fails on the orphans. That is by design. Lifting either is a separate decision, not part of this unit.

**Run recipe** (for when the gates pass; coordinator only):
```
unshare -rn systemd-run --user --scope -p CPUQuota=200% -p MemoryMax=6G -- \
  env -i PATH=/usr/bin DB_DRIVER=postgres DATABASE_URL='postgres://…@/db?host=/run/scratch' \
  DB_PG_TOOLS_QA_APPROVED=1 VC_PG_LOCAL_HTTP_QA=1 VC_PG_WK_KEYS=1 BO_DISABLE_BACKGROUND_WORKERS=1 \
  PORT=18080 ./server   # binds 127.0.0.1:18080
```
Use a clean env (`env -i`) so no provider variables leak. Then curl loopback endpoints by hand (search, list and login pages), with no WhatsApp webhooks.

**Not verified even then:**
- provider flows (they are blocked);
- custom-transport clients (blocked only by the netns);
- concurrency and load;
- WhatsApp inbound;
- writes against production data (never).

## Inventory r126
- 2540 sites: 2512 native, 19 dynamic.
- The 5 new dynamic sites are in the manual wk-qa diagnostic (candidate and comparison queries composed from site tables), by my attribution; the summary gives no per-file breakdown.

## Gates
- **Flag off; QA mode refuses** (marker not ready, shadow FK).
- About 29 lookups on the approximation.
- Stock document insert bug.
- GPL/provenance still open.
- 0 handler QA, 1117 pending, 731 orphans, R5 untouched.
