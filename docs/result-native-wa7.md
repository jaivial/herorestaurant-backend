# result-native-wa7 — WhatsApp storage, outbox and conversation history (33 sites)

**Runtime-verified: 0 sites. Syntax port only; ready:false.** MySQL stays the default and the server still refuses `DB_DRIVER=postgres`. No outbox worker, provider, send path or SQL was run.

## Scope (all 3 files complete)
| File | Sites |
|---|---|
| `whatsapp_bot_context_sqlite.go` | 16 |
| `whatsapp_outbox.go` | 9 |
| `conversation_history.go` | 8 |

## Build and inventory
- `go build -p 2 ./...` gave EXIT=0 (inherited CPU 200% / 6G limits). No tests, vet, fixtures or Playwright.
- Inventory r53:
  - Canonical 2475 = native 1705 (includes 4 provisional) + blocked 3 + unwrapped 767.
  - Dynamic 114; audit failures 0.
- Count change from r52: native +33.
- WhatsApp domain: 230/316 ported, 86 remaining (19 dynamic), 23 files done.
- Lexical check of the 12 dialect pairs: clean.

## Semantics (compile-only)
### Context store (`whatsapp_bot_context_sqlite.go`)
- Every statement goes through `*botstore.DB`. That store has its own dialect flag (SQLite, or the `bot_context` schema in `newvillacarmen_business`) and its own placeholder rebinding.
- The PG branches already in the file are kept unchanged (`jsonb_set` / `->>`). The sites are marked `selectedSQL` (dialect chosen by the store), with no rewrite.
- The r5 `bot_context` copy and roundtrip proof is untouched and was not redone.
- **New NUL gap:** `bot_context.conversation_messages.content` is TEXT. A future NUL byte would be rejected on PG while SQLite accepts it. The r5 data contained none.

### Outbox (`whatsapp_outbox.go`)
- **Enqueue:** `INSERT IGNORE` becomes `ON CONFLICT (delivery_key) DO NOTHING`.
  - `delivery_key` is the only unique key besides the identity PK, and NULL keys never collide on either engine.
  - Not reproduced: IGNORE's other effects, i.e. strict-mode truncation and other errors downgraded to warnings. PG raises instead. Open delta.
- **Claim:** `UPDATE … ORDER BY id LIMIT ?` becomes `UPDATE … WHERE id IN (SELECT … ORDER BY id LIMIT ? FOR UPDATE)`.
  - No `SKIP LOCKED`, because the source also blocks on locked rows.
  - Under concurrency, PG may claim fewer rows than the limit; MySQL keeps scanning to fill it.
  - There is no double claim: rows are read back by `locked_by` token. Code review only.
- **Time:** the naive `locked_at`, `next_attempt_at` and `sent_at` columns get the UTC statement time. `INTERVAL ? SECOND` becomes `CAST(? AS integer) * INTERVAL '1 second'`.
- **Reconnect flush:** the `LIMIT 1000` update becomes a subquery.

### Conversation history (`conversation_history.go`)
- Enums are read as text.
- `DATE_SUB(NOW(), INTERVAL ? HOUR)` becomes the statement time minus n hours.
- `NULLS FIRST` / `NULLS LAST` are added on the nullable timestamps.
- The message insert uses `InsertID`.

## Gates (all unwaived)
- **New:**
  - Outbox `INSERT IGNORE` non-duplicate effects (truncation and warnings).
  - Claim batch-size difference under concurrency.
  - NUL bytes in the `bot_context` text columns.
- **Carried from wa6:**
  - `uazapi:637` provisional: the cross-tenant `instance_name` collision behaviour is unresolved.
  - Webhook routing: `LIMIT 1` on a non-unique token/phone can pick an arbitrary tenant. Must be resolved before cutover, never silently chosen.
- **Earlier gates:**
  - Proposal hashes and backups across the cutover.
  - TIME range.
  - Gap locks (config, reference rows, duplicate create, document vs cancel).
  - NUL / JSON audit rows.
  - Bounce UTF-8.
  - UUIDs, SEO and multi-key upserts.
  - Analytics wiring.
  - Numeric, JSON and ID QA.
  - Import proof: 1117 pending, ready:false; 4 NOT VALID FKs with 731 orphans.

## Next checkpoint: about 31 sites
| File | Sites | Notes |
|---|---|---|
| `whatsapp_bot_rag.go` | 8 (2 dynamic) | BeginTx |
| `whatsapp_bot_media_ops.go` | 7 (3 dynamic) | |
| `whatsapp_bot_menu_tools.go` | 7 | |
| `member_whatsapp_verification.go` | 6 | |
| `whatsapp_bot_special_date_adelanto.go` | 5 (3 dynamic) | BeginTx |

After that: `inbound_queue` 5 (all dynamic, botstore).

## No actions taken
No SQL execution, DB writes, activation, providers, sends, `.env`/proof edits, commit, merge or deploy.

