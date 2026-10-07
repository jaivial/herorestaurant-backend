
-- Allergens on the POS product, as a JSON array of names in Spanish
-- ("Gluten", "Moluscos"): the waiter has to answer "does this contain nuts?"
-- from the till, and the 14 EU-listed allergens plus "sulfitos" and "altramuz"
-- are what the kitchen actually writes on the menu.
--
-- Kept as a JSON string rather than a join table because a product carries the
-- set as a unit — it is only ever read whole, never queried by allergen — and
-- because the rest of the catalogue (menu_dishes_catalog, stock_items,
-- stock_recipes) already stores its allergens the same way.
--
-- Written as an information_schema guard: this server rejects
-- `ALTER TABLE ... ADD COLUMN IF NOT EXISTS`, so re-running the migration would
-- otherwise crash the backend on boot with a duplicate-column error.
SET @pos_allergens_sql = IF(
  (SELECT COUNT(*) FROM information_schema.COLUMNS
     WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'pos_products'
       AND COLUMN_NAME = 'allergens_json') > 0,
  'SELECT 1',
  'ALTER TABLE pos_products ADD COLUMN allergens_json VARCHAR(512) NULL'
);
PREPARE pos_allergens_stmt FROM @pos_allergens_sql;
EXECUTE pos_allergens_stmt;
DEALLOCATE PREPARE pos_allergens_stmt;
