# result-repair-capacity2: capacity serialization across all writers

All changes are code only. `go build -p 2 ./...` exits 0. I ran no tests and no SQL, wrote nothing to the DB, and made no sends. Runtime-verified is 0. PG is still ready:false and the server still refuses PG. The R5 proof is untouched and the schema is unchanged.

## Inventory r90

`/tmp/villacarmen-bot-20261111/sql-inventory-r90.json`: 2498 sites in 284 files, all classified. 0 blocked, 0 provisional.

| Kind | Sites |
|---|---|
| native | 851 |
| portable | 1478 |
| selected | 82 |
| pgOnly | 43 |
| mysqlOnly | 44 |

The 5 new sites are: the PG restaurant FOR SHARE, the config FOR UPDATE, two date reads, and the in-tx default read. Callers keep their own SQL sites: the update helper takes a closure, so their statements stay inventoried.

## Policy (unchanged)

- Only the bot enforces capacity: per-day override, or the restaurant default, against SUM(party_size).
- No rule was added to front or admin. Manual overrides stay policy.
- The goal is only that every writer of the total or the limit joins the lock, so the bot recheck reads a committed and serialized state.

## Lock contract (`booking_day_lock_v1`)

Order is always **restaurant → days**. Every lock is transaction-scoped and ends at commit or rollback, so there are no pooled session locks.

**`lockBookingDays(tx, rid, dates…)`**
- **PG:** `SELECT id FROM restaurants WHERE id=? FOR SHARE`, then `pg_advisory_xact_lock(hashtextextended('booking_day:rid:date',0))` per distinct date in ascending order.
- **MySQL:** `SELECT id FROM restaurants WHERE id=? FOR UPDATE`, which covers all days. It locks an existing row; there is no reliance on missing-row or gap locks.

**`lockRestaurantCapacityConfig`** (new, both dialects): restaurants FOR UPDATE. On PG it excludes every day writer, because they hold the row FOR SHARE first. On MySQL it is the same row lock.

**Rows locked earlier in a transaction:** some transactions lock their own row before taking the restaurant lock. The checkout path locks its `booking_checkouts` row first; the proposal executes lock their `wa_bot_op_proposals` claim row first. No transaction ever takes those rows after the restaurant lock, so there is no cycle.

**Deadlock check:**
- A pair of date moves locks the restaurant first, then days in ascending order.
- PG FK inserts take FOR KEY SHARE, which is compatible with FOR SHARE.
- A default writer's FOR UPDATE waits for, and blocks, day writers. Nothing upgrades its lock.

## Isolation argument (code review)

- Only the bot **recheck** transactions read capacity: bot create, and bot modify for a date or party change. Both open the transaction and take the lock **first**, before any consistent read.
  - MySQL RR fixes its snapshot at the first non-locking read, so the snapshot is taken after the lock. It therefore includes every writer that committed before releasing the lock.
  - PG RC reads per statement.
- Plain `SUM` is enough. `SUM … FOR UPDATE` is not used, and there is no reliance on gap locks.
- Writers that do consistent reads before locking (reactivate, proposal executes) only see their own stale snapshot for their own reads. They have no capacity rule (source). Their writes become visible to the recheck only after their commit, which also releases the lock.
- **Default limit:** `botDayCapacityOn` now reads `restaurant_reservation_defaults.daily_limit` **inside the locked tx**. It applies exactly the rule from `loadReservationDefaults`: a valid 0..500 value is used; otherwise, or when there is no row, it falls back to `defaultDailyLimit`. There is no cfg cache.

## Exhaustive writer list (`INSERT`/`UPDATE`/`DELETE bookings`, `reservation_manager`, defaults)

### Capacity-increasing writers: all now locked

