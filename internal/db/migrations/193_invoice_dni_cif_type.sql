-- Coordination id: invoice_dni_cif_type_v1
--
-- The type of the customer's fiscal identifier, as chosen in the invoice form
-- ("dni" for a person's DNI/NIE, "cif" for a company). The invoice PDF, the
-- email body and the WhatsApp attachment print the label that matches it
-- instead of always "DNI/CIF".
-- Idempotent: the column is added only if missing. The invoices created
-- before this column keep NULL and fall back to the shape of the value.
SET @exists := (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'invoices' AND COLUMN_NAME = 'customer_dni_cif_type');
SET @sql := IF(@exists = 0, 'ALTER TABLE invoices ADD COLUMN customer_dni_cif_type VARCHAR(8) NULL AFTER customer_dni_cif', 'DO 0');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
