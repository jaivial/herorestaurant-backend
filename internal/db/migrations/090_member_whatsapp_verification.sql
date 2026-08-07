ALTER TABLE restaurant_members
  ADD COLUMN whatsapp_verified_at DATETIME NULL,
  ADD COLUMN whatsapp_verification_digest CHAR(64) NULL,
  ADD COLUMN whatsapp_verification_expires_at DATETIME NULL,
  ADD COLUMN whatsapp_verification_attempts INT NOT NULL DEFAULT 0,
  ADD INDEX idx_restaurant_members_whatsapp_verification (restaurant_id, whatsapp_verification_digest);
