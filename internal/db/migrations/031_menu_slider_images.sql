-- Add menu-level slider toggle and slider images table.
-- Supports multiple slider images per menu with ordering.

SET @menus_table_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'menusDeGrupos'
);

-- Add show_menu_slider toggle to menusDeGrupos
SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'menusDeGrupos'
    AND COLUMN_NAME = 'show_menu_slider'
);

SET @ddl := IF(
  @menus_table_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `menusDeGrupos` ADD COLUMN `show_menu_slider` TINYINT(1) NOT NULL DEFAULT 0 AFTER `show_menu_preview_image`',
  'SELECT 1'
);

PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- Create menu_slider_images table for storing slider images
CREATE TABLE IF NOT EXISTS menu_slider_images (
  id BIGINT NOT NULL AUTO_INCREMENT,
  restaurant_id INT NOT NULL,
  menu_id BIGINT NOT NULL,
  image_path VARCHAR(512) NOT NULL,
  position INT NOT NULL DEFAULT 0,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  INDEX idx_menu_slider_menu (restaurant_id, menu_id),
  INDEX idx_menu_slider_position (restaurant_id, menu_id, position)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
