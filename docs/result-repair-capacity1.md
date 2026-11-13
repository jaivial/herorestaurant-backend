# result-repair-capacity1: booking capacity and day lock

All of this is code only. `go build -p 2 ./...` exits 0. I ran no tests and no SQL, wrote to no DB, and made no sends. Runtime-verified is 0. PG is still ready:false and the server still refuses PG. R5 is untouched and the schema is unchanged.

## Inventory r89

`/tmp/villacarmen-bot-20261111/sql-inventory-r89.json`: 2493 sites in 283 files, all classified. Blocked 0, provisional 0.

| Classification | Sites |
|---|---|
| native | 849 |
| portable | 1476 |
| selected | 82 |
| pgOnly | 42 |
| mysqlOnly | 44 |

The 3 new sites are 2 lock statements plus 1 recheck read.

## Actual capacity rule (from the code)

- **The only server-side capacity rule is in the WhatsApp bot.** `botDayCapacity`:
  - limit = `reservation_manager.dailyLimit` for (restaurant_id, reservationDate), using the latest id; otherwise the `loadReservationDefaults(...).DailyLimit` default;
  - total = `SUM(bookings.party_size)` for (restaurant_id, reservation_date);
  - `botOverCapacity` rejects when `limit <= 0` or when total − own + want > limit.
- **The front booking path (`prepareFrontBooking`), the Stripe checkout and the admin insert have no capacity check in the source.** Their availability is UI and advisory only (`reservations_availability`, `computeAvailableHoursForPartySize` for modifications). I did **not** change that policy: no capacity rule was invented for those paths.
- **`reservation_location_occupancy`** (`applyBookingLocationOccupancy`) is a headcount ledger per floor and salon. It is **not** a capacity guard: it upserts counts and never refuses.

## Lock contract (`booking_day_lock_v1`, `lockBookingDays`)

- The lock is **transaction-scoped**. It is released only by the caller's commit or rollback, so no pooled session lock is involved.
- **PG:** `pg_advisory_xact_lock(hashtextextended('booking_day:<rid>:<YYYY-MM-DD>',0))` per distinct date, taken in **ascending order**. This avoids deadlock when a booking moves between dates.
- **MySQL:** `SELECT id FROM restaurants WHERE id=? FOR UPDATE`, taken once. It locks an existing parent row, which is a per-restaurant superset of the day key. MySQL has no transaction-scoped named lock, and no missing-row lock is relied on.
- The key matches the rule's granularity, (restaurant, date).
- **It must be the first statement in the tx that touches the day.** Under MySQL RR, the snapshot starts at the first consistent read, so reads after the lock see every committed booking. PG RC reads per statement.
- **Lock order across paths:** the checkout tx locks its `booking_checkouts` row first and then the day; the bot daily-limit tx locks its proposal claim row first and then the day. Every other path takes only the day lock, so there is no cycle. On MySQL, writers that do not take the lock still need a shared FK lock on the same restaurants row when they insert into bookings, so they wait briefly. That is safe but can contend.

## Coverage

| Writer | Change |
|---|---|
| `insertBookingTx` (front, `insertBooking` and admin `bookings_admin:752` through `insertBooking`, Stripe checkout `completeCheckoutBookingTx`) | Takes the day lock first. No capacity rule (source policy). |
| Bot `create_booking` | The advisory pre-check is kept. Then **one tx**: lock → `botDayCapacityOn(tx)` recheck → INSERT → commit. Previously the check and the insert were separate (race). |
| Bot `modify_booking` (date or people) | **One tx**: lock old and new days in order → re-read the booking under the lock (if it moved, the change is refused) → recheck on the tx → UPDATE → commit. Modification history is still written after commit (source). |
| Bot `config_daily_limit` execute | Takes the day lock inside the claim tx before DELETE+INSERT, so a limit decrease and an in-flight booking are serialised. |
| **Not covered (gate):** `handleBOConfigDailyLimitSet` and `handleUpdateDailyLimit` | Autocommit DELETE+INSERT, which is non-atomic: in between there is a window with no row, where the default applies. |
| **Not covered (gate):** `backoffice_booking_mutations` (2 sites), `backoffice_bookings:984`, `bookings_admin:1045`, `whatsapp_bot_special_admin_tools` (1766/2070), `whatsapp_bot_special_create:313`, `modifications_api` (hours check), cancellations | Inserts, moves and cancels not locked. Cancellation only reduces the total. These paths have no capacity rule today, but they change the total the bot recheck reads. |
| `loadReservationDefaults` | Still read on `s.db` (config), outside the tx, as in the source. A concurrent change of the default limit is not serialised (gate). |

## Race argument (code review only)

1. Two bot creates for the same day: B waits on A's lock. After A commits, B's recheck SUM includes A, so B cannot overbook.
2. A front or checkout insert racing a bot create: both take the day lock, so the bot recheck sees the committed front booking. The front path itself still does not check capacity (source).
3. A limit decrease (bot) racing a bot create: serialised.

## Crash and error behaviour

- A lock or recheck error answers the existing "error creando/modificando la reserva" or "error consultando capacidad", and the tx rolls back.
- "Over capacity" inside the tx rolls back with no write.
- In the Stripe checkout, a lock or insert failure rolls back. The checkout stays `paid` and is retried (booking-atomic1), with no financial action. The conditional failed-vs-completed transitions are unchanged.

## Verified from booking-atomic1 code

`finishFrontBooking` runs only after `completeCheckoutBookingTx` commits: QR (`ensureBookingQR`), receipt (`buildReceipt` + `storeReceipt`), broadcast and notifications. None of them run inside the tx.

## Gates (not waived)

- Notifications are at-most-once; no durable email outbox exists, so no exactly-once claim is made.
- Uncovered writers are listed above.
- The front path has no capacity rule (source).
- MySQL per-restaurant lock contention.
- Concurrency claims are from code review only.
- Remaining gates: the 10 guarded IGNORE sites, TIME, NUL/JSON, collation, 1117 import proof ready:false, 4 NOT VALID FKs with 731 orphans, and the general gates.

## Read-only and local QA plan (later)

1. Two concurrent bot creates at the limit: exactly one succeeds.
2. A bot modify moving between days while another booking is created: no overbooking and no deadlock.
3. A limit decrease concurrent with a create.
4. On MySQL, `SHOW ENGINE INNODB STATUS` shows no deadlocks.
5. On PG, `pg_locks` shows advisory locks released at commit.

## Next

1. TIME, NUL and collation adapters.
2. Day lock for the remaining writers and the daily-limit handlers (atomic DELETE+INSERT).
3. Manual read-only PG QA.

## Files and backup

- `internal/api/booking_day_lock.go` (new)
- `internal/api/booking_insert.go`
- `internal/api/whatsapp_bot_executor.go`
- `internal/api/whatsapp_bot_config_admin.go`

Backup: `/tmp/cap1.bak.tar`.

