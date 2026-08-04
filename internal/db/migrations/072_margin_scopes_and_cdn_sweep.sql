-- 072: Scoped margin bands + nightly CDN orphan sweep audit.
--
-- WHY A NEW TABLE INSTEAD OF ALTERING stock_margin_bands
-- ------------------------------------------------------
-- stock_margin_bands keyed on (restaurant_id, category_id, zone) with a NULLable
-- category_id. MySQL treats NULLs as DISTINCT in unique indexes, so two GLOBAL
-- rows (both category_id NULL) were both accepted. Adding scope columns cannot
-- fix that; uniqueness would have to be enforced in application code, which is
-- worse than no key at all.
--
-- stock_margin_scopes gives every scope a NOT NULL discriminator (scope_key), so
-- UNIQUE (restaurant_id, scope_kind, scope_key) is TOTAL and the database
-- enforces uniqueness on its own.
--
-- scope_key is type-qualified for categories ('platos:12', 'bebidas:12') because
-- comida_plato_categories and comida_bebida_categories are SEPARATE tables, each
-- with its own INT auto-increment: id=12 exists in both and means different
-- things. A bare category id would silently conflate them.
--
-- Trade-off, accepted deliberately: scope_key is a string, so there is no FK to
-- the category tables (a polymorphic FK is not expressible in MySQL) and a
-- deleted category can leave a stale scope. Resolution treats an unresolvable
-- scope_key as "not found" and falls through to the next level, so a stale row
-- can never corrupt a calculation.

CREATE TABLE IF NOT EXISTS stock_margin_scopes (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    scope_kind ENUM('GLOBAL','COMIDA_TYPE','COMIDA_CATEGORY','STOCK_CATEGORY') NOT NULL,
    -- NEVER NULL. GLOBAL -> '*', COMIDA_TYPE -> 'platos',
    -- COMIDA_CATEGORY -> 'platos:12', STOCK_CATEGORY -> '7'.
    scope_key VARCHAR(64) NOT NULL,
    label VARCHAR(140) NOT NULL,
    target_food_cost_pct DECIMAL(5,2) NULL,
    notes VARCHAR(500) NULL,
    is_active TINYINT(1) NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_stock_margin_scope_tenant_id (restaurant_id, id),
    UNIQUE KEY uq_stock_margin_scope (restaurant_id, scope_kind, scope_key),
    KEY idx_stock_margin_scopes_lookup (restaurant_id, is_active, scope_kind),
    CONSTRAINT fk_stock_margin_scopes_restaurant
      FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
    CONSTRAINT chk_stock_margin_scope_target
      CHECK (target_food_cost_pct IS NULL
             OR (target_food_cost_pct > 0 AND target_food_cost_pct < 100))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS stock_margin_scope_bands (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    restaurant_id INT NOT NULL,
    scope_id BIGINT UNSIGNED NOT NULL,
    zone ENUM('RED','AMBER','GREEN','PURPLE') NOT NULL,
    min_food_cost_pct DECIMAL(5,2) NULL,   -- NULL = open lower bound (PURPLE)
    max_food_cost_pct DECIMAL(5,2) NULL,   -- NULL = open upper bound (RED)
    sort_order INT NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_stock_margin_scope_band_tenant_id (restaurant_id, id),
    UNIQUE KEY uq_stock_margin_scope_band (restaurant_id, scope_id, zone),
    KEY idx_stock_margin_scope_bands_scope (restaurant_id, scope_id, sort_order),
    CONSTRAINT fk_stock_margin_scope_bands_restaurant
      FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
    CONSTRAINT fk_stock_margin_scope_bands_scope
      FOREIGN KEY (restaurant_id, scope_id)
      REFERENCES stock_margin_scopes(restaurant_id, id) ON DELETE CASCADE,
    CONSTRAINT chk_stock_margin_scope_band_range
      CHECK ((min_food_cost_pct IS NULL OR (min_food_cost_pct >= 0 AND min_food_cost_pct <= 100))
         AND (max_food_cost_pct IS NULL OR (max_food_cost_pct >= 0 AND max_food_cost_pct <= 100))
         AND (min_food_cost_pct IS NULL OR max_food_cost_pct IS NULL
              OR min_food_cost_pct < max_food_cost_pct))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Nightly CDN orphan sweep audit. Every run and every deletion is recorded:
-- this job deletes customer images, so it must never be unattributable.
CREATE TABLE IF NOT EXISTS cdn_object_sweeps (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    started_at DATETIME NOT NULL,
    finished_at DATETIME NULL,
    status ENUM('RUNNING','SUCCEEDED','FAILED','ABORTED') NOT NULL DEFAULT 'RUNNING',
    dry_run TINYINT(1) NOT NULL DEFAULT 1,
    objects_listed INT NOT NULL DEFAULT 0,
    objects_referenced INT NOT NULL DEFAULT 0,
    objects_deleted INT NOT NULL DEFAULT 0,
    objects_skipped INT NOT NULL DEFAULT 0,
    delete_failures INT NOT NULL DEFAULT 0,
    error_message VARCHAR(500) NULL,
    PRIMARY KEY (id),
    KEY idx_cdn_sweeps_started (started_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS cdn_object_sweep_deletions (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    sweep_id BIGINT UNSIGNED NOT NULL,
    object_path VARCHAR(1000) NOT NULL,
    size_bytes BIGINT NULL,
    last_modified_at DATETIME NULL,
    deleted TINYINT(1) NOT NULL DEFAULT 0,
    error_message VARCHAR(500) NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    KEY idx_cdn_sweep_deletions_sweep (sweep_id),
    CONSTRAINT fk_cdn_sweep_deletions_sweep
      FOREIGN KEY (sweep_id) REFERENCES cdn_object_sweeps(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Retire the unusable flat band table.
-- Verified empty in production before writing this migration; the guard below
-- makes the drop refuse to destroy data if that ever stops being true.
SET @dbname = DATABASE();
--
-- The row count must be read through a prepared statement, not an inline
-- subquery: MySQL resolves table references at parse time, so referencing a
-- possibly-absent table inside IF() fails with 1146 even when the guard is
-- false. That made a re-run of this migration crash instead of no-op.
SET @table_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='stock_margin_bands');

SET @band_rows = 0;
SET @s = (SELECT IF(@table_exists = 0,
  'SELECT 0 INTO @band_rows',
  'SELECT COUNT(*) FROM stock_margin_bands INTO @band_rows'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF(@table_exists > 0 AND @band_rows = 0,
  'DROP TABLE stock_margin_bands',
  'SELECT 1'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;
