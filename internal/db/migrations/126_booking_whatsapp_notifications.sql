-- Per-restaurant WhatsApp booking notification settings and reminder tracking.
-- Coordination id: bkg-wa-notif
CREATE TABLE IF NOT EXISTS booking_notification_settings (
  restaurant_id INT NOT NULL,
  send_confirmation TINYINT(1) NOT NULL DEFAULT 1,
  send_reconfirmation TINYINT(1) NOT NULL DEFAULT 0,
  reconfirmation_days_before TINYINT UNSIGNED NOT NULL DEFAULT 2,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (restaurant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS booking_reminder_deliveries (
  id BIGINT NOT NULL AUTO_INCREMENT,
  restaurant_id INT NOT NULL,
  booking_id BIGINT NOT NULL,
  delivery_key VARCHAR(128) NOT NULL,
  status VARCHAR(16) NOT NULL DEFAULT 'pending',
  error TEXT DEFAULT NULL,
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  sent_at DATETIME DEFAULT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uq_booking_reminder_key (delivery_key),
  KEY idx_bkg_reminder_restaurant_status (restaurant_id, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
