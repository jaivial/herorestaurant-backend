# result-native-wa8 — WhatsApp RAG, media, menus, verification and adelanto (33 sites)

**Runtime-verified: 0 sites. Syntax port only; ready:false.** MySQL stays the default and the server still refuses PG. No SQL, worker, provider or send path was run.

## Scope (all 5 files complete)
| File | Sites |
|---|---|
| `whatsapp_bot_rag.go` | 8 |
| `whatsapp_bot_media_ops.go` | 7 |
| `whatsapp_bot_menu_tools.go` | 7 |
| `member_whatsapp_verification.go` | 6 |
| `whatsapp_bot_special_date_adelanto.go` | 5 |

## Build and inventory
- `go build -p 2 ./...` gave EXIT=0 (inherited CPU 200% / 6G limits). No tests, vet, fixtures or Playwright.
- Inventory r54:
  - Canonical 2475 = native 1738 (includes 4 provisional) + blocked 3 + unwrapped 734.
  - Dynamic 106; audit failures 0.
- Count change from r53: native +33.
- WhatsApp domain: 263/316 ported, 53 remaining (11 dynamic), 28 files done.
- Lexical check of the dialect pairs: clean.

## Semantics (compile-only)
### RAG (`whatsapp_bot_rag.go`)
- Every statement goes through `*botstore.DB`. The SQLite FTS5 branch and the PG tsvector branch were already explicit; I only marked the sites `selectedSQL` (dialect chosen by the store).
- The rebuild transaction returns every error, so no savepoints were needed.
- **Search ranking is not equivalent; this is an open search gate.**
  - SQLite: FTS5 `unicode61 remove_diacritics` with `bm25`.
  - PG: `simple` tsvector, with diacritic folding done in Go, ranked by `ts_rank`.
  - Tokenizer, prefix, stopword and ranking behaviour can differ.

### Media ops (`whatsapp_bot_media_ops.go`)
- The AI-flag updates gate on RowsAffected==0 to report "not found". MySQL counts only changed rows, so an already-flagged row returns 0.
- The PG twins add an `IS DISTINCT FROM` predicate on the flags to keep that (comida, wine, dish and menu preview).
- The slider `+1` update always changes the row, so it is portable.

### Menu tools (`whatsapp_bot_menu_tools.go`)
- The numeric price is read with `CAST(price AS text)`, which matches the DECIMAL text MySQL returns.
- `IF` becomes `CASE`; `DATE_FORMAT` becomes `to_char`.
- `CONCAT` becomes `||`, which keeps MySQL's NULL result.
- `NULLS LAST` is added.
- Open: ordering on `ci_ai` text differs between ICU and MySQL.

### Member verification (`member_whatsapp_verification.go`)
- The `expires` `time.Time` is bound with `NaiveUTCArg`; `NOW()` becomes the UTC statement time.
- `NULLIF(whatsapp_number, '')` gets the PAD SPACE `rtrim` twin on PG. UCA ignorable characters remain a residual gap.
- Open: phone equality on `ci_ai` has PAD SPACE and accent deltas.

### Adelanto transaction (`whatsapp_bot_special_date_adelanto.go`)
- PG forbids a qualified SET target, so `SET sdm.adelanto_amount` becomes `SET adelanto_amount`; the alias stays for the shared filter.
- The `INSERT…SELECT` upsert becomes `ON CONFLICT (special_date_menu_id, section_id)`, the only unique key. The (menu, section) pairs are distinct, so PG never touches a row twice in one statement.
- The amount parameter is cast to `numeric(10,2)`. Float-to-decimal rounding at half-way values needs QA.
- Transaction errors propagate.

## Gates (all unwaived)
- **New:**
  - RAG FTS ranking and tokenizer equivalence.
  - Collation ordering in the menu tools.
- **Carried:**
  - `uazapi:637` provisional (cross-tenant name collision).
  - Webhook arbitrary-tenant routing.
  - Outbox `INSERT IGNORE` truncation, and claim batch size under concurrency.
  - NUL in `bot_context`; jsonb `\u0000` audit drops.
  - Proposal hashes and backups across the cutover.
  - TIME range.
  - Gap locks.
  - Bounce UTF-8.
  - UUIDs, SEO and multi-key upserts.
  - Analytics wiring.
  - Numeric and decimal QA.
  - Import proof: 1117 pending, ready:false; 4 NOT VALID FKs with 731 orphans.

## Next checkpoint: about 30 sites (rest of the WhatsApp domain, first part)
| File | Sites |
|---|---|
| `whatsapp_bot_inbound_queue` | 5 (all dynamic, botstore) |
| `conversation_state` | 4 |
| `privileged_control_tools` | 4 |
| `sameday` | 4 |
| `special_policy` | 4 |
| `chat_images` | 3 |
| `privileged_contacts` | 3 |
| `human_takeover` | 2 |
| `staff_bookings` | 2 |

The remaining 23 one- and two-site files follow, which will finish the WhatsApp domain.

