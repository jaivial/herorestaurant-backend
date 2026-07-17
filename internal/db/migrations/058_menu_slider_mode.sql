-- Menu slider customization: add slider_mode + is_default flag, seed defaults.
-- Idempotent (INFORMATION_SCHEMA guards + PREPARE/EXECUTE), mirrors 031/008.

-- 1. show_menu_slider on `menus` (migration 031 targeted the pre-rename table
--    name `menusDeGrupos` and no-op'd; the column is absent from live `menus`).
SET @has_show := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menus'
    AND COLUMN_NAME = 'show_menu_slider'
);
SET @ddl := IF(@has_show = 0,
  'ALTER TABLE `menus` ADD COLUMN `show_menu_slider` TINYINT(1) NOT NULL DEFAULT 0',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- 2. slider_mode enum on `menus`.
SET @has_mode := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menus'
    AND COLUMN_NAME = 'slider_mode'
);
SET @ddl := IF(@has_mode = 0,
  "ALTER TABLE `menus` ADD COLUMN `slider_mode` ENUM('default','custom','both','hidden') NOT NULL DEFAULT 'default'",
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- 3. is_default flag on menu_slider_images.
SET @has_isdef := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menu_slider_images'
    AND COLUMN_NAME = 'is_default'
);
SET @ddl := IF(@has_isdef = 0,
  'ALTER TABLE `menu_slider_images` ADD COLUMN `is_default` TINYINT(1) NOT NULL DEFAULT 0',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- 4. Seed the 5 previously-hardcoded default images per existing menu, once.
--    Guard on absence of any is_default row for that menu so re-runs are no-ops.
INSERT INTO menu_slider_images (restaurant_id, menu_id, image_path, position, is_default)
SELECT m.restaurant_id, m.id, d.url, d.pos, 1
FROM menus m
JOIN (
  SELECT 0 AS pos, 'https://villacarmenmedia.b-cdn.net/images/comida/9%3A16/ChatGPT%20Image%2017%20feb%202026%2C%2002_28_04%20%281%29.webp' AS url
  UNION ALL SELECT 1, 'https://villacarmenmedia.b-cdn.net/images/comida/9%3A16/ChatGPT%20Image%2017%20feb%202026%2C%2002_32_50.webp'
  UNION ALL SELECT 2, 'https://villacarmenmedia.b-cdn.net/images/comida/9%3A16/comid9_16_4.webp'
  UNION ALL SELECT 3, 'https://villacarmenmedia.b-cdn.net/images/comida/9%3A16/comida9_16_2.webp'
  UNION ALL SELECT 4, 'https://villacarmenmedia.b-cdn.net/images/comida/9%3A16/croquetas9_16.webp'
) d
WHERE NOT EXISTS (
  SELECT 1 FROM menu_slider_images x
  WHERE x.menu_id = m.id AND x.is_default = 1
);