| Writer | Lock |
|---|---|
| `insertBookingTx` (front, `insertBooking`, admin `bookings_admin:752`, Stripe checkout) | day (capacity1) |
| `boInsertBooking` (backoffice create) | day, after BeginTx |
| `boUpdateBooking` (backoffice edit) | `execBookingUpdateDayLocked`: old + new day; re-reads the day under the lock; a concurrent move returns an error |
| `handleEditBooking` (legacy admin edit) | same helper |
| `handleUpdateReservation` (`modifications_api`, date or party field) | same helper. Other fields are unchanged, with no capacity effect. |
| `handleReservationModify` (customer self-service) | same helper |
| `handleBOBookingReactivate` and `handleReactivateBooking` (re-insert from cancelled) | day, inside their tx |
| `botToolCreateBooking` / `botToolModifyBooking` | day + recheck in one tx (capacity1). Modify locks old + new days and re-reads under the lock. |
| `botSpecialRescheduleWrite` | old + new day, after the claim row |
| `botRestoreExecute` | day of the restored row |
| `botCreateExecute` / `botSpecialCreateExecute` | day |

### Limit writers: all now locked

| Writer | Change |
|---|---|
| `handleBOConfigDailyLimitSet` | DELETE + INSERT now one tx under the day lock. Previously it was autocommit with a no-row window, and the DELETE error was ignored; that error now propagates. |
| `handleUpdateDailyLimit` | same |
| `botConfigDailyLimitExecute` | day (capacity1) |
| `upsertReservationDefaults` (default `daily_limit`) | one tx under `lockRestaurantCapacityConfig`. Same upsert and the same types. |

### Not locked, by reasoning

- **Total-decreasing writers:**
  - cancellations and deletes: `handleBOBookingCancel`, `handleDeleteBooking`, bot cancel, `botCancelExecute`, `assistantDeleteBooking`, public cancel pages and API;
  - occupancy decrements.

  A concurrent decrease can only make the recheck see a total that is too high, which is conservative, so it can never cause an overbooking.
- **Non-capacity updates** that change neither date, party size nor existence:
  - notes (`botToolAddBookingNote`), table number, `is_event`/send-docs flags, QR and receipt URLs;
  - reminders, n8n, public confirm and rice;
  - `botEditExecute`: its whitelist has no date or party, and children are inside the party;
  - extras, flags, floor, mobility, group menu, rice proposal executes.
- **`provisionRestaurantDefaults`** inserts the defaults row with column defaults and does not set `daily_limit` (gate: insert while a recheck runs; first provisioning only).

## Error and crash behaviour

- A lock, re-read or recheck error makes the existing handler error path roll back with no write.
- A concurrent date move returns `errBookingMovedConcurrently` (fail closed; the caller retries).
- An absent booking keeps the source behaviour: a plain UPDATE that changes 0 rows.

## Remaining gates

- Concurrency is code-reviewed only, with no QA.
- Under MySQL every writer locks the whole restaurant (contention).
- The front and admin paths have no capacity rule (source).
- Notifications are at-most-once.
- Still open: the 10 guarded IGNORE sites, TIME, NUL/JSON, collation, the 1117 import proof (ready:false), 4 NOT VALID FKs with 731 orphans, and the general gates.

## Manual read-only and local QA plan (later)

1. Bot create at the limit concurrently with a backoffice create: the bot recheck counts the backoffice booking.
2. A limit decrease (backoffice) concurrent with a bot create.
3. A default limit change concurrent with a recheck.
4. Concurrent moves A→B and B→A: no deadlock.
5. A self-service modify while staff moves the same booking: the second one gets `errBookingMovedConcurrently`.
6. Inspect lock waits in PG `pg_locks` and MySQL `performance_schema.data_locks`.

## Next

1. TIME, NUL and collation adapters.
2. Manual read-only PG QA.

## Files

- `booking_day_lock.go`
- `booking_day_lock_update.go` (new)
- `booking_insert.go`
- `backoffice_booking_mutations.go`
- `backoffice_bookings.go`
- `bookings_admin.go`
- `modifications_api.go`
- `reservation_self_service.go`
- `whatsapp_bot_executor.go`
- `whatsapp_bot_special_admin_tools.go`
- `whatsapp_bot_special_create.go`
- `backoffice_config.go`
- `reservations_availability.go`

Backup: `/tmp/cap2.bak.tar`.

