-- Coordination id: invoice_numbering_v1
--
-- Numbering series of invoices, per restaurant and per year. The row holds the
-- format the operator configured in the BackOffice ("Numeracion de facturas")
-- plus the next sequence number of that year's series. next_number only grows,
-- so a number is never reused inside the same year.
-- Rollback: drop the table. The invoices keep the invoice_number they already
-- carry (this table only holds the series configuration, never invoice data).
CREATE TABLE IF NOT EXISTS `restaurant_invoice_settings` (
  `restaurant_id` INT NOT NULL,
  `year` INT NOT NULL,
  `prefix` VARCHAR(32) NOT NULL DEFAULT 'F-',
  `suffix` VARCHAR(32) NOT NULL DEFAULT '',
  `format` VARCHAR(64) NOT NULL DEFAULT 'F-{YYYY}-{0001}',
  `starting_number` INT NOT NULL DEFAULT 1,
  `padding_zeros` INT NOT NULL DEFAULT 4,
  `next_number` INT NOT NULL DEFAULT 1,
  `default_pdf_template` VARCHAR(16) NOT NULL DEFAULT 'basic',
  `default_payment_terms` INT NOT NULL DEFAULT 30,
  `created_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`restaurant_id`, `year`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
