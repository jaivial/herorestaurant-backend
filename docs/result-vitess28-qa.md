# result-vitess28-qa

Model: claude-merge/claude-opus-5-5-acc2 (low).
- Docs only: no Go code changed, nothing rebuilt, nothing run.
- No provider, WhatsApp, payment or send calls.
- Scratch only. The 731 orphans are reported only, never deleted.

## Part A: retiring the raw ci_ai unique indexes (scratch only)
Files:
- `docs/dbmigrate/ci-ai-retire-vitess28.sql`: precondition queries 0-3, then **116 `DROP INDEX IF EXISTS`** in one transaction ending **ROLLBACK** (the coordinator COMMITs on scratch);
- `docs/dbmigrate/ci-ai-retire-vitess28.tsv`: 121 rows, one per raw unique index with a text column. Columns: table, raw index, columns, MySQL collation, ixpad twin, ixwk twin, FK dependents, status, over-reject risk.

Source of the index names: `full-pg-plan/plan.sql`, the R5 import plan (211 raw `ix__` uniques; 121 have a text column). Query 1 counts them on scratch before the DROP.

| status | count | meaning |
|---|---|---|
| retire | **116** | an ixpad twin and an ixwk twin exist (matched by table and the exact column list from padkeys.go); no FK depends on it |
| KEEP (enum) | 4 | analytics_sales_documents.source_type, pos_products.source_type, stock_affluence_daily.service_type, stock_margin_scope_bands.zone. These are MySQL ENUMs, i.e. native PG enums: deterministic, not ci_ai, no over-reject risk |
| **KEEP / BLOCKER** | 1 | `ix__bot_inbound_queue__uq_inbound_msg` (restaurant_id, message_id). message_id is **utf8mb4_0900_ai_ci NO PAD** and not in the __wk registry, so there is no twin and the raw ci_ai index must stay. ICU primary vs 0900 diverges at least on æ=ae: ICU folds it and 0900 also folds it, but other pairs are unproven. Provider message ids are ASCII, so the practical risk is low; it is still not proven exact |

**Over-reject risk** of the 116: the columns are unicode_ci or general_ci (PAD SPACE). The raw ICU index can treat as equal values that MySQL keeps distinct, and can also miss equalities:
- **ß / ss:** unicode_ci equals ICU. But general_ci has ß = s and ß ≠ ss, so a general_ci column can see a false duplicate on 'ss' vs 'ß'.
- **æ / ae:** ICU equal; unicode_ci and general_ci distinct, so the raw index rejects a value MySQL accepts.
- **combining a+◌́ vs á:** ICU equal; general_ci distinct.
- **NUL embedded:** general_ci distinct; PG text cannot hold NUL anyway.
- **Trailing spaces:** the raw index has no rtrim, so 'x' and 'x ' are distinct in PG but equal in MySQL. The raw index under-rejects here; the ixpad/ixwk twins catch it.

**FK dependency:** none of the 116 is referenced by a plan FK (plan FKs reference PKs). On scratch, query 2 (`pg_constraint.contype='f' AND conindid = <index>`) must return 0 rows. Query 3 checks that none backs a UNIQUE or PK constraint, which would need `ALTER TABLE … DROP CONSTRAINT` instead.

**ON CONFLICT after the drops:**
- flag-off PG texts target the ixpad expression indexes (kept);
- flag-on texts target the ixwk columns (kept);
- a source scan found 0 app ON CONFLICT targets equal to the plain column list of a retired index.

So every upsert keeps a matching arbiter index.

After COMMIT, run `PADQA_WK=1 pad-gate-check`. It checks the ixpad and ixwk sets, not the raw indexes, so it must stay PASS.

## Part B: manual HTTP QA of the 14 newly wired writers
**Setup** (vitess20-22): server inside the container on 127.0.0.1:18080 with VC_PG_WK_KEYS=1; QA user 207 (superadmin, so role root); `bo_user_restaurants.app_version >= 0.2` (vitess23); a fresh login.
- Cookie file: /tmp/qa-cookie (0600).
- qa-http: `qa-http -X <METHOD> -url <loopback url> -cookie-file /tmp/qa-cookie [-H 'K: V;K: V'] [-n 600] < body.json`. It prints `status=… bytes=…` only.

