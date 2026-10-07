-- Manager PIN policy.
--
-- Until now a PIN was optional everywhere: it was verified when offered, and
-- its owner written to the audit, but nothing ever REQUIRED one. That is right
-- for a 2-euro coffee void and wrong for a 300-euro "invitation" at 2 a.m.
--
-- pin_threshold_cents: any single money-reducing action (line void, comp,
--   ticket discount, refund, cash movement) whose amount reaches this value
--   needs a verified manager PIN. NULL = never by amount (previous behaviour).
-- pin_required_for_discount: every discount and every comp ("Invita") needs a
--   PIN regardless of amount. 0 = previous behaviour.
--
-- Both default to "off" so a restaurant that never configured a PIN cannot be
-- locked out of its own till by a migration. Guarded so a re-run is a no-op.
SET @pos_pin_threshold_sql = IF(
  (SELECT COUNT(*) FROM information_schema.COLUMNS
     WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'pos_settings'
       AND COLUMN_NAME = 'pin_threshold_cents') > 0,
  'SELECT 1',
  'ALTER TABLE pos_settings ADD COLUMN pin_threshold_cents BIGINT NULL'
);
PREPARE pos_pin_threshold_stmt FROM @pos_pin_threshold_sql;
EXECUTE pos_pin_threshold_stmt;
DEALLOCATE PREPARE pos_pin_threshold_stmt;

SET @pos_pin_discount_sql = IF(
  (SELECT COUNT(*) FROM information_schema.COLUMNS
     WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'pos_settings'
       AND COLUMN_NAME = 'pin_required_for_discount') > 0,
  'SELECT 1',
  'ALTER TABLE pos_settings ADD COLUMN pin_required_for_discount TINYINT(1) NOT NULL DEFAULT 0'
);
PREPARE pos_pin_discount_stmt FROM @pos_pin_discount_sql;
EXECUTE pos_pin_discount_stmt;
DEALLOCATE PREPARE pos_pin_discount_stmt;
