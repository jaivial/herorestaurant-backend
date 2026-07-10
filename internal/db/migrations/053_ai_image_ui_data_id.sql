-- Correlation IDs let websocket events update optimistic cards before reload.
-- Uses information_schema because production MySQL lacks ADD ... IF NOT EXISTS.

SET @sql = IF((SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'comida_items' AND COLUMN_NAME = 'ui_data_id') = 0,
  'ALTER TABLE comida_items ADD COLUMN ui_data_id VARCHAR(64) NULL AFTER ai_generating', 'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
SET @sql = IF((SELECT COUNT(*) FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'comida_items' AND INDEX_NAME = 'uq_comida_items_restaurant_ui_data') = 0,
  'ALTER TABLE comida_items ADD UNIQUE KEY uq_comida_items_restaurant_ui_data (restaurant_id, ui_data_id)', 'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @sql = IF((SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'VINOS' AND COLUMN_NAME = 'ui_data_id') = 0,
  'ALTER TABLE VINOS ADD COLUMN ui_data_id VARCHAR(64) NULL AFTER ai_generated_img', 'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
SET @sql = IF((SELECT COUNT(*) FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'VINOS' AND INDEX_NAME = 'uq_vinos_restaurant_ui_data') = 0,
  'ALTER TABLE VINOS ADD UNIQUE KEY uq_vinos_restaurant_ui_data (restaurant_id, ui_data_id)', 'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @sql = IF((SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'group_menu_section_dishes_v2' AND COLUMN_NAME = 'ui_data_id') = 0,
  'ALTER TABLE group_menu_section_dishes_v2 ADD COLUMN ui_data_id VARCHAR(64) NULL AFTER ai_generated_img', 'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
SET @sql = IF((SELECT COUNT(*) FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'group_menu_section_dishes_v2' AND INDEX_NAME = 'uq_group_menu_dishes_restaurant_ui_data') = 0,
  'ALTER TABLE group_menu_section_dishes_v2 ADD UNIQUE KEY uq_group_menu_dishes_restaurant_ui_data (restaurant_id, ui_data_id)', 'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @sql = IF((SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'POSTRES' AND COLUMN_NAME = 'foto_url') = 0,
  'ALTER TABLE POSTRES ADD COLUMN foto_url VARCHAR(1024) NULL', 'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
SET @sql = IF((SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'POSTRES' AND COLUMN_NAME = 'ai_requested') = 0,
  'ALTER TABLE POSTRES ADD COLUMN ai_requested TINYINT(1) NOT NULL DEFAULT 0', 'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
SET @sql = IF((SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'POSTRES' AND COLUMN_NAME = 'ai_generating') = 0,
  'ALTER TABLE POSTRES ADD COLUMN ai_generating TINYINT(1) NOT NULL DEFAULT 0', 'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
SET @sql = IF((SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'POSTRES' AND COLUMN_NAME = 'ui_data_id') = 0,
  'ALTER TABLE POSTRES ADD COLUMN ui_data_id VARCHAR(64) NULL', 'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
SET @sql = IF((SELECT COUNT(*) FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'POSTRES' AND INDEX_NAME = 'uq_postres_restaurant_ui_data') = 0,
  'ALTER TABLE POSTRES ADD UNIQUE KEY uq_postres_restaurant_ui_data (restaurant_id, ui_data_id)', 'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
