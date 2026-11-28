# Result: repair-padidem1 (10 tenant-scoped client idempotency keys; code-only, nothing applied)

## Exact key scope (schema.json + derived local metadata: utf8mb4_unicode_ci PAD SPACE)
All are UNIQUE (restaurant_id, <key>), with the key a varchar utf8mb4_unicode_ci column:
- member_time_allocations.idempotency_key
- pos_cover_adjustments.idempotency_key
- pos_payments.idempotency_key
- pos_refunds.idempotency_key
- pos_stock_exceptions.replay_idempotency_key (NULL allowed; NULLs stay distinct)
- pos_tickets.checkout_idempotency_key (NULL allowed)
- pos_tickets.creation_idempotency_key
- pos_visits.open_idempotency_key
- stock_movements.idempotency_key
- stock_production_orders.idempotency_key

## Required future DDL (NOT executed; additive; keep the raw R5 indexes until QA; names = dbmigrate PGName, identical to the padplan output)
```
CREATE UNIQUE INDEX "ixpad__member_time_allocations__uq_member_time_allocat_b4151cea" ON "member_time_allocations" ("restaurant_id", (rtrim("idempotency_key", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__pos_cover_adjustments__uq_pos_cover_adjustment__f16820e6" ON "pos_cover_adjustments" ("restaurant_id", (rtrim("idempotency_key", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__pos_payments__uq_pos_payment_idempotency" ON "pos_payments" ("restaurant_id", (rtrim("idempotency_key", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__pos_refunds__uq_pos_refund_idempotency" ON "pos_refunds" ("restaurant_id", (rtrim("idempotency_key", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__pos_stock_exceptions__uq_pos_stock_exception_replay" ON "pos_stock_exceptions" ("restaurant_id", (rtrim("replay_idempotency_key", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__pos_tickets__uq_pos_ticket_checkout" ON "pos_tickets" ("restaurant_id", (rtrim("checkout_idempotency_key", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__pos_tickets__uq_pos_ticket_creation" ON "pos_tickets" ("restaurant_id", (rtrim("creation_idempotency_key", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__pos_visits__uq_pos_visit_open_idempotency" ON "pos_visits" ("restaurant_id", (rtrim("open_idempotency_key", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__stock_movements__uq_stock_movement_idempotency" ON "stock_movements" ("restaurant_id", (rtrim("idempotency_key", ' ')) COLLATE "dbmigrate"."ci_ai");
CREATE UNIQUE INDEX "ixpad__stock_production_orders__uq_stock_production_idempotency" ON "stock_production_orders" ("restaurant_id", (rtrim("idempotency_key", ' ')) COLLATE "dbmigrate"."ci_ai");
```
Before creating them, probe existing data for rows that collide after rtrim (read-only, not done).

## Caller catalogue (all writers incl. assistant) and treatment
| site | operation | treatment |
|---|---|---|
| backoffice_pos.go:847 / assistant_pos_tools.go:48 | INSERT pos_visits open key | raw value; uniqueness enforced by the index |
| backoffice_pos.go:853 | replay lookup v.open_idempotency_key | PATCHED, PG pad expression scoped by restaurant_id |
| backoffice_pos.go:870, operations.go:317, assistant:72 | INSERT pos_tickets creation key | raw; the "duplicate" substring check matches PG 23505 for any index (source contract) |
| backoffice_pos_operations.go:323 | replay lookup creation key | PATCHED, PG pad |
| backoffice_pos_checkout.go:316/319 | checkout replay identity | PATCHED on BOTH dialects (reviewed bugfix, see below) |
| backoffice_pos_checkout.go:383 | UPDATE SET checkout key (WHERE id) | raw, unchanged |
| checkout.go:356, assistant:83 | INSERT pos_payments | raw; error -> 400 / propagated (source) |
| checkout.go:700, assistant:92 | INSERT pos_refunds | raw; the "duplicate" substring returns the duplicate answer (source) |
| checkout.go:910 | INSERT pos_cover_adjustments | raw; "duplicate" substring (source) |
| production_labour.go:203 | INSERT member_time_allocations | raw; generic IsUniqueViolation (matches ixpad) |
| reports.go:285 | UPDATE SET replay key (WHERE id) | raw, unchanged |
| stock.go:1460/1463 | INSERT stock_movements + IsUniqueViolationOn | PATCHED: adds the ixpad name |
| stock.go:968/1147, stock_documents.go:671, recipes.go:531/553, checkout.go:246/780, stock_realtime.go:181/291/395 | INSERT stock_movements (mostly generated keys) | raw; errors propagate (index enforces) |
| recipes.go:499/502 | INSERT stock_production_orders + IsUniqueViolationOn | PATCHED: adds the ixpad name |

