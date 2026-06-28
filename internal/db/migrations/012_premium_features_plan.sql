-- 012_premium_features_plan.sql

-- EPIC 1 & 2: Website Builder and Domains
CREATE TABLE IF NOT EXISTS restaurant_websites (
  id INT AUTO_INCREMENT PRIMARY KEY,
  restaurant_id INT NOT NULL,
  template_id VARCHAR(64) NULL,
  custom_html LONGTEXT NULL,
  domain VARCHAR(255) NULL,
  is_published TINYINT(1) DEFAULT 0,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Make sure recurring_invoices exists (from BACKEND_RECURRING_BILLING.md)
CREATE TABLE IF NOT EXISTS recurring_invoices (
  id INT AUTO_INCREMENT PRIMARY KEY,
  restaurant_id INT NOT NULL,
  customer_name VARCHAR(255) NOT NULL,
  customer_email VARCHAR(255) NOT NULL,
  amount DECIMAL(10,2) NOT NULL,
  currency VARCHAR(3) DEFAULT 'EUR',
  iva_rate DECIMAL(5,2) DEFAULT 21.00,
  payment_method ENUM('efectivo', 'tarjeta', 'transferencia', 'bizum', 'cheque') NULL,
  frequency ENUM('weekly', 'monthly', 'quarterly', 'yearly') NOT NULL DEFAULT 'monthly',
  start_date DATE NOT NULL,
  end_date DATE NULL,
  auto_send TINYINT(1) DEFAULT 0,
  is_active TINYINT(1) DEFAULT 1,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS recurring_invoice_logs (
  id INT AUTO_INCREMENT PRIMARY KEY,
  recurring_invoice_id INT NOT NULL,
  generated_invoice_id INT NULL,
  status VARCHAR(64) NOT NULL,
  message TEXT NULL,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (recurring_invoice_id) REFERENCES recurring_invoices(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Check if recurring_invoice_id exists on invoices, add if not
SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'invoices'
    AND COLUMN_NAME = 'recurring_invoice_id'
);
SET @ddl := IF(@col_exists = 0,
  'ALTER TABLE invoices ADD COLUMN recurring_invoice_id INT NULL AFTER reservation_id',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- Check if due_date exists on invoices, add if not
SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'invoices'
    AND COLUMN_NAME = 'due_date'
);
SET @ddl := IF(@col_exists = 0,
  'ALTER TABLE invoices ADD COLUMN due_date DATE NULL AFTER invoice_date',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;


-- EPIC 3: Table Manager
CREATE TABLE IF NOT EXISTS restaurant_areas (
  id INT AUTO_INCREMENT PRIMARY KEY,
  restaurant_id INT NOT NULL,
  name VARCHAR(128) NOT NULL,
  bg_color VARCHAR(32) DEFAULT '#ffffff',
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS restaurant_tables (
  id INT AUTO_INCREMENT PRIMARY KEY,
  restaurant_id INT NOT NULL,
  area_id INT NOT NULL,
  name VARCHAR(64) NOT NULL,
  capacity INT NOT NULL DEFAULT 2,
  x_pos INT NOT NULL DEFAULT 0,
  y_pos INT NOT NULL DEFAULT 0,
  status ENUM('available', 'occupied', 'reserved') DEFAULT 'available',
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
  FOREIGN KEY (area_id) REFERENCES restaurant_areas(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;


-- EPIC 4: Staff/Member Management WhatsApp Number
-- Add whatsapp_number column to members if it doesn't exist
SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'restaurant_members'
    AND COLUMN_NAME = 'whatsapp_number'
);
SET @ddl := IF(@col_exists = 0,
  'ALTER TABLE restaurant_members ADD COLUMN whatsapp_number VARCHAR(32) NULL AFTER last_name',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;
