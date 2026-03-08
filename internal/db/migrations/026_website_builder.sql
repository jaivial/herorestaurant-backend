-- Website Builder Tables
-- Supports multi-tenant restaurant websites with drag-and-drop components
-- Generated static HTMX files are stored in BunnyCDN

-- Website templates (shared across restaurants)
CREATE TABLE IF NOT EXISTS website_templates (
    id INT AUTO_INCREMENT PRIMARY KEY,
    slug VARCHAR(100) NOT NULL UNIQUE,
    name VARCHAR(200) NOT NULL,
    description TEXT,
    thumbnail_url VARCHAR(500),
    template_data JSON NOT NULL COMMENT 'Full template structure with sections and component slots',
    category ENUM('restaurant', 'cafe', 'bar', 'bakery', 'catering') DEFAULT 'restaurant',
    is_active TINYINT(1) DEFAULT 1,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_category (category),
    INDEX idx_active (is_active)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Restaurant websites (one per restaurant, can have multiple pages)
CREATE TABLE IF NOT EXISTS restaurant_websites (
    id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    restaurant_id INT NOT NULL,
    template_id VARCHAR(64),
    custom_html MEDIUMTEXT NULL,
    domain VARCHAR(255) COMMENT 'Custom domain (e.g., restaurant.com)',
    domain_status VARCHAR(32) NOT NULL DEFAULT 'pending',
    is_published TINYINT(1) NOT NULL DEFAULT 0,
    subdomain VARCHAR(100) COMMENT 'Subdomain on our platform (e.g., restaurant.villacarmen.es)',
    status ENUM('draft', 'published', 'unpublished') DEFAULT 'draft',
    settings JSON COMMENT 'Global website settings (colors, fonts, logo, etc.)',
    published_at TIMESTAMP NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE,
    UNIQUE KEY uk_restaurant (restaurant_id),
    UNIQUE KEY uk_domain (domain),
    UNIQUE KEY uk_subdomain (subdomain),
    INDEX idx_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

SET @restaurant_websites_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_websites'
);

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_websites' AND COLUMN_NAME = 'subdomain'
);
SET @ddl := IF(
  @restaurant_websites_exists = 1 AND @col_exists = 0,
  'ALTER TABLE restaurant_websites ADD COLUMN subdomain VARCHAR(100) NULL AFTER domain',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_websites' AND COLUMN_NAME = 'status'
);
SET @ddl := IF(
  @restaurant_websites_exists = 1 AND @col_exists = 0,
  'ALTER TABLE restaurant_websites ADD COLUMN status ENUM(''draft'',''published'',''unpublished'') NOT NULL DEFAULT ''draft'' AFTER subdomain',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_websites' AND COLUMN_NAME = 'settings'
);
SET @ddl := IF(
  @restaurant_websites_exists = 1 AND @col_exists = 0,
  'ALTER TABLE restaurant_websites ADD COLUMN settings JSON NULL AFTER status',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_websites' AND COLUMN_NAME = 'published_at'
);
SET @ddl := IF(
  @restaurant_websites_exists = 1 AND @col_exists = 0,
  'ALTER TABLE restaurant_websites ADD COLUMN published_at TIMESTAMP NULL AFTER settings',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- Website pages (home, menu, contact, etc.)
CREATE TABLE IF NOT EXISTS website_pages (
    id INT AUTO_INCREMENT PRIMARY KEY,
    website_id BIGINT NOT NULL,
    slug VARCHAR(100) NOT NULL COMMENT 'URL path segment (e.g., "menu", "contact")',
    title VARCHAR(200) NOT NULL,
    meta_description VARCHAR(500),
    meta_keywords VARCHAR(500),
    is_homepage TINYINT(1) DEFAULT 0,
    status ENUM('draft', 'published') DEFAULT 'draft',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    FOREIGN KEY (website_id) REFERENCES restaurant_websites(id) ON DELETE CASCADE,
    UNIQUE KEY uk_website_slug (website_id, slug),
    INDEX idx_homepage (website_id, is_homepage)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Page sections (header, hero, menu-section, gallery, footer, etc.)
CREATE TABLE IF NOT EXISTS website_page_sections (
    id INT AUTO_INCREMENT PRIMARY KEY,
    page_id INT NOT NULL,
    section_type VARCHAR(50) NOT NULL COMMENT 'hero, menu, gallery, contact, hours, map, testimonials, footer, etc.',
    position INT NOT NULL DEFAULT 0,
    settings JSON COMMENT 'Section-specific settings (background, padding, etc.)',
    is_visible TINYINT(1) DEFAULT 1,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    FOREIGN KEY (page_id) REFERENCES website_pages(id) ON DELETE CASCADE,
    INDEX idx_position (page_id, position)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Component library (reusable components that can be dragged into sections)
CREATE TABLE IF NOT EXISTS website_components (
    id INT AUTO_INCREMENT PRIMARY KEY,
    component_type VARCHAR(50) NOT NULL COMMENT 'hero-banner, menu-card, dish-card, gallery-grid, contact-form, hours-table, map-embed, testimonial, cta-button, text-block, image-block, video-embed, social-links, newsletter-form',
    name VARCHAR(100) NOT NULL,
    description TEXT,
    icon VARCHAR(50) COMMENT 'Icon identifier for the builder UI',
    default_settings JSON COMMENT 'Default configuration for new instances',
    schema_json JSON COMMENT 'JSON Schema for component settings validation',
    is_active TINYINT(1) DEFAULT 1,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY uk_component_type (component_type),
    INDEX idx_type (component_type),
    INDEX idx_active (is_active)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Section components (components placed within sections)
CREATE TABLE IF NOT EXISTS website_section_components (
    id INT AUTO_INCREMENT PRIMARY KEY,
    section_id INT NOT NULL,
    component_id INT NOT NULL,
    position INT NOT NULL DEFAULT 0,
    settings JSON COMMENT 'Merged with component default_settings',
    dynamic_source VARCHAR(50) COMMENT 'API endpoint source (menus, vinos, postres, hours, etc.)',
    dynamic_params JSON COMMENT 'Parameters for dynamic data fetching',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    FOREIGN KEY (section_id) REFERENCES website_page_sections(id) ON DELETE CASCADE,
    FOREIGN KEY (component_id) REFERENCES website_components(id) ON DELETE CASCADE,
    INDEX idx_position (section_id, position)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Website assets (images, logos, etc.)
CREATE TABLE IF NOT EXISTS website_assets (
    id INT AUTO_INCREMENT PRIMARY KEY,
    website_id BIGINT NOT NULL,
    asset_type ENUM('image', 'logo', 'favicon', 'video', 'document') DEFAULT 'image',
    original_filename VARCHAR(255),
    storage_path VARCHAR(500) NOT NULL COMMENT 'BunnyCDN storage path',
    public_url VARCHAR(500) NOT NULL COMMENT 'BunnyCDN pull URL',
    mime_type VARCHAR(100),
    file_size INT,
    width INT,
    height INT,
    alt_text VARCHAR(255),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (website_id) REFERENCES restaurant_websites(id) ON DELETE CASCADE,
    INDEX idx_type (website_id, asset_type)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Website publish history (for rollback and audit)
CREATE TABLE IF NOT EXISTS website_publish_history (
    id INT AUTO_INCREMENT PRIMARY KEY,
    website_id BIGINT NOT NULL,
    version INT NOT NULL,
    snapshot_json JSON NOT NULL COMMENT 'Full website snapshot at publish time',
    published_by INT COMMENT 'bo_users.id',
    published_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    storage_path VARCHAR(500) COMMENT 'BunnyCDN path to generated files',
    FOREIGN KEY (website_id) REFERENCES restaurant_websites(id) ON DELETE CASCADE,
    FOREIGN KEY (published_by) REFERENCES bo_users(id) ON DELETE SET NULL,
    UNIQUE KEY uk_website_version (website_id, version),
    INDEX idx_published (website_id, published_at DESC)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Insert default templates
INSERT INTO website_templates (slug, name, description, category, template_data) VALUES
('elegant-bistro', 'Elegant Bistro', 'Classic fine dining restaurant template with elegant typography and sophisticated layout', 'restaurant', JSON_OBJECT('sections', JSON_ARRAY(JSON_OBJECT('type', 'header', 'position', 0), JSON_OBJECT('type', 'hero', 'position', 1), JSON_OBJECT('type', 'about', 'position', 2), JSON_OBJECT('type', 'menu', 'position', 3), JSON_OBJECT('type', 'gallery', 'position', 4), JSON_OBJECT('type', 'contact', 'position', 5), JSON_OBJECT('type', 'footer', 'position', 6)), 'settings', JSON_OBJECT('primaryColor', '#2c3e50', 'accentColor', '#c0392b', 'fontFamily', 'Playfair Display', 'fontBody', 'Lato'))),
('modern-minimal', 'Modern Minimal', 'Clean, contemporary design with focus on whitespace and typography', 'restaurant', JSON_OBJECT('sections', JSON_ARRAY(JSON_OBJECT('type', 'header', 'position', 0), JSON_OBJECT('type', 'hero', 'position', 1), JSON_OBJECT('type', 'menu', 'position', 2), JSON_OBJECT('type', 'hours', 'position', 3), JSON_OBJECT('type', 'contact', 'position', 4), JSON_OBJECT('type', 'footer', 'position', 5)), 'settings', JSON_OBJECT('primaryColor', '#1a1a1a', 'accentColor', '#e74c3c', 'fontFamily', 'Montserrat', 'fontBody', 'Open Sans'))),
('rustic-tavern', 'Rustic Tavern', 'Warm, inviting template perfect for traditional restaurants and taverns', 'restaurant', JSON_OBJECT('sections', JSON_ARRAY(JSON_OBJECT('type', 'header', 'position', 0), JSON_OBJECT('type', 'hero', 'position', 1), JSON_OBJECT('type', 'about', 'position', 2), JSON_OBJECT('type', 'menu', 'position', 3), JSON_OBJECT('type', 'testimonials', 'position', 4), JSON_OBJECT('type', 'gallery', 'position', 5), JSON_OBJECT('type', 'contact', 'position', 6), JSON_OBJECT('type', 'footer', 'position', 7)), 'settings', JSON_OBJECT('primaryColor', '#5d4037', 'accentColor', '#ff6f00', 'fontFamily', 'Merriweather', 'fontBody', 'Source Sans Pro'))),
('cafe-bright', 'Cafe Bright', 'Fresh, vibrant template ideal for cafes, bakeries, and casual dining', 'cafe', JSON_OBJECT('sections', JSON_ARRAY(JSON_OBJECT('type', 'header', 'position', 0), JSON_OBJECT('type', 'hero', 'position', 1), JSON_OBJECT('type', 'menu', 'position', 2), JSON_OBJECT('type', 'gallery', 'position', 3), JSON_OBJECT('type', 'hours', 'position', 4), JSON_OBJECT('type', 'contact', 'position', 5), JSON_OBJECT('type', 'footer', 'position', 6)), 'settings', JSON_OBJECT('primaryColor', '#4caf50', 'accentColor', '#ff9800', 'fontFamily', 'Pacifico', 'fontBody', 'Quicksand'))),
('wine-bar', 'Wine Bar', 'Sophisticated template designed for wine bars and tapas restaurants', 'bar', JSON_OBJECT('sections', JSON_ARRAY(JSON_OBJECT('type', 'header', 'position', 0), JSON_OBJECT('type', 'hero', 'position', 1), JSON_OBJECT('type', 'about', 'position', 2), JSON_OBJECT('type', 'menu', 'position', 3), JSON_OBJECT('type', 'wines', 'position', 4), JSON_OBJECT('type', 'contact', 'position', 5), JSON_OBJECT('type', 'footer', 'position', 6)), 'settings', JSON_OBJECT('primaryColor', '#4a0e0e', 'accentColor', '#d4af37', 'fontFamily', 'Cormorant Garamond', 'fontBody', 'Raleway')))
ON DUPLICATE KEY UPDATE
  name = VALUES(name),
  description = VALUES(description),
  category = VALUES(category),
  template_data = VALUES(template_data);

-- Insert default components
INSERT INTO website_components (component_type, name, description, icon, default_settings, schema_json) VALUES
('hero-banner', 'Hero Banner', 'Full-width hero section with background image and text overlay', 'image', JSON_OBJECT('height', '600px', 'overlay', 'dark', 'showCTA', true), JSON_OBJECT('type', 'object')),
('menu-card', 'Menu Card', 'Display menu items from backend API', 'utensils', JSON_OBJECT('layout', 'grid', 'columns', 2, 'showImages', true), JSON_OBJECT('type', 'object')),
('dish-card', 'Dish Card', 'Individual dish display component', 'cookie', JSON_OBJECT('showImage', true, 'showDescription', true, 'showPrice', true), JSON_OBJECT('type', 'object')),
('wine-list', 'Wine List', 'Display wines from backend API', 'wine-glass', JSON_OBJECT('groupBy', 'tipo', 'showPrices', true, 'layout', 'cards'), JSON_OBJECT('type', 'object')),
('gallery-grid', 'Gallery Grid', 'Responsive image gallery', 'images', JSON_OBJECT('layout', 'grid', 'columns', 3), JSON_OBJECT('type', 'object')),
('contact-form', 'Contact Form', 'Contact form with validation', 'mail', JSON_OBJECT('showPhone', true, 'showEmail', true, 'showMessage', true), JSON_OBJECT('type', 'object')),
('hours-table', 'Opening Hours', 'Display opening hours', 'clock', JSON_OBJECT('format', '24h'), JSON_OBJECT('type', 'object')),
('map-embed', 'Map Embed', 'Google Maps or OpenStreetMap embed', 'map-pin', JSON_OBJECT('provider', 'openstreetmap', 'zoom', 15, 'height', '400px'), JSON_OBJECT('type', 'object')),
('testimonials', 'Testimonials', 'Customer testimonials carousel', 'quote-left', JSON_OBJECT('layout', 'carousel', 'autoplay', true), JSON_OBJECT('type', 'object')),
('cta-button', 'CTA Button', 'Call-to-action button', 'mouse-pointer', JSON_OBJECT('style', 'primary', 'size', 'large'), JSON_OBJECT('type', 'object')),
('text-block', 'Text Block', 'Rich text content block', 'align-left', JSON_OBJECT('align', 'left', 'maxWidth', '800px'), JSON_OBJECT('type', 'object')),
('image-block', 'Image Block', 'Single image with optional caption', 'image', JSON_OBJECT('align', 'center', 'maxWidth', '100%'), JSON_OBJECT('type', 'object')),
('video-embed', 'Video Embed', 'YouTube/Vimeo video embed', 'video', JSON_OBJECT('provider', 'youtube', 'aspectRatio', '16:9', 'autoplay', false), JSON_OBJECT('type', 'object')),
('social-links', 'Social Links', 'Social media links', 'share-2', JSON_OBJECT('layout', 'horizontal', 'size', 'medium', 'showLabels', false), JSON_OBJECT('type', 'object')),
('reservation-form', 'Reservation Form', 'Booking form linked to backend API', 'calendar-check', JSON_OBJECT('showPartySize', true, 'showTime', true, 'showPhone', true, 'showEmail', true, 'redirectToConfirmation', true), JSON_OBJECT('type', 'object')),
('spacer', 'Spacer', 'Vertical spacing element', 'minus', JSON_OBJECT('height', '40px'), JSON_OBJECT('type', 'object')),
('divider', 'Divider', 'Horizontal divider line', 'minus', JSON_OBJECT('style', 'solid', 'width', '100%', 'color', 'currentColor'), JSON_OBJECT('type', 'object'))
ON DUPLICATE KEY UPDATE
  name = VALUES(name),
  description = VALUES(description),
  icon = VALUES(icon),
  default_settings = VALUES(default_settings),
  schema_json = VALUES(schema_json);
