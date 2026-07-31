-- 078: Shared modifier schema serving Combinado (07), Suplemento (20) and Pack (22).
--
-- One mechanism, three product experiences:
--   * pos_modifier_groups / pos_modifier_options  -> the tenant catalogue of
--     choices attachable to a product (e.g. "Refresco" for a combinado,
--     "Punto de la carne" or a paid supplement).
--   * pos_ticket_line_modifiers                   -> what the operator actually
--     picked on a concrete ticket line, with an immutable price/name snapshot.
--   * pos_packs / pos_pack_components             -> a bundle sold at a fixed
--     price that expands into component lines.
--
-- Money rule: the parent line keeps the base price; every modifier contributes
-- price_delta_cents so line_total_gross_cents = (unit + sum(deltas)) * quantity.
-- Snapshots mean editing the catalogue never rewrites paid ticket history.
--
-- FK types verified against the live schema before writing:
--   restaurants.id INT, pos_products.id BIGINT UNSIGNED,
--   pos_ticket_lines.id BIGINT UNSIGNED, stock_vat_rates.id BIGINT UNSIGNED.

CREATE TABLE IF NOT EXISTS pos_modifier_groups (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    name VARCHAR(120) NOT NULL,
    kind ENUM('COMBO','SUPPLEMENT','OPTION') NOT NULL DEFAULT 'OPTION',
    min_select INT NOT NULL DEFAULT 0,
    max_select INT NOT NULL DEFAULT 1,
    sort_order INT NOT NULL DEFAULT 0,
    is_active TINYINT(1) NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_pos_modifier_groups_tenant_id (restaurant_id, id),
    UNIQUE KEY uq_pos_modifier_group_name (restaurant_id, name),
    KEY idx_pos_modifier_groups_active (restaurant_id, is_active, sort_order),
    CONSTRAINT fk_pos_modifier_groups_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
    CONSTRAINT chk_pos_modifier_group_select CHECK (min_select >= 0 AND max_select >= min_select)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS pos_modifier_options (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    group_id BIGINT UNSIGNED NOT NULL,
    name VARCHAR(180) NOT NULL,
    -- Signed: a supplement is positive, a "sin guarnición" rebate is negative.
    price_delta_cents BIGINT NOT NULL DEFAULT 0,
    -- Optional link so the modifier can carry its own stock rules and kitchen route.
    source_product_id BIGINT UNSIGNED NULL,
    sort_order INT NOT NULL DEFAULT 0,
    is_active TINYINT(1) NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_pos_modifier_options_tenant_id (restaurant_id, id),
    UNIQUE KEY uq_pos_modifier_option_name (restaurant_id, group_id, name),
    KEY idx_pos_modifier_options_group (restaurant_id, group_id, is_active, sort_order),
    CONSTRAINT fk_pos_modifier_options_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
    CONSTRAINT fk_pos_modifier_options_group FOREIGN KEY (restaurant_id, group_id) REFERENCES pos_modifier_groups(restaurant_id, id) ON DELETE CASCADE,
    CONSTRAINT fk_pos_modifier_options_product FOREIGN KEY (restaurant_id, source_product_id) REFERENCES pos_products(restaurant_id, id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS pos_product_modifier_groups (
    restaurant_id INT NOT NULL,
    pos_product_id BIGINT UNSIGNED NOT NULL,
    group_id BIGINT UNSIGNED NOT NULL,
    is_required TINYINT(1) NOT NULL DEFAULT 0,
    sort_order INT NOT NULL DEFAULT 0,
    PRIMARY KEY (restaurant_id, pos_product_id, group_id),
    KEY idx_pos_product_modifier_groups_group (restaurant_id, group_id),
    CONSTRAINT fk_pos_product_modifier_groups_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
    CONSTRAINT fk_pos_product_modifier_groups_product FOREIGN KEY (restaurant_id, pos_product_id) REFERENCES pos_products(restaurant_id, id) ON DELETE CASCADE,
    CONSTRAINT fk_pos_product_modifier_groups_group FOREIGN KEY (restaurant_id, group_id) REFERENCES pos_modifier_groups(restaurant_id, id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS pos_ticket_line_modifiers (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    ticket_line_id BIGINT UNSIGNED NOT NULL,
    modifier_option_id BIGINT UNSIGNED NULL,
    -- Immutable snapshots: catalogue edits never rewrite sold history.
    name_snapshot VARCHAR(180) NOT NULL,
    price_delta_cents BIGINT NOT NULL DEFAULT 0,
    quantity DECIMAL(12,3) NOT NULL DEFAULT 1,
    source_product_id BIGINT UNSIGNED NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_pos_ticket_line_modifiers_tenant_id (restaurant_id, id),
    KEY idx_pos_ticket_line_modifiers_line (restaurant_id, ticket_line_id, id),
    CONSTRAINT fk_pos_ticket_line_modifiers_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
    CONSTRAINT fk_pos_ticket_line_modifiers_line FOREIGN KEY (restaurant_id, ticket_line_id) REFERENCES pos_ticket_lines(restaurant_id, id),
    CONSTRAINT fk_pos_ticket_line_modifiers_option FOREIGN KEY (restaurant_id, modifier_option_id) REFERENCES pos_modifier_options(restaurant_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_pos_ticket_line_modifiers_product FOREIGN KEY (restaurant_id, source_product_id) REFERENCES pos_products(restaurant_id, id) ON DELETE RESTRICT,
    CONSTRAINT chk_pos_ticket_line_modifier_qty CHECK (quantity > 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS pos_packs (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    name VARCHAR(180) NOT NULL,
    description VARCHAR(1000) NULL,
    price_gross_cents BIGINT NOT NULL DEFAULT 0,
    vat_rate_id BIGINT UNSIGNED NULL,
    is_active TINYINT(1) NOT NULL DEFAULT 1,
    sort_order INT NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_pos_packs_tenant_id (restaurant_id, id),
    UNIQUE KEY uq_pos_pack_name (restaurant_id, name),
    KEY idx_pos_packs_active (restaurant_id, is_active, sort_order),
    CONSTRAINT fk_pos_packs_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
    CONSTRAINT fk_pos_packs_vat FOREIGN KEY (restaurant_id, vat_rate_id) REFERENCES stock_vat_rates(restaurant_id, id) ON DELETE RESTRICT,
    CONSTRAINT chk_pos_pack_price CHECK (price_gross_cents >= 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS pos_pack_components (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    pack_id BIGINT UNSIGNED NOT NULL,
    pos_product_id BIGINT UNSIGNED NOT NULL,
    quantity DECIMAL(12,3) NOT NULL DEFAULT 1,
    -- Components sharing a slot_group are alternatives the operator chooses between.
    slot_group VARCHAR(80) NULL,
    is_default TINYINT(1) NOT NULL DEFAULT 1,
    sort_order INT NOT NULL DEFAULT 0,
    PRIMARY KEY (id),
    UNIQUE KEY uq_pos_pack_components_tenant_id (restaurant_id, id),
    KEY idx_pos_pack_components_pack (restaurant_id, pack_id, slot_group, sort_order),
    CONSTRAINT fk_pos_pack_components_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
    CONSTRAINT fk_pos_pack_components_pack FOREIGN KEY (restaurant_id, pack_id) REFERENCES pos_packs(restaurant_id, id) ON DELETE CASCADE,
    CONSTRAINT fk_pos_pack_components_product FOREIGN KEY (restaurant_id, pos_product_id) REFERENCES pos_products(restaurant_id, id) ON DELETE RESTRICT,
    CONSTRAINT chk_pos_pack_component_qty CHECK (quantity > 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Pack provenance on the sold line: which pack produced it, and which line is the parent.
SET @dbname = DATABASE();

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='pos_ticket_lines' AND COLUMN_NAME='pack_id') > 0,
  'SELECT 1',
  'ALTER TABLE pos_ticket_lines ADD COLUMN pack_id BIGINT UNSIGNED NULL'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='pos_ticket_lines' AND COLUMN_NAME='parent_line_id') > 0,
  'SELECT 1',
  'ALTER TABLE pos_ticket_lines ADD COLUMN parent_line_id BIGINT UNSIGNED NULL'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='pos_ticket_lines' AND INDEX_NAME='idx_pos_ticket_lines_parent') > 0,
  'SELECT 1',
  'ALTER TABLE pos_ticket_lines ADD KEY idx_pos_ticket_lines_parent (restaurant_id, parent_line_id)'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='pos_ticket_lines' AND CONSTRAINT_NAME='fk_pos_ticket_lines_pack') > 0,
  'SELECT 1',
  'ALTER TABLE pos_ticket_lines ADD CONSTRAINT fk_pos_ticket_lines_pack FOREIGN KEY (restaurant_id, pack_id) REFERENCES pos_packs(restaurant_id, id) ON DELETE RESTRICT'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='pos_ticket_lines' AND CONSTRAINT_NAME='fk_pos_ticket_lines_parent') > 0,
  'SELECT 1',
  'ALTER TABLE pos_ticket_lines ADD CONSTRAINT fk_pos_ticket_lines_parent FOREIGN KEY (restaurant_id, parent_line_id) REFERENCES pos_ticket_lines(restaurant_id, id)'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;
