-- Single source of truth for "do not contact", per restaurant and per channel.
-- Coordination id prefix: camp-sup
-- The legacy tables invalid_emails / invalid_phones / no_marketing were copied
-- verbatim from the legacy `villacarmen` DB and keep their historical bounce and
-- opt-out data untouched; they stay as read-only history. campaign_suppressions
-- is the table the campaign send filter must honour from now on.
--
-- Normalization contract for `target`: emails are LOWER(TRIM(x)), phones are
-- TRIM(x). The send filter MUST apply the same normalization before comparing.
CREATE TABLE IF NOT EXISTS campaign_suppressions (
  id BIGINT NOT NULL AUTO_INCREMENT,
  restaurant_id INT NOT NULL,
  channel VARCHAR(16) NOT NULL,           -- 'email' | 'whatsapp'
  target VARCHAR(190) NOT NULL,           -- normalized email or phone
  booking_id BIGINT DEFAULT NULL,         -- who unsubscribed (bookings.id)
  reason VARCHAR(255) DEFAULT NULL,       -- selected reason
  source VARCHAR(32) NOT NULL DEFAULT 'unsubscribe', -- 'unsubscribe'|'bounce'|'invalid'|'legacy'
  created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uq_suppression (restaurant_id, channel, target),
  KEY idx_suppression_booking (booking_id, channel),
  KEY idx_suppression_lookup (restaurant_id, channel)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Backfill of the legacy rows. Restaurant 1 is the Alqueria (legacy) tenant.
-- INSERT IGNORE + the uq_suppression unique key make every statement below
-- idempotent, so re-running this migration can never duplicate a row.
INSERT IGNORE INTO campaign_suppressions (restaurant_id, channel, target, booking_id, reason, source)
SELECT 1, 'email', LOWER(TRIM(ie.email)), ie.id_booking, ie.reason, 'invalid'
FROM invalid_emails ie
WHERE TRIM(COALESCE(ie.email, '')) <> '';

INSERT IGNORE INTO campaign_suppressions (restaurant_id, channel, target, booking_id, reason, source)
SELECT 1, 'whatsapp', TRIM(ip.phone), ip.id_booking, ip.reason, 'invalid'
FROM invalid_phones ip
WHERE TRIM(COALESCE(ip.phone, '')) <> '';

INSERT IGNORE INTO campaign_suppressions (restaurant_id, channel, target, booking_id, reason, source)
SELECT 1, 'whatsapp', TRIM(nm.contact_phone), nm.source_booking_id, nm.reason, 'legacy'
FROM no_marketing nm
WHERE TRIM(COALESCE(nm.contact_phone, '')) <> '';

INSERT IGNORE INTO campaign_suppressions (restaurant_id, channel, target, booking_id, reason, source)
SELECT 1, 'email', LOWER(TRIM(nm.contact_email)), nm.source_booking_id, nm.reason, 'legacy'
FROM no_marketing nm
WHERE TRIM(COALESCE(nm.contact_email, '')) <> '';
