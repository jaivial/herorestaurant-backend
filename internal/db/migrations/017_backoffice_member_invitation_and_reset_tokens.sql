-- Backoffice: member invitations + password reset tokens + username support.

-- bo_users.username (nullable, unique)
SET @bo_users_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bo_users'
);

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bo_users' AND COLUMN_NAME = 'username'
);
SET @ddl := IF(
  @bo_users_exists = 1 AND @col_exists = 0,
  'ALTER TABLE bo_users ADD COLUMN username VARCHAR(80) NULL AFTER email',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @idx_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bo_users' AND INDEX_NAME = 'uniq_bo_users_username'
);
SET @ddl := IF(
  @bo_users_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE bo_users ADD UNIQUE KEY uniq_bo_users_username (username)',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- bo_users.must_change_password
SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bo_users' AND COLUMN_NAME = 'must_change_password'
);
SET @ddl := IF(
  @bo_users_exists = 1 AND @col_exists = 0,
  'ALTER TABLE bo_users ADD COLUMN must_change_password TINYINT(1) NOT NULL DEFAULT 0 AFTER is_superadmin',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- Invitation tokens.
CREATE TABLE IF NOT EXISTS bo_member_invitation_tokens (
  id BIGINT NOT NULL AUTO_INCREMENT,
  restaurant_id INT NOT NULL,
  member_id INT NOT NULL,
  bo_user_id INT NOT NULL,
  role_slug VARCHAR(32) NOT NULL,
  onboarding_guid CHAR(36) NULL,
  token_sha256 CHAR(64) NOT NULL,
  expires_at DATETIME NOT NULL,
  used_at DATETIME NULL,
  used_ip VARCHAR(64) NULL,
  used_user_agent VARCHAR(255) NULL,
  invalidated_at DATETIME NULL,
  invalidated_reason VARCHAR(64) NULL,
  created_by_user_id INT NULL,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uniq_bo_member_invitation_tokens_token (token_sha256),
  UNIQUE KEY uniq_bo_member_invitation_tokens_guid (onboarding_guid),
  KEY idx_bo_member_invitation_tokens_member (restaurant_id, member_id),
  KEY idx_bo_member_invitation_tokens_user (restaurant_id, bo_user_id),
  KEY idx_bo_member_invitation_tokens_validity (expires_at, used_at, invalidated_at),
  CONSTRAINT fk_bo_member_invitation_tokens_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
  CONSTRAINT fk_bo_member_invitation_tokens_member FOREIGN KEY (member_id) REFERENCES restaurant_members(id) ON DELETE CASCADE,
  CONSTRAINT fk_bo_member_invitation_tokens_user FOREIGN KEY (bo_user_id) REFERENCES bo_users(id) ON DELETE CASCADE,
  CONSTRAINT fk_bo_member_invitation_tokens_role FOREIGN KEY (role_slug) REFERENCES bo_roles(slug) ON DELETE RESTRICT,
  CONSTRAINT fk_bo_member_invitation_tokens_created_by FOREIGN KEY (created_by_user_id) REFERENCES bo_users(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Password reset tokens.
CREATE TABLE IF NOT EXISTS bo_password_reset_tokens (
  id BIGINT NOT NULL AUTO_INCREMENT,
  restaurant_id INT NOT NULL,
  member_id INT NOT NULL,
  bo_user_id INT NOT NULL,
  token_sha256 CHAR(64) NOT NULL,
  expires_at DATETIME NOT NULL,
  used_at DATETIME NULL,
  used_ip VARCHAR(64) NULL,
  used_user_agent VARCHAR(255) NULL,
  invalidated_at DATETIME NULL,
  invalidated_reason VARCHAR(64) NULL,
  created_by_user_id INT NULL,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uniq_bo_password_reset_tokens_token (token_sha256),
  KEY idx_bo_password_reset_tokens_member (restaurant_id, member_id),
  KEY idx_bo_password_reset_tokens_user (restaurant_id, bo_user_id),
  KEY idx_bo_password_reset_tokens_validity (expires_at, used_at, invalidated_at),
  CONSTRAINT fk_bo_password_reset_tokens_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
  CONSTRAINT fk_bo_password_reset_tokens_member FOREIGN KEY (member_id) REFERENCES restaurant_members(id) ON DELETE CASCADE,
  CONSTRAINT fk_bo_password_reset_tokens_user FOREIGN KEY (bo_user_id) REFERENCES bo_users(id) ON DELETE CASCADE,
  CONSTRAINT fk_bo_password_reset_tokens_created_by FOREIGN KEY (created_by_user_id) REFERENCES bo_users(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