After **every** write:
1. `PADQA_WK=1 pad-gate-check`, expecting business.wk_keys PASS;
2. the per-row key check below (read-only; manual SELECT through psql on the scratch socket).

The generic key check works for any table, column and id. With VC_PG_WK_KEYS=1 it must return 0:
```
SELECT COUNT(*) FROM <table> WHERE id = <id> AND (<col> IS NULL) <> (<col>__wk IS NULL);
```
The exact key value is verified by the gate (CheckStoredKeys recomputes it in Go). SQL cannot recompute a MySQL weight.

`API=http://127.0.0.1:18080/api/admin`

| # | writer (file) | how to trigger | payload | expected | key check |
|---|---|---|---|---|---|
| 1 | POS daily ticket sequence (backoffice_pos.go nextPOSTicketNumber) | create a visit, then a ticket. `POST $API/pos/visits`, then `POST $API/pos/visits/{visitId}/tickets` | visit `{"channel":"DINE_IN","covers":2,"idempotencyKey":"qa-v28-visit-1"}`; ticket `{"idempotencyKey":"qa-v28-tk-1","guestLabel":"QA"}` | 200/201 with ticket id and number | pos_daily_sequences row for (rid, today, 'TICKET'): `SELECT COUNT(*) FROM pos_daily_sequences WHERE restaurant_id=1 AND sequence_type__wk IS NULL` returns 0 |
| 2 | checkout ticket key (backoffice_pos_checkout.go UPDATE pos_tickets) | `POST $API/pos/tickets/{ticketId}/checkout` | `{"idempotencyKey":"qa-v28-co-1","expectedVersion":<from ticket GET>,"payments":[{"method":"CASH","amountCents":<total>,"idempotencyKey":"qa-v28-pay-1","tenderedCents":<total>}],"closeVisit":true}`. **CASH only**: never a card/Stripe provider value | 200, ticket PAID | pos_tickets: checkout_idempotency_key / __wk generic check on the ticket id; pos_payments idempotency_key likewise. The ticket needs ≥1 line first (`POST $API/pos/tickets/{id}/lines` with an existing pos_product); an empty ticket may be refused |
| 3 | fiscal series (backoffice_pos_fiscal.go ensurePOSFiscalSeries) | `POST $API/pos/tickets/{ticketId}/fiscal-document` after #2 | `{"action":"issue","terminalKey":"qa-v28"}` | 200 with document, or 409 if the restaurant profile has no name/NIF (then QA stops here) | pos_fiscal_series terminal_key: generic check; pos_fiscal_documents full_number likewise. Writes the fiscal chain **on scratch only**; no AEAT/VeriFactu call exists in this code path (no HTTP client in pos_fiscal/checkout) |
| 4 | stock-exception resolve (pos_reports.go replay UPDATE) | `POST $API/pos/stock-exceptions/replay` | `{"exceptionIds":[<OPEN id>],"idempotencyKey":"qa-v28-replay-1"}` | 200 | pos_stock_exceptions.replay_idempotency_key: generic check. **Needs an OPEN exception**, which only exists if a checkout hit a stock shortfall (a tracked item without stock). If none exists, mark it not exercisable rather than inserting one |
| 5 | POS role permissions (pos_admin.go) | `PUT $API/pos/roles/admin/permissions` | `{"permissions":["sell","checkout"]}` (use keys from `GET $API/pos/roles/admin/permissions` or its list endpoint) | 200 | `SELECT COUNT(*) FROM pos_role_permissions WHERE restaurant_id=1 AND (role_slug__wk IS NULL OR permission_key__wk IS NULL)` returns 0 |
| 6 | stock role permissions (stock_permissions.go) | `PUT $API/stock/roles/admin/permissions` | `{"permissions":[<keys from the GET>]}` | 200 | the same check on stock_role_permissions |
| 7 | customer anonymise (pos_customers.go) | create one (`POST $API/pos/customers`, `{"displayName":"QA Ñandú","phone":"600000001","email":"qa-v28@invalid.local"}`), then `POST $API/pos/customers/{id}/anonymise` | none | 200 | the row: phone, email, phone__wk and email__wk are all NULL; generic check returns 0 |
| 8 | product-category import from comida (pos_catalog.go INSERT…SELECT categoria) | `POST $API/pos/products/import-confirm` | `{"items":[{"sourceType":"COMIDA_ITEM","sourceId":<comida_items.id with categoria ≠ ''>}]}` | 200 | `SELECT COUNT(*) FROM pos_product_categories WHERE restaurant_id=1 AND name__wk IS NULL` returns 0. Repeat with an accent or case variant of an existing category: no duplicate row |
| 9 | product-category import from wines (pos_catalog.go …tipo) | the same endpoint | `{"items":[{"sourceType":"VINO","sourceId":<legacy wine num>}]}` | 200 | as #8 |
| 10 | stock category import upsert (stock_import.go) | `POST $API/stock/items/import` **multipart** (`file` field, CSV). Check the dry-run/commit parameter in handleBOStockItemsImport first. qa-http sends JSON only, so build the multipart body by hand with a fixed boundary and `-H 'Content-Type: multipart/form-data; boundary=QAB'` | CSV row with category "QA Categoría ß" | 200 | stock_categories name__wk: generic check on the new row; re-import with "qa categoria ss" → upsert, no second row |
| 11 | platform settings (stripe_connect_fees.go) | `PUT $API/platform/stripe-connect/settings` (superadmin) | `{"fee_percent":1.5}` (read the current value first and restore it) | 200. It writes platform_settings only and calls no Stripe API (no client in that handler path) | `SELECT COUNT(*) FROM platform_settings WHERE setting_key__wk IS NULL` returns 0 |
| 12 | AI provider keys (whatsapp_bot_ai_config.go) | `PUT $API/bot/ai/1` (root) | `{"apiKeys":{"<provider>":"qa-dummy-not-a-key"}}` | **flagged**: the key is encrypted with VAULT_KEY (vault.EncryptBound). The QA env has no VAULT_KEY (the startup guard refuses provider env, and VAULT_KEY would also switch on the admin shared-secret layer), so the handler is expected to fail before the INSERT. Exercisable only with a throwaway VAULT_KEY in the process env; no provider call is made by the PUT itself | if exercised: `SELECT COUNT(*) FROM restaurant_ai_provider_keys WHERE restaurant_id=1 AND provider__wk IS NULL` returns 0. Delete the dummy row afterwards |
| 13 | bot booking extras (whatsapp_bot_catalog_extras.go) | **NOT exercisable over HTTP**: botCatalogExtraCreateExecute runs only from a WhatsApp bot conversation (webhook message → proposal → execute). Forbidden (no WhatsApp) | none | none | none |
| 14 | pre-shift message deliveries (pre_shift_reminders.go Claim) | **NOT exercisable**: called only by the pre-shift reminder background loop, which is disabled (BO_DISABLE_BACKGROUND_WORKERS=1), and it leads to a WhatsApp send. Forbidden | none | none | none |

**Order and cleanup:**
- Run in the order 1 → 2 → 3 (each depends on the previous), then 4 only if an OPEN exception exists, then 5-11.
- Record every created id.
- Cleanup is logical, with the scratch treated as disposable:
  - restore the role permission sets and the platform fee from the values read before;
  - remove the QA customer (already anonymised), the imported categories and products, and the QA stock category through their DELETE endpoints where they exist;
  - POS tickets, payments and fiscal documents **are not deleted**: the fiscal chain is append-only. Leave them on the scratch and record them.
- Then logout and `qa-seed-scratch -cleanup`. Rows referencing user 207 (tickets, fiscal docs) will block the user delete; keep the user on scratch and record that.

**Not covered:** #13 and #14 (WhatsApp or worker only), #12 without a VAULT_KEY, and #4 without an OPEN exception. Concurrency, and any non-scratch DB.
