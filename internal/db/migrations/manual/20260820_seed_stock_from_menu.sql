-- Data migration: seed stock articles + technical sheets (fichas tecnicas)
-- from the existing menu catalogue for restaurant 1.
--
-- Rules (per product brief):
--   * Dishes (comida_items, source_type='platos')  -> PRODUCTO ELABORADO
--       -> stock_items(kind=SEMI_FINISHED, deduction_source=SALE) + a DRAFT
--          stock_recipes (the ficha tecnica, left empty for now) linked back.
--   * Desserts (POSTRES)                            -> PRODUCTO ELABORADO (same as above)
--   * Wines (VINOS)                                -> MATERIA PRIMA (RAW)
--       -> stock_items(kind=RAW, deduction_source=SALE), no recipe.
--   * Beverages (BEBIDAS) / Coffees (CAFES)        -> intentionally omitted:
--       those tables have no stock_item_id column yet and currently hold no rows.
--
-- Idempotent: every loop only touches rows whose stock_item_id IS NULL, so a
-- second run is a no-op. Run with: mysql newvillacarmen < this-file.sql
--
-- NOTE: comida_items.id and POSTRES.NUM share overlapping id ranges, so the
-- elaborados are seeded with SEPARATE cursors (never a UNION) to avoid a
-- postre primary key updating an unrelated plato row.
DELIMITER //

