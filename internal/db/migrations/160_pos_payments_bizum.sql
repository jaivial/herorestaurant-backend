-- POS checkout: Bizum as a first-class tender.
-- Coordination id: pos_payment_method_bizum_v1
--   backoffice /app/pos checkout modal (pos-checkout-method-BIZUM)
--     -> pos_payments.method ENUM value 'BIZUM'
--     -> GET /admin/pos/cash-days/{date}/billing byMethod.BIZUM
--     -> pos_refunds.payment_method accepts 'BIZUM' so a Bizum sale can be refunded
-- BANK stays "Transferencia" and OTHER keeps absorbing unknown tenders.

SET @dbname := DATABASE();

SET @ddl := IF(
    (SELECT COUNT(*) FROM information_schema.COLUMNS
      WHERE TABLE_SCHEMA = @dbname AND TABLE_NAME = 'pos_payments'
        AND COLUMN_NAME = 'method' AND COLUMN_TYPE LIKE '%''BIZUM''%') = 0,
    'ALTER TABLE pos_payments MODIFY COLUMN method ENUM(''CASH'',''CARD'',''BIZUM'',''BANK'',''OTHER'') NOT NULL',
    'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @ddl := IF(
    (SELECT COUNT(*) FROM information_schema.COLUMNS
      WHERE TABLE_SCHEMA = @dbname AND TABLE_NAME = 'pos_refunds'
        AND COLUMN_NAME = 'payment_method' AND COLUMN_TYPE LIKE '%''BIZUM''%') = 0,
    'ALTER TABLE pos_refunds MODIFY COLUMN payment_method ENUM(''CASH'',''CARD'',''BIZUM'',''BANK'',''OTHER'') NOT NULL',
    'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;
