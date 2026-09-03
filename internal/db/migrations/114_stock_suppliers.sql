-- Supplier registry for the stock module. Until now suppliers only existed as
-- free-text supplier_name on stock_item_prices / stock_document_scans /
-- stock_supplier_aliases. This table is the registry keyed by that same name,
-- so existing history and OCR aliases keep matching without any backfill of
-- old tables. Additive only.

CREATE TABLE IF NOT EXISTS stock_suppliers (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    name VARCHAR(180) NOT NULL,
    notes VARCHAR(500) NOT NULL DEFAULT '',
    is_active TINYINT(1) NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_stock_suppliers (restaurant_id, name),
    CONSTRAINT fk_stock_suppliers_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Adopt supplier names already present in purchase prices and scanned documents.
INSERT IGNORE INTO stock_suppliers (restaurant_id, name)
SELECT DISTINCT restaurant_id, supplier_name FROM stock_item_prices WHERE supplier_name IS NOT NULL AND supplier_name <> '';

INSERT IGNORE INTO stock_suppliers (restaurant_id, name)
SELECT DISTINCT restaurant_id, supplier_name FROM stock_document_scans WHERE supplier_name IS NOT NULL AND supplier_name <> '';
