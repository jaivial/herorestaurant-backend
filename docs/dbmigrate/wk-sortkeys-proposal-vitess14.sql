-- wk-sortkeys-proposal-vitess14.sql: PROPOSAL ONLY, NOT APPLIED (repair-vitess14).
-- Sort-only weight keys (no unique index) for LIKE sites that ORDER BY a text column
-- not in the 149 padding-key registry. Same key function (internal/mysqlcoll,
-- utf8mb4_unicode_ci PAD SPACE per source metadata). Raw text unchanged.
-- Activating requires, in this order: apply; backfill (cmd/wk-backfill after adding
-- the entries to mysqlcoll.WKColumns); wire EVERY writer of the column; then switch
-- ORDER BY to the key. Until then ORDER BY stays on the raw column (ci_ai approximation).
BEGIN;
ALTER TABLE "stock_items" ADD COLUMN "name__wk" bytea;   -- stock list / stock options ORDER BY i.name
ALTER TABLE "stock_recipes" ADD COLUMN "name__wk" bytea; -- dish allergens ORDER BY name LIMIT 5
CREATE INDEX "ixwksort__stock_items__name" ON "stock_items" ("restaurant_id", "name__wk");
CREATE INDEX "ixwksort__stock_recipes__name" ON "stock_recipes" ("restaurant_id", "name__wk");
ROLLBACK; -- proposal: never COMMIT from this file
-- Writers to wire before activation (lexical, non-test):
--   stock_items.name: backoffice_stock.go (create ~731, update ~782), backoffice_stock_import.go ~396,
--     backoffice_technical_sheet_duplicate.go ~60, backoffice_technical_sheet_ensure.go ~107,
--     backoffice_technical_sheets.go ~149, cmd/comida-stock-backfill (tool).
--   stock_recipes.name: backoffice_technical_sheet_duplicate.go ~81, _ensure.go ~124,
--     backoffice_technical_sheets.go ~168 and the dynamic SET builder ~265,
--     backoffice_stock_documents.go ~510, backoffice_stock_recipes.go ~260/~267.
