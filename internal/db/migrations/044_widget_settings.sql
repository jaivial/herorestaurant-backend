-- 044_widget_settings.sql
-- Widget customization settings per restaurant (embeddable booking widget)

CREATE TABLE IF NOT EXISTS widget_settings (
  restaurant_id INT NOT NULL PRIMARY KEY,
  primary_color VARCHAR(7) NOT NULL DEFAULT '#7c3aed',
  success_color VARCHAR(7) NOT NULL DEFAULT '#16a34a',
  border_color VARCHAR(7) NOT NULL DEFAULT '#e5e7eb',
  surface_color VARCHAR(7) NOT NULL DEFAULT '#ffffff',
  text_color VARCHAR(7) NOT NULL DEFAULT '#1f2937',
  muted_color VARCHAR(7) NOT NULL DEFAULT '#6b7280',
  font_stack VARCHAR(255) NOT NULL DEFAULT 'system-ui, -apple-system, sans-serif',
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
