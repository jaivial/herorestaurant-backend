CREATE TABLE IF NOT EXISTS stock_affluence_daily (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    service_date DATE NOT NULL,
    service_type ENUM('LUNCH','DINNER','OTHER') NOT NULL DEFAULT 'OTHER',
    covers INT NOT NULL,
    source ENUM('MANUAL','POS') NOT NULL DEFAULT 'MANUAL',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_stock_affluence_day (restaurant_id, service_date, service_type),
    KEY idx_stock_affluence_restaurant (restaurant_id, service_date),
    CONSTRAINT fk_stock_affluence_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
    CONSTRAINT chk_stock_affluence_covers CHECK (covers >= 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS stock_ai_entitlements (
    restaurant_id INT NOT NULL,
    global_ai_enabled TINYINT(1) NOT NULL DEFAULT 0,
    stock_ai_enabled TINYINT(1) NOT NULL DEFAULT 0,
    monthly_call_limit INT NOT NULL DEFAULT 100,
    calls_used INT NOT NULL DEFAULT 0,
    usage_month CHAR(7) NULL,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (restaurant_id),
    CONSTRAINT fk_stock_ai_entitlements_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS stock_ai_usage (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    feature VARCHAR(64) NOT NULL,
    model VARCHAR(120) NOT NULL,
    input_tokens INT NOT NULL DEFAULT 0,
    output_tokens INT NOT NULL DEFAULT 0,
    estimated_cost DECIMAL(12,6) NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    KEY idx_stock_ai_usage_restaurant (restaurant_id, feature, created_at),
    CONSTRAINT fk_stock_ai_usage_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS stock_ai_reports (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    report_type ENUM('FORECAST','COSTING','COMBINED') NOT NULL DEFAULT 'COMBINED',
    model VARCHAR(120) NOT NULL,
    input_snapshot JSON NOT NULL,
    report JSON NOT NULL,
    created_by INT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    KEY idx_stock_ai_reports_restaurant (restaurant_id, created_at),
    CONSTRAINT fk_stock_ai_reports_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS stock_vat_rates (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    name VARCHAR(100) NOT NULL,
    rate DECIMAL(5,2) NOT NULL,
    is_default TINYINT(1) NOT NULL DEFAULT 0,
    is_active TINYINT(1) NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_stock_vat_name (restaurant_id, name),
    KEY idx_stock_vat_restaurant (restaurant_id, is_active),
    CONSTRAINT fk_stock_vat_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
    CONSTRAINT chk_stock_vat_rate CHECK (rate >= 0 AND rate <= 100)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS stock_margin_bands (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    category_id BIGINT UNSIGNED NULL,
    name VARCHAR(80) NOT NULL,
    zone ENUM('RED','AMBER','GREEN','PURPLE') NOT NULL,
    min_food_cost_pct DECIMAL(5,2) NULL,
    max_food_cost_pct DECIMAL(5,2) NULL,
    sort_order INT NOT NULL DEFAULT 0,
    is_active TINYINT(1) NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_stock_margin_band (restaurant_id, category_id, zone),
    KEY idx_stock_margin_bands_restaurant (restaurant_id, category_id, sort_order),
    CONSTRAINT fk_stock_margin_bands_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
    CONSTRAINT fk_stock_margin_bands_category FOREIGN KEY (restaurant_id, category_id) REFERENCES stock_categories(restaurant_id, id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS stock_item_prices (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    stock_item_id BIGINT UNSIGNED NOT NULL,
    supplier_name VARCHAR(180) NULL,
    unit_cost_base DECIMAL(18,6) NOT NULL,
    source ENUM('MANUAL','OCR','PURCHASE') NOT NULL DEFAULT 'MANUAL',
    effective_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    KEY idx_stock_item_prices_item (restaurant_id, stock_item_id, effective_at),
    CONSTRAINT fk_stock_item_prices_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
    CONSTRAINT fk_stock_item_prices_item FOREIGN KEY (restaurant_id, stock_item_id) REFERENCES stock_items(restaurant_id, id),
    CONSTRAINT chk_stock_item_price CHECK (unit_cost_base >= 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

ALTER TABLE stock_recipes
    ADD COLUMN selling_price_gross DECIMAL(18,2) NULL,
    ADD COLUMN vat_rate_id BIGINT UNSIGNED NULL,
    ADD COLUMN overhead_pct DECIMAL(5,2) NOT NULL DEFAULT 0,
    ADD COLUMN is_protected TINYINT(1) NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS stock_document_scans (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    document_type ENUM('INVOICE','RECIPE') NOT NULL,
    source ENUM('UPLOAD','PHOTO','PASTE') NOT NULL,
    file_path VARCHAR(1000) NULL,
    file_hash CHAR(64) NULL,
    raw_text LONGTEXT NULL,
    status ENUM('PENDING','PROCESSING','NEEDS_REVIEW','CONFIRMED','REJECTED','FAILED') NOT NULL DEFAULT 'PENDING',
    supplier_name VARCHAR(180) NULL,
    document_number VARCHAR(100) NULL,
    document_date DATE NULL,
    raw_extraction JSON NULL,
    model VARCHAR(120) NULL,
    confidence DECIMAL(5,4) NULL,
    error_message VARCHAR(500) NULL,
    uploaded_by INT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_stock_document_scans_tenant_id (restaurant_id, id),
    UNIQUE KEY uq_stock_document_hash (restaurant_id, file_hash),
    KEY idx_stock_documents_restaurant (restaurant_id, status, created_at),
    CONSTRAINT fk_stock_documents_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS stock_document_lines (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    document_scan_id BIGINT UNSIGNED NOT NULL,
    line_no INT NOT NULL,
    raw_description VARCHAR(500) NOT NULL,
    raw_code VARCHAR(100) NULL,
    raw_qty DECIMAL(18,4) NULL,
    raw_unit VARCHAR(100) NULL,
    raw_unit_price DECIMAL(18,6) NULL,
    raw_total DECIMAL(18,2) NULL,
    matched_stock_item_id BIGINT UNSIGNED NULL,
    matched_unit_id BIGINT UNSIGNED NULL,
    match_confidence DECIMAL(5,4) NULL,
    status ENUM('OK','NEEDS_MATCH','IGNORED') NOT NULL DEFAULT 'NEEDS_MATCH',
    PRIMARY KEY (id),
    UNIQUE KEY uq_stock_document_line (restaurant_id, document_scan_id, line_no),
    CONSTRAINT fk_stock_document_lines_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
    CONSTRAINT fk_stock_document_lines_scan FOREIGN KEY (restaurant_id, document_scan_id) REFERENCES stock_document_scans(restaurant_id, id) ON DELETE CASCADE,
    CONSTRAINT fk_stock_document_lines_item FOREIGN KEY (restaurant_id, matched_stock_item_id) REFERENCES stock_items(restaurant_id, id),
    CONSTRAINT fk_stock_document_lines_unit FOREIGN KEY (restaurant_id, matched_unit_id) REFERENCES stock_item_units(restaurant_id, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS stock_supplier_aliases (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    supplier_name VARCHAR(180) NOT NULL,
    supplier_code VARCHAR(100) NOT NULL DEFAULT '',
    normalized_description VARCHAR(255) NOT NULL,
    stock_item_id BIGINT UNSIGNED NOT NULL,
    stock_unit_id BIGINT UNSIGNED NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_stock_supplier_alias (restaurant_id, supplier_name, supplier_code, normalized_description),
    CONSTRAINT fk_stock_supplier_alias_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
    CONSTRAINT fk_stock_supplier_alias_item FOREIGN KEY (restaurant_id, stock_item_id) REFERENCES stock_items(restaurant_id, id),
    CONSTRAINT fk_stock_supplier_alias_unit FOREIGN KEY (restaurant_id, stock_unit_id) REFERENCES stock_item_units(restaurant_id, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
