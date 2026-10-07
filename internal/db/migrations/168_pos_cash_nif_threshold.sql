-- Ley 37/1992 (LIVA) art. 20.Uno.2º b): a cash payment over 3.000 € needs the
-- buyer's NIF. The threshold is stored per restaurant because it can only ever
-- go DOWN from the statutory value, never above it: a column with a CHECK that
-- caps it means a misconfiguration cannot quietly let the till accept 5.000 €
-- in cash without identifying the payer.
--
-- The check lives in the checkout path, not here: the threshold only becomes
-- meaningful when a payment method is actually CASH.
SET @pos_cash_nif_sql = IF(
  (SELECT COUNT(*) FROM information_schema.COLUMNS
     WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'pos_settings'
       AND COLUMN_NAME = 'cash_nif_threshold_cents') > 0,
  'SELECT 1',
  'ALTER TABLE pos_settings ADD COLUMN cash_nif_threshold_cents BIGINT NOT NULL DEFAULT 300000'
);
PREPARE pos_cash_nif_stmt FROM @pos_cash_nif_sql;
EXECUTE pos_cash_nif_stmt;
DEALLOCATE PREPARE pos_cash_nif_stmt;
