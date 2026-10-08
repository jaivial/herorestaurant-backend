-- What the guest physically handed over for a cash payment.
--
-- pos_payments.amount_cents (+ tip_cents) is what was APPLIED to the ticket:
-- paying a 34,00 bill with a 50 note stores 3400. The receipt could therefore
-- never print "Entregado 50,00 / Cambio 16,00" without inventing the 50.
--
-- tendered_cents is recorded only when the till actually knows it (the cashier
-- typed the amount received) and only for CASH; it is NULL everywhere else,
-- including every payment taken before this column existed, and the receipt
-- prints change only when it is present. Change = tendered - (amount + tip).
SET @pos_tendered_sql = IF(
  (SELECT COUNT(*) FROM information_schema.COLUMNS
     WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'pos_payments'
       AND COLUMN_NAME = 'tendered_cents') > 0,
  'SELECT 1',
  'ALTER TABLE pos_payments ADD COLUMN tendered_cents BIGINT NULL AFTER tip_cents'
);
PREPARE pos_tendered_stmt FROM @pos_tendered_sql;
EXECUTE pos_tendered_stmt;
DEALLOCATE PREPARE pos_tendered_stmt;
