-- Bookings: store country calling code separately and keep a children count.
-- This enables WhatsApp reminders and future automations to work for international numbers
-- without assuming +34, while keeping legacy "national digits" storage for contact_phone.

-- Some legacy dumps contain invalid "zero" dates (0000-00-00). With strict SQL_MODE
-- (`NO_ZERO_DATE` / `STRICT_TRANS_TABLES`) any ALTER TABLE that rebuilds `bookings`
-- fails while copying those rows. Fix them upfront so the migration can be applied.
UPDATE bookings
SET reservation_date = '1970-01-01'
WHERE reservation_date < '1000-01-01';

ALTER TABLE bookings
  MODIFY COLUMN contact_phone VARCHAR(32) NULL,
  ADD COLUMN contact_phone_country_code VARCHAR(8) NOT NULL DEFAULT '34' AFTER contact_phone,
  ADD COLUMN children INT NOT NULL DEFAULT 0 AFTER party_size;

-- Defensive backfill (older rows will typically be Spanish).
UPDATE bookings
SET contact_phone_country_code = '34'
WHERE (contact_phone_country_code IS NULL OR contact_phone_country_code = '')
  AND contact_phone IS NOT NULL
  AND contact_phone <> '';
