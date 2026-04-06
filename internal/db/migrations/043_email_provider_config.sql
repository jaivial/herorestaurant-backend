-- Email provider configuration table
-- Stores SMTP or Gmail provider settings per restaurant for sending emails

CREATE TABLE IF NOT EXISTS email_provider_config (
  id INT AUTO_INCREMENT PRIMARY KEY,
  restaurant_id INT NOT NULL,
  provider ENUM('smtp', 'gmail') NOT NULL DEFAULT 'smtp',
  -- SMTP fields
  smtp_host VARCHAR(255) DEFAULT '',
  smtp_port INT DEFAULT 587,
  smtp_username VARCHAR(255) DEFAULT '',
  smtp_password VARCHAR(512) DEFAULT '',
  smtp_encryption ENUM('none', 'tls', 'ssl') DEFAULT 'tls',
  smtp_from_email VARCHAR(255) DEFAULT '',
  -- Gmail fields
  gmail_app_password VARCHAR(255) DEFAULT '',
  gmail_from_email VARCHAR(255) DEFAULT '',
  -- Common
  is_active TINYINT(1) DEFAULT 0,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  UNIQUE KEY uniq_restaurant (restaurant_id),
  CONSTRAINT fk_epc_restaurant FOREIGN KEY (restaurant_id) REFERENCES restaurants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
