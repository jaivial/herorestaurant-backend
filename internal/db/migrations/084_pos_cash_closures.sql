-- POS cash reports and auditable cash movements.
-- X = provisional report, Y = intermediate cut, Z = final shift close.

CREATE TABLE IF NOT EXISTS pos_cash_movements (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    shift_id BIGINT UNSIGNED NOT NULL,
    terminal_key VARCHAR(64) NOT NULL,
    movement_type ENUM('IN','OUT') NOT NULL,
    amount_cents BIGINT NOT NULL,
    reason VARCHAR(500) NOT NULL,
    idempotency_key VARCHAR(120) NOT NULL,
    created_by INT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_pos_cash_movements_tenant_id (restaurant_id, id),
    UNIQUE KEY uq_pos_cash_movement_idempotency (restaurant_id, idempotency_key),
    KEY idx_pos_cash_movements_shift (restaurant_id, shift_id, created_at),
    CONSTRAINT fk_pos_cash_movements_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
    CONSTRAINT fk_pos_cash_movements_shift FOREIGN KEY (restaurant_id, shift_id) REFERENCES pos_shifts(restaurant_id, id),
    CONSTRAINT chk_pos_cash_movement_amount CHECK (amount_cents > 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS pos_cash_closures (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    shift_id BIGINT UNSIGNED NOT NULL,
    terminal_key VARCHAR(64) NOT NULL,
    closure_type ENUM('X','Y','Z') NOT NULL,
    status ENUM('COMPLETED') NOT NULL DEFAULT 'COMPLETED',
    opened_at DATETIME NOT NULL,
    closed_at DATETIME NULL,
    generated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    opening_cash_cents BIGINT NOT NULL DEFAULT 0,
    sales_gross_cents BIGINT NOT NULL DEFAULT 0,
    refunds_cents BIGINT NOT NULL DEFAULT 0,
    discounts_cents BIGINT NOT NULL DEFAULT 0,
    surcharges_cents BIGINT NOT NULL DEFAULT 0,
    tips_cents BIGINT NOT NULL DEFAULT 0,
    cash_sales_cents BIGINT NOT NULL DEFAULT 0,
    cash_tips_cents BIGINT NOT NULL DEFAULT 0,
    card_sales_cents BIGINT NOT NULL DEFAULT 0,
    card_tips_cents BIGINT NOT NULL DEFAULT 0,
    bank_sales_cents BIGINT NOT NULL DEFAULT 0,
    bank_tips_cents BIGINT NOT NULL DEFAULT 0,
    other_sales_cents BIGINT NOT NULL DEFAULT 0,
    other_tips_cents BIGINT NOT NULL DEFAULT 0,
    cash_refunds_cents BIGINT NOT NULL DEFAULT 0,
    cash_in_cents BIGINT NOT NULL DEFAULT 0,
    cash_out_cents BIGINT NOT NULL DEFAULT 0,
    expected_cash_cents BIGINT NOT NULL DEFAULT 0,
    counted_cash_cents BIGINT NULL,
    difference_cents BIGINT NULL,
    ticket_count INT NOT NULL DEFAULT 0,
    voided_ticket_count INT NOT NULL DEFAULT 0,
    covers INT NOT NULL DEFAULT 0,
    open_visit_count INT NOT NULL DEFAULT 0,
    open_ticket_count INT NOT NULL DEFAULT 0,
    note VARCHAR(500) NULL,
    discrepancy_reason VARCHAR(500) NULL,
    idempotency_key VARCHAR(120) NOT NULL,
    created_by INT NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_pos_cash_closures_tenant_id (restaurant_id, id),
    UNIQUE KEY uq_pos_cash_closure_idempotency (restaurant_id, idempotency_key),
    KEY idx_pos_cash_closures_shift (restaurant_id, shift_id, generated_at),
    CONSTRAINT fk_pos_cash_closures_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
    CONSTRAINT fk_pos_cash_closures_shift FOREIGN KEY (restaurant_id, shift_id) REFERENCES pos_shifts(restaurant_id, id),
    CONSTRAINT chk_pos_cash_closure_amounts CHECK (
      opening_cash_cents >= 0 AND sales_gross_cents >= 0 AND refunds_cents >= 0 AND
      discounts_cents >= 0 AND surcharges_cents >= 0 AND tips_cents >= 0 AND
      cash_sales_cents >= 0 AND card_sales_cents >= 0 AND bank_sales_cents >= 0 AND
      other_sales_cents >= 0 AND cash_in_cents >= 0 AND cash_out_cents >= 0 AND
      expected_cash_cents >= 0
    )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
