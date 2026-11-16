# WhatsApp bot — Phase 1: hard policies + PostgreSQL bot store

Branch `bot/postgres-policy-20261111` (base `origin/main` 6a717a2). **Not committed, merged or deployed.** No tests were written, compiled or run. Build check: `set -o pipefail; go build -p 2 ./...` (exit 0).

**Status: NOT a full bot-DB migration.** Done: the SQLite bot context store (all 6 content tables, knowledge included) can be copied, verified and rolled back. Remaining: the MySQL bot-owned tables (`8`).

## 1. Source evidence (read-only, no PG yet)

Backup `source-context.sqlite`, sha256 `212d844d…aef272`. The hash is unchanged after the read-only runs. The empty `-wal`/`-shm` files beside it predate them. `botstore-migrate -mode inspect` (report `/tmp/villacarmen-bot-20261111/inspect-source.json`):

| table | rows | max id | next id | digest (first 16) |
|---|---|---|---|---|
| conversation_messages | 2056 | 2881 | 2882 | 6797fe49c824070b |
| pipeline_decisions | 481 | 481 | 482 | 555a138f85fee571 |
| management_requests | 19 | 79 | 80 | ade1f2db60231b11 |
| handoff_state | 12 | – | – | ea6466981e709c2f |
| chat_images | 0 | – | – | e3b0c44298fc1c14 (empty) |
| bot_knowledge_fts | 19 | 19 (rowid) | 20 | 7f66f8bb39c44d28 |

Ids have gaps (e.g. 2056 messages up to id 2881). They are preserved exactly, and each sequence is set to `max(source next id, max id + 1)`.

**Knowledge (19 chunks, restaurant 1)** is now migrated as data: `ord` = FTS5 rowid, plus restaurant_id, chunk_id, tags, title and body. Only the FTS5 shadow tables are rebuilt. Export `/tmp/villacarmen-bot-20261111/knowledge-export.json` (0600, file sha256 `4f081d27…74ec0`; restaurant 1 content sha256 `c24f1d4e…dcbd1`).

Not yet proven: that MySQL `restaurant_bot_ai_config.knowledge_json` plus `botMergeKnowledgeV2` reconstructs these 19 chunks exactly. That needs a read-only MySQL export; compare the per-restaurant hash. Note: at runtime the index is re-synced from MySQL every 5 minutes (unchanged prod behaviour). After cutover that will overwrite the migrated chunks with whatever MySQL holds.

## 2. Store shape

- `internal/botstore`: a dialect-aware handle (pgx v5 stdlib driver, `?` rebound to `$n`).
- The schema mirrors SQLite column by column, with BIGINT identity ids and `bot_store_meta` for the migration marker.
- The knowledge table is `(ord identity PK, restaurant_id, chunk_id, tags, title, body, tags_tsv, doc_tsv)` with GIN indexes. Text is accent-folded (`botstore.FTSFold`) and indexed with the `'simple'` config. Ranking uses `ts_rank` instead of BM25: the same rule set, but the 2 free-text extras may come back in a different order.
- Dialect SQL: `RETURNING id`, `jsonb_set` / `->>` (PG re-serializes JSON for rows written after cutover), `ON CONFLICT DO NOTHING`, a subquery alias.
- Fail closed: no SQLite fallback in postgres mode, and startup refuses PG without the verified marker (`BOT_CONTEXT_POSTGRES_ALLOW_EMPTY=1` only for a brand-new install).
- Copy runs in one transaction: per-table empty check, insert, re-read inside the transaction, per-row comparison by key, sequences, marker, then commit. Anything else rolls back.
- Digest: sha256 of the sorted per-row sha256 values, each over length-prefixed columns with NULL encoded separately from an empty string. It does not depend on order or collation.

## 3. Runbook for the coordinator (local container, unix socket, no TCP)

Binary: `/tmp/villacarmen-bot-20261111/botstore-migrate`. Static (`CGO_ENABLED=0 -trimpath`), sha256 `53c8b5e40a807722f300e86539b977aa9baa5ba5869d26ce3e599ead6d095e7f`.

