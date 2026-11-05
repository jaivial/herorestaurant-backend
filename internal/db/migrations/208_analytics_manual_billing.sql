-- Coordination id: manual_billing_stats_v1
--
-- Simple billing entries typed by the restaurant owner for the Ventas tab.
-- Amounts are stored in cents and already split at write time so every read
-- (series, weekday, VAT breakdown) is a plain SUM. `gross_cents` keeps the
-- sign: a negative entry subtracts from every aggregate.
CREATE TABLE IF NOT EXISTS analytics_manual_billing_entries (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    entry_date DATE NOT NULL,
    gross_cents BIGINT NOT NULL,
    net_cents BIGINT NOT NULL,
    vat_cents BIGINT NOT NULL,
    vat_rate_bp INT NOT NULL DEFAULT 0,
    includes_vat TINYINT(1) NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    KEY idx_analytics_manual_billing_date (restaurant_id, entry_date),
    CONSTRAINT fk_analytics_manual_billing_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
