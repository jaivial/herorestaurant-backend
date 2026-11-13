-- wk-sortkeys-vitess16.sql: PROPOSAL, NOT APPLIED (repair-vitess16). Supersedes
-- wk-sortkeys-proposal-vitess14.sql. stock_items.name and stock_recipes.name are now
-- registered in internal/mysqlcoll/wkcolumns.go (151 entries): with VC_PG_WK_KEYS on, the
-- startup guard requires these columns and CheckStoredKeys = 0, so apply + backfill
-- (cmd/wk-backfill, columns file lines below) BEFORE enabling the flag.
--   stock_items name id utf8mb4_unicode_ci PAD restaurant_id
--   stock_recipes name id utf8mb4_unicode_ci PAD restaurant_id
BEGIN;
ALTER TABLE "stock_items" ADD COLUMN "name__wk" bytea;
ALTER TABLE "stock_recipes" ADD COLUMN "name__wk" bytea;
CREATE INDEX "ixwksort__stock_items__name" ON "stock_items" ("restaurant_id", "name__wk");
CREATE INDEX "ixwksort__stock_recipes__name" ON "stock_recipes" ("restaurant_id", "name__wk");
ROLLBACK; -- proposal: never COMMIT from this file
