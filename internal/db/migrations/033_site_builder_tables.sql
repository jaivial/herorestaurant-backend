-- Site Builder Tables
-- Creates the core tables for the visual website builder system

-- Sites table: main container for each restaurant's website
CREATE TABLE IF NOT EXISTS site_builder_sites (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    restaurant_id INT NOT NULL,
    name VARCHAR(255) NOT NULL,
    subdomain VARCHAR(100) NOT NULL,
    custom_domain VARCHAR(255) NULL,
    theme_config JSON NULL,
    status ENUM('draft', 'published', 'unpublished') NOT NULL DEFAULT 'draft',
    published_version_id VARCHAR(36) NULL,
    settings JSON NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY uk_subdomain (subdomain),
    UNIQUE KEY uk_custom_domain (custom_domain),
    INDEX idx_restaurant_id (restaurant_id),
    INDEX idx_status (status),
    CONSTRAINT fk_site_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Site pages table: individual pages within a site
CREATE TABLE IF NOT EXISTS site_builder_pages (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    site_id VARCHAR(36) NOT NULL,
    slug VARCHAR(255) NOT NULL,
    name VARCHAR(255) NOT NULL,
    page_type ENUM('static', 'collection_template') NOT NULL DEFAULT 'static',
    tree JSON NOT NULL,
    seo_config JSON NULL,
    collection_binding JSON NULL,
    is_home TINYINT(1) NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY uk_site_slug (site_id, slug),
    INDEX idx_site_id (site_id),
    INDEX idx_page_type (page_type),
    CONSTRAINT fk_page_site FOREIGN KEY (site_id) REFERENCES site_builder_sites(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Site assets table: images, videos, files uploaded for a site
CREATE TABLE IF NOT EXISTS site_builder_assets (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    site_id VARCHAR(36) NOT NULL,
    type ENUM('image', 'video', 'document', 'font', 'other') NOT NULL DEFAULT 'image',
    filename VARCHAR(500) NOT NULL,
    original_filename VARCHAR(500) NULL,
    url VARCHAR(1000) NOT NULL,
    thumbnail_url VARCHAR(1000) NULL,
    mime_type VARCHAR(100) NULL,
    file_size INT NULL,
    width INT NULL,
    height INT NULL,
    alt_text VARCHAR(500) NULL,
    metadata JSON NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_site_id (site_id),
    INDEX idx_type (type),
    CONSTRAINT fk_asset_site FOREIGN KEY (site_id) REFERENCES site_builder_sites(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Site versions table: immutable snapshots for versioning and rollback
CREATE TABLE IF NOT EXISTS site_builder_versions (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    site_id VARCHAR(36) NOT NULL,
    version_number INT NOT NULL,
    pages_snapshot JSON NOT NULL,
    theme_snapshot JSON NOT NULL,
    assets_snapshot JSON NULL,
    settings_snapshot JSON NULL,
    status ENUM('draft', 'published', 'archived') NOT NULL DEFAULT 'draft',
    change_summary TEXT NULL,
    storage_path VARCHAR(500) NULL,
    published_at TIMESTAMP NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE KEY uk_site_version (site_id, version_number),
    INDEX idx_site_id (site_id),
    INDEX idx_status (status),
    CONSTRAINT fk_version_site FOREIGN KEY (site_id) REFERENCES site_builder_sites(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Component registry table: defines available components and their schemas
CREATE TABLE IF NOT EXISTS site_builder_component_registry (
    id INT AUTO_INCREMENT PRIMARY KEY,
    type VARCHAR(100) NOT NULL UNIQUE,
    category VARCHAR(50) NOT NULL,
    label VARCHAR(100) NOT NULL,
    description TEXT NULL,
    props_schema JSON NOT NULL,
    style_schema JSON NULL,
    bindings_schema JSON NULL,
    nesting_rules JSON NULL,
    icon VARCHAR(50) NULL,
    thumbnail_url VARCHAR(500) NULL,
    is_active TINYINT(1) NOT NULL DEFAULT 1,
    sort_order INT NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_category (category),
    INDEX idx_is_active (is_active),
    INDEX idx_sort_order (sort_order)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Bindings table: connects components to dynamic data sources
CREATE TABLE IF NOT EXISTS site_builder_bindings (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    site_id VARCHAR(36) NOT NULL,
    page_id VARCHAR(36) NULL,
    node_id VARCHAR(36) NOT NULL,
    resource_type VARCHAR(100) NOT NULL,
    query_config JSON NOT NULL,
    refresh_mode ENUM('publish', 'load', 'poll') NOT NULL DEFAULT 'publish',
    cache_ttl INT NULL DEFAULT 300,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_site_id (site_id),
    INDEX idx_page_id (page_id),
    INDEX idx_resource_type (resource_type),
    CONSTRAINT fk_binding_site FOREIGN KEY (site_id) REFERENCES site_builder_sites(id) ON DELETE CASCADE,
    CONSTRAINT fk_binding_page FOREIGN KEY (page_id) REFERENCES site_builder_pages(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Collection definitions table: CMS collections for dynamic content
CREATE TABLE IF NOT EXISTS site_builder_collections (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    site_id VARCHAR(36) NOT NULL,
    name VARCHAR(100) NOT NULL,
    slug_pattern VARCHAR(255) NOT NULL,
    source_type ENUM('internal', 'external') NOT NULL DEFAULT 'internal',
    source_config JSON NULL,
    fields_schema JSON NOT NULL,
    is_active TINYINT(1) NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY uk_site_name (site_id, name),
    INDEX idx_site_id (site_id),
    CONSTRAINT fk_collection_site FOREIGN KEY (site_id) REFERENCES site_builder_sites(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Domain mappings table: tracks custom domains and their status
CREATE TABLE IF NOT EXISTS site_builder_domain_mappings (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    site_id VARCHAR(36) NOT NULL,
    domain VARCHAR(255) NOT NULL,
    is_primary TINYINT(1) NOT NULL DEFAULT 0,
    verification_token VARCHAR(100) NULL,
    verification_method ENUM('dns', 'file') NULL,
    status ENUM('pending', 'verified', 'active', 'failed') NOT NULL DEFAULT 'pending',
    ssl_status ENUM('none', 'pending', 'active', 'failed') NOT NULL DEFAULT 'none',
    error_message TEXT NULL,
    verified_at TIMESTAMP NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY uk_domain (domain),
    INDEX idx_site_id (site_id),
    INDEX idx_status (status),
    CONSTRAINT fk_domain_site FOREIGN KEY (site_id) REFERENCES site_builder_sites(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Publish queue table: manages concurrent publish operations
CREATE TABLE IF NOT EXISTS site_builder_publish_queue (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    site_id VARCHAR(36) NOT NULL,
    version_id VARCHAR(36) NOT NULL,
    action ENUM('publish', 'unpublish', 'rollback') NOT NULL DEFAULT 'publish',
    status ENUM('pending', 'processing', 'completed', 'failed') NOT NULL DEFAULT 'pending',
    progress INT NOT NULL DEFAULT 0,
    total_steps INT NOT NULL DEFAULT 0,
    error_message TEXT NULL,
    started_at TIMESTAMP NULL,
    completed_at TIMESTAMP NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_site_id (site_id),
    INDEX idx_status (status),
    CONSTRAINT fk_queue_site FOREIGN KEY (site_id) REFERENCES site_builder_sites(id) ON DELETE CASCADE,
    CONSTRAINT fk_queue_version FOREIGN KEY (version_id) REFERENCES site_builder_versions(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
