-- Coordination id: campaign_tools_v1
--
-- Design history of the campaign tools (Forky and the WhatsApp bot). Every
-- design change a tool applies first stores the design it replaces here, so a
-- change made from a chat without seeing the canvas can always be undone
-- (action "undo" of the tools restores the newest row and deletes it). The
-- visual editor keeps its own in-memory undo and does not write here.
-- body_html NULL = the campaign had no design yet (legacy markdown campaign);
-- body_markdown is the WhatsApp text it had, restored together with it.
CREATE TABLE IF NOT EXISTS campaign_design_revisions (
  id BIGINT NOT NULL AUTO_INCREMENT,
  restaurant_id INT NOT NULL,
  campaign_id BIGINT NOT NULL,
  body_html MEDIUMTEXT NULL,
  body_markdown MEDIUMTEXT NULL,
  source VARCHAR(32) NOT NULL DEFAULT '',
  summary VARCHAR(255) NOT NULL DEFAULT '',
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_campaign_design_rev (restaurant_id, campaign_id, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
