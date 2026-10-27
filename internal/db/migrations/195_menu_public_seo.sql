-- Coordination id: menu_public_seo_v1
-- Optional, editor-controlled public URL and bilingual search metadata.
-- Empty values keep the existing title/subtitle fallbacks. NULL slugs allow
-- unlimited legacy menus while the composite index prevents duplicate URLs.
SET @column_exists := (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menus' AND COLUMN_NAME = 'seo_slug');
SET @sql := IF(@column_exists = 0, 'ALTER TABLE menus ADD COLUMN seo_slug VARCHAR(190) NULL AFTER menu_title', 'DO 0');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @column_exists := (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menus' AND COLUMN_NAME = 'seo_title');
SET @sql := IF(@column_exists = 0, 'ALTER TABLE menus ADD COLUMN seo_title VARCHAR(180) NOT NULL DEFAULT '''' AFTER seo_slug', 'DO 0');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
SET @column_exists := (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menus' AND COLUMN_NAME = 'seo_title_english');
SET @sql := IF(@column_exists = 0, 'ALTER TABLE menus ADD COLUMN seo_title_english VARCHAR(180) NOT NULL DEFAULT '''' AFTER seo_title', 'DO 0');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @column_exists := (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menus' AND COLUMN_NAME = 'seo_description');
SET @sql := IF(@column_exists = 0, 'ALTER TABLE menus ADD COLUMN seo_description VARCHAR(500) NOT NULL DEFAULT '''' AFTER seo_title_english', 'DO 0');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
SET @column_exists := (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menus' AND COLUMN_NAME = 'seo_description_english');
SET @sql := IF(@column_exists = 0, 'ALTER TABLE menus ADD COLUMN seo_description_english VARCHAR(500) NOT NULL DEFAULT '''' AFTER seo_description', 'DO 0');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @column_exists := (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menus' AND COLUMN_NAME = 'seo_h1');
SET @sql := IF(@column_exists = 0, 'ALTER TABLE menus ADD COLUMN seo_h1 VARCHAR(180) NOT NULL DEFAULT '''' AFTER seo_description_english', 'DO 0');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
SET @column_exists := (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menus' AND COLUMN_NAME = 'seo_h1_english');
SET @sql := IF(@column_exists = 0, 'ALTER TABLE menus ADD COLUMN seo_h1_english VARCHAR(180) NOT NULL DEFAULT '''' AFTER seo_h1', 'DO 0');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @index_exists := (SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menus' AND INDEX_NAME = 'idx_menus_restaurant_seo_slug');
SET @sql := IF(@index_exists = 0, 'ALTER TABLE menus ADD UNIQUE KEY idx_menus_restaurant_seo_slug (restaurant_id, seo_slug)', 'DO 0');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
