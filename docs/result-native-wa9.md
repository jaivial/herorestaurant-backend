# result-native-wa9 — WhatsApp domain syntax-complete (last 53 sites)

**Runtime-verified: 0 sites. Syntax coverage is not semantic validation; ready:false.** MySQL stays the default and the server still refuses PG. No SQL, worker, provider or send path was run.

## Scope
- The remaining 53 sites, taken from the actual r54 inventory, across 25 files:
  - conversation_state, the special_date_whatsapp_only pair, booking_resend, chat_images, dish_allergens, groupmenu_party, history;
  - human_takeover, inbound_queue, invoice_tools, privileged_contacts, privileged_control_tools, prompt, sameday;
  - service_session, special_crud, special_policy, special_tools, staff_bookings, tools_v2, unsubscribe;
  - webhook_evolution, ws_ops, whatsapp_unreachable.
- **WhatsApp domain: 316/316 syntax-ported**, 0 blocked, 53/53 files.

## Build and inventory
- `go build -p 2 ./...` gave EXIT=0 (inherited CPU 200% / 6G limits). No tests, vet, fixtures or Playwright.
- Inventory r55:
  - Canonical 2475 = native 1791 (includes 4 provisional) + blocked 3 + unwrapped 681.
  - Dynamic 95; portable audit failures 0.
- Unwrapped by domain:

| Domain | Unwrapped sites |
|---|---|
| pos_billing | 564 |
| tools_cmd | 57 |
| migration_tooling | 55 |
| db_runner | 5 |
| whatsapp_bot | 0 |

- Lexical check of the dialect pairs: clean.
- **Bug found and fixed:** the automated wrapping had produced `nativeSQL(nativeSQL(..))` in `privileged_contacts.go` and `privileged_control_tools.go` (the same class as the wa6 `bot_admin` issue). Unwrapped; a repo-wide grep finds none left.

## Semantics (compile-only)
### Bot-store state paths
- Files: `inbound_queue`, `human_takeover`, `history`, `chat_images`.
- They run on `botStateDB()` / botstore, whose per-store branches already existed (`LOCALTIMESTAMP(3)`, `ON CONFLICT`, the quarantined filter). Marked `selectedSQL` only.
- **Gate:** `LOCALTIMESTAMP` uses the session zone, and the botstore pool TimeZone was not re-verified to be UTC.

### Suppressions (`unsubscribe`, `unreachable`)
- `INSERT IGNORE` becomes `ON CONFLICT (restaurant_id, channel, target) DO NOTHING`, the only unique key besides the PK.
- **Gate (safety-critical):** an over-long `target` or `reason` makes PG raise where MySQL truncated. The suppression is then lost (logged).

### Config JSON
- `config_json` is jsonb: written with `CAST(? AS jsonb)`, read as text, upserted with `ON CONFLICT (restaurant_id)`.
- The privileged control re-read keeps `FOR UPDATE`.

### Notes (`tools_v2`)
- The MySQL `'\n'` literal is a real newline, because `NO_BACKSLASH_ESCAPES` is not in the source sql_mode. The PG twin uses `chr(10)`.
- `IF` and `CONCAT` become `CASE` and `||`.

### Dates and phones
- `DATE_FORMAT` / `TIME_FORMAT` become `to_char`; `CURDATE()` becomes the UTC statement date.
- In `special_date_whatsapp_only_tools`, an empty date parameter is typed through `NULLIF`, so it cannot raise a cast error.
- Phone matching `CONCAT` becomes `||`.

### Time arguments
- The bot service session's `expires_at` (naive) is bound with `NaiveUTCArg`.
- `conversation_states.expires_at` keeps the source quirk: the Go local-time string is interpreted in session UTC on both engines.

### Collation
- The `stock_recipes` LIKE uses `LOWER(...) COLLATE "C"`; the accent/PAD SPACE gap remains.
- The `special_policy` `custom_title` NULLIF gets the PAD SPACE `rtrim` twin.

### Routing
- `webhook_evolution` resolves by `provider_instance_id` with `LIMIT 1` on a non-unique column. It joins the arbitrary-tenant routing gate.

## WhatsApp domain gates (all open)
- `uazapi:637` provisional (cross-tenant name collision).
- Webhook token / phone / `provider_instance_id` routing to an arbitrary tenant.
- Outbox `INSERT IGNORE` truncation, and claim batch size under concurrency.
- Suppression truncation loss.
- Proposal hashes and backups across the cutover; TIME range.
- Gap locks: config before-state, reference rows, duplicate create, document vs cancel.
- RAG FTS ranking and tokenizer.
- NUL bytes in `bot_context`; jsonb `\u0000` audit drops.
- botstore session TimeZone.
- Collation, PAD SPACE and accent.
- Float/decimal rounding.

**Repository-wide gates (unchanged):** 1117 import-proof pending, ready:false; 4 NOT VALID FKs with 731 orphans; UUIDs; SEO and multi-key upserts; analytics wiring; numeric, JSON and ID QA; source anomalies (only `modification_history.restaurant_id` is production-confirmed).

## Next: POS billing, 564 sites — first checkpoint about 30 sites
- Largest files: `backoffice_pos_rail.go` 53 (2 dynamic), `backoffice_pos_checkout.go` 48, `backoffice_pos.go` 44 (3), `backoffice_premium.go` 39 (16), `backoffice_pos_modifiers.go` 30 (1).
- Recommended first checkpoint: **`backoffice_pos_modifiers.go` (30 sites)**, a coherent CRUD file.
- Then rail, checkout and core POS in roughly 30-site parts. Those carry money, idempotency keys, tickets and the `UUID()` ticket-number gate.

