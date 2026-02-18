-- Backoffice auth + restaurants registry.

CREATE TABLE IF NOT EXISTS restaurants (
  id INT NOT NULL AUTO_INCREMENT,
  slug VARCHAR(64) NOT NULL,
  name VARCHAR(255) NOT NULL,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uniq_restaurants_slug (slug)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT INTO restaurants (id, slug, name)
VALUES (1, 'villacarmen', 'Alqueria Villa Carmen')
ON DUPLICATE KEY UPDATE
  slug = VALUES(slug),
  name = VALUES(name);

CREATE TABLE IF NOT EXISTS bo_users (
  id INT NOT NULL AUTO_INCREMENT,
  email VARCHAR(255) NOT NULL,
  name VARCHAR(255) NOT NULL,
  password_hash VARCHAR(255) NOT NULL,
  is_superadmin TINYINT(1) NOT NULL DEFAULT 0,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uniq_bo_users_email (email)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS bo_user_restaurants (
  user_id INT NOT NULL,
  restaurant_id INT NOT NULL,
  role VARCHAR(32) NOT NULL DEFAULT 'admin',
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (user_id, restaurant_id),
  KEY idx_bo_user_restaurants_restaurant (restaurant_id),
  CONSTRAINT fk_bo_user_restaurants_user FOREIGN KEY (user_id) REFERENCES bo_users(id) ON DELETE CASCADE,
  CONSTRAINT fk_bo_user_restaurants_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS bo_sessions (
  id BIGINT NOT NULL AUTO_INCREMENT,
  token_sha256 CHAR(64) NOT NULL,
  user_id INT NOT NULL,
  active_restaurant_id INT NOT NULL,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  last_seen_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at DATETIME NOT NULL,
  ip VARCHAR(64) DEFAULT NULL,
  user_agent VARCHAR(255) DEFAULT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uniq_bo_sessions_token (token_sha256),
  KEY idx_bo_sessions_user (user_id),
  KEY idx_bo_sessions_expires (expires_at),
  CONSTRAINT fk_bo_sessions_user FOREIGN KEY (user_id) REFERENCES bo_users(id) ON DELETE CASCADE,
  CONSTRAINT fk_bo_sessions_active_restaurant FOREIGN KEY (active_restaurant_id) REFERENCES restaurants(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