```
C=instinct-villacarmen-bot-pg-20261111
docker cp /tmp/villacarmen-bot-20261111/botstore-migrate $C:/tmp/botstore-migrate
docker cp /home/jaime/orq/villacarmen-bot-20261111/source-context.sqlite $C:/tmp/src.sqlite
docker exec -u 0 $C sh -c 'chown postgres /tmp/botstore-migrate /tmp/src.sqlite && chmod 0700 /tmp/botstore-migrate && chmod 0600 /tmp/src.sqlite && sha256sum /tmp/src.sqlite'
docker exec -u postgres $C createdb villacarmen_bot   # skip if it exists; it must be EMPTY
R() { docker exec -u postgres -e BOT_CONTEXT_POSTGRES_DSN='host=/var/run/postgresql user=postgres dbname=villacarmen_bot sslmode=disable' "$@"; }
R $C /tmp/botstore-migrate -mode inspect          -sqlite /tmp/src.sqlite > inspect.json
R $C /tmp/botstore-migrate -mode copy             -sqlite /tmp/src.sqlite > copy.json;     echo copy=$?
R $C /tmp/botstore-migrate -mode verify           -sqlite /tmp/src.sqlite > verify.json;   echo verify=$?
R $C /tmp/botstore-migrate -mode rollback         -sqlite /tmp/rollback.sqlite > rollback.json; echo rollback=$?
R $C /tmp/botstore-migrate -mode compare-sqlite   -sqlite /tmp/src.sqlite -sqlite2 /tmp/rollback.sqlite > roundtrip.json; echo roundtrip=$?
R $C /tmp/botstore-migrate -mode verify           -sqlite /tmp/src.sqlite > verify-after.json; echo verify2=$?   # PG unchanged by rollback
docker exec -u postgres $C sha256sum /tmp/src.sqlite   # must still be 212d844d…
```

Exit codes: 0 ok, 1 error (message on stderr, never the DSN), 2 mismatch. Every JSON report lists per table `source_rows/target_rows/source_sha256/target_sha256/max_id/source_next_id/target_next_id/mismatched_keys`. Pass condition: every `ok:true`, digests equal across `inspect`, `copy` and `roundtrip`, and `target_next_id >= source_next_id`. Re-running `copy` against a non-empty DB is refused by design. To retry, drop and recreate the database.

Cutover (later, gated): stop the backend, take a fresh backup, run copy and verify, then set `BOT_CONTEXT_DRIVER=postgres` and `BOT_CONTEXT_POSTGRES_DSN=<placeholder>`. Rollback after cutover: stop, run `-mode rollback` to a new file, point `BOT_CONTEXT_SQLITE_PATH` at it, and set `BOT_CONTEXT_DRIVER=sqlite`.

## 4. Local outbound hard stop (`wa_outbound_allowlist_v1`)

The guard sits on every provider HTTP chokepoint: `sendUazAPI`, `uazapiJSONRequest` (Evolution and provisioning), and the Evolution group/instance/chat reads.

```
WA_OUTBOUND_ALLOWLIST=34692747052   # only message sends whose "number" is exactly this pass
WA_OUTBOUND_BLOCK_ALL=1             # optional: nothing passes
BO_DISABLE_BACKGROUND_WORKERS=1     # no outbox, reminders, campaign bounce, watchdog, DSPy demo, inbound-queue recovery
BOT_CONTEXT_DRIVER=postgres
BOT_CONTEXT_POSTGRES_DSN=host=/var/run/postgresql user=<user> dbname=<db> sslmode=disable
```

With the allowlist set, these all hard-fail: groups (`@g.us`), `@lid`, every other number, status/provision/webhook/connect calls, and unparsable payloads. That covers management-group forwards and contact cards sent to anyone else. Logs show only the decision, the path (token stripped) and the last 3 recipient digits.

Not covered by code (coordinator side):
- LLM, pipeline, email and SMS providers. Use local config without live credentials.
- Webhook registration happens on the provider side.
- Copied MySQL `message_deliveries` rows stay queued but are not sent while workers are disabled.

## 5. Hard policies (`internal/api/whatsapp_bot_hard_policy.go`)

