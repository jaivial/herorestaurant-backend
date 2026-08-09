-- 094: Cash day (jornada de caja) as a first-class, restaurant-wide entity.
--
-- Until now the "business day" was purely derived: pos_settings.business_day_cutoff
-- plus resolvePOSBusinessMoment() produced pos_visits.service_date, but no row ever
-- recorded that a day had been opened, by whom, or with how much float. pos_shifts
-- is per-terminal and cannot answer "is today's till open for this restaurant?".
--
-- pos_cash_days fills that gap. One row per (restaurant, business_date), enforced by
-- a unique key so two terminals racing to open the same day cannot both win.
--
-- Closing a cash day is a Z closure: the close handler reuses loadPOSCashSummary and
-- writes into pos_cash_closures, so the accounting rules stay in exactly one place.
--
-- FK types verified against the live schema before writing:
--   restaurants.id INT, bo_users.id INT,
--   pos_shifts.id BIGINT UNSIGNED, pos_visits.id BIGINT UNSIGNED,
--   pos_cash_closures.id BIGINT UNSIGNED.

SET @dbname = DATABASE();

CREATE TABLE IF NOT EXISTS pos_cash_days (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    business_date DATE NOT NULL,
    status ENUM('OPEN','CLOSED') NOT NULL DEFAULT 'OPEN',
    opened_by INT NOT NULL,
    closed_by INT NULL,
    opening_cash_cents BIGINT NOT NULL DEFAULT 0,
    opened_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    closed_at DATETIME NULL,
    -- Set when the day was opened while earlier days were still unclosed and the
    -- operator confirmed the override. Kept for audit, never used as a filter.
    forced_open TINYINT(1) NOT NULL DEFAULT 0,
    notes VARCHAR(500) NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_pos_cash_days_tenant_id (restaurant_id, id),
    -- The invariant: one cash day per restaurant per business date, enforced by the DB
    -- rather than by a read-then-write race in the handler.
    UNIQUE KEY uq_pos_cash_days_date (restaurant_id, business_date),
    KEY idx_pos_cash_days_open (restaurant_id, status, business_date),
    CONSTRAINT fk_pos_cash_days_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
    CONSTRAINT chk_pos_cash_days_cash CHECK (opening_cash_cents >= 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ---------------------------------------------------------------------------
-- Link existing POS aggregates to the cash day. Nullable so historical rows and
-- any future terminal-only flow keep working without a cash day.
-- ---------------------------------------------------------------------------
SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='pos_visits' AND COLUMN_NAME='cash_day_id') > 0,
  'SELECT 1',
  'ALTER TABLE pos_visits ADD COLUMN cash_day_id BIGINT UNSIGNED NULL'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='pos_visits' AND INDEX_NAME='idx_pos_visits_cash_day') > 0,
  'SELECT 1',
  'ALTER TABLE pos_visits ADD KEY idx_pos_visits_cash_day (restaurant_id, cash_day_id)'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='pos_visits' AND CONSTRAINT_NAME='fk_pos_visits_cash_day') > 0,
  'SELECT 1',
  'ALTER TABLE pos_visits ADD CONSTRAINT fk_pos_visits_cash_day FOREIGN KEY (restaurant_id, cash_day_id) REFERENCES pos_cash_days(restaurant_id, id)'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='pos_shifts' AND COLUMN_NAME='cash_day_id') > 0,
  'SELECT 1',
  'ALTER TABLE pos_shifts ADD COLUMN cash_day_id BIGINT UNSIGNED NULL'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='pos_shifts' AND INDEX_NAME='idx_pos_shifts_cash_day') > 0,
  'SELECT 1',
  'ALTER TABLE pos_shifts ADD KEY idx_pos_shifts_cash_day (restaurant_id, cash_day_id)'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='pos_shifts' AND CONSTRAINT_NAME='fk_pos_shifts_cash_day') > 0,
  'SELECT 1',
  'ALTER TABLE pos_shifts ADD CONSTRAINT fk_pos_shifts_cash_day FOREIGN KEY (restaurant_id, cash_day_id) REFERENCES pos_cash_days(restaurant_id, id)'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='pos_cash_closures' AND COLUMN_NAME='cash_day_id') > 0,
  'SELECT 1',
  'ALTER TABLE pos_cash_closures ADD COLUMN cash_day_id BIGINT UNSIGNED NULL'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- A cash-day Z closure spans the whole restaurant day, not one terminal shift, so
