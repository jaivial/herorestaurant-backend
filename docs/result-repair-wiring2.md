# Result: repair-wiring2 (MySQL-origin bot tables on the business pool; code-only; nothing applied)

## Finding (coordinator scratch, 09:05)
- Restored R5: public.whatsapp_bot_sessions has 64 rows.
- bot_context has exactly 7 tables: bot_knowledge_fts, bot_store_meta, chat_images, conversation_messages, handoff_state, management_requests, pipeline_decisions. It has no whatsapp_bot_sessions; the bot index attempt failed and was rolled back.
- **padfk1's namespace assumption was wrong.** Source confirms it: botstore.Init in unified mode (Schema != "") creates only postgresSchema[:postgresContextStmts], the SQLite-origin context tables. The MySQL-origin tables come with the full business import into public.

## Exact table → pool ownership
| tables | origin | pool / dialect |
|---|---|---|
| bot_inbound_queue, whatsapp_bot_sessions, whatsapp_message_logs, bot_conversation_messages, whatsapp_bot_messages (botstore.MySQLTables) | MySQL | business pool (s.db), business dialect (MySQL default; PG public). Last two: no runtime Go caller |
| wa_bot_op_events (MySQLRetained) | MySQL | business pool (already s.db) |
| bot_knowledge_fts, bot_store_meta, chat_images, conversation_messages, handoff_state, management_requests, pipeline_decisions | SQLite context | bot store: SQLite (default) or PG bot_context (BOT_CONTEXT_DRIVER=postgres). Unchanged; R5 roundtrip untouched |

## Code changes
- **whatsapp_bot_context_sqlite.go botStateDB():** now always returns the business pool with Postgres = (sqlDialect == Postgres) and Schema = "public". It never returns the context store.
  - The padfk1 "PG business requires PG bot store" refusal is removed (its premise was wrong).
  - The padfk1 bot-store pad-index check on open is removed (that table does not exist there).
- **Callers (all 5, via botStateDB):**
  - whatsapp_bot_history.go:46 (session touch);
  - whatsapp_bot_human_takeover.go:83/104 (pause upsert/read);
  - whatsapp_bot_inbound_queue.go:41/69/95/133/200 (enqueue/finish/claim/recover);
  - ai_usage_logging.go:101 (message log insert);
  - backoffice_ai_usage.go:250 (message log reads).
- **PG texts re-checked against the public schema (schema.json/plan):**
  - **inbound queue:** the MySQL width guard (message_id 120 / chat_key 190 runes) now applies on PG too (public is varchar). ON CONFLICT (restaurant_id, message_id) matches uq_inbound_msg (NO PAD, raw). botQueueLive now returns "": public has no `quarantined` column, which existed only in the retired bot-store copy.
  - **sessions:** user_phone is varchar(32) ci_ai and PAD. The ON CONFLICT and pause read use the ixpad expression; the provider phone is never trimmed. last_message_at is timestamp(3); LOCALTIMESTAMP(3) under the pinned UTC session equals MySQL NOW(3) in UTC.
  - **message logs:** the direction/status enums take untyped params. The search used ILIKE, which PG rejects on a nondeterministic ci_ai column, so it now uses `LOWER(col) COLLATE "C" LIKE LOWER(?)` (the same pattern as other ported searches). MySQL LIKE accent folding stays gated. The timestamp(3) casts are unchanged.
- **internal/db/padkeys.go:** the ixpad__whatsapp_bot_sessions__PRIMARY entry is restored to the business list (134 business entries; the bot list is removed). It matches the 134 indexes the coordinator created in scratch public.
- **internal/botstore/mysql.go:** ImportMySQL and RollbackMySQL refuse unified mode (Schema != ""), so empty duplicate MySQL-origin tables can never be written into bot_context. Legacy separate-DB use is unchanged.
- **cmd/pad-gate-check:**
  - The bot pad gate is replaced by ownership checks: business.mysql_origin_bot_tables (all 5 present in the business current_schema) and bot.no_mysql_origin_tables (0 present in bot_context).
  - It stays read-only (default_transaction_read_only plus a SHOW check) and prints no DSN.

## Not resolved / gates
- **Missing-table block:** the code is fixed, but this is not confirmed until the coordinator runs the diagnostic.
- **Inbound queue replay on PG:** the queue replays the restored source rows within the source 15s..6h window, with no quarantine. Staging must keep background workers disabled. That is an existing gate, now explicit.
- **public.bot_inbound_queue.created_at:** MySQL timestamp becomes PG timestamptz. Comparisons with LOCALTIMESTAMP rely on the UTC session pin. Manual QA.
- **Still open:** collation, LIKE accents, enums, time, numeric and triggers.
- **Unchanged:** 1117 pending / 731 orphans; checkout 503; 0 handler QA; PG server disabled; R5 untouched.

## Checkout replay payload (research only, not fixed)
- **What the source does:**
  - checkoutTicketInTx returns Duplicate before ANY write when the DB-collation key matches and the ticket is not OPEN. No payment row is inserted again, so there is no double charge in the DB.
  - POS checkout calls no payment provider.
  - The handler answers 200 {duplicate:true, ticket} with the stored state.
  - A replay whose payments differ (method, amountCents, tipCents, tenderedCents, provider, providerReference, cardLast4, per-payment idempotencyKey) is answered as success, but the new payload is NOT applied. The client may believe a different split was recorded.
- **Stored facts usable for a bounded check (no new financial rule):** pos_payments(ticket_id, method, amount_cents, tip_cents, tendered_cents, provider, provider_reference, card_last4, idempotency_key), pos_tickets.paid_cents/tip_cents, and the per-payment idempotency_key of the original request.
- **Bulk day-close path:** keys are in.IdempotencyKey+":t:"+ticketID with a single CASH payment of the ticket total.
- **Proposal:** on Duplicate, load that ticket's payments and compare them as a multiset (method, amount, tip, idempotency key; provider fields only if sent). On mismatch, return 409 IDEMPOTENCY_KEY_REUSED instead of 200 duplicate; on match, keep 200.
- **This is a contract change on both engines,** so it needs sign-off before coding.

## Manual diagnostic (coordinator, scratch, read-only)
```
go build -o /tmp/pad-gate-check ./cmd/pad-gate-check
PADQA_BUSINESS_DSN="$(cat <scratch-dsn-file>)" PADQA_BOT_DSN="$(cat <scratch-dsn-file>)" /tmp/pad-gate-check -business-schema public -bot-schema bot_context
```
Expected:
- business.pad_key_indexes ok, if the strict regex matches;
- business.pad_shadow_fks FAIL (shadows not applied);
- business.mysql_origin_bot_tables ok (5/5);
- bot.no_mysql_origin_tables ok (0).

## Build / inventory
- go build -p 2 ./... OK.
- Inventory r107: 2507 sites (+1 diagnostic catalog query), all native, 0 portable audit failures.