They run before the pipeline and the agent, and again as the first check in the tool executor. Exempt: `msg.PrivilegedGroup` only. That flag is set only by the authenticated webhooks (UAZAPI instance token, Evolution URL secret, constant-time compare) through `botConversationIsPrivileged`: the chat JID is checked against the server-side tenant config, and staff DMs need a fresh group-membership fetch. Customer text cannot claim it. Non-privileged groups go through the guards.

**Horizon, read-review:**
- The last bookable day is `Madrid wall-clock today + 35 days`, compared as `YYYY-MM-DD` strings (so no DST effect). That day is allowed; the next is refused. On 2026-11-11 the limit is 2026-12-16.
- Text dates recognised:
  - `d de <mes> [de yyyy]` (ES/EN);
  - ISO `yyyy-mm-dd` (fixed this round; previously missed);
  - `d/m[/yy(yy)]` and `d-m-yyyy` (`14.10` and `2-3 personas` are not read as dates);
  - a bare month after `para/en`, except the current month.

  A missing year means the next occurrence on or after today. An explicit past year is never "far".
- Tool boundary: any of `date/date_from/date_to/from_date/to_date/new_date` parsed by `parseBotDate`, the same parser the tools use. An unparsable value is refused by the tool itself.

**Event + menu/budget:**
- Detection covers the current block plus the previous 6 customer blocks. History merges consecutive customer messages, and the current block is included, so a merged "comunión … menú" is still caught.
- Party size is the **sum** of every `N <unit>` (personas, pax, comensales, adultos, niños/niñas, peques, bebés, menores, EN equivalents). "8 adultos y 4 niños" = 12, so no exit. `somos/seremos N` only counts when no unit was written.
- Exit needs all of: booking intent, no event word, no menu/budget word, none of the same-topic phrases (`ahora somos`, `al final somos`, `para eso/ello`, `para ese día`, `esa/misma fecha`, …), and a total under 10. So "ahora somos 8 para el evento" stays in the loop.
- On a genuine exit the state becomes `special_event_cleared`. Until the 7-day TTL, only the *current* message can re-open the loop, so older event turns no longer re-lock it. The pipeline does not see that marker as an open handoff.
- While the loop is open, the menu tools are refused at the boundary.

**Knowledge vs policy:** the existing `eventos` chunk already said "no negocies precios, menús" but it is retrieved only on event routes. The prod turn routed `menu_content` (0.49) and sent the €30 menu. The `negociacion_cercana` chunk invites "sin problema … menú infantil" talk and should be reviewed. The prod policy copies (`prod-prompt-sources`: allergen/extras/outbound guards, pipeline, prompt, rag) are identical to origin/main. MySQL `whatsapp_bot_config.rules` (custom tenant rules, which bypass knowledge retrieval when set) were **not available**. Please export them read-only, because a custom rule could still tell the model to quote prices. The guards hold either way.

## 6. Open ambiguities

1. While the event loop is open, every message (even "¿a qué hora abrís?") gets the contact card. Please confirm.
2. Special dates more than 35 days ahead (pre-reserva links) now go to management. Changes or cancellations of far-dated bookings do too.
3. Keyword limits: misspellings and relative dates ("dentro de 6 meses") are missed. "evento"/"party" can trigger falsely. The tool boundary still blocks any far date that reaches a tool.
4. The management contact card uses the same-day contact chain. Please confirm it is the management contact.
5. Replies already sent in prod cannot be recalled.

## 7. Phase 2 GraphRAG (unchanged analysis)

- neo4j-rust has one global bearer token (HTTP/MCP), **no Bolt auth enforcement** and no RBAC. Named graphs are not an isolation boundary.
- Proposal: one instance per restaurant on loopback, with its own token and data dir; a server-side `restaurant_id → instance` map; a fail-closed retriever (no context on any error); fixed parameterized read queries only. The hard guards stay ahead of retrieval.

## 8. Remaining for a FULL bot-DB move to PG (not done)

MySQL tables the bot owns or touches (query counts in bot/WhatsApp files):

