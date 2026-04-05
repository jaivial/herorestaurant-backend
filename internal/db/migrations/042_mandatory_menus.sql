-- Create mandatory_menus table for day-specific menu reservation requirements
CREATE TABLE IF NOT EXISTS mandatory_menus (
  id INT AUTO_INCREMENT PRIMARY KEY,
  restaurant_id INT NOT NULL DEFAULT 1,
  date DATE NOT NULL,
  status TINYINT(1) NOT NULL DEFAULT 0,
  mandatory TINYINT(1) NOT NULL DEFAULT 0,
  menu_id JSON NOT NULL,
  menu_choose_main JSON NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  UNIQUE KEY uk_restaurant_date (restaurant_id, date),
  KEY idx_restaurant_id (restaurant_id),
  KEY idx_date (date)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
