-- Legal pages CMS: per-tenant editable content for the three legal pages
-- (aviso-legal, booking-policies, proteccion-datos). One row per
-- (restaurant_id, slug). content_json holds the BlockNote block tree
-- (string-encoded); '[]' means "no blocks, fall back to content_html".
-- Idempotent: CREATE IF NOT EXISTS + INSERT IGNORE against the unique key.

CREATE TABLE IF NOT EXISTS legal_pages (
    id INT AUTO_INCREMENT PRIMARY KEY,
    restaurant_id INT NOT NULL DEFAULT 1,
    slug VARCHAR(40) NOT NULL,
    title VARCHAR(200) NOT NULL,
    content_json LONGTEXT NOT NULL,
    content_html LONGTEXT NOT NULL,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    updated_by_user_id BIGINT NULL,
    UNIQUE KEY uniq_legal_pages_restaurant_slug (restaurant_id, slug)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT IGNORE INTO legal_pages (restaurant_id, slug, title, content_json, content_html, updated_by_user_id)
VALUES
    (1, 'aviso-legal',      'Aviso Legal',          '[]', '<p>Placeholder</p>', NULL),
    (1, 'booking-policies', 'Políticas de Reserva', '[]', '<p>Placeholder</p>', NULL),
    (1, 'proteccion-datos', 'Protección de Datos',  '[]', '<p>Placeholder</p>', NULL);
