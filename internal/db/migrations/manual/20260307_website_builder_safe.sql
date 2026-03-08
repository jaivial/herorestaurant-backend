-- Manual website builder schema patch.
-- Date: 2026-03-07
-- Purpose: create website builder tables compatible with existing premium schema.

SET NAMES utf8mb4;

CREATE TABLE IF NOT EXISTS website_templates (
  id INT AUTO_INCREMENT PRIMARY KEY,
  slug VARCHAR(100) NOT NULL UNIQUE,
  name VARCHAR(200) NOT NULL,
  description TEXT,
  thumbnail_url VARCHAR(500),
  template_data JSON NOT NULL,
  category ENUM('restaurant', 'cafe', 'bar', 'bakery', 'catering') DEFAULT 'restaurant',
  is_active TINYINT(1) DEFAULT 1,
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  KEY idx_category (category),
  KEY idx_active (is_active)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

SET @restaurant_websites_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_websites'
);

SET @ddl := IF(
  @restaurant_websites_exists = 0,
  "CREATE TABLE restaurant_websites (
    id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    restaurant_id INT NOT NULL,
    template_id VARCHAR(64) NULL,
    custom_html MEDIUMTEXT NULL,
    domain VARCHAR(255) NULL,
    domain_status VARCHAR(32) NOT NULL DEFAULT 'pending',
    is_published TINYINT(1) NOT NULL DEFAULT 0,
    subdomain VARCHAR(100) NULL,
    status ENUM('draft','published','unpublished') NOT NULL DEFAULT 'draft',
    settings JSON NULL,
    published_at TIMESTAMP NULL,
    created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY uk_restaurant (restaurant_id),
    UNIQUE KEY uk_domain (domain),
    UNIQUE KEY uk_subdomain (subdomain),
    KEY idx_status (status),
    CONSTRAINT fk_restaurant_websites_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
  ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci",
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_websites' AND COLUMN_NAME = 'subdomain'
);
SET @ddl := IF(@restaurant_websites_exists = 1 AND @col_exists = 0, 'ALTER TABLE restaurant_websites ADD COLUMN subdomain VARCHAR(100) NULL AFTER domain', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_websites' AND COLUMN_NAME = 'status'
);
SET @ddl := IF(@restaurant_websites_exists = 1 AND @col_exists = 0, 'ALTER TABLE restaurant_websites ADD COLUMN status ENUM(''draft'',''published'',''unpublished'') NOT NULL DEFAULT ''draft'' AFTER subdomain', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_websites' AND COLUMN_NAME = 'settings'
);
SET @ddl := IF(@restaurant_websites_exists = 1 AND @col_exists = 0, 'ALTER TABLE restaurant_websites ADD COLUMN settings JSON NULL AFTER status', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_websites' AND COLUMN_NAME = 'published_at'
);
SET @ddl := IF(@restaurant_websites_exists = 1 AND @col_exists = 0, 'ALTER TABLE restaurant_websites ADD COLUMN published_at TIMESTAMP NULL AFTER settings', 'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

CREATE TABLE IF NOT EXISTS website_pages (
  id INT AUTO_INCREMENT PRIMARY KEY,
  website_id BIGINT NOT NULL,
  slug VARCHAR(100) NOT NULL,
  title VARCHAR(200) NOT NULL,
  meta_description VARCHAR(500),
  meta_keywords VARCHAR(500),
  is_homepage TINYINT(1) DEFAULT 0,
  status ENUM('draft', 'published') DEFAULT 'draft',
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  CONSTRAINT fk_website_pages_website FOREIGN KEY (website_id) REFERENCES restaurant_websites(id) ON DELETE CASCADE,
  UNIQUE KEY uk_website_slug (website_id, slug),
  KEY idx_homepage (website_id, is_homepage)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS website_page_sections (
  id INT AUTO_INCREMENT PRIMARY KEY,
  page_id INT NOT NULL,
  section_type VARCHAR(50) NOT NULL,
  position INT NOT NULL DEFAULT 0,
  settings JSON NULL,
  is_visible TINYINT(1) DEFAULT 1,
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  CONSTRAINT fk_website_page_sections_page FOREIGN KEY (page_id) REFERENCES website_pages(id) ON DELETE CASCADE,
  KEY idx_position (page_id, position)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS website_components (
  id INT AUTO_INCREMENT PRIMARY KEY,
  component_type VARCHAR(50) NOT NULL,
  name VARCHAR(100) NOT NULL,
  description TEXT,
  icon VARCHAR(50),
  default_settings JSON NULL,
  schema_json JSON NULL,
  is_active TINYINT(1) DEFAULT 1,
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  UNIQUE KEY uk_component_type (component_type),
  KEY idx_type (component_type),
  KEY idx_active (is_active)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS website_section_components (
  id INT AUTO_INCREMENT PRIMARY KEY,
  section_id INT NOT NULL,
  component_id INT NOT NULL,
  position INT NOT NULL DEFAULT 0,
  settings JSON NULL,
  dynamic_source VARCHAR(50) NULL,
  dynamic_params JSON NULL,
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  CONSTRAINT fk_website_section_components_section FOREIGN KEY (section_id) REFERENCES website_page_sections(id) ON DELETE CASCADE,
  CONSTRAINT fk_website_section_components_component FOREIGN KEY (component_id) REFERENCES website_components(id) ON DELETE CASCADE,
  KEY idx_position (section_id, position)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS website_assets (
  id INT AUTO_INCREMENT PRIMARY KEY,
  website_id BIGINT NOT NULL,
  asset_type ENUM('image', 'logo', 'favicon', 'video', 'document') DEFAULT 'image',
  original_filename VARCHAR(255),
  storage_path VARCHAR(500) NOT NULL,
  public_url VARCHAR(500) NOT NULL,
  mime_type VARCHAR(100),
  file_size INT,
  width INT,
  height INT,
  alt_text VARCHAR(255),
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  CONSTRAINT fk_website_assets_website FOREIGN KEY (website_id) REFERENCES restaurant_websites(id) ON DELETE CASCADE,
  KEY idx_type (website_id, asset_type)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS website_publish_history (
  id INT AUTO_INCREMENT PRIMARY KEY,
  website_id BIGINT NOT NULL,
  version INT NOT NULL,
  snapshot_json JSON NOT NULL,
  published_by INT NULL,
  published_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  storage_path VARCHAR(500),
  CONSTRAINT fk_website_publish_history_website FOREIGN KEY (website_id) REFERENCES restaurant_websites(id) ON DELETE CASCADE,
  CONSTRAINT fk_website_publish_history_user FOREIGN KEY (published_by) REFERENCES bo_users(id) ON DELETE SET NULL,
  UNIQUE KEY uk_website_version (website_id, version),
  KEY idx_published (website_id, published_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT INTO website_templates (slug, name, description, category, template_data) VALUES
('elegant-bistro', 'Elegant Bistro', 'Classic fine dining restaurant template with elegant typography and sophisticated layout', 'restaurant', JSON_OBJECT('sections', JSON_ARRAY(JSON_OBJECT('type', 'header', 'position', 0), JSON_OBJECT('type', 'hero', 'position', 1), JSON_OBJECT('type', 'about', 'position', 2), JSON_OBJECT('type', 'menu', 'position', 3), JSON_OBJECT('type', 'gallery', 'position', 4), JSON_OBJECT('type', 'contact', 'position', 5), JSON_OBJECT('type', 'footer', 'position', 6)))) ,
('modern-minimal', 'Modern Minimal', 'Clean, contemporary design with focus on whitespace and typography', 'restaurant', JSON_OBJECT('sections', JSON_ARRAY(JSON_OBJECT('type', 'header', 'position', 0), JSON_OBJECT('type', 'hero', 'position', 1), JSON_OBJECT('type', 'menu', 'position', 2), JSON_OBJECT('type', 'hours', 'position', 3), JSON_OBJECT('type', 'contact', 'position', 4), JSON_OBJECT('type', 'footer', 'position', 5))))
ON DUPLICATE KEY UPDATE
  name = VALUES(name),
  description = VALUES(description),
  category = VALUES(category),
  template_data = VALUES(template_data);

INSERT INTO website_components (component_type, name, description, icon, default_settings, schema_json) VALUES
('hero-banner', 'Hero Banner', 'Full-width hero section with background image and text overlay', 'image', JSON_OBJECT('height', '600px'), JSON_OBJECT('type', 'object')),
('menu-card', 'Menu Card', 'Display menu items from backend API', 'utensils', JSON_OBJECT('layout', 'grid', 'columns', 2), JSON_OBJECT('type', 'object')),
('wine-list', 'Wine List', 'Display wines from backend API', 'wine-glass', JSON_OBJECT('layout', 'cards'), JSON_OBJECT('type', 'object')),
('gallery-grid', 'Gallery Grid', 'Responsive image gallery', 'images', JSON_OBJECT('layout', 'grid', 'columns', 3), JSON_OBJECT('type', 'object')),
('contact-form', 'Contact Form', 'Contact block', 'mail', JSON_OBJECT(), JSON_OBJECT('type', 'object')),
('hours-table', 'Opening Hours', 'Display opening hours', 'clock', JSON_OBJECT('format', '24h'), JSON_OBJECT('type', 'object')),
('testimonials', 'Testimonials', 'Customer testimonials', 'quote-left', JSON_OBJECT('layout', 'grid'), JSON_OBJECT('type', 'object')),
('cta-button', 'CTA Button', 'Call-to-action button', 'mouse-pointer', JSON_OBJECT('style', 'primary'), JSON_OBJECT('type', 'object')),
('text-block', 'Text Block', 'Rich text content block', 'align-left', JSON_OBJECT('align', 'left'), JSON_OBJECT('type', 'object')),
('image-block', 'Image Block', 'Single image block', 'image', JSON_OBJECT('align', 'center'), JSON_OBJECT('type', 'object')),
('social-links', 'Social Links', 'Social media links', 'share-2', JSON_OBJECT('layout', 'horizontal'), JSON_OBJECT('type', 'object')),
('reservation-form', 'Reservation Form', 'Reservation CTA block', 'calendar-check', JSON_OBJECT(), JSON_OBJECT('type', 'object')),
('spacer', 'Spacer', 'Vertical spacing element', 'minus', JSON_OBJECT('height', '40px'), JSON_OBJECT('type', 'object')),
('divider', 'Divider', 'Horizontal divider line', 'minus', JSON_OBJECT('style', 'solid'), JSON_OBJECT('type', 'object'))
ON DUPLICATE KEY UPDATE
  name = VALUES(name),
  description = VALUES(description),
  icon = VALUES(icon),
  default_settings = VALUES(default_settings),
  schema_json = VALUES(schema_json);
