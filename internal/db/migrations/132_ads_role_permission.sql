-- Anuncios becomes a first-class backoffice module (own sidenav / bottom-nav
-- entry) instead of a tab inside Config. Grant the section to the
-- administrative roles so it shows up in the navigation. Coordination id: ads
INSERT IGNORE INTO bo_role_permissions (role_slug, section_key, is_allowed)
VALUES ('root', 'anuncios', 1), ('admin', 'anuncios', 1);
