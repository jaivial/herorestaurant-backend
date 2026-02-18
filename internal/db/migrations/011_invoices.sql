-- Invoices table for billing customers
CREATE TABLE IF NOT EXISTS invoices (
  id INT AUTO_INCREMENT PRIMARY KEY,
  restaurant_id INT NOT NULL,

  -- Datos del cliente
  customer_name VARCHAR(255) NOT NULL,
  customer_surname VARCHAR(255) NULL,
  customer_email VARCHAR(255) NOT NULL,
  customer_dni_cif VARCHAR(32) NULL,
  customer_phone VARCHAR(32) NULL,
  customer_address_street VARCHAR(255) NULL,
  customer_address_number VARCHAR(32) NULL,
  customer_address_postal_code VARCHAR(16) NULL,
  customer_address_city VARCHAR(128) NULL,
  customer_address_province VARCHAR(128) NULL,
  customer_address_country VARCHAR(128) NULL,

  -- Datos de la factura
  amount DECIMAL(10,2) NOT NULL,
  payment_method ENUM('efectivo', 'tarjeta', 'transferencia', 'bizum', 'cheque') NULL,
  account_image_url VARCHAR(1024) NULL,
  invoice_date DATE NOT NULL,
  payment_date DATE NULL,

  -- Estado y metadata
  status ENUM('borrador', 'solicitada', 'pendiente', 'enviada') DEFAULT 'borrador',
  is_reservation TINYINT(1) DEFAULT 0,
  reservation_id INT NULL,
  reservation_date DATE NULL,
  reservation_customer_name VARCHAR(255) NULL,
  reservation_party_size INT NULL,

  -- URLs de archivos
  pdf_url VARCHAR(1024) NULL,

  -- Timestamps
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,

  FOREIGN KEY (restaurant_id) REFERENCES restaurants(id),
  FOREIGN KEY (reservation_id) REFERENCES bookings(id),
  INDEX idx_invoices_restaurant_date (restaurant_id, invoice_date),
  INDEX idx_invoices_status (restaurant_id, status),
  INDEX idx_invoices_customer_email (restaurant_id, customer_email(255)),
  INDEX idx_invoices_is_reservation (restaurant_id, is_reservation),
  INDEX idx_invoices_reservation_date (restaurant_id, reservation_date)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Add avatar column to restaurants if it doesn't exist
-- This is for the restaurant logo used in invoices
-- Using a simple check approach that works across MySQL versions
SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'restaurants'
    AND COLUMN_NAME = 'avatar'
);

SET @ddl := IF(@col_exists = 0,
  'ALTER TABLE restaurants ADD COLUMN avatar VARCHAR(1024) NULL AFTER name',
  'SELECT 1 -- column already exists'
);

PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