| table | role | proposal |
|---|---|---|
| `wa_bot_op_proposals` (35) / `wa_bot_op_events` (3) | privileged op approvals + audit | move together; verify no join with business tables, then dual-read |
| `bot_inbound_queue` (5) | inbound durability / recovery | move (pure bot runtime) |
| `whatsapp_bot_sessions` (3) | per-contact session, push_name, last_message_at | move; check backoffice readers |
| `whatsapp_bot_config` (12+) | tenant config_json (rules, grants, privileged contacts) | keep in MySQL (backoffice-owned config) or move with a backoffice API change |
| `restaurant_bot_ai_config` (2) | models + knowledge_json (canonical knowledge) | same as above |
| `message_deliveries` (12) | WhatsApp outbox shared with booking confirmations and campaigns | keep in MySQL (business outbox) |
| `restaurant_uazapi_instances` (16), `uazapi_servers` (6) | provider credentials | keep in MySQL (secrets, provisioning) |
| `whatsapp_message_logs` | AI usage logs | keep or move with analytics |

Shared business tables read and written by bot tools stay in MySQL and must remain coherent: bookings, cancelled_bookings, booking_modifications, special_dates/menus, menus, restaurant_days, restaurant_members, booking extras, campaigns, invoices and others.

A full move therefore needs, per table: a schema map, a copy/verify like `botstore`, a cutover that never splits one transaction across MySQL and PG, and backoffice reader changes. None of that is implemented.




## 9. Round 3 — MySQL bot tables to PG, knowledge reconcile, policy updates

### 9.1 Scope decision per table (inventory of every caller)

| MySQL table | rows (snapshot) | callers | decision |
|---|---|---|---|
| `bot_inbound_queue` | 134 (all `done`) | `whatsapp_bot_inbound_queue.go` (enqueue / claim / finish / boot recover), burst worker | **PG runtime** when `BOT_CONTEXT_DRIVER=postgres`. Imported rows get `quarantined=TRUE` and every queue query adds `AND NOT quarantined`, so a copied payload is never replayed or drained |
| `whatsapp_bot_sessions` | 64 | `botTouchSession` (history), human takeover (pause upsert + `botIsPausedForHuman`) | **PG runtime** |
| `whatsapp_message_logs` | 677 (max id 863) | `logWhatsAppMessage` (writer), backoffice `whatsappLogReport` (summary, series, list) | **PG runtime**, analytics ported (no joins: the AI-usage report reads `ai_usage_logs`, which stays in MySQL) |
| `bot_conversation_messages` | 1171 | none (legacy, only in migrations 002 and manual) | **archived in PG** (same columns), not left behind |
| `whatsapp_bot_messages` | 14 (max id 154) | none (legacy, migration 056) | **archived in PG** |
| `wa_bot_op_events` | 102 | `botSpecialAdminEvent` (≈25 sites) and `botSpecialAdminRateOK` | **retained in MySQL**: the `proposed`/`executed` rows are written inside the same MySQL `tx` as `wa_bot_op_proposals` and the booking/config mutation, and the approval rate limit reads it. Moving it would split one atomic transaction across two databases. Its snapshot digest is reported as "retained, not imported". |

Still in MySQL by design: `whatsapp_bot_config`, `restaurant_bot_ai_config`, `wa_bot_op_proposals`, `wa_bot_op_events`, `message_deliveries` (outbox), provider instances/servers, `ai_usage_logs` and every business table. **The business DB is not migrated.**

Driver switch: in `sqlite` mode all of these stay on MySQL with unchanged SQL. In `postgres` mode they use the PG bot store (`botStateDB()`). PG startup now requires **both** markers (`migration_verified` and `mysql_import_verified`), unless `BOT_CONTEXT_POSTGRES_ALLOW_EMPTY=1`.

Dialect notes:
- `INSERT IGNORE` becomes `ON CONFLICT (restaurant_id, message_id) DO NOTHING`.
- `ON DUPLICATE KEY` becomes `ON CONFLICT … DO UPDATE` with `EXCLUDED`.
- `NOW()`/`NOW(3)` become `LOCALTIMESTAMP(0|3)`, and `DATE_ADD … MINUTE` becomes `make_interval`.
- `SUM(cond)` becomes `SUM(CASE…)` (used on both dialects).
- `LIKE` becomes `ILIKE` on PG, to keep MySQL's case-insensitive search.
- `DATE_FORMAT` becomes `to_char`, and the week bucket uses `date_trunc('week')` (Monday, as MySQL `WEEKDAY` does).
- The log time is bound as UTC text, which is what the MySQL driver stores for `time.Time`.

