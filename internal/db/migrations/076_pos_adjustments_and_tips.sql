-- 076: Signed ticket adjustments (Descuento 11 / Recargo 12) and tips (Propina 21).
--
-- pos_tickets.discount_cents stays the authoritative deduction used by
-- recalculatePOSTicket; this table records WHY the ticket total moved and lets
-- discounts and surcharges coexist as an append-only, auditable history.
--
-- Tips are money collected on top of the sale: they never enter subtotal,
-- discount, tax or total_gross_cents, so net sales and VAT stay untouched.
--
-- FK types verified against the live schema before writing:
--   restaurants.id INT, pos_tickets.id BIGINT UNSIGNED, bo_users.id INT.

SET @dbname = DATABASE();

CREATE TABLE IF NOT EXISTS pos_ticket_adjustments (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    ticket_id BIGINT UNSIGNED NOT NULL,
    type ENUM('DISCOUNT','SURCHARGE') NOT NULL,
    mode ENUM('AMOUNT','PERCENT') NOT NULL DEFAULT 'AMOUNT',
    percent_value DECIMAL(5,2) NULL,
    -- Signed against the ticket total: DISCOUNT stores a negative value,
    -- SURCHARGE a positive one, so SUM() yields the net adjustment directly.
    amount_cents BIGINT NOT NULL,
    reason VARCHAR(500) NOT NULL,
    status ENUM('ACTIVE','REVERSED') NOT NULL DEFAULT 'ACTIVE',
    idempotency_key VARCHAR(120) NOT NULL,
    created_by INT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    reversed_at DATETIME NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_pos_ticket_adjustments_tenant_id (restaurant_id, id),
    UNIQUE KEY uq_pos_ticket_adjustment_idempotency (restaurant_id, idempotency_key),
    KEY idx_pos_ticket_adjustments_ticket (restaurant_id, ticket_id, status, id),
    CONSTRAINT fk_pos_ticket_adjustments_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
    CONSTRAINT fk_pos_ticket_adjustments_ticket FOREIGN KEY (restaurant_id, ticket_id) REFERENCES pos_tickets(restaurant_id, id),
    CONSTRAINT chk_pos_ticket_adjustment_sign CHECK (
        (type = 'DISCOUNT' AND amount_cents <= 0) OR (type = 'SURCHARGE' AND amount_cents >= 0)
    ),
    CONSTRAINT chk_pos_ticket_adjustment_percent CHECK (
        (mode = 'PERCENT' AND percent_value IS NOT NULL AND percent_value > 0 AND percent_value <= 100)
        OR (mode = 'AMOUNT' AND percent_value IS NULL)
    )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ---------------------------------------------------------------------------
-- Recargo: the net surcharge currently applied to the ticket. Kept alongside
-- discount_cents so recalculatePOSTicket stays a pure function of the ticket row.
-- ---------------------------------------------------------------------------
SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='pos_tickets' AND COLUMN_NAME='surcharge_cents') > 0,
  'SELECT 1',
  'ALTER TABLE pos_tickets ADD COLUMN surcharge_cents BIGINT NOT NULL DEFAULT 0'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='pos_tickets' AND CONSTRAINT_NAME='chk_pos_ticket_surcharge') > 0,
  'SELECT 1',
  'ALTER TABLE pos_tickets ADD CONSTRAINT chk_pos_ticket_surcharge CHECK (surcharge_cents >= 0)'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- ---------------------------------------------------------------------------
-- Propina: tip cents recorded on the tender, excluded from net sales and VAT.
-- ---------------------------------------------------------------------------
SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='pos_payments' AND COLUMN_NAME='tip_cents') > 0,
  'SELECT 1',
  'ALTER TABLE pos_payments ADD COLUMN tip_cents BIGINT NOT NULL DEFAULT 0'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='pos_payments' AND CONSTRAINT_NAME='chk_pos_payment_tip') > 0,
  'SELECT 1',
  'ALTER TABLE pos_payments ADD CONSTRAINT chk_pos_payment_tip CHECK (tip_cents >= 0)'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='pos_tickets' AND COLUMN_NAME='tip_cents') > 0,
  'SELECT 1',
  'ALTER TABLE pos_tickets ADD COLUMN tip_cents BIGINT NOT NULL DEFAULT 0'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='pos_tickets' AND CONSTRAINT_NAME='chk_pos_ticket_tip') > 0,
  'SELECT 1',
  'ALTER TABLE pos_tickets ADD CONSTRAINT chk_pos_ticket_tip CHECK (tip_cents >= 0)'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;
