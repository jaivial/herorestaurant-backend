-- Grant the campaigns section to the administrative roles so it shows up in
-- the backoffice navigation. Coordination id prefix: camp
INSERT IGNORE INTO bo_role_permissions (role_slug, section_key, is_allowed)
VALUES ('root', 'campanas', 1), ('admin', 'campanas', 1);