**Timezone:** queue and session `NOW()` wall-clock values follow the database session timezone. Set the PG DSN `timezone=<MySQL @@time_zone>` so imported and new rows agree.

### 9.2 Snapshot evidence (read-only, no PG)

`botstore-migrate -mode mysql-inspect` on `source-mysql-bot.json` (sha256 `b147fcc5…0a6486`) returned ok. Column mapping matches `columns` exactly; there are no NUL bytes or invalid UTF-8.

| table | rows | max id | next id | digest |
|---|---|---|---|---|
| bot_inbound_queue | 134 | 134 | 135 | 164cf37d3bef7663 |
| whatsapp_bot_sessions | 64 | – | – | 829ec437046607a2 |
| whatsapp_message_logs | 677 | 863 | 864 | c8faaed139bfc27e |
| bot_conversation_messages | 1171 | 1171 | 1172 | 2d882947cb0c7e24 |
| whatsapp_bot_messages | 14 | 154 | 155 | 3e8d0dc1f9741437 |
| wa_bot_op_events (retained) | 102 | – | – | 264d634fa1089819 |

Values compare as the snapshot wrote them: `to_char(... 'YYYY-MM-DD HH24:MI:SS[.MS]')` for timestamp(0)/(3), and `numeric::text` keeps 8 decimals. MySQL `AUTO_INCREMENT` is not in the snapshot, so the next id is `max+1`.

### 9.3 Knowledge reconcile (done, read-only)

`-mode reconcile-knowledge` compares the 19 indexed chunks with the chunks the **runtime's own derivation** (`api.BotIndexedKnowledge`: parse `knowledge_json`, then defaults or `botMergeKnowledgeV2`) rebuilds from the canonical MySQL rows.

Result: **restaurant 1: 19/19 equal**. Tenants 994501, 994502, 994511 and 994512 were never indexed. Their MySQL knowledge is empty, so they would get the 19 defaults on their first turn. No knowledge content is lost; the PG copy also keeps the 19 chunks as data.

### 9.4 Coordinator runbook (container, unix socket)

New binary `/tmp/villacarmen-bot-20261111/botstore-migrate`, sha256 `d4a14aee56a4c5bbec449441734baf9b02efcbce52934fb6730bde19a207618b` (static). It replaces the earlier binary; the context modes are unchanged. Static server: `/tmp/villacarmen-bot-20261111/server`, sha256 `2b414bcd…9e7`.

```
C=instinct-villacarmen-bot-pg-20261111
docker cp /tmp/villacarmen-bot-20261111/botstore-migrate $C:/tmp/botstore-migrate
docker cp /home/jaime/orq/villacarmen-bot-20261111/source-mysql-bot.json $C:/tmp/mysql.json
docker exec -u 0 $C sh -c 'chown postgres /tmp/botstore-migrate /tmp/mysql.json && chmod 0700 /tmp/botstore-migrate && chmod 0600 /tmp/mysql.json && sha256sum /tmp/mysql.json'
R() { docker exec -u postgres -e BOT_CONTEXT_POSTGRES_DSN='host=/var/run/postgresql user=postgres dbname=villacarmen_bot sslmode=disable' "$@"; }
R $C /tmp/botstore-migrate -mode mysql-inspect -mysql /tmp/mysql.json           > mysql-inspect.json;  echo inspect=$?
R $C /tmp/botstore-migrate -mode verify        -sqlite /tmp/src.sqlite          > ctx-verify-before.json; echo ctx=$?   # context untouched before
R $C /tmp/botstore-migrate -mode mysql-import  -mysql /tmp/mysql.json           > mysql-import.json;   echo import=$?
R $C /tmp/botstore-migrate -mode mysql-import  -mysql /tmp/mysql.json           > mysql-import-again.json; echo again=$?  # MUST fail (idempotency guard)
R $C /tmp/botstore-migrate -mode mysql-verify  -mysql /tmp/mysql.json           > mysql-verify.json;   echo verify=$?
R $C /tmp/botstore-migrate -mode mysql-export  -out /tmp/mysql-export.json      > mysql-export.json;   echo export=$?
R $C /tmp/botstore-migrate -mode mysql-verify  -mysql /tmp/mysql-export.json    > mysql-roundtrip.json; echo roundtrip=$?
R $C /tmp/botstore-migrate -mode verify        -sqlite /tmp/src.sqlite          > ctx-verify-after.json; echo ctx=$?    # context still identical
R $C /tmp/botstore-migrate -mode reconcile-knowledge -mysql /tmp/mysql.json     > knowledge-pg.json;   echo knowledge=$?
# rollback rehearsal (pre-cutover undo), then re-import:
R $C /tmp/botstore-migrate -mode mysql-rollback -confirm                       ; echo rb=$?
R $C /tmp/botstore-migrate -mode verify        -sqlite /tmp/src.sqlite          > ctx-verify-rb.json;  echo ctx=$?
R $C /tmp/botstore-migrate -mode mysql-import  -mysql /tmp/mysql.json           > mysql-import-2.json; echo import2=$?
```

