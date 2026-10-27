-- Coordination id: invoice_phone_country_code_v1
--
-- The country calling code of the customer's phone, chosen in the invoice
-- form next to the number (digits only, e.g. "34"). With it customer_phone
-- holds the national number and the WhatsApp send of the invoice reaches an
-- unambiguous E.164 destination without asking the operator to confirm it.
-- Idempotent: the column is added only if missing. The invoices created
-- before this column (and CSV imports without a code) keep NULL and fall back
-- to the shape of the stored number.
SET @exists := (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'invoices' AND COLUMN_NAME = 'customer_phone_country_code');
SET @sql := IF(@exists = 0, 'ALTER TABLE invoices ADD COLUMN customer_phone_country_code VARCHAR(8) NULL AFTER customer_phone', 'DO 0');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
