CREATE TABLE IF NOT EXISTS stock_settings (
    restaurant_id INT NOT NULL,
    warehouse_display_mode ENUM('AGGREGATED','BY_WAREHOUSE') NOT NULL DEFAULT 'AGGREGATED',
    count_cadence ENUM('DAILY','WEEKLY','BIWEEKLY','MONTHLY','NEVER') NOT NULL DEFAULT 'WEEKLY',
    allow_negative_stock TINYINT(1) NOT NULL DEFAULT 1,
    labour_cost_enabled TINYINT(1) NOT NULL DEFAULT 0,
    business_profile TEXT NULL,
    seasonality_profile JSON NULL,
    onboarding_completed TINYINT(1) NOT NULL DEFAULT 0,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (restaurant_id),
    CONSTRAINT fk_stock_settings_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS stock_role_permissions (
    restaurant_id INT NOT NULL,
    role_slug VARCHAR(32) NOT NULL,
    permission_key VARCHAR(64) NOT NULL,
    is_allowed TINYINT(1) NOT NULL DEFAULT 1,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (restaurant_id, role_slug, permission_key),
    KEY idx_stock_role_permissions_restaurant (restaurant_id, role_slug),
    CONSTRAINT fk_stock_role_permissions_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS stock_warehouses (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    name VARCHAR(120) NOT NULL,
    code VARCHAR(32) NULL,
    type ENUM('KITCHEN','BAR','STORAGE','COLD','FREEZER','CELLAR','OTHER') NOT NULL DEFAULT 'STORAGE',
    is_default TINYINT(1) NOT NULL DEFAULT 0,
    is_active TINYINT(1) NOT NULL DEFAULT 1,
    sort_order INT NOT NULL DEFAULT 0,
    notes VARCHAR(500) NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_stock_warehouses_tenant_id (restaurant_id, id),
    UNIQUE KEY uq_stock_warehouse_code (restaurant_id, code),
    KEY idx_stock_warehouses_restaurant (restaurant_id, is_active, sort_order),
    CONSTRAINT fk_stock_warehouses_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS stock_categories (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    name VARCHAR(120) NOT NULL,
    sort_order INT NOT NULL DEFAULT 0,
    is_active TINYINT(1) NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_stock_categories_tenant_id (restaurant_id, id),
    UNIQUE KEY uq_stock_category_name (restaurant_id, name),
    KEY idx_stock_categories_restaurant (restaurant_id, is_active, sort_order),
    CONSTRAINT fk_stock_categories_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS stock_items (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    category_id BIGINT UNSIGNED NULL,
    sku VARCHAR(64) NULL,
    name VARCHAR(180) NOT NULL,
    description VARCHAR(1000) NULL,
    kind ENUM('RAW','SEMI_FINISHED','FINISHED','CONSUMABLE') NOT NULL DEFAULT 'RAW',
    base_dimension ENUM('MASS','VOLUME','COUNT') NOT NULL,
    base_unit ENUM('g','ml','ud') NOT NULL,
    is_tracked TINYINT(1) NOT NULL DEFAULT 1,
    deduction_source ENUM('PRODUCTION','SALE','BOTH_MANUAL') NOT NULL DEFAULT 'BOTH_MANUAL',
    shelf_life_days INT NULL,
    image_url VARCHAR(1000) NULL,
    is_active TINYINT(1) NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_stock_items_tenant_id (restaurant_id, id),
    UNIQUE KEY uq_stock_item_sku (restaurant_id, sku),
    KEY idx_stock_items_restaurant (restaurant_id, is_active, name),
    KEY idx_stock_items_category (restaurant_id, category_id),
    CONSTRAINT fk_stock_items_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
    CONSTRAINT fk_stock_items_category FOREIGN KEY (restaurant_id, category_id) REFERENCES stock_categories(restaurant_id, id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS stock_item_units (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    stock_item_id BIGINT UNSIGNED NOT NULL,
    code VARCHAR(48) NOT NULL,
    label VARCHAR(100) NOT NULL,
    factor_to_base DECIMAL(18,6) NOT NULL,
    is_default_purchase TINYINT(1) NOT NULL DEFAULT 0,
    is_default_display TINYINT(1) NOT NULL DEFAULT 0,
    can_purchase TINYINT(1) NOT NULL DEFAULT 0,
    can_recipe TINYINT(1) NOT NULL DEFAULT 0,
    can_count TINYINT(1) NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_stock_item_units_tenant_id (restaurant_id, id),
    UNIQUE KEY uq_stock_item_unit_code (restaurant_id, stock_item_id, code),
    KEY idx_stock_item_units_item (restaurant_id, stock_item_id),
    CONSTRAINT fk_stock_item_units_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
    CONSTRAINT fk_stock_item_units_item FOREIGN KEY (restaurant_id, stock_item_id) REFERENCES stock_items(restaurant_id, id) ON DELETE CASCADE,
    CONSTRAINT chk_stock_item_unit_factor CHECK (factor_to_base > 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS stock_levels (
    restaurant_id INT NOT NULL,
    stock_item_id BIGINT UNSIGNED NOT NULL,
    warehouse_id BIGINT UNSIGNED NOT NULL,
    qty_base DECIMAL(18,4) NOT NULL DEFAULT 0,
    avg_unit_cost DECIMAL(18,6) NOT NULL DEFAULT 0,
    par_level_base DECIMAL(18,4) NOT NULL DEFAULT 0,
    reorder_point_base DECIMAL(18,4) NOT NULL DEFAULT 0,
    version INT NOT NULL DEFAULT 1,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (restaurant_id, stock_item_id, warehouse_id),
    KEY idx_stock_levels_warehouse (restaurant_id, warehouse_id),
    CONSTRAINT fk_stock_levels_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
    CONSTRAINT fk_stock_levels_item FOREIGN KEY (restaurant_id, stock_item_id) REFERENCES stock_items(restaurant_id, id) ON DELETE CASCADE,
    CONSTRAINT fk_stock_levels_warehouse FOREIGN KEY (restaurant_id, warehouse_id) REFERENCES stock_warehouses(restaurant_id, id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS stock_movements (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    stock_item_id BIGINT UNSIGNED NOT NULL,
    warehouse_id BIGINT UNSIGNED NOT NULL,
    qty_base DECIMAL(18,4) NOT NULL,
    type ENUM('PURCHASE','ADJUSTMENT','PRODUCTION_IN','PRODUCTION_OUT','SALE','WASTE','TRANSFER_IN','TRANSFER_OUT','INVENTORY_COUNT','RETURN') NOT NULL,
    waste_reason ENUM('SPOILAGE','BREAKAGE','OVERPRODUCTION','STAFF_MEAL','CUSTOMER_RETURN','PREP_LOSS','THEFT','OTHER') NULL,
    entered_qty DECIMAL(18,4) NOT NULL,
    entered_unit_id BIGINT UNSIGNED NOT NULL,
    unit_cost DECIMAL(18,6) NULL,
    total_cost DECIMAL(18,2) NULL,
    ref_type VARCHAR(40) NULL,
    ref_id BIGINT UNSIGNED NULL,
    transfer_id CHAR(36) NULL,
    idempotency_key VARCHAR(120) NOT NULL,
    note VARCHAR(500) NULL,
    actor_user_id INT NOT NULL,
    occurred_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_stock_movement_idempotency (restaurant_id, idempotency_key),
    KEY idx_stock_movements_item (restaurant_id, stock_item_id, warehouse_id, occurred_at),
    KEY idx_stock_movements_type (restaurant_id, type, occurred_at),
    CONSTRAINT fk_stock_movements_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
    CONSTRAINT fk_stock_movements_item FOREIGN KEY (restaurant_id, stock_item_id) REFERENCES stock_items(restaurant_id, id),
    CONSTRAINT fk_stock_movements_warehouse FOREIGN KEY (restaurant_id, warehouse_id) REFERENCES stock_warehouses(restaurant_id, id),
    CONSTRAINT fk_stock_movements_unit FOREIGN KEY (restaurant_id, entered_unit_id) REFERENCES stock_item_units(restaurant_id, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS stock_count_sheets (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    warehouse_id BIGINT UNSIGNED NOT NULL,
    status ENUM('OPEN','CLOSED','CANCELLED') NOT NULL DEFAULT 'OPEN',
    opened_by INT NOT NULL,
    closed_by INT NULL,
    opened_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    closed_at TIMESTAMP NULL,
    notes VARCHAR(500) NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_stock_count_sheets_tenant_id (restaurant_id, id),
    KEY idx_stock_count_sheets_restaurant (restaurant_id, status, opened_at),
    CONSTRAINT fk_stock_count_sheets_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
    CONSTRAINT fk_stock_count_sheets_warehouse FOREIGN KEY (restaurant_id, warehouse_id) REFERENCES stock_warehouses(restaurant_id, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS stock_count_lines (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    count_sheet_id BIGINT UNSIGNED NOT NULL,
    stock_item_id BIGINT UNSIGNED NOT NULL,
    observed_qty_base DECIMAL(18,4) NULL,
    expected_qty_base DECIMAL(18,4) NOT NULL DEFAULT 0,
    entered_qty DECIMAL(18,4) NULL,
    entered_unit_id BIGINT UNSIGNED NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_stock_count_line_item (restaurant_id, count_sheet_id, stock_item_id),
    CONSTRAINT fk_stock_count_lines_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
    CONSTRAINT fk_stock_count_lines_sheet FOREIGN KEY (restaurant_id, count_sheet_id) REFERENCES stock_count_sheets(restaurant_id, id) ON DELETE CASCADE,
    CONSTRAINT fk_stock_count_lines_item FOREIGN KEY (restaurant_id, stock_item_id) REFERENCES stock_items(restaurant_id, id),
    CONSTRAINT fk_stock_count_lines_unit FOREIGN KEY (restaurant_id, entered_unit_id) REFERENCES stock_item_units(restaurant_id, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
