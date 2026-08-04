-- Analytics V1 foundation. New tables only: source tables remain untouched.
CREATE TABLE IF NOT EXISTS analytics_sync_runs (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    from_date DATE NOT NULL,
    to_date DATE NOT NULL,
    status ENUM('RUNNING','COMPLETED','FAILED') NOT NULL DEFAULT 'RUNNING',
    rows_written INT NOT NULL DEFAULT 0,
    quality_json JSON NULL,
    started_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    completed_at DATETIME NULL,
    error_message VARCHAR(500) NULL,
    PRIMARY KEY (id),
    KEY idx_analytics_sync_runs_tenant (restaurant_id, started_at),
    CONSTRAINT fk_analytics_sync_runs_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS analytics_customers (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    identity_key VARCHAR(255) NOT NULL,
    display_name VARCHAR(255) NULL,
    email VARCHAR(255) NULL,
    phone VARCHAR(64) NULL,
    tax_id VARCHAR(64) NULL,
    first_seen DATE NULL,
    last_seen DATE NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_analytics_customers_identity (restaurant_id, identity_key),
    KEY idx_analytics_customers_tenant (restaurant_id, last_seen),
    CONSTRAINT fk_analytics_customers_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS analytics_customer_sources (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    customer_id BIGINT UNSIGNED NOT NULL,
    source_type VARCHAR(32) NOT NULL,
    source_ref VARCHAR(64) NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_analytics_customer_source (restaurant_id, source_type, source_ref),
    KEY idx_analytics_customer_sources_customer (restaurant_id, customer_id),
    CONSTRAINT fk_analytics_customer_sources_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS analytics_sales_documents (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    source_type ENUM('INVOICE','POS') NOT NULL,
    source_id BIGINT UNSIGNED NOT NULL,
    customer_id BIGINT UNSIGNED NULL,
    occurred_on DATE NOT NULL,
    status VARCHAR(32) NOT NULL,
    currency CHAR(3) NOT NULL DEFAULT 'EUR',
    gross_cents BIGINT NOT NULL DEFAULT 0,
    refunded_cents BIGINT NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_analytics_sales_document_source (restaurant_id, source_type, source_id),
    KEY idx_analytics_sales_documents_date (restaurant_id, occurred_on, source_type),
    KEY idx_analytics_sales_documents_customer (restaurant_id, customer_id, occurred_on),
    CONSTRAINT fk_analytics_sales_documents_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS analytics_sales_lines (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    sales_document_id BIGINT UNSIGNED NOT NULL,
    source_line_id BIGINT UNSIGNED NOT NULL,
    description VARCHAR(512) NOT NULL DEFAULT '',
    quantity DECIMAL(18,4) NOT NULL DEFAULT 0,
    revenue_cents BIGINT NOT NULL DEFAULT 0,
    refunded_cents BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (id),
    UNIQUE KEY uq_analytics_sales_line_source (restaurant_id, sales_document_id, source_line_id),
    KEY idx_analytics_sales_lines_document (restaurant_id, sales_document_id),
    CONSTRAINT fk_analytics_sales_lines_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
    CONSTRAINT fk_analytics_sales_lines_document FOREIGN KEY (sales_document_id) REFERENCES analytics_sales_documents(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS analytics_stock_facts (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    source_type VARCHAR(32) NOT NULL,
    source_id BIGINT UNSIGNED NOT NULL,
    occurred_on DATE NOT NULL,
    stock_item_id BIGINT UNSIGNED NULL,
    fact_type VARCHAR(32) NOT NULL,
    waste_reason VARCHAR(64) NULL,
    quantity_base DECIMAL(18,4) NOT NULL DEFAULT 0,
    cost_cents BIGINT NULL,
    cost_known TINYINT(1) NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_analytics_stock_fact_source (restaurant_id, source_type, source_id),
    KEY idx_analytics_stock_facts_date (restaurant_id, occurred_on, fact_type),
    CONSTRAINT fk_analytics_stock_facts_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS analytics_daily_rollups (
    restaurant_id INT NOT NULL,
    rollup_date DATE NOT NULL,
    invoiced_revenue_cents BIGINT NOT NULL DEFAULT 0,
    pos_revenue_cents BIGINT NOT NULL DEFAULT 0,
    pos_refunded_cents BIGINT NOT NULL DEFAULT 0,
    cogs_known_cost_cents BIGINT NOT NULL DEFAULT 0,
    cogs_quantity DECIMAL(18,4) NOT NULL DEFAULT 0,
    cogs_known_quantity DECIMAL(18,4) NOT NULL DEFAULT 0,
    stock_purchase_known_cost_cents BIGINT NOT NULL DEFAULT 0,
    stock_purchase_quantity DECIMAL(18,4) NOT NULL DEFAULT 0,
    stock_purchase_known_quantity DECIMAL(18,4) NOT NULL DEFAULT 0,
    stock_purchase_unknown_quantity DECIMAL(18,4) NOT NULL DEFAULT 0,
    stock_return_known_cost_cents BIGINT NOT NULL DEFAULT 0,
    stock_return_quantity DECIMAL(18,4) NOT NULL DEFAULT 0,
    stock_return_known_quantity DECIMAL(18,4) NOT NULL DEFAULT 0,
    waste_known_cost_cents BIGINT NOT NULL DEFAULT 0,
    waste_quantity DECIMAL(18,4) NOT NULL DEFAULT 0,
    waste_unknown_quantity DECIMAL(18,4) NOT NULL DEFAULT 0,
    sales_documents INT NOT NULL DEFAULT 0,
    identified_sales_documents INT NOT NULL DEFAULT 0,
    non_eur_documents INT NOT NULL DEFAULT 0,
    waste_breakdown_json JSON NULL,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (restaurant_id, rollup_date),
    CONSTRAINT fk_analytics_daily_rollups_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