None of these keys has an ON CONFLICT or INSERT IGNORE site (repo grep).

### Checkout replay bugfix (MySQL text changed — explicit, reviewed)
- **Before:** Go compared checkoutKey.String == idempotencyKey exactly. That is stricter than the source unique key (unicode_ci PAD SPACE), so a key the collation treats as equal (case, accent, trailing space) answered "not open" (409) instead of duplicate.
- **Now:** the locked SELECT returns the DB comparison:
  - MySQL: COALESCE(checkout_idempotency_key = ?, 0), under the column collation;
  - PG: the ixpad expression comparison.
  It is scanned as sameKey, and a NULL stored key gives false. No Go EqualFold and no trimming of stored values.
- **Payload:** still not compared, as in the source. A replay returns Duplicate before any payment insert, so a replay never charges or records again.
- **Args:** idempotencyKey, restaurantID, ticketID, rebound once by nativeSQL.

## Composite readiness gate
- internal/db/padkeys.go now holds RequiredPadKeyIndexes: 15 indexes, the 5 global ones from padglobal1 plus these 10 tenant-scoped ones.
- Each part is either a plain column or a pad expression. Validation is a read-only pg_catalog check (two queries) and needs every one of the following:
  - the table and index both in current_schema();
  - btree, unique, valid, ready, live, non-partial;
  - indnkeyatts equal to the number of parts;
  - for a plain part, indkey[k] equal to the column attnum;
  - for a pad part, indkey[k]=0, indcollation[k]=dbmigrate.ci_ai, indclass[k]=the default btree text opclass, and pg_get_indexdef(col k) matching ^rtrim((col)::text, ' '::text)$-style rendering.
- The rendering regex is UNVERIFIED (no real index observed) and needs manual QA. On mismatch it refuses; it never allows.
- The index name is not a shortcut: the name only selects the row, and every property is checked.
- It runs in OpenBusinessForTool. The server PG refusal is unchanged (comment only).
- The previous single-key regex gate is replaced by this validator.

## Build / inventory
- go build -p 2 ./... OK.
- Inventory r97: 2501 sites (+1 gate query), 285 files, all native, 0 portable audit failures. This is syntax only.

## Coverage from derived local metadata (not approx)
- Of the 121 unique text keys: 116 have a PAD SPACE text part (proposed), 4 are enum-only (gated, no pad expression), and 1 is NO PAD (bot_inbound_queue.uq_inbound_msg, 0900_ai_ci).
- Also 18 PRIMARY keys with PAD text (plus 1 enum-only).
- Caller-patched: 15 keys (5 global + 10 idempotency). Remaining: 101 of the 121 plus the 18 PRIMARY keys, none of them caller-patched.

## Gates (not claimed)
- Runtime 0; no handler QA.
- ci_ai is only an approximation of unicode_ci (general_ci/ICU equivalence unresolved).
- The DDL is unapplied; the PG server is disabled; tools are report-only and index-gated.
- R5 untouched; 1117 pending / 731 orphans; checkout 503; 17 legitimate MySQL-only; 5 query shapes.
- No SQL, DB writes, provider, payment or fiscal calls.

## Next
1. The coordinator reviews the 15 DDL statements.
2. Read-only collision probes.
3. Manual QA of the gate rendering and of these callers.
4. The next bounded key group (the remaining S-CLIENT-MIXED keys, then the PRIMARY keys).
