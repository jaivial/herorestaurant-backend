-- 077: Cash-drawer events (Cajón 10) and the tag catalogue (Tags 16).
--
-- Cajón records a NO_SALE drawer opening against the current cash shift so the
-- event is auditable even before a physical printer/drawer agent exists.
--
-- Tags are a tenant catalogue attachable to a whole ticket or a single line
-- ("sin gluten", "para llevar"); line tags travel with the kitchen dispatch.
--
-- FK types verified against the live schema before writing:
--   restaurants.id INT, pos_shifts.id BIGINT UNSIGNED, pos_tickets.id BIGINT UNSIGNED,
--   pos_ticket_lines.id BIGINT UNSIGNED, bo_users.id INT.

CREATE TABLE IF NOT EXISTS pos_drawer_events (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    shift_id BIGINT UNSIGNED NOT NULL,
    reason ENUM('NO_SALE','CHANGE','COUNT','OTHER') NOT NULL DEFAULT 'NO_SALE',
    note VARCHAR(300) NULL,
    idempotency_key VARCHAR(120) NOT NULL,
    opened_by INT NOT NULL,
    opened_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_pos_drawer_events_tenant_id (restaurant_id, id),
    UNIQUE KEY uq_pos_drawer_event_idempotency (restaurant_id, idempotency_key),
    KEY idx_pos_drawer_events_shift (restaurant_id, shift_id, opened_at),
    CONSTRAINT fk_pos_drawer_events_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
    CONSTRAINT fk_pos_drawer_events_shift FOREIGN KEY (restaurant_id, shift_id) REFERENCES pos_shifts(restaurant_id, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS pos_tags (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    name VARCHAR(80) NOT NULL,
    color VARCHAR(20) NULL,
    scope ENUM('LINE','TICKET','BOTH') NOT NULL DEFAULT 'BOTH',
    sort_order INT NOT NULL DEFAULT 0,
    is_active TINYINT(1) NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_pos_tags_tenant_id (restaurant_id, id),
    UNIQUE KEY uq_pos_tag_name (restaurant_id, name),
    KEY idx_pos_tags_active (restaurant_id, is_active, sort_order),
    CONSTRAINT fk_pos_tags_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS pos_ticket_tags (
    restaurant_id INT NOT NULL,
    ticket_id BIGINT UNSIGNED NOT NULL,
    tag_id BIGINT UNSIGNED NOT NULL,
    created_by INT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (restaurant_id, ticket_id, tag_id),
    KEY idx_pos_ticket_tags_tag (restaurant_id, tag_id),
    CONSTRAINT fk_pos_ticket_tags_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
    CONSTRAINT fk_pos_ticket_tags_ticket FOREIGN KEY (restaurant_id, ticket_id) REFERENCES pos_tickets(restaurant_id, id),
    CONSTRAINT fk_pos_ticket_tags_tag FOREIGN KEY (restaurant_id, tag_id) REFERENCES pos_tags(restaurant_id, id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS pos_ticket_line_tags (
    restaurant_id INT NOT NULL,
    ticket_line_id BIGINT UNSIGNED NOT NULL,
    tag_id BIGINT UNSIGNED NOT NULL,
    created_by INT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (restaurant_id, ticket_line_id, tag_id),
    KEY idx_pos_ticket_line_tags_tag (restaurant_id, tag_id),
    CONSTRAINT fk_pos_ticket_line_tags_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
    CONSTRAINT fk_pos_ticket_line_tags_line FOREIGN KEY (restaurant_id, ticket_line_id) REFERENCES pos_ticket_lines(restaurant_id, id),
    CONSTRAINT fk_pos_ticket_line_tags_tag FOREIGN KEY (restaurant_id, tag_id) REFERENCES pos_tags(restaurant_id, id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
