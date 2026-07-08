-- 050: reservation_manager — enforce one row per (restaurant_id, reservationDate)
--
-- Background
--   The legacy schema has no unique key on (restaurant_id, reservationDate).
--   Legacy PHP dumps inserted directly, leaving many dates with multiple rows
--   (e.g. 2026-07-18 had 5 rows with dailyLimit = 10, 10, 30, 20, 35).
--   The Go backoffice setter does DELETE+INSERT, so new edits land as the
--   highest-id row — but the orphans stay, and inconsistent LIMIT-1 reads
--   across handlers return different limits for the same date.
--
-- The runtime sql_mode includes NO_ZERO_DATE, which rejects comparisons and
-- inserts of the literal '0000-00-00' sentinel. The legacy table contains
-- rows with that value (PHP "no date" placeholder). We relax the session
-- for the duration of this migration so we can inspect and clean them.
--
-- Strategy
--   1. Drop the rows whose reservationDate is '0000-00-00' — they cannot
--      represent a real booking day and would block the unique index.
--   2. Keep the row with the highest `id` per (restaurant_id, reservationDate)
--      and delete the rest in a single self-join DELETE.
--   3. Ensure a UNIQUE index on (restaurant_id, reservationDate) exists.
--      Idempotent: the index creation is skipped if it's already present,
--      regardless of which migration created it.
--
-- All statements are idempotent. Re-running this migration is a no-op.

SET SESSION sql_mode = 'STRICT_TRANS_TABLES,ERROR_FOR_DIVISION_BY_ZERO,NO_ENGINE_SUBSTITUTION';

-- ── 1. Drop the '0000-00-00' placeholder rows ────────────────────────────────
-- They cannot be used by the booking flow (hour-data, month-availability all
-- require valid ISO dates) and they would block the unique index because
-- multiple legacy rows share the same sentinel value.

DELETE FROM reservation_manager WHERE reservationDate = '0000-00-00';

-- ── 2. Dedupe ────────────────────────────────────────────────────────────────
-- For each (restaurant_id, reservationDate) with more than one row, delete
-- every row whose id is not the max. Rows that already have only one entry
-- are left untouched.

DELETE rm
FROM reservation_manager rm
INNER JOIN (
    SELECT restaurant_id, reservationDate, MAX(id) AS max_id
    FROM reservation_manager
    GROUP BY restaurant_id, reservationDate
    HAVING COUNT(*) > 1
) keepers
    ON keepers.restaurant_id   = rm.restaurant_id
   AND keepers.reservationDate = rm.reservationDate
   AND rm.id < keepers.max_id;

-- ── 3. Unique index ──────────────────────────────────────────────────────────
-- Drop the legacy non-unique composite index from migration 002 if it's still
-- present (it conflicts by name with the UNIQUE one we want), then add the
-- unique key. Both steps are conditional so re-running is a no-op.

SET @legacy_idx := (
    SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
    WHERE TABLE_SCHEMA = DATABASE()
      AND TABLE_NAME   = 'reservation_manager'
      AND INDEX_NAME   = 'idx_reservation_manager_restaurant_date'
);
SET @uniq_idx := (
    SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
    WHERE TABLE_SCHEMA = DATABASE()
      AND TABLE_NAME   = 'reservation_manager'
      AND INDEX_NAME   = 'uniq_reservation_manager_restaurant_date'
);

-- Drop the legacy non-unique composite index if present.
SET @ddl := IF(
    @legacy_idx > 0,
    'ALTER TABLE reservation_manager DROP INDEX idx_reservation_manager_restaurant_date',
    'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- Add the unique key if not already present.
SET @ddl := IF(
    @uniq_idx = 0,
    'ALTER TABLE reservation_manager ADD UNIQUE KEY uniq_reservation_manager_restaurant_date (restaurant_id, reservationDate)',
    'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;
