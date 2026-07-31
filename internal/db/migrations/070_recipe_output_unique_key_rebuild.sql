-- 070: Rebuild uq_stock_recipe_output so DRAFT technical sheets can coexist.
--
-- The generated column active_output_item_id currently keys on is_active alone,
-- so two DRAFT sheets targeting the same output item would collide before either
-- is published. Draft sheets are not active stock configuration and must not
-- reserve the output slot; only ACTIVE ones may.
--
-- After this migration:
--   is_active=1 AND status='ACTIVE'  -> slot reserved (at most one per tenant)
--   anything else                    -> NULL, and MySQL treats NULLs as distinct
--
-- Owner-approved. Isolated in its own migration so a failure is unambiguous and
-- rollback is a single file. Requires 069 (adds stock_recipes.status).
--
-- ROLLBACK:
--   ALTER TABLE stock_recipes DROP INDEX uq_stock_recipe_output;
--   ALTER TABLE stock_recipes DROP COLUMN active_output_item_id;
--   ALTER TABLE stock_recipes ADD COLUMN active_output_item_id BIGINT UNSIGNED
--     GENERATED ALWAYS AS (CASE WHEN is_active = 1 THEN output_item_id ELSE NULL END) STORED;
--   ALTER TABLE stock_recipes ADD UNIQUE KEY uq_stock_recipe_output (restaurant_id, active_output_item_id);

SET @dbname = DATABASE();

-- Guard: only rebuild once. Detect the new definition by its reference to `status`.
SET @already = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='stock_recipes'
    AND COLUMN_NAME='active_output_item_id'
    AND GENERATION_EXPRESSION LIKE '%status%');

-- Drop the unique key first: the generated column cannot be dropped while indexed.
SET @s = (SELECT IF(@already > 0
    OR (SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
        WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='stock_recipes'
          AND INDEX_NAME='uq_stock_recipe_output') = 0,
  'SELECT 1',
  'ALTER TABLE stock_recipes DROP INDEX uq_stock_recipe_output'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF(@already > 0
    OR (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
        WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='stock_recipes'
          AND COLUMN_NAME='active_output_item_id') = 0,
  'SELECT 1',
  'ALTER TABLE stock_recipes DROP COLUMN active_output_item_id'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF(@already > 0,
  'SELECT 1',
  "ALTER TABLE stock_recipes ADD COLUMN active_output_item_id BIGINT UNSIGNED GENERATED ALWAYS AS (CASE WHEN is_active = 1 AND status = 'ACTIVE' THEN output_item_id ELSE NULL END) STORED"));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @s = (SELECT IF((SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA=@dbname AND TABLE_NAME='stock_recipes' AND INDEX_NAME='uq_stock_recipe_output') > 0,
  'SELECT 1',
  'ALTER TABLE stock_recipes ADD UNIQUE KEY uq_stock_recipe_output (restaurant_id, active_output_item_id)'));
PREPARE stmt FROM @s; EXECUTE stmt; DEALLOCATE PREPARE stmt;
