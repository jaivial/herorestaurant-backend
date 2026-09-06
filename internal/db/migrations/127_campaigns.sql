-- Email + WhatsApp broadcast campaigns written once in markdown.
-- Coordination id prefix: camp
CREATE TABLE IF NOT EXISTS campaigns (
  id BIGINT NOT NULL AUTO_INCREMENT,
  restaurant_id INT NOT NULL,
  coord_id VARCHAR(64) NOT NULL,
  name VARCHAR(180) NOT NULL,
  subject VARCHAR(200) NOT NULL DEFAULT '',
  body_markdown MEDIUMTEXT NOT NULL,
  theme_json TEXT DEFAULT NULL,
  channels VARCHAR(32) NOT NULL DEFAULT 'email',
  audience VARCHAR(32) NOT NULL DEFAULT 'bookings',
  audience_days INT NOT NULL DEFAULT 365,
  manual_recipients MEDIUMTEXT DEFAULT NULL,
  status VARCHAR(16) NOT NULL DEFAULT 'draft',
  sent_at DATETIME DEFAULT NULL,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uq_campaign_coord (coord_id),
  KEY idx_campaign_restaurant (restaurant_id, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS campaign_recipients (
  id BIGINT NOT NULL AUTO_INCREMENT,
  campaign_id BIGINT NOT NULL,
  restaurant_id INT NOT NULL,
  channel VARCHAR(16) NOT NULL,
  target VARCHAR(190) NOT NULL,
  name VARCHAR(190) NOT NULL DEFAULT '',
  status VARCHAR(16) NOT NULL DEFAULT 'pending',
  error TEXT DEFAULT NULL,
  sent_at DATETIME DEFAULT NULL,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uq_campaign_target (campaign_id, channel, target),
  KEY idx_campaign_recipient_status (campaign_id, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
