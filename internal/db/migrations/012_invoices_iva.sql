-- Add IVA/tax fields to invoices table
ALTER TABLE invoices
  ADD COLUMN invoice_number VARCHAR(32) NULL AFTER restaurant_id,
  ADD COLUMN iva_rate DECIMAL(5,2) NULL DEFAULT 10.00 AFTER amount,
  ADD COLUMN iva_amount DECIMAL(10,2) NULL AFTER iva_rate,
  ADD COLUMN total DECIMAL(10,2) NULL AFTER iva_amount;

-- Add index for invoice_number
ALTER TABLE invoices
  ADD INDEX idx_invoices_invoice_number (restaurant_id, invoice_number);

-- Add restaurant CIF for invoice generation
ALTER TABLE restaurants
  ADD COLUMN cif VARCHAR(32) NULL AFTER avatar;
