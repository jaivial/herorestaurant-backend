-- Coordination id: camp-bounce-events
-- One row per delivery-failure notice (NDR/DSN) read from the sender mailbox.
-- event_key (the NDR Message-ID, or a body hash) makes every sync idempotent:
-- the same notice can never invalidate or block an address twice.
-- class: 'hard' = permanent bad address (5.1.x / user unknown) -> invalid for e-mail only
--        'soft' = anything else that failed (4.x.x, mailbox full, policy, auth, unknown)
--                 -> blocked as pending_verify, NEVER marked invalid
CREATE TABLE IF NOT EXISTS campaign_bounce_events (
  id BIGINT NOT NULL AUTO_INCREMENT,
  restaurant_id INT NOT NULL,
  event_key VARCHAR(190) NOT NULL,
  address VARCHAR(190) NOT NULL,
  status_code VARCHAR(16) DEFAULT NULL,
  class VARCHAR(8) NOT NULL,
  diagnostic VARCHAR(300) DEFAULT NULL,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uq_bounce_event (restaurant_id, event_key),
  KEY idx_bounce_address (restaurant_id, address)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
