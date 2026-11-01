-- 204_cafes_page_seo_section_cta.sql
--
-- 1) Coordination id: cafes_page_seo_v1
--    SEO of the public /cafes page, edited in /app/comida/cafes > Configuración
--    with the same fields as a menu's SEO panel (menus.seo_*). One row per
--    restaurant in the existing page-visibility table.
-- 2) Coordination id: menu_section_cta_v1
--    Optional call-to-action button per menu section (JSON: enabled, label,
--    target). The public site renders it under the section; target is a
--    whitelisted public route (for now only "cafes").

SET @t := 'restaurant_page_visibility';
SET @c := (SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = @t AND COLUMN_NAME = 'cafes_seo_title');
SET @sql := IF(@c = 0, 'ALTER TABLE restaurant_page_visibility
  ADD COLUMN cafes_seo_title VARCHAR(180) NOT NULL DEFAULT '''',
  ADD COLUMN cafes_seo_title_english VARCHAR(180) NOT NULL DEFAULT '''',
  ADD COLUMN cafes_seo_description VARCHAR(500) NOT NULL DEFAULT '''',
  ADD COLUMN cafes_seo_description_english VARCHAR(500) NOT NULL DEFAULT '''',
  ADD COLUMN cafes_seo_h1 VARCHAR(180) NOT NULL DEFAULT '''',
  ADD COLUMN cafes_seo_h1_english VARCHAR(180) NOT NULL DEFAULT '''',
  ADD COLUMN cafes_seo_intro TEXT NULL,
  ADD COLUMN cafes_seo_intro_english TEXT NULL,
  ADD COLUMN cafes_seo_faq TEXT NULL,
  ADD COLUMN cafes_seo_faq_english TEXT NULL', 'DO 0');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @c := (SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'group_menu_sections_v2' AND COLUMN_NAME = 'cta_json');
SET @sql := IF(@c = 0, 'ALTER TABLE group_menu_sections_v2 ADD COLUMN cta_json JSON NULL', 'DO 0');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
