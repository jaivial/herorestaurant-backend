-- Backoffice RBAC v3: role importance hierarchy, root role and custom-role metadata.

ALTER TABLE bo_roles
  ADD COLUMN importance INT NOT NULL DEFAULT 0 AFTER sort_order,
  ADD COLUMN icon_key VARCHAR(32) NULL AFTER importance,
  ADD COLUMN is_system TINYINT(1) NOT NULL DEFAULT 0 AFTER is_active;

INSERT INTO bo_roles (slug, label, sort_order, is_active, importance, icon_key, is_system) VALUES
  ('root', 'Root', 0, 1, 100, 'crown', 1),
  ('admin', 'Admin', 10, 1, 90, 'shield-user', 1),
  ('metre', 'Metre', 20, 1, 75, 'clipboard-list', 1),
  ('jefe_cocina', 'Jefe de cocina', 30, 1, 74, 'chef-hat', 1),
  ('arrocero', 'Arrocero', 40, 1, 60, 'flame', 1),
  ('pinche_cocina', 'Pinche de cocina', 50, 1, 35, 'utensils-crossed', 1),
  ('fregaplatos', 'Fregaplatos', 60, 1, 30, 'droplets', 1),
  ('ayudante_cocina', 'Ayudante de cocina', 70, 1, 40, 'utensils', 1),
  ('camarero', 'Camarero', 80, 1, 58, 'glass-water', 1),
  ('responsable_sala', 'Responsable de sala', 90, 1, 65, 'users-round', 1),
  ('ayudante_camarero', 'Ayudante camarero', 100, 1, 45, 'user-round-plus', 1),
  ('runner', 'Runner', 110, 1, 50, 'route', 1),
  ('barista', 'Barista', 120, 1, 55, 'coffee', 1)
ON DUPLICATE KEY UPDATE
  label = VALUES(label),
  sort_order = VALUES(sort_order),
  is_active = VALUES(is_active),
  importance = VALUES(importance),
  icon_key = VALUES(icon_key),
  is_system = VALUES(is_system);

-- Root and admin get full access to all backoffice sections.
INSERT INTO bo_role_permissions (role_slug, section_key, is_allowed) VALUES
  ('root', 'reservas', 1),
  ('root', 'menus', 1),
  ('root', 'ajustes', 1),
  ('root', 'miembros', 1),
  ('root', 'fichaje', 1),
  ('root', 'horarios', 1),
  ('admin', 'reservas', 1),
  ('admin', 'menus', 1),
  ('admin', 'ajustes', 1),
  ('admin', 'miembros', 1),
  ('admin', 'fichaje', 1),
  ('admin', 'horarios', 1)
ON DUPLICATE KEY UPDATE
  is_allowed = VALUES(is_allowed);

-- Core service roles.
INSERT INTO bo_role_permissions (role_slug, section_key, is_allowed) VALUES
  ('metre', 'reservas', 1),
  ('metre', 'menus', 1),
  ('metre', 'fichaje', 1),
  ('jefe_cocina', 'reservas', 1),
  ('jefe_cocina', 'menus', 1),
  ('jefe_cocina', 'fichaje', 1)
ON DUPLICATE KEY UPDATE
  is_allowed = VALUES(is_allowed);

-- Remaining default roles: fichaje only.
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
