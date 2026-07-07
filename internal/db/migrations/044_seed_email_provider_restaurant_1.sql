-- Seed email_provider_config for restaurant 1 (Alqueria Villa Carmen)
-- Idempotent: re-running updates the existing row instead of failing on the
-- uniq_restaurant unique key.

INSERT INTO email_provider_config
  (restaurant_id, provider, smtp_host, smtp_port, smtp_username, smtp_password, smtp_encryption, smtp_from_email, gmail_app_password, gmail_from_email, is_active)
VALUES
  (1, 'smtp', 'smtp.titan.email', 587, 'reservas@alqueriavillacarmen.com', '!aLQueria_5225@', 'tls', 'reservas@alqueriavillacarmen.com', '', '', 1)
ON DUPLICATE KEY UPDATE
  provider = VALUES(provider),
  smtp_host = VALUES(smtp_host),
  smtp_port = VALUES(smtp_port),
  smtp_username = VALUES(smtp_username),
  smtp_password = VALUES(smtp_password),
  smtp_encryption = VALUES(smtp_encryption),
  smtp_from_email = VALUES(smtp_from_email),
  is_active = VALUES(is_active);
