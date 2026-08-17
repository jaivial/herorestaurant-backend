-- Per-restaurant BunnyCDN credentials for public media, member avatars, and private documents.
CREATE TABLE IF NOT EXISTS restaurant_bunnycdn_config (
  id INT AUTO_INCREMENT PRIMARY KEY,
  restaurant_id INT NOT NULL,
  public_pull_base_url VARCHAR(512) NOT NULL DEFAULT '',
  public_storage_zone VARCHAR(255) NOT NULL DEFAULT '',
  public_storage_access_key VARCHAR(512) NOT NULL DEFAULT '',
  member_pull_base_url VARCHAR(512) NOT NULL DEFAULT '',
  member_storage_zone VARCHAR(255) NOT NULL DEFAULT '',
  member_storage_access_key VARCHAR(512) NOT NULL DEFAULT '',
  private_storage_zone VARCHAR(255) NOT NULL DEFAULT '',
  private_storage_access_key VARCHAR(512) NOT NULL DEFAULT '',
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  UNIQUE KEY uniq_restaurant_bunnycdn (restaurant_id),
  CONSTRAINT fk_restaurant_bunnycdn_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
