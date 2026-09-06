-- Per-channel send pacing and booking traceability for campaigns.
-- Coordination id prefix: camp
ALTER TABLE campaigns
  ADD COLUMN email_per_minute INT NOT NULL DEFAULT 60 AFTER manual_recipients,
  ADD COLUMN whatsapp_per_minute INT NOT NULL DEFAULT 12 AFTER email_per_minute;

ALTER TABLE campaign_recipients
  ADD COLUMN booking_id BIGINT DEFAULT NULL AFTER name,
  ADD INDEX idx_campaign_recipient_booking (booking_id, channel);
