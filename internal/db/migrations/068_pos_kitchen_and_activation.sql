CREATE TABLE IF NOT EXISTS pos_activation_acceptances (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    acceptance_type ENUM('STOCK_LIVE','COVERS_LIVE') NOT NULL,
    evidence_note VARCHAR(1000) NOT NULL,
    snapshot_json JSON NOT NULL,
    accepted_by INT NOT NULL,
    accepted_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    consumed_at DATETIME NULL,
    PRIMARY KEY (id),
    KEY idx_pos_activation_acceptance (restaurant_id, acceptance_type, consumed_at, accepted_at),
    CONSTRAINT fk_pos_activation_acceptance_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS pos_kitchen_stations (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    name VARCHAR(120) NOT NULL,
    sort_order INT NOT NULL DEFAULT 0,
    is_active TINYINT(1) NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_pos_kitchen_station_tenant_id (restaurant_id, id),
    UNIQUE KEY uq_pos_kitchen_station_name (restaurant_id, name),
    KEY idx_pos_kitchen_stations (restaurant_id, is_active, sort_order),
    CONSTRAINT fk_pos_kitchen_station_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS pos_kitchen_routes (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    station_id BIGINT UNSIGNED NOT NULL,
    pos_product_id BIGINT UNSIGNED NULL,
    category_id BIGINT UNSIGNED NULL,
    priority INT NOT NULL DEFAULT 0,
    is_active TINYINT(1) NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_pos_kitchen_route_product (restaurant_id, station_id, pos_product_id),
    UNIQUE KEY uq_pos_kitchen_route_category (restaurant_id, station_id, category_id),
    KEY idx_pos_kitchen_routes_match_product (restaurant_id, pos_product_id, is_active, priority),
    KEY idx_pos_kitchen_routes_match_category (restaurant_id, category_id, is_active, priority),
    CONSTRAINT fk_pos_kitchen_route_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
    CONSTRAINT fk_pos_kitchen_route_station FOREIGN KEY (restaurant_id, station_id) REFERENCES pos_kitchen_stations(restaurant_id, id) ON DELETE CASCADE,
    CONSTRAINT fk_pos_kitchen_route_product FOREIGN KEY (restaurant_id, pos_product_id) REFERENCES pos_products(restaurant_id, id) ON DELETE CASCADE,
    CONSTRAINT fk_pos_kitchen_route_category FOREIGN KEY (restaurant_id, category_id) REFERENCES pos_product_categories(restaurant_id, id) ON DELETE CASCADE,
    CONSTRAINT chk_pos_kitchen_route_target CHECK ((pos_product_id IS NOT NULL AND category_id IS NULL) OR (pos_product_id IS NULL AND category_id IS NOT NULL))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS pos_kitchen_dispatches (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    station_id BIGINT UNSIGNED NOT NULL,
    visit_id BIGINT UNSIGNED NOT NULL,
    ticket_id BIGINT UNSIGNED NOT NULL,
    sequence_no INT NOT NULL,
    command_key VARCHAR(120) NOT NULL,
    status ENUM('PENDING','ACKNOWLEDGED','PREPARING','READY','FAILED','CANCELLED') NOT NULL DEFAULT 'PENDING',
    payload_hash CHAR(64) NOT NULL,
    created_by INT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    acknowledged_at DATETIME NULL,
    ready_at DATETIME NULL,
    last_error VARCHAR(500) NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_pos_kitchen_dispatch_tenant_id (restaurant_id, id),
    UNIQUE KEY uq_pos_kitchen_dispatch_command (restaurant_id, station_id, command_key),
    UNIQUE KEY uq_pos_kitchen_dispatch_sequence (restaurant_id, station_id, ticket_id, sequence_no),
    KEY idx_pos_kitchen_queue (restaurant_id, station_id, status, created_at),
    CONSTRAINT fk_pos_kitchen_dispatch_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
    CONSTRAINT fk_pos_kitchen_dispatch_station FOREIGN KEY (restaurant_id, station_id) REFERENCES pos_kitchen_stations(restaurant_id, id),
    CONSTRAINT fk_pos_kitchen_dispatch_visit FOREIGN KEY (restaurant_id, visit_id) REFERENCES pos_visits(restaurant_id, id),
    CONSTRAINT fk_pos_kitchen_dispatch_ticket FOREIGN KEY (restaurant_id, ticket_id) REFERENCES pos_tickets(restaurant_id, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS pos_kitchen_dispatch_lines (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    dispatch_id BIGINT UNSIGNED NOT NULL,
    ticket_line_id BIGINT UNSIGNED NOT NULL,
    action ENUM('ADD','VOID','NOTE','FIRE') NOT NULL,
    quantity_delta DECIMAL(12,3) NOT NULL DEFAULT 0,
    product_name_snapshot VARCHAR(180) NOT NULL,
    notes_snapshot VARCHAR(500) NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    KEY idx_pos_kitchen_dispatch_line (restaurant_id, dispatch_id, ticket_line_id, action),
    KEY idx_pos_kitchen_line_history (restaurant_id, ticket_line_id, dispatch_id),
    CONSTRAINT fk_pos_kitchen_dispatch_line_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
    CONSTRAINT fk_pos_kitchen_dispatch_line_dispatch FOREIGN KEY (restaurant_id, dispatch_id) REFERENCES pos_kitchen_dispatches(restaurant_id, id) ON DELETE CASCADE,
    CONSTRAINT fk_pos_kitchen_dispatch_line_ticket_line FOREIGN KEY (restaurant_id, ticket_line_id) REFERENCES pos_ticket_lines(restaurant_id, id),
    CONSTRAINT chk_pos_kitchen_dispatch_line_qty CHECK ((action IN ('ADD','VOID') AND quantity_delta <> 0) OR (action IN ('NOTE','FIRE') AND quantity_delta = 0))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
