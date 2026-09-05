-- =============================================================================
-- Manual data operation: split ARROZ dishes out of "principales" into a new
-- "Arroces" section for group menus 41 (Menú del día) and 42 (Menú fin de semana).
--
-- Restaurant: id 1 (villacarmen). Apply once per database:
--   mysql -u<user> -p <database> < 20260905_menus_41_42_arroces_section.sql
--
-- Rationale
-- ---------
-- Every dish tagged as rice lives in `comida_items` with tipo = 'ARROZ'
-- (categoria = 'Arroz'). In both menus those dishes currently sit inside the
-- section with section_kind = 'principales'. This script:
--   1. Creates an "Arroces" section (section_kind = 'arroces') right after
--      "principales" when missing.
--   2. Moves the rice dishes (curated from the comida_items ARROZ tag by
--      normalized-name match, listed below with their title_snapshot) from the
--      "principales" section into the new one.
--   3. Renumbers section and dish positions so ordering stays contiguous.
--
-- Idempotent: safe to re-run (guarded inserts, moves only touch rows still in
-- "principales", renumbering is deterministic).
-- Wrapped in a transaction: any failed statement rolls the whole batch back.
-- =============================================================================

START TRANSACTION;

-- ---------------------------------------------------------------------------
-- 1. Create the "Arroces" section for each menu, right after "principales",
--    only when it does not exist yet.
-- ---------------------------------------------------------------------------
INSERT INTO group_menu_sections_v2 (restaurant_id, menu_id, title, section_kind, position, annotations_json)
SELECT m.restaurant_id,
       m.id,
       'Arroces',
       'arroces',
       (SELECT MIN(sec.position) FROM group_menu_sections_v2 sec
         WHERE sec.menu_id = m.id AND sec.section_kind = 'principales') + 1,
       NULL
FROM menus m
WHERE m.id IN (41, 42)
  AND m.restaurant_id = 1
  AND NOT EXISTS (
        SELECT 1 FROM group_menu_sections_v2 existing
        WHERE existing.menu_id = m.id AND existing.section_kind = 'arroces'
      );

-- ---------------------------------------------------------------------------
-- 2. Resolve the affected section ids.
-- ---------------------------------------------------------------------------
SET @princ41 := (SELECT id FROM group_menu_sections_v2 WHERE menu_id = 41 AND section_kind = 'principales' LIMIT 1);
SET @arroz41 := (SELECT id FROM group_menu_sections_v2 WHERE menu_id = 41 AND section_kind = 'arroces' LIMIT 1);
SET @princ42 := (SELECT id FROM group_menu_sections_v2 WHERE menu_id = 42 AND section_kind = 'principales' LIMIT 1);
SET @arroz42 := (SELECT id FROM group_menu_sections_v2 WHERE menu_id = 42 AND section_kind = 'arroces' LIMIT 1);

-- ---------------------------------------------------------------------------
-- 3. Move the ARROZ dishes from "principales" into "Arroces".
--    Menu 41 (ids listed with title_snapshot):
--      320 Meloso de pescado y marisco (6€)
--      321 Arroz a banda
--      322 Fideua de pescado
--      323 Arroz del señoret (+5€)
--      324 Arroz meloso de pato, setas y foie (+6€)
--      325 Meloso de secreto con ajetes
--      326 Meloso de carrillada con boletus (+2€)
--      328 Meloso de pulpo, calamar y gambones (+5€)
--      329 Arroz seco de secreto con ajetes
--      330 Arroz seco de carrillada y boletus (+2)
--    Menu 42 (ids listed with title_snapshot):
--      339 Arroz meloso de secreto con ajetes.
--      340 Arroz a banda.
--      341 Arroz meloso de pescado y marisco (+5€).
--      342 Arroz de señoret (+3€)
--      343 Fideuá de pato y setas (+4€)
--      344 Paella Valenciana, por encargo (+6€)
--      345 Arroz seco de pato, setas y foie (+6€).
--      346 Arroz meloso de pulpo y gambones (+5€)
--      347 Arroz seco de pulpo y gambones (+5€)
--      348 Arroz seco de secreto con ajetes.
--      349 Arroz meloso de pato, setas y foie (+6€)
--      350 Arroz meloso de carrillada con boletus
--      351 Arroz seco de carrillada con boletus
--      362 Paella valenciana de la Albufera, con pato, pollo, garrofo,
--          bachoqueta y caracoles (+6€) Por encargo.
--      366 Arroz seco de verduras de la huerta
-- ---------------------------------------------------------------------------
UPDATE group_menu_section_dishes_v2
SET section_id = @arroz41
WHERE menu_id = 41
  AND section_id = @princ41
  AND id IN (320, 321, 322, 323, 324, 325, 326, 328, 329, 330);

UPDATE group_menu_section_dishes_v2
SET section_id = @arroz42
WHERE menu_id = 42
  AND section_id = @princ42
  AND id IN (339, 340, 341, 342, 343, 344, 345, 346, 347, 348, 349, 350, 351, 362, 366);

-- ---------------------------------------------------------------------------
-- 4. Renumber dish positions inside every touched section so each one is
--    contiguous (0-based) and keeps the previous relative order.
-- ---------------------------------------------------------------------------
UPDATE group_menu_section_dishes_v2 t
JOIN (
    SELECT id,
           ROW_NUMBER() OVER (
               PARTITION BY section_id
               ORDER BY position ASC, id ASC
           ) - 1 AS new_position
    FROM group_menu_section_dishes_v2
    WHERE section_id IN (@princ41, @arroz41, @princ42, @arroz42)
) ranked ON ranked.id = t.id
SET t.position = ranked.new_position;

-- ---------------------------------------------------------------------------
-- 5. Renumber sections of both menus so the canonical order is
--    entrantes -> principales -> arroces -> resto, contiguous and 0-based.
-- ---------------------------------------------------------------------------
UPDATE group_menu_sections_v2 t
JOIN (
    SELECT id,
           ROW_NUMBER() OVER (
               PARTITION BY menu_id
               ORDER BY
                 CASE section_kind
                   WHEN 'entrantes'   THEN 0
                   WHEN 'principales' THEN 1
                   WHEN 'arroces'     THEN 2
                   ELSE 3
                 END ASC,
                 position ASC,
                 id ASC
           ) - 1 AS new_position
    FROM group_menu_sections_v2
    WHERE menu_id IN (41, 42)
) ranked ON ranked.id = t.id
SET t.position = ranked.new_position;

COMMIT;
