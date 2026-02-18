-- Migration 013: Food management tables (CAKES, BEBIDAS, PLATOS)
-- Tables for managing coffee, beverages, and dishes in the restaurant

-- Create CAFES table (coffee)
CREATE TABLE IF NOT EXISTS CAFES (
    num INT AUTO_INCREMENT PRIMARY KEY,
    restaurant_id INT NOT NULL,
    tipo VARCHAR(50) NOT NULL DEFAULT 'CAFE',
    nombre VARCHAR(255) NOT NULL,
    precio DECIMAL(10, 2) NOT NULL DEFAULT 0,
    descripcion TEXT,
    titulo VARCHAR(255),
    suplemento DECIMAL(10, 2) DEFAULT 0,
    alergenos JSON,
    active TINYINT(1) DEFAULT 1,
    foto_path VARCHAR(512),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_cafe_restaurant (restaurant_id),
    INDEX idx_cafe_tipo (tipo),
    INDEX idx_cafe_active (active)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Create BEBIDAS table (beverages)
CREATE TABLE IF NOT EXISTS BEBIDAS (
    num INT AUTO_INCREMENT PRIMARY KEY,
    restaurant_id INT NOT NULL,
    tipo VARCHAR(50) NOT NULL DEFAULT 'BEBIDA',
    nombre VARCHAR(255) NOT NULL,
    precio DECIMAL(10, 2) NOT NULL DEFAULT 0,
    descripcion TEXT,
    titulo VARCHAR(255),
    suplemento DECIMAL(10, 2) DEFAULT 0,
    alergenos JSON,
    active TINYINT(1) DEFAULT 1,
    foto_path VARCHAR(512),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_bebida_restaurant (restaurant_id),
    INDEX idx_bebida_tipo (tipo),
    INDEX idx_bebida_active (active)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Create PLATOS table (dishes)
CREATE TABLE IF NOT EXISTS PLATOS (
    num INT AUTO_INCREMENT PRIMARY KEY,
    restaurant_id INT NOT NULL,
    tipo VARCHAR(50) NOT NULL DEFAULT 'PLATO',
    nombre VARCHAR(255) NOT NULL,
    precio DECIMAL(10, 2) NOT NULL DEFAULT 0,
    descripcion TEXT,
    titulo VARCHAR(255),
    suplemento DECIMAL(10, 2) DEFAULT 0,
    alergenos JSON,
    active TINYINT(1) DEFAULT 1,
    foto_path VARCHAR(512),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_plato_restaurant (restaurant_id),
    INDEX idx_plato_tipo (tipo),
    INDEX idx_plato_active (active)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
