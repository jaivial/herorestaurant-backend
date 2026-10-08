-- Who signed a cash movement.
--
-- A cash movement (an Entrada or a Salida) moves the cash the till is expected
-- to hold, and the till is the thing a restaurant most wants protected. Until
-- now the audit trail of a movement named only the user account that posted
-- it, which for a waiter account says nothing about who authorised the money.
--
-- approved_by holds the name of the member whose PIN was actually verified by
-- the server. It is NULL when no PIN was offered, which is the normal case for
-- an ordinary waiter; it is deliberately a name and not an id, because the
-- audit trail has to stay readable after a member leaves.
--
-- Guarded on information_schema so re-running it is a no-op: a bare ADD COLUMN
-- aborts the boot of the whole backend when the column already exists, which
-- once crash-looped the dev container (see migration 169).
SET @exists := (
  SELECT COUNT(*) FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'pos_cash_movements' AND COLUMN_NAME = 'approved_by'
);
SET @sql := IF(@exists > 0, 'SELECT 1', 'ALTER TABLE pos_cash_movements ADD COLUMN approved_by VARCHAR(120) NULL AFTER created_by');
PREPARE stmt FROM @sql;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
