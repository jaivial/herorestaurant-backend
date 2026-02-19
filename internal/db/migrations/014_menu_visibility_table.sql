-- 014_menu_visibility_table.sql
-- Create menu_visibility table if it doesn't exist (from legacy PHP)
-- This migration ensures the table exists with all required columns for the Go backend

-- Create table if not exists (matching legacy PHP schema)
CREATE TABLE IF NOT EXISTS menu_visibility (
    id INT AUTO_INCREMENT PRIMARY KEY,
    menu_key VARCHAR(50) UNIQUE NOT NULL,
    menu_name VARCHAR(100) NOT NULL,
    is_active TINYINT(1) DEFAULT 1,
    restaurant_id INT NOT NULL DEFAULT 1,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    KEY idx_menu_visibility_restaurant (restaurant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Seed initial data if table is empty (only for restaurant_id = 1)
INSERT IGNORE INTO menu_visibility (menu_key, menu_name, is_active, restaurant_id) VALUES
    ('menudeldia', 'Menú Del Día', 1, 1),
    ('menufindesemana', 'Menú Fin de Semana', 1, 1);
