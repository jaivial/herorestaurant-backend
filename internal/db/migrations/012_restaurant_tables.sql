-- Migration: Create restaurant_tables table
-- For TableManager feature

CREATE TABLE IF NOT EXISTS restaurant_tables (
    id INT AUTO_INCREMENT PRIMARY KEY,
    restaurant_id INT NOT NULL,
    numero_mesa INT NOT NULL,
    name VARCHAR(255),
    shape ENUM('round', 'square') NOT NULL DEFAULT 'round',
    capacity INT NOT NULL DEFAULT 4,
    position_x DECIMAL(10, 2) NOT NULL DEFAULT 0,
    position_y DECIMAL(10, 2) NOT NULL DEFAULT 0,
    width DECIMAL(10, 2) NOT NULL DEFAULT 80,
    height DECIMAL(10, 2) NOT NULL DEFAULT 80,
    color VARCHAR(50),
    status ENUM('available', 'occupied', 'reserved') NOT NULL DEFAULT 'available',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY idx_restaurant_numero (restaurant_id, numero_mesa),
    KEY idx_restaurant_id (restaurant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Add table_id column to bookings if not exists
-- ALTER TABLE bookings ADD COLUMN IF NOT EXISTS table_id INT NULL;
-- Note: This should be done separately if needed
