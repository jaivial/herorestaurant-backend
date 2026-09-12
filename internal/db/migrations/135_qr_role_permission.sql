-- QR becomes a first-class backoffice module (own sidenav entry /app/qr).
-- Grant the section to the administrative roles so it shows up in the
-- navigation, including the restricted 0.4 line (owner account).
-- Coordination id prefix: qr
INSERT IGNORE INTO bo_role_permissions (role_slug, section_key, is_allowed)
VALUES ('root', 'qr', 1), ('admin', 'qr', 1);
