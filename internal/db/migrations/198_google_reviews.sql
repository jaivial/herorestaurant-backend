-- Coordination id: google_reviews_ai_v1
--
-- Google Business Profile connection per restaurant (OAuth refresh token
-- encrypted with VAULT_TOKEN, chosen location and the reply settings) and the
-- local copy of its reviews with the AI draft / published reply of each one.
CREATE TABLE IF NOT EXISTS restaurant_google_business (
  restaurant_id INT NOT NULL,
  google_email VARCHAR(255) NOT NULL DEFAULT '',
  refresh_token_encrypted TEXT NULL,
  account_name VARCHAR(255) NOT NULL DEFAULT '',
  location_name VARCHAR(255) NOT NULL DEFAULT '',
  location_title VARCHAR(255) NOT NULL DEFAULT '',
  auto_publish_min_stars TINYINT NOT NULL DEFAULT 4,
  formality VARCHAR(8) NOT NULL DEFAULT 'tu',
  signature VARCHAR(160) NOT NULL DEFAULT '',
  complaint_contact VARCHAR(255) NOT NULL DEFAULT '',
  sync_enabled TINYINT(1) NOT NULL DEFAULT 1,
  last_sync_at DATETIME NULL,
  last_sync_error VARCHAR(500) NOT NULL DEFAULT '',
  connected_at DATETIME NULL,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (restaurant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS google_reviews (
  id BIGINT NOT NULL AUTO_INCREMENT,
  restaurant_id INT NOT NULL,
  review_name VARCHAR(255) NOT NULL,
  reviewer_name VARCHAR(255) NOT NULL DEFAULT '',
  star_rating TINYINT NOT NULL DEFAULT 0,
  comment TEXT NULL,
  review_created_at DATETIME NULL,
  review_updated_at DATETIME NULL,
  reply TEXT NULL,
  status VARCHAR(16) NOT NULL DEFAULT 'pending',
  error VARCHAR(500) NOT NULL DEFAULT '',
  published_at DATETIME NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uq_google_reviews_name (review_name),
  KEY idx_google_reviews_status (restaurant_id, status),
  KEY idx_google_reviews_created (restaurant_id, review_created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
