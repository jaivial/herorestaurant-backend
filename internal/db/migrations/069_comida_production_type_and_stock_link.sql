-- 069: Link comida products to stock via a raw/manufactured distinction.
--
-- RAW          -> the comida item IS a stock item; a sale deducts 1 unit of itself.
-- MANUFACTURED -> the comida item has a technical sheet (stock_recipes row);
--                 a sale deducts 1 portion of the recipe output item.
--
-- Raw ingredients leave stock at PRODUCTION time, not at sale time. Unsold or
-- discarded portions are written off as WASTE/OVERPRODUCTION (merma).
--
-- FK types verified against the live schema before writing:
--   comida_items.id INT, VINOS.num INT, restaurants.id INT, bo_users.id INT,
--   stock_items.id BIGINT UNSIGNED, stock_recipes.id BIGINT UNSIGNED.
-- Migration 065 crash-looped production by referencing an INT key from a BIGINT
-- column, so every new reference column below matches its target exactly.
--
-- Idempotent: every ALTER is guarded by an information_schema check.

SET @dbname = DATABASE();

-- ---------------------------------------------------------------------------
-- comida_items
-- ---------------------------------------------------------------------------
SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='comida_items' AND COLUMN_NAME='production_type') > 0,
  'SELECT 1',
  "ALTER TABLE comida_items ADD COLUMN production_type ENUM('RAW','MANUFACTURED') NOT NULL DEFAULT 'RAW'"));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='comida_items' AND COLUMN_NAME='stock_item_id') > 0,
  'SELECT 1',
  'ALTER TABLE comida_items ADD COLUMN stock_item_id BIGINT UNSIGNED NULL'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='comida_items' AND COLUMN_NAME='stock_recipe_id') > 0,
  'SELECT 1',
  'ALTER TABLE comida_items ADD COLUMN stock_recipe_id BIGINT UNSIGNED NULL'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='comida_items' AND INDEX_NAME='idx_comida_items_production') > 0,
  'SELECT 1',
  'ALTER TABLE comida_items ADD KEY idx_comida_items_production (restaurant_id, production_type)'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='comida_items' AND INDEX_NAME='idx_comida_items_stock_item') > 0,
  'SELECT 1',
  'ALTER TABLE comida_items ADD KEY idx_comida_items_stock_item (restaurant_id, stock_item_id)'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='comida_items' AND INDEX_NAME='idx_comida_items_stock_recipe') > 0,
  'SELECT 1',
  'ALTER TABLE comida_items ADD KEY idx_comida_items_stock_recipe (restaurant_id, stock_recipe_id)'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- ---------------------------------------------------------------------------
-- VINOS (separate legacy table, still authoritative for wine)
-- ---------------------------------------------------------------------------
SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='VINOS' AND COLUMN_NAME='production_type') > 0,
  'SELECT 1',
  "ALTER TABLE VINOS ADD COLUMN production_type ENUM('RAW','MANUFACTURED') NOT NULL DEFAULT 'RAW'"));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='VINOS' AND COLUMN_NAME='stock_item_id') > 0,
  'SELECT 1',
  'ALTER TABLE VINOS ADD COLUMN stock_item_id BIGINT UNSIGNED NULL'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='VINOS' AND COLUMN_NAME='stock_recipe_id') > 0,
  'SELECT 1',
  'ALTER TABLE VINOS ADD COLUMN stock_recipe_id BIGINT UNSIGNED NULL'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='VINOS' AND INDEX_NAME='idx_vinos_production') > 0,
  'SELECT 1',
  'ALTER TABLE VINOS ADD KEY idx_vinos_production (restaurant_id, production_type)'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- ---------------------------------------------------------------------------
-- stock_items: allergens are declared on the raw good and derived upward.
-- ---------------------------------------------------------------------------
SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='stock_items' AND COLUMN_NAME='allergens_json') > 0,
  'SELECT 1',
  'ALTER TABLE stock_items ADD COLUMN allergens_json JSON NULL'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- ---------------------------------------------------------------------------
-- stock_recipes: a technical sheet IS a recipe row.
-- ---------------------------------------------------------------------------
SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='stock_recipes' AND COLUMN_NAME='status') > 0,
  'SELECT 1',
  "ALTER TABLE stock_recipes ADD COLUMN status ENUM('DRAFT','ACTIVE','ARCHIVED') NOT NULL DEFAULT 'ACTIVE'"));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='stock_recipes' AND COLUMN_NAME='portions') > 0,
  'SELECT 1',
  'ALTER TABLE stock_recipes ADD COLUMN portions INT NOT NULL DEFAULT 1'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='stock_recipes' AND COLUMN_NAME='derived_allergens_json') > 0,
  'SELECT 1',
  'ALTER TABLE stock_recipes ADD COLUMN derived_allergens_json JSON NULL'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='stock_recipes' AND COLUMN_NAME='derived_allergens_at') > 0,
  'SELECT 1',
  'ALTER TABLE stock_recipes ADD COLUMN derived_allergens_at DATETIME NULL'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- {"added":[],"disabled":[]} -- a derived allergen may never be disabled.
SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='stock_recipes' AND COLUMN_NAME='manual_allergens_json') > 0,
  'SELECT 1',
  'ALTER TABLE stock_recipes ADD COLUMN manual_allergens_json JSON NULL'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='stock_recipes' AND COLUMN_NAME='copied_from_recipe_id') > 0,
  'SELECT 1',
  'ALTER TABLE stock_recipes ADD COLUMN copied_from_recipe_id BIGINT UNSIGNED NULL'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='stock_recipes' AND COLUMN_NAME='draft_owner_user_id') > 0,
  'SELECT 1',
  'ALTER TABLE stock_recipes ADD COLUMN draft_owner_user_id INT NULL'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='stock_recipes' AND COLUMN_NAME='draft_expires_at') > 0,
  'SELECT 1',
  'ALTER TABLE stock_recipes ADD COLUMN draft_expires_at DATETIME NULL'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='stock_recipes' AND INDEX_NAME='idx_stock_recipes_status') > 0,
  'SELECT 1',
  'ALTER TABLE stock_recipes ADD KEY idx_stock_recipes_status (restaurant_id, status, name)'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;
