-- Restaurant contact and fiscal information table
-- Stores per-restaurant address, phone, email and fiscal data for invoices

CREATE TABLE IF NOT EXISTS restaurant_info (
  restaurant_id INT NOT NULL,
  direccion VARCHAR(512) DEFAULT '',
  telefono VARCHAR(64) DEFAULT '',
  email VARCHAR(255) DEFAULT '',
  cif VARCHAR(32) DEFAULT '',
  direccion_facturacion VARCHAR(512) DEFAULT '',
  clasificacion ENUM('persona_fisica', 'sociedad') DEFAULT 'sociedad',
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (restaurant_id),
  CONSTRAINT fk_restaurant_info_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
