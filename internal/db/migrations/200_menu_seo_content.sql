-- Coordination id: menu_seo_content_v1
-- Optional editor-controlled intro paragraph and FAQ block rendered on the
-- public menu page (and emitted as FAQPage structured data). FAQ text uses
-- blocks separated by a blank line: first line question, remaining lines answer.
SET @column_exists := (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menus' AND COLUMN_NAME = 'seo_intro');
SET @sql := IF(@column_exists = 0, 'ALTER TABLE menus ADD COLUMN seo_intro TEXT NULL AFTER seo_h1_english', 'DO 0');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
SET @column_exists := (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menus' AND COLUMN_NAME = 'seo_intro_english');
SET @sql := IF(@column_exists = 0, 'ALTER TABLE menus ADD COLUMN seo_intro_english TEXT NULL AFTER seo_intro', 'DO 0');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @column_exists := (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menus' AND COLUMN_NAME = 'seo_faq');
SET @sql := IF(@column_exists = 0, 'ALTER TABLE menus ADD COLUMN seo_faq TEXT NULL AFTER seo_intro_english', 'DO 0');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
SET @column_exists := (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'menus' AND COLUMN_NAME = 'seo_faq_english');
SET @sql := IF(@column_exists = 0, 'ALTER TABLE menus ADD COLUMN seo_faq_english TEXT NULL AFTER seo_faq', 'DO 0');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
