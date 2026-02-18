-- Backoffice RBAC v2 + members/time tracking.

CREATE TABLE IF NOT EXISTS bo_roles (
  slug VARCHAR(32) NOT NULL,
  label VARCHAR(64) NOT NULL,
  sort_order INT NOT NULL DEFAULT 0,
  is_active TINYINT(1) NOT NULL DEFAULT 1,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (slug)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS bo_role_permissions (
  role_slug VARCHAR(32) NOT NULL,
  section_key VARCHAR(32) NOT NULL,
  is_allowed TINYINT(1) NOT NULL DEFAULT 0,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (role_slug, section_key),
  KEY idx_bo_role_permissions_section (section_key),
  CONSTRAINT fk_bo_role_permissions_role FOREIGN KEY (role_slug) REFERENCES bo_roles(slug) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT INTO bo_roles (slug, label, sort_order, is_active) VALUES
  ('admin', 'Admin', 10, 1),
  ('metre', 'Metre', 20, 1),
  ('jefe_cocina', 'Jefe de cocina', 30, 1),
  ('arrocero', 'Arrocero', 40, 1),
  ('pinche_cocina', 'Pinche de cocina', 50, 1),
  ('fregaplatos', 'Fregaplatos', 60, 1),
  ('ayudante_cocina', 'Ayudante de cocina', 70, 1),
  ('camarero', 'Camarero', 80, 1),
  ('responsable_sala', 'Responsable de sala', 90, 1),
  ('ayudante_camarero', 'Ayudante camarero', 100, 1),
  ('runner', 'Runner', 110, 1),
  ('barista', 'Barista', 120, 1)
ON DUPLICATE KEY UPDATE
  label = VALUES(label),
  sort_order = VALUES(sort_order),
  is_active = VALUES(is_active);

-- Admin permissions.
INSERT INTO bo_role_permissions (role_slug, section_key, is_allowed) VALUES
  ('admin', 'reservas', 1),
  ('admin', 'menus', 1),
  ('admin', 'ajustes', 1),
  ('admin', 'miembros', 1)
ON DUPLICATE KEY UPDATE
  is_allowed = VALUES(is_allowed);

-- Metre and kitchen lead.
INSERT INTO bo_role_permissions (role_slug, section_key, is_allowed) VALUES
  ('metre', 'reservas', 1),
  ('metre', 'menus', 1),
  ('jefe_cocina', 'reservas', 1),
  ('jefe_cocina', 'menus', 1)
ON DUPLICATE KEY UPDATE
  is_allowed = VALUES(is_allowed);

-- Remaining roles: fichaje only.
INSERT INTO bo_role_permissions (role_slug, section_key, is_allowed) VALUES
  ('arrocero', 'fichaje', 1),
  ('pinche_cocina', 'fichaje', 1),
  ('fregaplatos', 'fichaje', 1),
  ('ayudante_cocina', 'fichaje', 1),
  ('camarero', 'fichaje', 1),
  ('responsable_sala', 'fichaje', 1),
  ('ayudante_camarero', 'fichaje', 1),
  ('runner', 'fichaje', 1),
  ('barista', 'fichaje', 1)
ON DUPLICATE KEY UPDATE
  is_allowed = VALUES(is_allowed);

-- Normalize legacy role values.
UPDATE bo_user_restaurants
SET role = LOWER(TRIM(role))
WHERE role IS NOT NULL AND role <> LOWER(TRIM(role));

UPDATE bo_user_restaurants
SET role = 'admin'
WHERE role IS NULL OR TRIM(role) = '' OR role = 'owner';

UPDATE bo_user_restaurants
SET role = 'admin'
WHERE role NOT IN (
  'admin',
  'metre',
  'jefe_cocina',
  'arrocero',
  'pinche_cocina',
  'fregaplatos',
  'ayudante_cocina',
  'camarero',
  'responsable_sala',
  'ayudante_camarero',
  'runner',
  'barista'
);

CREATE TABLE IF NOT EXISTS restaurant_members (
  id INT NOT NULL AUTO_INCREMENT,
  restaurant_id INT NOT NULL,
  bo_user_id INT NULL,
  first_name VARCHAR(128) NOT NULL,
  last_name VARCHAR(128) NOT NULL,
  email VARCHAR(255) NULL,
  dni VARCHAR(32) NULL,
  bank_account VARCHAR(64) NULL,
  phone VARCHAR(32) NULL,
  photo_url VARCHAR(1024) NULL,
  is_active TINYINT(1) NOT NULL DEFAULT 1,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_restaurant_members_restaurant_active (restaurant_id, is_active),
  KEY idx_restaurant_members_restaurant_name (restaurant_id, last_name, first_name),
  UNIQUE KEY uniq_restaurant_members_restaurant_user (restaurant_id, bo_user_id),
  UNIQUE KEY uniq_restaurant_members_restaurant_email (restaurant_id, email),
  CONSTRAINT fk_restaurant_members_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
  CONSTRAINT fk_restaurant_members_bo_user FOREIGN KEY (bo_user_id) REFERENCES bo_users(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS member_contracts (
  id INT NOT NULL AUTO_INCREMENT,
  restaurant_member_id INT NOT NULL,
  restaurant_id INT NOT NULL,
  weekly_hours DECIMAL(6,2) NOT NULL DEFAULT 40.00,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uniq_member_contracts_member (restaurant_member_id),
  KEY idx_member_contracts_restaurant (restaurant_id),
  CONSTRAINT fk_member_contracts_member FOREIGN KEY (restaurant_member_id) REFERENCES restaurant_members(id) ON DELETE CASCADE,
  CONSTRAINT fk_member_contracts_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS member_time_entries (
  id BIGINT NOT NULL AUTO_INCREMENT,
  restaurant_member_id INT NOT NULL,
  restaurant_id INT NOT NULL,
  work_date DATE NOT NULL,
  start_time TIME NULL,
  end_time TIME NULL,
  minutes_worked INT NOT NULL DEFAULT 0,
  source VARCHAR(16) NOT NULL DEFAULT 'manual',
  notes VARCHAR(255) NULL,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_member_time_entries_rest_member_date (restaurant_id, restaurant_member_id, work_date),
  KEY idx_member_time_entries_rest_date (restaurant_id, work_date),
  CONSTRAINT fk_member_time_entries_member FOREIGN KEY (restaurant_member_id) REFERENCES restaurant_members(id) ON DELETE CASCADE,
  CONSTRAINT fk_member_time_entries_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
