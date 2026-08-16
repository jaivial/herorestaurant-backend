-- Menus v2: dish fetch speed.
-- Every menu load reads `group_menu_section_dishes_v2` by `restaurant_id` +
-- `menu_id` (menu get, per-section dishes, AI tracker). The previous
-- single-column indexes (`idx_..._menu`, `idx_..._restaurant`,
-- `idx_..._section_position`) could not serve the `ORDER BY section_id, position, id`
-- without a filesort and left the optimizer guessing between `menu_id` and
-- `restaurant_id`. A composite `(restaurant_id, menu_id, section_id, position)`
-- index makes the common queries index-only and keeps the result pre-sorted
-- (the PK `id` is appended implicitly by InnoDB, covering the trailing `id`).
-- Uses information_schema because production MySQL lacks ADD ... IF NOT EXISTS.

SET @sql = IF((SELECT COUNT(*) FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'group_menu_section_dishes_v2' AND INDEX_NAME = 'idx_gmsd_restaurant_menu_section_pos') = 0,
  'ALTER TABLE group_menu_section_dishes_v2 ADD INDEX idx_gmsd_restaurant_menu_section_pos (restaurant_id, menu_id, section_id, position)', 'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
