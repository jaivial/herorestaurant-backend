-- Durable, single-use Forky confirmation tokens. Persisting them lets
-- confirmations survive a backend restart and work across replicas; consume is
-- atomic (the row is deleted), so replay/expiry/mismatch is rejected.

CREATE TABLE IF NOT EXISTS forky_confirmation_tokens (
  id BIGINT NOT NULL AUTO_INCREMENT,
  token_hash CHAR(64) NOT NULL,
  user_id VARCHAR(64) NOT NULL DEFAULT '',
  restaurant_id VARCHAR(64) NOT NULL,
  tool VARCHAR(64) NOT NULL,
  args_hash CHAR(64) NOT NULL,
  session_key VARCHAR(64) NOT NULL DEFAULT '',
  expires_at DATETIME NOT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uq_forky_conf_token_hash (token_hash),
  KEY idx_forky_conf_expires (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
