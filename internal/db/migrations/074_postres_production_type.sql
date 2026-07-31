-- 074: Give POSTRES the production/stock columns every other product type got
-- in migration 069.
--
-- WHY POSTRES WAS LEFT OUT
-- ------------------------
-- POSTRES is a legacy table (NUM/DESCRIPCION, no price column) that predates
-- comida_items. Migration 069 covered comida_items and VINOS but not this one,
-- so desserts were the only catalogue products that could not be linked to
-- stock at all.
--
-- Existing rows default to 'RAW': no dessert has a technical sheet yet, and
-- defaulting to MANUFACTURED would claim a recipe that does not exist.
--
-- Each ALTER is guarded with PREPARE/EXECUTE rather than a plain
-- "ADD COLUMN IF NOT EXISTS" because MySQL resolves table and column
-- references at PARSE time: a statement mentioning a column that is not there
-- yet fails even when the surrounding guard is false. The same pattern is used
-- by migration 072 for the same reason.

SET @dbname := DATABASE();

SET @stmt := (SELECT IF(
  (SELECT COUNT(*) FROM information_schema.COLUMNS
    WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='POSTRES' AND COLUMN_NAME='production_type') > 0,
  'SELECT 1',
  "ALTER TABLE POSTRES ADD COLUMN production_type ENUM('RAW','MANUFACTURED') NOT NULL DEFAULT 'RAW'"));
PREPARE s FROM @stmt; EXECUTE s; DEALLOCATE PREPARE s;

SET @stmt := (SELECT IF(
  (SELECT COUNT(*) FROM information_schema.COLUMNS
    WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='POSTRES' AND COLUMN_NAME='stock_item_id') > 0,
  'SELECT 1',
  'ALTER TABLE POSTRES ADD COLUMN stock_item_id BIGINT UNSIGNED NULL'));
PREPARE s FROM @stmt; EXECUTE s; DEALLOCATE PREPARE s;

SET @stmt := (SELECT IF(
  (SELECT COUNT(*) FROM information_schema.COLUMNS
    WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='POSTRES' AND COLUMN_NAME='stock_recipe_id') > 0,
  'SELECT 1',
  'ALTER TABLE POSTRES ADD COLUMN stock_recipe_id BIGINT UNSIGNED NULL'));
PREPARE s FROM @stmt; EXECUTE s; DEALLOCATE PREPARE s;

-- Index the link used by the sheet-usage lookup: deleting a technical sheet
-- must check whether a dessert still depends on it, and that runs per delete.
SET @stmt := (SELECT IF(
  (SELECT COUNT(*) FROM information_schema.STATISTICS
    WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='POSTRES' AND INDEX_NAME='idx_postres_stock_recipe') > 0,
  'SELECT 1',
  'ALTER TABLE POSTRES ADD KEY idx_postres_stock_recipe (restaurant_id, stock_recipe_id)'));
PREPARE s FROM @stmt; EXECUTE s; DEALLOCATE PREPARE s;
