-- Coordination id: campaign_html_builder_v1
--
-- Campaigns designed in the backoffice HTML builder (Figma-like editor):
--   body_html  = the editable design, a fragment written with Tailwind classes.
--   email_html = the compiled email the builder exports from it (Tailwind CSS
--                inlined + responsive media queries), the document SMTP sends.
-- Legacy campaigns keep both NULL and are still rendered from body_markdown.
-- Idempotent: each column is added only if missing.
SET @exists := (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'campaigns' AND COLUMN_NAME = 'body_html');
SET @sql := IF(@exists = 0, 'ALTER TABLE campaigns ADD COLUMN body_html MEDIUMTEXT NULL AFTER body_markdown', 'DO 0');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
SET @exists := (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'campaigns' AND COLUMN_NAME = 'email_html');
SET @sql := IF(@exists = 0, 'ALTER TABLE campaigns ADD COLUMN email_html MEDIUMTEXT NULL AFTER body_html', 'DO 0');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
