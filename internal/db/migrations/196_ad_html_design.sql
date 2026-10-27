-- Coordination id: ads_html_editor_v1
-- HTML design of an ad (Tailwind HTML pages built in the backoffice editor,
-- the same builder as campaigns): {version, kind: single|multiple, container,
-- main: {html, css}, steps: [{id, title, html, css}]}. NULL keeps the legacy
-- content/layout/canvas rendering of ads not migrated yet.
SET @column_exists := (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_ads' AND COLUMN_NAME = 'design_json');
SET @sql := IF(@column_exists = 0, 'ALTER TABLE restaurant_ads ADD COLUMN design_json JSON NULL AFTER canvas_json', 'DO 0');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- Design history of the ad tools (Forky and the WhatsApp bot): every design
-- change a tool applies first stores the design it replaces, so a change made
-- from a chat without seeing the canvas can be undone (action "undo" restores
-- the newest row and deletes it; newest 20 per ad). design_json NULL = the ad
-- had no HTML design yet (previous editor format).
CREATE TABLE IF NOT EXISTS ad_design_revisions (
  id BIGINT NOT NULL AUTO_INCREMENT,
  restaurant_id INT NOT NULL,
  ad_id BIGINT NOT NULL,
  design_json JSON NULL,
  source VARCHAR(32) NOT NULL DEFAULT '',
  summary VARCHAR(255) NOT NULL DEFAULT '',
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_ad_design_rev (restaurant_id, ad_id, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