The guards to expect: import refuses without the context marker, refuses when the marker is already present, and refuses non-empty tables. Everything runs in one transaction, re-read and digested inside it. Rollback deletes only the five imported tables and their marker. The queue export keeps `quarantined` out of the digest, so the 134 rows round-trip unchanged.

### 9.5 Outbound (unchanged code, scope confirmed)

The allowlist guard sits on `sendUazAPI`, `uazapiJSONRequest` and the three raw Evolution reads. Every text, media, menu, poll, location, contact, group, staff/self notification, outbox send, webhook registration, connect and status call goes through them. With `WA_OUTBOUND_ALLOWLIST=34692747052`, anything not a message send to exactly that number fails at transport.

**Credential isolation is a coordinator step:** the copied MySQL `restaurant_uazapi_instances`/`uazapi_servers` hold provider credentials. An allowed send to the target would use them. Keep copied credentials out of the local canonical config, or replace them with a dedicated test instance, before safe QA starts.

### 9.6 Settled policy changes in code

- **Management card:** hard-policy referrals send **only** `branding.ManagementPhone` (as "Gestión <brand>"). If none is configured, the customer gets the referral text and no card. The same-day chain, which can resolve to a different person, is used only for `same_day`. The referral texts no longer promise a card.
- **Event loop:**
  - A practical question with nothing of the event topic (hours, address, parking, wifi, phone; no event/menu/budget/same-topic words, no party size, no booking intent) is answered by the agent. The `special_event` state stays, and the menu tools stay blocked at the boundary.
  - Everything else stays on the referral.
  - The pipeline can no longer clear an open event loop (`HandoffCleared` is ignored), and other handoffs (`send_contact`, pipeline reasons) no longer overwrite it.
- **Horizon:** 35 days applies to customer booking and availability, special dates included, and those go to management, not the pre-reserva link (as implemented).

### 9.7 Tenant rules (source-tenant-rules.json, 10 tenants)

- One tenant (994403) has non-empty custom rules (10 chars, no price/menu/event/date terms). The other 9 use knowledge retrieval.
- Tone length ranges from 1 to 21 chars; 8 tenants default to Spanish and 2 to English.
- For GraphRAG this means:
  - restaurant 1 is the only tenant with custom knowledge (19 chunks);
  - the others would be seeded from defaults;
  - custom rules replace retrieval entirely, so the graph retriever must be skipped (or scoped) for tenants with rules.
- Hard guards are independent of all of this.

### 9.8 Not verified / limitations

- No PG run of the new MySQL modes or of the PG runtime SQL yet: no PG is reachable from this sandbox. Any PG error will surface in the coordinator run, and no pass is claimed until then.
- The bot store's LIKE/ILIKE search and the `make_interval` pause are compiled, not executed.
- `timezone` in the DSN must match MySQL's.
- `wa_bot_op_events` is not moved (see 9.1).

