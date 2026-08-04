ALTER TABLE restaurant_members
    ADD UNIQUE KEY uq_restaurant_members_tenant_id (restaurant_id, id);

CREATE TABLE IF NOT EXISTS member_compensations (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    restaurant_member_id INT NOT NULL,
    pay_type ENUM('MONTHLY','HOURLY') NOT NULL,
    gross_amount DECIMAL(12,2) NOT NULL,
    monthly_hours DECIMAL(7,2) NULL,
    employer_cost_pct DECIMAL(5,2) NOT NULL DEFAULT 0,
    effective_from DATE NOT NULL,
    effective_to DATE NULL,
    notes VARCHAR(500) NULL,
    created_by INT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_member_compensations_tenant_id (restaurant_id, id),
    KEY idx_member_compensations_effective (restaurant_id, restaurant_member_id, effective_from, effective_to, deleted_at),
    CONSTRAINT fk_member_compensations_member FOREIGN KEY (restaurant_id, restaurant_member_id) REFERENCES restaurant_members(restaurant_id, id) ON DELETE CASCADE,
    CONSTRAINT chk_member_compensations_amount CHECK (gross_amount >= 0),
    CONSTRAINT chk_member_compensations_hours CHECK ((pay_type = 'HOURLY' AND monthly_hours IS NULL) OR (pay_type = 'MONTHLY' AND monthly_hours > 0)),
    CONSTRAINT chk_member_compensations_burden CHECK (employer_cost_pct >= 0 AND employer_cost_pct <= 300),
    CONSTRAINT chk_member_compensations_dates CHECK (effective_to IS NULL OR effective_to >= effective_from)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS member_compensation_audit (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    compensation_id BIGINT UNSIGNED NOT NULL,
    action ENUM('CREATE','UPDATE','DELETE') NOT NULL,
    snapshot JSON NOT NULL,
    actor_user_id INT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    KEY idx_member_compensation_audit (restaurant_id, compensation_id, created_at),
    CONSTRAINT fk_member_compensation_audit_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS stock_recipe_labour (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    recipe_id BIGINT UNSIGNED NOT NULL,
    restaurant_member_id INT NOT NULL,
    minutes_per_batch DECIMAL(8,2) NOT NULL,
    notes VARCHAR(500) NULL,
    sort_order INT NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_stock_recipe_labour_member (restaurant_id, recipe_id, restaurant_member_id),
    KEY idx_stock_recipe_labour_recipe (restaurant_id, recipe_id, sort_order),
    CONSTRAINT fk_stock_recipe_labour_recipe FOREIGN KEY (restaurant_id, recipe_id) REFERENCES stock_recipes(restaurant_id, id) ON DELETE CASCADE,
    CONSTRAINT fk_stock_recipe_labour_member FOREIGN KEY (restaurant_id, restaurant_member_id) REFERENCES restaurant_members(restaurant_id, id),
    CONSTRAINT chk_stock_recipe_labour_minutes CHECK (minutes_per_batch > 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

ALTER TABLE stock_production_orders
    ADD COLUMN standard_labour_cost DECIMAL(12,4) NOT NULL DEFAULT 0 AFTER qty_produced_base,
    ADD COLUMN labour_cost_complete TINYINT(1) NOT NULL DEFAULT 1 AFTER standard_labour_cost;