DROP PROCEDURE IF EXISTS seed_stock_from_menu //
CREATE PROCEDURE seed_stock_from_menu()
BEGIN
  -- ---- Elaborados: comida_items (platos) ----
  BEGIN
    DECLARE done INT DEFAULT FALSE;
    DECLARE v_rid INT;
    DECLARE v_id BIGINT;
    DECLARE v_name VARCHAR(900);
    DECLARE cur CURSOR FOR
      SELECT restaurant_id, id, nombre
        FROM comida_items
       WHERE source_type = 'platos' AND active = 1 AND stock_item_id IS NULL;
    DECLARE CONTINUE HANDLER FOR NOT FOUND SET done = TRUE;
    OPEN cur;
    plato_loop: LOOP
      FETCH cur INTO v_rid, v_id, v_name;
      IF done THEN LEAVE plato_loop; END IF;
      SET @nm = LEFT(TRIM(v_name), 180);
      IF @nm = '' THEN SET @nm = 'Ficha tecnica'; END IF;
      INSERT INTO stock_items (restaurant_id, name, kind, base_dimension, base_unit, is_tracked, deduction_source)
      VALUES (v_rid, @nm, 'SEMI_FINISHED', 'COUNT', 'ud', 1, 'SALE');
      SET @item_id = LAST_INSERT_ID();
      INSERT INTO stock_item_units (restaurant_id, stock_item_id, code, label, factor_to_base, is_default_display, can_recipe, can_count)
      VALUES (v_rid, @item_id, 'ud', 'ud', 1, 1, 1, 1);
      INSERT INTO stock_recipes (restaurant_id, name, output_item_id, output_qty_base, waste_pct, portions, status, is_active)
      VALUES (v_rid, @nm, @item_id, 1, 0, 1, 'DRAFT', 1);
      SET @recipe_id = LAST_INSERT_ID();
      UPDATE comida_items
         SET production_type = 'MANUFACTURED', stock_item_id = @item_id, stock_recipe_id = @recipe_id
       WHERE id = v_id AND restaurant_id = v_rid AND source_type = 'platos';
    END LOOP;
    CLOSE cur;
  END;

  -- ---- Elaborados: POSTRES ----
  BEGIN
    DECLARE done INT DEFAULT FALSE;
    DECLARE v_rid INT;
    DECLARE v_num BIGINT;
    DECLARE v_desc VARCHAR(900);
    DECLARE cur CURSOR FOR
      SELECT restaurant_id, NUM, DESCRIPCION
        FROM POSTRES
       WHERE active = 1 AND stock_item_id IS NULL;
    DECLARE CONTINUE HANDLER FOR NOT FOUND SET done = TRUE;
    OPEN cur;
    postre_loop: LOOP
      FETCH cur INTO v_rid, v_num, v_desc;
      IF done THEN LEAVE postre_loop; END IF;
      SET @nm = LEFT(TRIM(v_desc), 180);
      IF @nm = '' THEN SET @nm = 'Ficha tecnica'; END IF;
      INSERT INTO stock_items (restaurant_id, name, kind, base_dimension, base_unit, is_tracked, deduction_source)
      VALUES (v_rid, @nm, 'SEMI_FINISHED', 'COUNT', 'ud', 1, 'SALE');
      SET @item_id = LAST_INSERT_ID();
      INSERT INTO stock_item_units (restaurant_id, stock_item_id, code, label, factor_to_base, is_default_display, can_recipe, can_count)
      VALUES (v_rid, @item_id, 'ud', 'ud', 1, 1, 1, 1);
      INSERT INTO stock_recipes (restaurant_id, name, output_item_id, output_qty_base, waste_pct, portions, status, is_active)
      VALUES (v_rid, @nm, @item_id, 1, 0, 1, 'DRAFT', 1);
      SET @recipe_id = LAST_INSERT_ID();
      UPDATE POSTRES
         SET production_type = 'MANUFACTURED', stock_item_id = @item_id, stock_recipe_id = @recipe_id
       WHERE NUM = v_num AND restaurant_id = v_rid;
    END LOOP;
    CLOSE cur;
  END;

  -- ---- Raw: VINOS ----
  BEGIN
    DECLARE done INT DEFAULT FALSE;
    DECLARE v_rid INT;
    DECLARE v_num BIGINT;
    DECLARE v_name VARCHAR(900);
    DECLARE cur CURSOR FOR
      SELECT restaurant_id, num, nombre FROM VINOS WHERE active = 1 AND stock_item_id IS NULL;
    DECLARE CONTINUE HANDLER FOR NOT FOUND SET done = TRUE;
    OPEN cur;
    vino_loop: LOOP
      FETCH cur INTO v_rid, v_num, v_name;
      IF done THEN LEAVE vino_loop; END IF;
      SET @nm = LEFT(TRIM(v_name), 180);
      IF @nm = '' THEN SET @nm = 'Articulo'; END IF;
      INSERT INTO stock_items (restaurant_id, name, kind, base_dimension, base_unit, is_tracked, deduction_source)
      VALUES (v_rid, @nm, 'RAW', 'COUNT', 'ud', 1, 'SALE');
      SET @raw_item_id = LAST_INSERT_ID();
      INSERT INTO stock_item_units (restaurant_id, stock_item_id, code, label, factor_to_base, is_default_display, can_purchase, can_recipe, can_count)
      VALUES (v_rid, @raw_item_id, 'ud', 'ud', 1, 1, 1, 1, 1);
      UPDATE VINOS SET stock_item_id = @raw_item_id WHERE num = v_num AND restaurant_id = v_rid;
    END LOOP;
    CLOSE cur;
  END;
END //

DELIMITER ;

CALL seed_stock_from_menu();
DROP PROCEDURE IF EXISTS seed_stock_from_menu;

-- Summary (for the operator running the migration).
SELECT 'comida_items linked' AS label, COUNT(*) AS count FROM comida_items WHERE source_type='platos' AND stock_item_id IS NOT NULL
UNION ALL SELECT 'postres linked', COUNT(*) FROM POSTRES WHERE stock_item_id IS NOT NULL
UNION ALL SELECT 'vinos linked', COUNT(*) FROM VINOS WHERE stock_item_id IS NOT NULL
UNION ALL SELECT 'stock_items total', COUNT(*) FROM stock_items
UNION ALL SELECT 'stock_recipes total', COUNT(*) FROM stock_recipes;
