-- Backfill POS product allergens from the carta they were imported from.
--
-- Why this join is reliable (measured on dev before writing it): a product with
-- source_type='COMIDA_ITEM' was created by the Carta import as
-- INSERT ... SELECT ... FROM comida_items c WHERE c.id=?, storing c.id in
-- source_id. 31/31 such products resolve to a comida_items row of the same
-- restaurant, 31/31 carry the identical name, 0 orphans. (Round 2 looked at
-- menu_dishes_catalog, which is a different table and NOT the import source.)
--
-- What it does NOT do:
--   - never overwrites a product that already has allergens (someone set them);
--   - never writes from an empty carta list: "[]" in the carta can mean "not
--     filled in" as easily as "none", and a POS that says "sin alérgenos" when
--     nobody checked is worse than one that says nothing;
--   - never writes a list containing a value outside the known vocabulary.
--
-- The carta spells allergens differently from the POS ("Leche", "Frutos de
-- cascara", no accents); the mapping below is closed and explicit, and the
-- guard refuses any row with a value it does not know.
UPDATE pos_products p
JOIN comida_items c ON c.restaurant_id = p.restaurant_id AND c.id = p.source_id
SET p.allergens_json = (
  SELECT CAST(JSON_ARRAYAGG(CASE jt.a
      WHEN 'Leche' THEN 'Lácteos'
      WHEN 'Frutos de cascara' THEN 'Frutos secos'
      WHEN 'Crustaceos' THEN 'Crustáceos'
      WHEN 'Sesamo' THEN 'Sésamo'
      WHEN 'Altramuces' THEN 'Altramuz'
      ELSE jt.a END) AS CHAR(512))
  FROM JSON_TABLE(c.alergenos_json, '$[*]' COLUMNS (a VARCHAR(64) PATH '$')) jt
)
WHERE p.source_type = 'COMIDA_ITEM'
  AND p.allergens_json IS NULL
  AND c.alergenos_json IS NOT NULL
  AND JSON_TYPE(c.alergenos_json) = 'ARRAY'
  AND JSON_LENGTH(c.alergenos_json) > 0
  AND NOT EXISTS (
    SELECT 1 FROM JSON_TABLE(c.alergenos_json, '$[*]' COLUMNS (a VARCHAR(64) PATH '$')) jt2
    WHERE jt2.a NOT IN ('Gluten','Crustaceos','Crustáceos','Huevos','Pescado','Cacahuetes','Soja',
                        'Leche','Lácteos','Frutos de cascara','Frutos secos','Apio','Mostaza',
                        'Sesamo','Sésamo','Sulfitos','Altramuces','Altramuz','Moluscos')
  );
