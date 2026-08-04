ALTER TABLE stock_document_scans
    ADD COLUMN storage_provider VARCHAR(32) NULL AFTER file_path,
    ADD COLUMN storage_bucket VARCHAR(191) NULL AFTER storage_provider,
    ADD COLUMN content_type VARCHAR(120) NULL AFTER storage_bucket,
    ADD COLUMN size_bytes BIGINT UNSIGNED NULL AFTER content_type,
    ADD COLUMN original_filename VARCHAR(255) NULL AFTER size_bytes,
    ADD COLUMN retention_until DATE NULL AFTER original_filename,
    ADD COLUMN original_deleted_at TIMESTAMP NULL AFTER retention_until;

CREATE TABLE IF NOT EXISTS stock_document_access_audit (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    document_scan_id BIGINT UNSIGNED NOT NULL,
    action ENUM('DOWNLOAD','DELETE','RETENTION_DELETE') NOT NULL,
    actor_user_id INT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    KEY idx_stock_document_access (restaurant_id, document_scan_id, created_at),
    CONSTRAINT fk_stock_document_access_document FOREIGN KEY (restaurant_id, document_scan_id) REFERENCES stock_document_scans(restaurant_id, id) ON DELETE CASCADE,
    CONSTRAINT fk_stock_document_access_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS integration_job_runs (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NULL,
    job_key VARCHAR(64) NOT NULL,
    status ENUM('RUNNING','OK','WARNING','FAILED','SKIPPED') NOT NULL,
    issue_count INT NOT NULL DEFAULT 0,
    summary_json JSON NULL,
    error_message VARCHAR(500) NULL,
    started_at DATETIME NOT NULL,
    finished_at DATETIME NULL,
    PRIMARY KEY (id),
    KEY idx_integration_job_runs (job_key, started_at),
    KEY idx_integration_job_tenant (restaurant_id, job_key, started_at),
    CONSTRAINT fk_integration_job_runs_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS accounting_exports (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    export_type ENUM('SALES_VAT','PAYMENTS','REFUNDS','STOCK','LABOUR') NOT NULL,
    period_from DATE NOT NULL,
    period_to DATE NOT NULL,
    format VARCHAR(16) NOT NULL DEFAULT 'CSV',
    payload_hash CHAR(64) NOT NULL,
    row_count INT NOT NULL DEFAULT 0,
    status ENUM('GENERATED','SUBMITTED','ACCEPTED','FAILED') NOT NULL DEFAULT 'GENERATED',
    provider VARCHAR(64) NULL,
    provider_reference VARCHAR(191) NULL,
    generated_by INT NOT NULL,
    generated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_accounting_export_hash (restaurant_id, export_type, period_from, period_to, payload_hash),
    KEY idx_accounting_exports_period (restaurant_id, export_type, period_from, period_to),
    CONSTRAINT fk_accounting_exports_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

ALTER TABLE pos_visits
    ADD KEY idx_pos_visit_booking (restaurant_id, booking_id);

ALTER TABLE member_time_entries
    ADD UNIQUE KEY uq_member_time_entries_tenant_id (restaurant_id, id);

CREATE TABLE IF NOT EXISTS member_time_allocations (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    time_entry_id BIGINT NOT NULL,
    production_order_id BIGINT UNSIGNED NOT NULL,
    minutes INT NOT NULL,
    hourly_cost_snapshot DECIMAL(12,4) NULL,
    actual_cost DECIMAL(12,4) NULL,
    cost_complete TINYINT(1) NOT NULL DEFAULT 0,
    idempotency_key VARCHAR(120) NOT NULL,
    created_by INT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_member_time_allocation_idempotency (restaurant_id, idempotency_key),
    KEY idx_member_time_allocations_entry (restaurant_id, time_entry_id),
    KEY idx_member_time_allocations_production (restaurant_id, production_order_id),
    CONSTRAINT fk_member_time_allocations_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
    CONSTRAINT fk_member_time_allocations_entry FOREIGN KEY (restaurant_id, time_entry_id) REFERENCES member_time_entries(restaurant_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_member_time_allocations_production FOREIGN KEY (restaurant_id, production_order_id) REFERENCES stock_production_orders(restaurant_id, id) ON DELETE CASCADE,
    CONSTRAINT chk_member_time_allocation_minutes CHECK (minutes > 0),
    CONSTRAINT chk_member_time_allocation_cost CHECK ((cost_complete=0 AND actual_cost IS NULL) OR (cost_complete=1 AND hourly_cost_snapshot IS NOT NULL AND actual_cost IS NOT NULL))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS member_time_allocation_audit (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    allocation_id BIGINT UNSIGNED NOT NULL,
    action ENUM('CREATE','DELETE') NOT NULL,
    snapshot JSON NOT NULL,
    actor_user_id INT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    KEY idx_member_time_allocation_audit (restaurant_id, allocation_id, created_at),
    CONSTRAINT fk_member_time_allocation_audit_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

ALTER TABLE stock_production_orders
    ADD COLUMN actual_labour_minutes INT NOT NULL DEFAULT 0 AFTER labour_cost_complete,
    ADD COLUMN actual_labour_cost DECIMAL(12,4) NULL AFTER actual_labour_minutes,
    ADD COLUMN actual_labour_cost_complete TINYINT(1) NOT NULL DEFAULT 0 AFTER actual_labour_cost;