-- shift_id has to become optional. Terminal X/Y/Z closures keep populating it.
SET @s = (SELECT IF((SELECT IS_NULLABLE FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='pos_cash_closures' AND COLUMN_NAME='shift_id') = 'YES',
  'SELECT 1',
  'ALTER TABLE pos_cash_closures MODIFY COLUMN shift_id BIGINT UNSIGNED NULL'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='pos_cash_closures' AND INDEX_NAME='idx_pos_cash_closures_cash_day') > 0,
  'SELECT 1',
  'ALTER TABLE pos_cash_closures ADD KEY idx_pos_cash_closures_cash_day (restaurant_id, cash_day_id)'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='pos_cash_closures' AND CONSTRAINT_NAME='fk_pos_cash_closures_cash_day') > 0,
  'SELECT 1',
  'ALTER TABLE pos_cash_closures ADD CONSTRAINT fk_pos_cash_closures_cash_day FOREIGN KEY (restaurant_id, cash_day_id) REFERENCES pos_cash_days(restaurant_id, id)'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- ---------------------------------------------------------------------------
-- Backfill: one cash day per service_date already present in pos_visits.
--
-- A day is only marked CLOSED when it is in the past AND has no open visits and
-- no open tickets left. Anything else stays OPEN so it surfaces in the "unclosed
-- previous days" alert, which is the behaviour the operator expects: the system
-- must not silently swallow a day that was never actually cashed up.
--
-- INSERT IGNORE keeps the migration re-runnable against uq_pos_cash_days_date.
-- ---------------------------------------------------------------------------
INSERT IGNORE INTO pos_cash_days
    (restaurant_id, business_date, status, opened_by, opening_cash_cents, opened_at, closed_at, notes)
SELECT
    v.restaurant_id,
    v.service_date,
    IF(
        v.service_date < CURDATE()
        AND NOT EXISTS (
            SELECT 1 FROM pos_visits ov
            WHERE ov.restaurant_id = v.restaurant_id
              AND ov.service_date = v.service_date
              AND ov.status = 'OPEN'
        )
        AND NOT EXISTS (
            SELECT 1 FROM pos_tickets ot
            JOIN pos_visits tv ON tv.restaurant_id = ot.restaurant_id AND tv.id = ot.visit_id
            WHERE ot.restaurant_id = v.restaurant_id
              AND tv.service_date = v.service_date
              AND ot.status = 'OPEN'
        ),
        'CLOSED', 'OPEN'
    ) AS status,
    -- opened_by / opened_at come from the earliest visit of that service date.
    (SELECT fv.opened_by FROM pos_visits fv
      WHERE fv.restaurant_id = v.restaurant_id AND fv.service_date = v.service_date
      ORDER BY fv.opened_at ASC, fv.id ASC LIMIT 1) AS opened_by,
    0 AS opening_cash_cents,
    MIN(v.opened_at) AS opened_at,
    IF(
        v.service_date < CURDATE()
        AND NOT EXISTS (
            SELECT 1 FROM pos_visits ov
            WHERE ov.restaurant_id = v.restaurant_id
              AND ov.service_date = v.service_date
              AND ov.status = 'OPEN'
        )
        AND NOT EXISTS (
            SELECT 1 FROM pos_tickets ot
            JOIN pos_visits tv ON tv.restaurant_id = ot.restaurant_id AND tv.id = ot.visit_id
            WHERE ot.restaurant_id = v.restaurant_id
              AND tv.service_date = v.service_date
              AND ot.status = 'OPEN'
        ),
        MAX(v.opened_at), NULL
    ) AS closed_at,
    'Backfill 094' AS notes
FROM pos_visits v
GROUP BY v.restaurant_id, v.service_date;

UPDATE pos_visits v
JOIN pos_cash_days d
  ON d.restaurant_id = v.restaurant_id AND d.business_date = v.service_date
SET v.cash_day_id = d.id
WHERE v.cash_day_id IS NULL;
