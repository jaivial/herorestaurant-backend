SET @idx_exists := (SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='restaurant_tables' AND INDEX_NAME='uq_restaurant_tables_tenant_id');
SET @ddl := IF(@idx_exists=0, 'ALTER TABLE restaurant_tables ADD UNIQUE KEY uq_restaurant_tables_tenant_id (restaurant_id, id)', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;
SET @idx_exists := (SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='stock_vat_rates' AND INDEX_NAME='uq_stock_vat_rates_tenant_id');
SET @ddl := IF(@idx_exists=0, 'ALTER TABLE stock_vat_rates ADD UNIQUE KEY uq_stock_vat_rates_tenant_id (restaurant_id, id)', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

CREATE TABLE IF NOT EXISTS pos_settings (
    restaurant_id INT NOT NULL,
    is_enabled TINYINT(1) NOT NULL DEFAULT 0,
    stock_mode ENUM('OFF','SHADOW','LIVE') NOT NULL DEFAULT 'OFF',
    covers_mode ENUM('MANUAL','SHADOW','LIVE') NOT NULL DEFAULT 'MANUAL',
    timezone VARCHAR(64) NOT NULL DEFAULT 'Europe/Madrid',
    business_day_cutoff TIME NOT NULL DEFAULT '05:00:00',
    auto_close_visit TINYINT(1) NOT NULL DEFAULT 1,
    require_open_shift TINYINT(1) NOT NULL DEFAULT 0,
    receipt_prefix VARCHAR(16) NOT NULL DEFAULT 'TPV',
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (restaurant_id),
    CONSTRAINT fk_pos_settings_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS pos_service_periods (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    name VARCHAR(80) NOT NULL,
    service_type ENUM('LUNCH','DINNER','OTHER') NOT NULL DEFAULT 'OTHER',
    start_time TIME NOT NULL,
    end_time TIME NOT NULL,
    sort_order INT NOT NULL DEFAULT 0,
    is_active TINYINT(1) NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_pos_service_period_tenant_id (restaurant_id, id),
    UNIQUE KEY uq_pos_service_period_name (restaurant_id, name),
    KEY idx_pos_service_periods_restaurant (restaurant_id, is_active, sort_order),
    CONSTRAINT fk_pos_service_periods_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS pos_product_categories (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    name VARCHAR(120) NOT NULL,
    sort_order INT NOT NULL DEFAULT 0,
    is_active TINYINT(1) NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_pos_product_categories_tenant_id (restaurant_id, id),
    UNIQUE KEY uq_pos_product_category_name (restaurant_id, name),
    KEY idx_pos_product_categories_restaurant (restaurant_id, is_active, sort_order),
    CONSTRAINT fk_pos_product_categories_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS pos_products (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    category_id BIGINT UNSIGNED NULL,
    name VARCHAR(180) NOT NULL,
    description VARCHAR(1000) NULL,
    source_type ENUM('COMIDA_ITEM','VINO','POSTRE','MENU_DISH','MANUAL') NOT NULL DEFAULT 'MANUAL',
    source_id BIGINT NULL,
    sku VARCHAR(64) NULL,
    price_gross_cents BIGINT NOT NULL DEFAULT 0,
    vat_rate_id BIGINT UNSIGNED NULL,
    is_active TINYINT(1) NOT NULL DEFAULT 1,
    version INT NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_pos_products_tenant_id (restaurant_id, id),
    UNIQUE KEY uq_pos_product_source (restaurant_id, source_type, source_id),
    UNIQUE KEY uq_pos_product_sku (restaurant_id, sku),
    KEY idx_pos_products_restaurant (restaurant_id, is_active, name),
    KEY idx_pos_products_category (restaurant_id, category_id),
    CONSTRAINT fk_pos_products_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
    CONSTRAINT fk_pos_products_category FOREIGN KEY (restaurant_id, category_id) REFERENCES pos_product_categories(restaurant_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_pos_products_vat FOREIGN KEY (restaurant_id, vat_rate_id) REFERENCES stock_vat_rates(restaurant_id, id) ON DELETE RESTRICT,
    CONSTRAINT chk_pos_product_price CHECK (price_gross_cents >= 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS pos_product_stock_rules (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    pos_product_id BIGINT UNSIGNED NOT NULL,
    stock_item_id BIGINT UNSIGNED NOT NULL,
    stock_recipe_id BIGINT UNSIGNED NULL,
    warehouse_id BIGINT UNSIGNED NOT NULL,
    qty_base_per_sale DECIMAL(18,4) NOT NULL,
    sort_order INT NOT NULL DEFAULT 0,
    is_active TINYINT(1) NOT NULL DEFAULT 1,
    version INT NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_pos_stock_rules_tenant_id (restaurant_id, id),
    UNIQUE KEY uq_pos_stock_rule_version (restaurant_id, pos_product_id, stock_item_id, warehouse_id, version),
    KEY idx_pos_stock_rules_product (restaurant_id, pos_product_id, is_active, sort_order),
    CONSTRAINT fk_pos_stock_rules_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
    CONSTRAINT fk_pos_stock_rules_product FOREIGN KEY (restaurant_id, pos_product_id) REFERENCES pos_products(restaurant_id, id) ON DELETE CASCADE,
    CONSTRAINT fk_pos_stock_rules_item FOREIGN KEY (restaurant_id, stock_item_id) REFERENCES stock_items(restaurant_id, id),
    CONSTRAINT fk_pos_stock_rules_recipe FOREIGN KEY (restaurant_id, stock_recipe_id) REFERENCES stock_recipes(restaurant_id, id),
    CONSTRAINT fk_pos_stock_rules_warehouse FOREIGN KEY (restaurant_id, warehouse_id) REFERENCES stock_warehouses(restaurant_id, id),
    CONSTRAINT chk_pos_stock_rule_qty CHECK (qty_base_per_sale > 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS pos_role_permissions (
    restaurant_id INT NOT NULL,
    role_slug VARCHAR(32) NOT NULL,
    permission_key VARCHAR(64) NOT NULL,
    is_allowed TINYINT(1) NOT NULL DEFAULT 1,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (restaurant_id, role_slug, permission_key),
    KEY idx_pos_role_permissions_restaurant (restaurant_id, role_slug),
    CONSTRAINT fk_pos_role_permissions_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
